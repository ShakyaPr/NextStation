import { useEffect, useMemo, useRef, useState } from "react";
import { api, locationQuality, titleCase } from "./api";
import type { Meta, Station } from "./api";
import StationMap, { type MapMarker } from "./StationMap";
import { scrollToEl, useIsMobile } from "./useMediaQuery";

type StatusFilter = "all" | "problems" | "manual";

interface Props {
  meta: Meta;
  onChanged: () => void;
}

export default function StationsView({ meta, onChanged }: Props) {
  const [stations, setStations] = useState<Station[]>([]);
  const [text, setText] = useState("");
  const [status, setStatus] = useState<StatusFilter>("all");
  const [selected, setSelected] = useState<string | null>(null);
  const [editing, setEditing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const isMobile = useIsMobile();
  const topRef = useRef<HTMLElement>(null);

  // On phones the list is below the map: jump back up to the map/details on selection.
  function select(id: string, fromList: boolean) {
    setSelected(id);
    setEditing(false);
    if (isMobile && fromList) scrollToEl(topRef.current);
  }

  const load = () => api.stations().then(setStations).catch((e) => setError(e.message));
  useEffect(() => {
    load();
  }, [meta.locatedCount, meta.geocoding.done]);

  const filtered = useMemo(() => {
    const t = text.trim().toUpperCase();
    return stations.filter((s) => {
      if (t && !s.name.includes(t) && !s.district.includes(t)) return false;
      const q = locationQuality(s.location);
      if (status === "problems" && q.tone === "good") return false;
      if (status === "manual" && s.location?.source !== "manual") return false;
      return true;
    });
  }, [stations, text, status]);

  const sel = stations.find((s) => s.id === selected);

  const markers: MapMarker[] = filtered
    .filter((s) => s.location?.location)
    .map((s) => {
      const q = locationQuality(s.location);
      return {
        id: s.id,
        pos: s.location!.location!,
        label: "",
        tone: q.tone,
        popup: (
          <div>
            <strong>{titleCase(s.name)}</strong>
            <div>{titleCase(s.district)}</div>
          </div>
        ),
      };
    });

  async function geocode(retry: boolean) {
    setError(null);
    try {
      await api.geocode(retry);
      onChanged();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  async function setManual(p: { lat: number; lng: number }) {
    if (!sel) return;
    try {
      await api.setLocation(sel.id, p);
      setEditing(false);
      await load();
      onChanged();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  async function clearManual() {
    if (!sel) return;
    await api.clearLocation(sel.id);
    await load();
    onChanged();
  }

  const failed = stations.filter((s) => s.location?.status === "failed").length;
  const g = meta.geocoding;

  return (
    <div className="layout stations-layout">
      <aside className="panel">
        <h2>Station locations</h2>
        <p className="muted small">
          {meta.locatedCount} of {meta.stationCount} stations located · {meta.suspectCount} to check · {failed} not found
        </p>
        {g.running && (
          <div className="progress">
            <div style={{ width: `${(g.done / Math.max(g.total, 1)) * 100}%` }} />
            <span>
              Geocoding {g.done}/{g.total}…
            </span>
          </div>
        )}
        <div className="row gap">
          <button className="btn small" disabled={g.running || !meta.apiKeyConfigured} onClick={() => geocode(false)}>
            Locate missing
          </button>
          <button className="btn small" disabled={g.running || !meta.apiKeyConfigured || failed === 0} onClick={() => geocode(true)}>
            Retry not found
          </button>
        </div>
        {g.lastError && <p className="error small">Last error: {g.lastError}</p>}
        {error && <p className="error">{error}</p>}

        <input className="input" value={text} onChange={(e) => setText(e.target.value)} placeholder="Search station or district" />
        <div className="seg">
          {(["all", "problems", "manual"] as StatusFilter[]).map((s) => (
            <button key={s} className={status === s ? "active" : ""} onClick={() => setStatus(s)}>
              {s === "all" ? "All" : s === "problems" ? "Needs review" : "Manual"}
            </button>
          ))}
        </div>

        <ul className="station-list">
          {filtered.map((s) => {
            const q = locationQuality(s.location);
            return (
              <li key={s.id} className={selected === s.id ? "selected" : ""} onClick={() => select(s.id, true)}>
                <div>
                  <div className="station-name">{titleCase(s.name)}</div>
                  <div className="muted small">
                    {titleCase(s.district)} · {s.vacancies.length} vacanc{s.vacancies.length === 1 ? "y" : "ies"}
                  </div>
                </div>
                <span className={`badge ${q.tone}`}>{q.label}</span>
              </li>
            );
          })}
        </ul>
      </aside>

      <main className="content" ref={topRef}>
        <StationMap
          markers={markers}
          selectedId={selected}
          onMarkerClick={(id) => select(id, false)}
          fitKey={`${status}-${text}-${stations.length}`}
          crosshair={editing}
          onMapClick={(p) => editing && setManual(p)}
        />
        {sel && (
          <section className="results">
            <h3>{titleCase(sel.name)}</h3>
            <p className="muted small">
              {titleCase(sel.district)}, {titleCase(sel.province)} · {sel.category}
            </p>
            <dl className="details">
              <dt>Status</dt>
              <dd>
                <span className={`badge ${locationQuality(sel.location).tone}`}>{locationQuality(sel.location).label}</span>
                {sel.location?.source && <span className="muted small"> via {sel.location.source}</span>}
              </dd>
              {sel.location?.location && (
                <>
                  <dt>Coordinates</dt>
                  <dd>
                    {sel.location.location.lat.toFixed(5)}, {sel.location.location.lng.toFixed(5)}{" "}
                    <a href={`https://www.google.com/maps/search/?api=1&query=${sel.location.location.lat},${sel.location.location.lng}`} target="_blank" rel="noreferrer">
                      Open in Google Maps ↗
                    </a>
                  </dd>
                </>
              )}
              {sel.location?.matchedName && (<><dt>Matched</dt><dd>{sel.location.matchedName}</dd></>)}
              {sel.location?.address && (<><dt>Address</dt><dd>{sel.location.address}</dd></>)}
              {sel.location?.query && (<><dt>Query</dt><dd>{sel.location.query}</dd></>)}
              {sel.location?.districtDistanceKm !== undefined && (
                <><dt>From district centre</dt><dd>{sel.location.districtDistanceKm.toFixed(1)} km</dd></>
              )}
              {sel.location?.error && (<><dt>Error</dt><dd className="error">{sel.location.error}</dd></>)}
              <dt>Vacancies</dt>
              <dd>
                <div className="chips">
                  {sel.vacancies.map((v, i) => (
                    <span key={i} className={`chip ${v.type === "Difficult station" ? "chip-warn" : ""}`} title={v.type}>
                      {v.designation}
                    </span>
                  ))}
                </div>
              </dd>
            </dl>
            <div className="row gap">
              <button className={`btn small ${editing ? "active" : ""}`} onClick={() => setEditing((e) => !e)}>
                {editing ? `${isMobile ? "Tap" : "Click"} the correct spot on the map…` : "Set location manually"}
              </button>
              {sel.location?.source === "manual" && (
                <button className="btn small" onClick={clearManual}>
                  Remove manual location
                </button>
              )}
            </div>
          </section>
        )}
      </main>
    </div>
  );
}
