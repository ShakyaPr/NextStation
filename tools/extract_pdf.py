"""Extract the vacancy table from the PDF into JSON.

Usage: python extract_pdf.py <input.pdf> <output.json>
Requires: pip install pdfplumber
"""
import json
import sys

import pdfplumber


def clean(cell):
    if cell is None:
        return ""
    return " ".join(cell.split()).strip()


def main(src, dst):
    rows = []
    with pdfplumber.open(src) as pdf:
        for page_no, page in enumerate(pdf.pages, start=1):
            # Drop the huge "DRAFT" watermark glyphs.
            page = page.filter(lambda o: o.get("object_type") != "char" or o.get("size", 0) < 20)
            for table in page.extract_tables():
                for raw in table:
                    cells = [clean(c) for c in raw]
                    cells = [c for c in cells if c]
                    if len(cells) < 5 or cells[0].lower().startswith("province"):
                        continue
                    if len(cells) > 5:
                        # Merge overflow into institution column.
                        cells = cells[:2] + [" ".join(cells[2:-2])] + cells[-2:]
                    province, district, institution, designation, vtype = cells
                    province = province.replace("NOTHERN", "NORTHERN")
                    rows.append({
                        "province": province,
                        "district": district,
                        "institution": institution,
                        "designation": designation,
                        "type": vtype,
                        "page": page_no,
                    })
    with open(dst, "w") as f:
        json.dump(rows, f, indent=1)
    print(f"extracted {len(rows)} rows")


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2])
