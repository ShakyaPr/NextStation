import { useEffect, useMemo, useState } from "react";
import { api, locationQuality, titleCase } from "./api";
import type { Meta, Station } from "./api";
import StationMap, { type MapMarker } from "./StationMap";
import { useIsPhone } from "./useMediaQuery";
import { Alert, QualityChip, Segmented, VacancyChips } from "./ui";
import { CloseIcon, ExternalIcon, ListIcon, MapIcon, PinIcon, SearchIcon } from "./icons";

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
  const [view, setView] = useState<"list" | "map">("list"); // phone only
  const phone = useIsPhone();

  function select(id: string | null) {
    setSelected(id);
    setEditing(false);
    if (phone && id) setView("map");
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
    .map((s) => ({
      id: s.id,
      pos: s.location!.location!,
      label: `${titleCase(s.name)}, ${titleCase(s.district)}`,
      tone: locationQuality(s.location).tone,
    }));

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
    try {
      await api.clearLocation(sel.id);
      await load();
      onChanged();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  const failed = stations.filter((s) => s.location?.status === "failed").length;
  const g = meta.geocoding;

  const listPanel = (
    <aside className="st-panel" aria-label="Stations">
      <div className="st-head">
        <div>
          <h1 className="results-title">Station locations</h1>
          <p className="results-sub">
            <strong className="num">{meta.locatedCount}</strong> of {meta.stationCount} located ·{" "}
            <strong className="num">{meta.suspectCount}</strong> to check · <strong className="num">{failed}</strong> not found
          </p>
        </div>
        {g.running && (
          <div className="progress" role="status">
            <div className="progress-bar">
              <div style={{ width: `${(g.done / Math.max(g.total, 1)) * 100}%` }} />
            </div>
            <span>
              Locating {g.done}/{g.total}…
            </span>
          </div>
        )}
        <div className="btn-row">
          <button className="btn btn-outlined btn-sm" disabled={g.running || !meta.apiKeyConfigured} onClick={() => geocode(false)}>
            Locate missing
          </button>
          <button
            className="btn btn-outlined btn-sm"
            disabled={g.running || !meta.apiKeyConfigured || failed === 0}
            onClick={() => geocode(true)}
          >
            Retry not found
          </button>
        </div>
        {g.lastError && <Alert severity="warning">Last error: {g.lastError}</Alert>}
        {error && <Alert severity="error">{error}</Alert>}
        <div className="search-field">
          <SearchIcon />
          <input
            className="input"
            value={text}
            onChange={(e) => setText(e.target.value)}
            placeholder="Search station or district"
            aria-label="Search station or district"
          />
        </div>
        <Segmented
          label="Show"
          stretch
          value={status}
          onChange={setStatus}
          options={[
            { value: "all", label: "All" },
            { value: "problems", label: "Needs review" },
            { value: "manual", label: "Manual" },
          ]}
        />
      </div>
      <ul className="station-list">
        {filtered.map((s) => (
          <li key={s.id} className={selected === s.id ? "on" : ""}>
            <button type="button" className="station-item stretch" onClick={() => select(s.id)} aria-pressed={selected === s.id}>
              <span className="station-item-text">
                <span className="station-item-name">{titleCase(s.name)}</span>
                <span className="result-meta">
                  {titleCase(s.district)} · {s.vacancies.length} vacanc{s.vacancies.length === 1 ? "y" : "ies"}
                </span>
              </span>
              <QualityChip entry={s.location} always />
            </button>
          </li>
        ))}
        {filtered.length === 0 && <li className="empty-results">No stations match.</li>}
      </ul>
    </aside>
  );

  const details = sel && (
    <section className="st-details" aria-label="Station details">
      <div className="st-details-head">
        <div>
          <h2>{titleCase(sel.name)}</h2>
          <span className="result-meta">
            {titleCase(sel.district)}, {titleCase(sel.province)} · {sel.category}
          </span>
        </div>
        <button type="button" className="icon-btn plain" aria-label="Close details" onClick={() => select(null)}>
          <CloseIcon size={18} />
        </button>
      </div>
      <dl className="details">
        <dt>Status</dt>
        <dd>
          <QualityChip entry={sel.location} always />
          {sel.location?.source && <span className="muted small"> via {sel.location.source}</span>}
        </dd>
        {sel.location?.location && (
          <>
            <dt>Coordinates</dt>
            <dd>
              <span className="mono">
                {sel.location.location.lat.toFixed(5)}, {sel.location.location.lng.toFixed(5)}
              </span>{" "}
              <a
                className="link"
                href={`https://www.google.com/maps/search/?api=1&query=${sel.location.location.lat},${sel.location.location.lng}`}
                target="_blank"
                rel="noreferrer"
              >
                Google Maps <ExternalIcon size={12} />
              </a>
            </dd>
          </>
        )}
        {sel.location?.matchedName && (
          <>
            <dt>Matched</dt>
            <dd>{sel.location.matchedName}</dd>
          </>
        )}
        {sel.location?.address && (
          <>
            <dt>Address</dt>
            <dd>{sel.location.address}</dd>
          </>
        )}
        {sel.location?.query && (
          <>
            <dt>Query</dt>
            <dd>{sel.location.query}</dd>
          </>
        )}
        {sel.location?.districtDistanceKm !== undefined && (
          <>
            <dt>From district centre</dt>
            <dd className="num">{sel.location.districtDistanceKm.toFixed(1)} km</dd>
          </>
        )}
        {sel.location?.error && (
          <>
            <dt>Error</dt>
            <dd className="text-error">{sel.location.error}</dd>
          </>
        )}
        <dt>Vacancies</dt>
        <dd>
          <VacancyChips vacancies={sel.vacancies} />
        </dd>
      </dl>
      <div className="btn-row">
        <button className={`btn btn-outlined btn-sm${editing ? " pressed" : ""}`} onClick={() => setEditing((e) => !e)}>
          <PinIcon size={14} />
          {editing ? `${phone ? "Tap" : "Click"} the correct spot on the map…` : "Set location manually"}
        </button>
        {sel.location?.source === "manual" && (
          <button className="btn btn-text btn-sm" onClick={clearManual}>
            Remove manual location
          </button>
        )}
      </div>
    </section>
  );

  const mapPane = (
    <section className="map-pane st-map" aria-label="Map of stations">
      {editing && (
        <div className="map-hint" role="status">
          <span>{phone ? "Tap" : "Click"} the correct location of {sel ? titleCase(sel.name) : "the station"}</span>
          <button type="button" className="btn btn-text btn-sm" onClick={() => setEditing(false)}>
            Cancel
          </button>
        </div>
      )}
      <div className="map-area">
        <StationMap
          markers={markers}
          pinStyle="status"
          selectedId={selected}
          onMarkerClick={(id) => select(id)}
          fitKey={`${status}-${text}-${stations.length}`}
          crosshair={editing}
          compactControls={phone}
          fitLabel="Fit to stations"
          zoomOnSelect
          onMapClick={(p) => editing && setManual(p)}
        />
        {!phone && details}
      </div>
      {phone && details}
    </section>
  );

  if (phone) {
    return (
      <div className="st st-phone">
        <div className="phone-top">
          <nav className="view-toggle" aria-label="Stations view">
            <button type="button" aria-current={view === "list" ? "page" : undefined} onClick={() => setView("list")}>
              <ListIcon size={15} />
              List
            </button>
            <button type="button" aria-current={view === "map" ? "page" : undefined} onClick={() => setView("map")}>
              <MapIcon size={15} />
              Map
            </button>
          </nav>
        </div>
        <div className="sv-body">{view === "list" ? listPanel : mapPane}</div>
      </div>
    );
  }

  return (
    <div className="st">
      {listPanel}
      {mapPane}
    </div>
  );
}
