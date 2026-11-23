"""Extract a *scanned* vacancy / station list PDF into JSON.

The Ministry's newer lists are scans with a poor embedded OCR layer. This tool reads
every page twice - once with macOS's Vision OCR and once from the embedded text layer -
groups the words into table rows by position, and snaps each cell to the spellings used
in a reference list (data/vacancies.json) so stations keep the same IDs, and therefore
the same cached locations, across lists. Cells the two readings disagree on are
reported for manual review; fixes go in tools/corrections.json.

Usage:
  # Difficult stations list (SN | Province | RDHS | Station)
  python extract_scanned_pdf.py difficult "source-pdfs/Difficult Vacancy signed.pdf" \
      data/difficult-stations-2026.json --reference data/vacancies.json

  # Vacancy list (Province | District | Institution | Designation)
  python extract_scanned_pdf.py vacancies source-pdfs/PDF345-vac-new2.pdf \
      data/vacancies-2025-11-04.json --reference data/vacancies.json \
      --mark-difficult data/difficult-stations-2026.json

Requires macOS and: pip install pdfplumber pyobjc-framework-Vision pyobjc-framework-Quartz
"""
import argparse
import difflib
import io
import json
import os
import re
import sys
from collections import Counter, defaultdict

import pdfplumber

# Vision sometimes returns Cyrillic look-alikes for Latin capitals.
HOMOGLYPHS = str.maketrans("АВСЕНІЈКМОРЅТХУаерсух", "ABCEHIJKMOPSTXYaepcyx")

# District spellings used by the newer lists -> spelling in the reference list.
DISTRICT_ALIASES = {
    "NUWARAELIYA": "NUWARA ELIYA",
    "MONARAGALA": "MONERAGALA",
    "MULLATHIVU": "MULLAITIVU",
    "HAMBANTHOTA": "HAMBANTOTA",
    "RATNAPURA": "RATHNAPURA",
}

# Signature block text that the scanner runs into the last row of a page.
SIGNATURE = re.compile(r"\s+(Dr\.?|Director|Diractor|Difector|MBBS|MDDO|DDG|D\(MS\)|Deputy|Ministry)\b.*$")

COLUMNS = {
    # left edge of each column, as a fraction of the page width
    "vacancies": [0.13, 0.27, 0.37, 0.70],  # province, district, institution, designation
    "difficult": [0.14, 0.19, 0.34, 0.52],  # SN, province, RDHS, station
}
TABLE_Y = {"vacancies": (0.135, 0.84), "difficult": (0.19, 0.92)}


# ---------------------------------------------------------------- OCR


def vision_pages(pdf_path, cache_dir):
    """Vision OCR observations per page: {t, x, y, w, h} with a top-left origin."""
    cache = None
    if cache_dir:
        os.makedirs(cache_dir, exist_ok=True)
        cache = os.path.join(cache_dir, os.path.basename(pdf_path) + ".vision.json")
        if os.path.exists(cache):
            with open(cache) as f:
                return json.load(f)

    import Quartz
    import Vision
    from Foundation import NSData

    def ocr(png):
        data = NSData.dataWithBytes_length_(png, len(png))
        img = Quartz.CGImageSourceCreateImageAtIndex(Quartz.CGImageSourceCreateWithData(data, None), 0, None)
        req = Vision.VNRecognizeTextRequest.alloc().init()
        req.setRecognitionLevel_(Vision.VNRequestTextRecognitionLevelAccurate)
        req.setUsesLanguageCorrection_(False)
        req.setRecognitionLanguages_(["en-US"])
        ok, err = Vision.VNImageRequestHandler.alloc().initWithCGImage_options_(img, None).performRequests_error_([req], None)
        if not ok:
            raise RuntimeError(err)
        out = []
        for o in req.results():
            b = o.boundingBox()
            out.append({"t": o.topCandidates_(1)[0].string(), "x": b.origin.x,
                        "y": 1 - (b.origin.y + b.size.height), "w": b.size.width, "h": b.size.height})
        return out

    pages = []
    with pdfplumber.open(pdf_path) as pdf:
        for i, page in enumerate(pdf.pages, 1):
            buf = io.BytesIO()
            page.to_image(resolution=300).original.save(buf, format="PNG")
            pages.append(ocr(buf.getvalue()))
            print(f"  OCR page {i}/{len(pdf.pages)}", file=sys.stderr)
    if cache:
        with open(cache, "w") as f:
            json.dump(pages, f)
    return pages


def text_layer_pages(pdf_path):
    pages = []
    with pdfplumber.open(pdf_path) as pdf:
        for page in pdf.pages:
            W, H = page.width, page.height
            pages.append([{"t": w["text"], "x": w["x0"] / W, "y": w["top"] / H, "h": (w["bottom"] - w["top"]) / H}
                          for w in page.extract_words()])
    return pages


def group_rows(obs, cols, ymin, ymax, tol=0.009):
    """Cluster observations into rows by vertical centre, then into columns by x."""
    obs = sorted((o for o in obs if ymin < o["y"] < ymax and o["x"] >= cols[0] - 0.02), key=lambda o: o["y"] + o["h"] / 2)
    rows = []
    for o in obs:
        cy = o["y"] + o["h"] / 2
        if rows and abs(rows[-1]["cy"] - cy) < tol:
            rows[-1]["obs"].append(o)
        else:
            rows.append({"cy": cy, "obs": [o]})
    for r in rows:
        cells = [[] for _ in cols]
        for o in sorted(r["obs"], key=lambda o: o["x"]):
            ci = max([i for i, c in enumerate(cols) if o["x"] >= c - 0.02] or [0])
            cells[ci].append(o)
        r["cells"] = cells
    return rows


def cell_text(cell, max_x=None):
    return " ".join(o["t"] for o in cell if max_x is None or o["x"] < max_x)


# ---------------------------------------------------------------- normalising


def norm(s):
    s = s.translate(HOMOGLYPHS)
    s = SIGNATURE.sub("", s)
    s = s.upper().replace("|", " ").replace("_", " ")
    s = re.sub(r"\s+", " ", s).strip(" .,:;-•~^'„")
    return s.replace("( ", "(").replace(" )", ")")


class Vocab:
    def __init__(self, reference_rows):
        self.provinces = sorted({r["province"] for r in reference_rows})
        self.districts = sorted({r["district"] for r in reference_rows})
        self.district_province = {r["district"]: r["province"] for r in reference_rows}
        self.institutions = defaultdict(set)
        for r in reference_rows:
            self.institutions[r["district"]].add(r["institution"])
        self.designations = {r["designation"] for r in reference_rows if r.get("designation")}

    @staticmethod
    def close(s, options, cutoff):
        m = difflib.get_close_matches(s, list(options), n=1, cutoff=cutoff) if s else []
        return m[0] if m else None

    def province(self, *texts):
        for t in texts:
            t = t.replace("NORTHCENTRAL", "NORTH CENTRAL").replace("NORTHWESTERN", "NORTH WESTERN")
            if t and not t.endswith("PROVINCE"):
                t += " PROVINCE"
            m = self.close(t, self.provinces, 0.75)
            if m:
                return m
        return None

    def district(self, *texts):
        for t in texts:
            t = DISTRICT_ALIASES.get(t, t)
            m = self.close(t, self.districts, 0.75)
            if m:
                return m
        return None

    def pick(self, readings, options, cutoff, label, notes):
        """Choose a value from several OCR readings of one cell.

        Exact match in the reference > all readings agree > close match > first reading."""
        readings = [r for r in readings if r]
        for r in readings:
            if r in options:
                return r
        # Readings that differ only in spacing agree; the embedded layer drops spaces.
        if len(readings) > 1 and all(r.replace(" ", "") == readings[0].replace(" ", "") for r in readings):
            notes.append(f"new {label} (OCR readings agree): {readings[0]}")
            return readings[0]
        for r in readings:
            m = self.close(r, options, cutoff)
            if m:
                notes.append(f"{label} {r!r} -> {m!r}")
                return m
        notes.append(f"REVIEW {label}: readings {readings!r}")
        return readings[0] if readings else ""


# ---------------------------------------------------------------- extraction


def aligned_rows(pdf_path, kind, cache_dir):
    """Yield (page, vision_row, text_row_or_None) for each table row."""
    cols, (ymin, ymax) = COLUMNS[kind], TABLE_Y[kind]
    vision = vision_pages(pdf_path, cache_dir)
    text = text_layer_pages(pdf_path)
    for pno, (vobs, tobs) in enumerate(zip(vision, text), 1):
        trows = group_rows(tobs, cols, ymin, ymax)
        used = set()
        for vr in group_rows(vobs, cols, ymin, ymax):
            match = None
            for j, tr in enumerate(trows):
                if j not in used and abs(tr["cy"] - vr["cy"]) < 0.009:
                    match = tr
                    used.add(j)
                    break
            yield pno, vr, match


def extract_vacancies(args, vocab, corrections):
    rows, review = [], []
    for pno, vr, tr in aligned_rows(args.pdf, "vacancies", args.ocr_cache):
        v = [norm(cell_text(c)) for c in vr["cells"]]
        t = [norm(cell_text(c)) for c in tr["cells"]] if tr else [""] * 4
        notes = []
        district = vocab.district(v[1], t[1])
        province = vocab.province(v[0], t[0])
        if v[2] == "INSTITUTION" or not (district or (province and re.search("[A-Z]{3}", v[2] + t[2]))):
            continue  # title, header, page number or signature lines
        if not district:
            notes.append(f"REVIEW district: readings {[v[1], t[1]]!r}")
        elif province != vocab.district_province[district]:
            province = vocab.district_province[district]
        inst = vocab.pick([v[2], t[2]], vocab.institutions.get(district, ()), 0.88, "institution", notes)
        desig = vocab.pick([v[3], t[3]], vocab.designations, 0.9, "designation", notes)
        row = {"province": province, "district": district or "", "institution": inst,
               "designation": desig, "type": "", "page": pno}
        key = f"{pno}:{len([r for r in rows if r['page'] == pno]) + 1}"
        if key in corrections:
            row.update(corrections[key])
            notes = [n for n in notes if not n.startswith("REVIEW")] + [f"corrected by tools/corrections.json[{key}]"]
        rows.append(row)
        if notes:
            review.append((key, row, notes))
    return rows, review


def extract_difficult(args, vocab, corrections):
    rows, review = [], []
    for pno, vr, tr in aligned_rows(args.pdf, "difficult", args.ocr_cache):
        v = [norm(cell_text(c, max_x=0.75)) for c in vr["cells"]]
        t = [norm(cell_text(c, max_x=0.75)) for c in tr["cells"]] if tr else [""] * 4
        notes = []
        district = vocab.district(v[2], t[2])
        province = vocab.province(v[1], t[1])
        if not district and not (province and (v[3] or t[3])):
            continue  # title, header or signature lines
        if district:
            province = vocab.district_province[district]
        else:
            notes.append(f"REVIEW district: readings {[v[2], t[2]]!r}")
        station = vocab.pick([v[3], t[3]], vocab.institutions.get(district, ()), 0.88, "station", notes)
        sn = len(rows) + 1
        printed_sn = re.sub(r"\D", "", v[0])
        if printed_sn and int(printed_sn) != sn and v[0].isdigit():
            notes.append(f"REVIEW row number: counted {sn}, printed {v[0]}")
        row = {"province": province or "", "district": district or "", "institution": station,
               "designation": "", "type": "Difficult station", "page": pno, "sn": sn}
        key = f"sn:{sn}"
        if key in corrections:
            row.update(corrections[key])
            notes = [n for n in notes if not n.startswith("REVIEW")] + [f"corrected by tools/corrections.json[{key}]"]
        rows.append(row)
        if notes:
            review.append((key, row, notes))
    return rows, review


def station_key(r):
    return re.sub(r"[^A-Z0-9]+", " ", f"{r['district']} {r['institution']}").strip()


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("kind", choices=["vacancies", "difficult"])
    ap.add_argument("pdf")
    ap.add_argument("out")
    ap.add_argument("--reference", required=True, help="existing list whose spellings are reused (data/vacancies.json)")
    ap.add_argument("--mark-difficult", help="difficult stations JSON; matching vacancies get type 'Difficult station'")
    ap.add_argument("--corrections", default=os.path.join(os.path.dirname(__file__), "corrections.json"))
    ap.add_argument("--ocr-cache", help="directory to cache Vision OCR results in")
    args = ap.parse_args()

    with open(args.reference) as f:
        vocab = Vocab(json.load(f))
    corrections = {}
    if os.path.exists(args.corrections):
        with open(args.corrections) as f:
            corrections = json.load(f).get(os.path.basename(args.out), {})
    # Stations that only appear in the difficult list are valid spellings too.
    if args.mark_difficult:
        with open(args.mark_difficult) as f:
            difficult = json.load(f)
        for r in difficult:
            vocab.institutions[r["district"]].add(r["institution"])

    extract = extract_vacancies if args.kind == "vacancies" else extract_difficult
    rows, review = extract(args, vocab, corrections)

    if args.mark_difficult:
        hard = {station_key(r) for r in difficult}
        for r in rows:
            if station_key(r) in hard:
                r["type"] = "Difficult station"
        print(f"marked {sum(r['type'] == 'Difficult station' for r in rows)} vacancies at difficult stations")

    with open(args.out, "w") as f:
        json.dump(rows, f, indent=1)
    print(f"extracted {len(rows)} rows -> {args.out}")
    print(f"pages: {dict(Counter(r['page'] for r in rows))}")
    for key, row, notes in review:
        flag = "!!" if any(n.startswith("REVIEW") for n in notes) else "  "
        print(f"{flag} [{key}] p{row['page']} {row['district']} | {row['institution']} | {row['designation']}  <- {'; '.join(notes)}")
    if any(any(n.startswith("REVIEW") for n in notes) for _, _, notes in review):
        print("Rows marked !! need a manual check against the PDF (add fixes to tools/corrections.json).")


if __name__ == "__main__":
    main()
