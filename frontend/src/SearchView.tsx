import { useMemo, useState } from "react";
import { api, formatDuration, locationQuality, titleCase } from "./api";
import type { LatLng, Meta, SearchRequest, SearchResponse } from "./api";
import StationMap, { type MapMarker } from "./StationMap";

interface Props {
  meta: Meta;
}

function MultiSelect({
  label,
  options,
  value,
  onChange,
}: {
  label: string;
  options: { name: string; count: number }[];
  value: string[];
  onChange: (v: string[]) => void;
}) {
  const toggle = (n: string) => onChange(value.includes(n) ? value.filter((x) => x !== n) : [...value, n]);
  return (
    <details className="multi">
      <summary>
        {label}
        <span className="muted">{value.length ? ` · ${value.length} selected` : " · any"}</span>
      </summary>
      <div className="multi-options">
        {value.length > 0 && (
          <button type="button" className="link" onClick={() => onChange([])}>
            Clear
          </button>
        )}
        {options.map((o) => (
          <label key={o.name} className="check">
            <input type="checkbox" checked={value.includes(o.name)} onChange={() => toggle(o.name)} />
            <span>{titleCase(o.name)}</span>
            <span className="muted">{o.count}</span>
          </label>
        ))}
      </div>
    </details>
  );
}

const fmtCoord = (p: LatLng) => `${p.lat.toFixed(5)}, ${p.lng.toFixed(5)}`;

export default function SearchView({ meta }: Props) {
  const [originText, setOriginText] = useState("Kegalle");
  const [originPoint, setOriginPoint] = useState<LatLng | null>(null);
  const [pickMode, setPickMode] = useState(false);
  const [maxDuration, setMaxDuration] = useState("");
  const [maxDistance, setMaxDistance] = useState("");
  const [limit, setLimit] = useState(String(meta.defaultLimit || 10));
  const [sortBy, setSortBy] = useState<"duration" | "distance">("duration");
  const [provinces, setProvinces] = useState<string[]>([]);
  const [districts, setDistricts] = useState<string[]>([]);
  const [types, setTypes] = useState<string[]>([]);
  const [categories, setCategories] = useState<string[]>([]);
  const [designation, setDesignation] = useState("");

  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [resp, setResp] = useState<SearchResponse | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [searchCount, setSearchCount] = useState(0);

  const districtOptions = useMemo(
    () =>
      provinces.length
        ? meta.districts.filter((d) => provinces.includes(meta.districtProvince[d.name]))
        : meta.districts,
    [meta, provinces],
  );

  async function runSearch(e?: React.FormEvent) {
    e?.preventDefault();
    setError(null);
    const req: SearchRequest = {
      origin: originPoint ? { lat: originPoint.lat, lng: originPoint.lng } : { query: originText },
      limit: Number(limit) || 10,
      sortBy,
      provinces,
      districts: districts.filter((d) => districtOptions.some((o) => o.name === d)),
      types,
      categories,
      designation: designation.trim() || undefined,
    };
    if (Number(maxDuration) > 0) req.maxDurationMin = Number(maxDuration);
    if (Number(maxDistance) > 0) req.maxDistanceKm = Number(maxDistance);
    setLoading(true);
    try {
      const r = await api.search(req);
      setResp(r);
      setSelected(null);
      setSearchCount((c) => c + 1);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setLoading(false);
    }
  }

  const markers: MapMarker[] = (resp?.results ?? []).map((r) => {
    const q = locationQuality(r.location);
    return {
      id: r.station.id,
      pos: r.location.location!,
      label: String(r.rank),
      tone: q.tone,
      popup: (
        <div>
          <strong>
            {r.rank}. {titleCase(r.station.name)}
          </strong>
          <div>
            {r.distanceKm.toFixed(1)} km · {formatDuration(r.durationMin)}
          </div>
        </div>
      ),
    };
  });

  const originOnMap = originPoint ?? (resp ? { lat: resp.origin.lat, lng: resp.origin.lng } : null);

  return (
    <div className="layout">
      <aside className="panel">
        <form onSubmit={runSearch}>
          <h2>Starting point</h2>
          {originPoint ? (
            <div className="picked">
              <span>📍 {fmtCoord(originPoint)}</span>
              <button type="button" className="link" onClick={() => setOriginPoint(null)}>
                Use a place name instead
              </button>
            </div>
          ) : (
            <input
              className="input"
              value={originText}
              onChange={(e) => setOriginText(e.target.value)}
              placeholder="City, town or address (e.g. Kegalle)"
            />
          )}
          <div className="row gap">
            <button
              type="button"
              className={`btn small ${pickMode ? "active" : ""}`}
              onClick={() => setPickMode((p) => !p)}
            >
              {pickMode ? "Click on the map…" : "Pick on map"}
            </button>
            <button
              type="button"
              className="btn small"
              onClick={() =>
                navigator.geolocation?.getCurrentPosition(
                  (p) => setOriginPoint({ lat: p.coords.latitude, lng: p.coords.longitude }),
                  (err) => setError(`Location unavailable: ${err.message}`),
                )
              }
            >
              My location
            </button>
          </div>

          <h2>Limits</h2>
          <div className="grid2">
            <label>
              Max travel time (min)
              <input className="input" type="number" min="0" value={maxDuration} onChange={(e) => setMaxDuration(e.target.value)} placeholder="no limit" />
            </label>
            <label>
              Max road distance (km)
              <input className="input" type="number" min="0" value={maxDistance} onChange={(e) => setMaxDistance(e.target.value)} placeholder="no limit" />
            </label>
            <label>
              Number of stations
              <input className="input" type="number" min="1" max="200" value={limit} onChange={(e) => setLimit(e.target.value)} />
            </label>
            <label>
              Rank by
              <select className="input" value={sortBy} onChange={(e) => setSortBy(e.target.value as "duration" | "distance")}>
                <option value="duration">Travel time</option>
                <option value="distance">Road distance</option>
              </select>
            </label>
          </div>

          <h2>Filters</h2>
          <MultiSelect label="Province" options={meta.provinces} value={provinces} onChange={setProvinces} />
          <MultiSelect label="District / RDHS" options={districtOptions} value={districts} onChange={setDistricts} />
          <MultiSelect label="Vacancy type" options={meta.types} value={types} onChange={setTypes} />
          <MultiSelect label="Station type" options={meta.categories} value={categories} onChange={setCategories} />
          <label>
            Designation contains
            <input className="input" list="designations" value={designation} onChange={(e) => setDesignation(e.target.value)} placeholder="e.g. MO, SHO, ANAESTHESIA" />
            <datalist id="designations">
              {["MO", "SHO", "MOIC", "ADDITIONAL MOH", "MO OPD", "ANAESTHESIA", "PAEDIATRICS", "MEDICINE", "SURGERY"].map((d) => (
                <option key={d} value={d} />
              ))}
            </datalist>
          </label>

          <button className="btn primary wide" disabled={loading || !meta.apiKeyConfigured}>
            {loading ? "Calculating routes…" : "Find stations"}
          </button>
          {!meta.apiKeyConfigured && <p className="error">Set google.apiKey in config.yaml and restart the backend.</p>}
          {error && <p className="error">{error}</p>}
        </form>
      </aside>

      <main className="content">
        <StationMap
          origin={originOnMap}
          markers={markers}
          selectedId={selected}
          onMarkerClick={setSelected}
          fitKey={String(searchCount)}
          crosshair={pickMode}
          onMapClick={(p) => {
            if (!pickMode) return;
            setOriginPoint(p);
            setPickMode(false);
          }}
        />

        {resp && (
          <section className="results">
            <div className="results-head">
              <div>
                <strong>{resp.results.length}</strong> station{resp.results.length === 1 ? "" : "s"} from{" "}
                <strong>{resp.origin.address || fmtCoord(resp.origin)}</strong>
              </div>
              <div className="muted small">
                {resp.stats.stationsMatchingFilters} matched filters · {resp.stats.routeCandidates} routed ·{" "}
                {resp.stats.withinLimits} within limits
              </div>
            </div>
            {resp.warnings?.map((w) => (
              <p key={w} className="warning">
                {w}
              </p>
            ))}
            {resp.results.length === 0 ? (
              <p className="muted">No stations match. Try relaxing the limits or filters.</p>
            ) : (
              <table className="table">
                <thead>
                  <tr>
                    <th>#</th>
                    <th>Station</th>
                    <th className="num">Road distance</th>
                    <th className="num">Travel time</th>
                    <th>Vacancies</th>
                    <th></th>
                  </tr>
                </thead>
                <tbody>
                  {resp.results.map((r) => {
                    const q = locationQuality(r.location);
                    const dest = r.location.location!;
                    const dir = `https://www.google.com/maps/dir/?api=1&origin=${resp.origin.lat},${resp.origin.lng}&destination=${dest.lat},${dest.lng}&travelmode=driving`;
                    return (
                      <tr key={r.station.id} className={selected === r.station.id ? "selected" : ""} onClick={() => setSelected(r.station.id)}>
                        <td>{r.rank}</td>
                        <td>
                          <div className="station-name">{titleCase(r.station.name)}</div>
                          <div className="muted small">
                            {titleCase(r.station.district)} · {r.station.category}{" "}
                            {q.tone !== "good" && <span className={`badge ${q.tone}`}>{q.label}</span>}
                          </div>
                        </td>
                        <td className="num">
                          {r.distanceKm.toFixed(1)} km
                          <div className="muted small">{r.straightLineKm.toFixed(1)} km direct</div>
                        </td>
                        <td className="num">{formatDuration(r.durationMin)}</td>
                        <td>
                          <div className="chips">
                            {r.vacancies.map((v, i) => (
                              <span key={i} className={`chip ${v.type === "Difficult station" ? "chip-warn" : ""}`} title={v.type}>
                                {v.designation}
                              </span>
                            ))}
                          </div>
                        </td>
                        <td>
                          <a href={dir} target="_blank" rel="noreferrer" onClick={(e) => e.stopPropagation()}>
                            Directions ↗
                          </a>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            )}
          </section>
        )}
      </main>
    </div>
  );
}
