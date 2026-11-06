import { useEffect, useMemo, useRef, useState } from "react";
import { api, formatDuration, titleCase } from "./api";
import type { LatLng, Meta, SearchRequest, SearchResponse, SearchResult } from "./api";
import StationMap, { type MapMarker } from "./StationMap";
import PlaceSearch, { type SelectedPlace } from "./PlaceSearch";
import { useLayout, type Layout } from "./useMediaQuery";
import { Alert, QualityChip, Segmented, TypeLegend, VacancyChips } from "./ui";
import {
  ChevronLeft,
  ChevronRight,
  ClockIcon,
  CloseIcon,
  ExternalIcon,
  InfoIcon,
  ListIcon,
  LocateIcon,
  MapIcon,
  NavigateIcon,
  PinIcon,
  ResetIcon,
  RouteIcon,
  SearchIcon,
  SlidersIcon,
  SortIcon,
} from "./icons";

type Sort = "duration" | "distance" | "straight";

const SORTS: { value: Sort; label: string; phrase: string }[] = [
  { value: "duration", label: "Travel time", phrase: "travel time" },
  { value: "distance", label: "Road distance", phrase: "road distance" },
  { value: "straight", label: "Straight line", phrase: "straight-line distance" },
];

/** The right end of each slider means "no limit". */
const TIME_MAX = 300;
const DIST_MAX = 200;

interface Settings {
  maxTime: number;
  maxDist: number;
  sort: Sort;
  limit: number;
  province: string; // "" = any
  district: string;
  category: string;
  type: string;
  designation: string;
}

const defaults = (limit: number): Settings => ({
  maxTime: TIME_MAX,
  maxDist: DIST_MAX,
  sort: "duration",
  limit,
  province: "",
  district: "",
  category: "",
  type: "",
  designation: "",
});

const DESIGNATIONS = ["MO", "SHO", "MOIC", "ADDITIONAL MOH", "MO OPD", "ANAESTHESIA", "PAEDIATRICS", "MEDICINE", "SURGERY"];

const fmtKm = (v: number) => `${v.toFixed(1)} km`;
const fmtCoord = (p: LatLng) => `${p.lat.toFixed(5)}, ${p.lng.toFixed(5)}`;
const num = (n: number) => n.toLocaleString("en-US");

function directionsUrl(from: LatLng, to: LatLng) {
  return `https://www.google.com/maps/dir/?api=1&origin=${from.lat},${from.lng}&destination=${to.lat},${to.lng}&travelmode=driving`;
}

function activeFilterCount(s: Settings) {
  return [s.province, s.district, s.category, s.type, s.designation.trim()].filter(Boolean).length;
}

function buildRequest(origin: SelectedPlace, s: Settings): SearchRequest {
  const req: SearchRequest = {
    origin: { lat: origin.lat, lng: origin.lng, label: origin.label },
    limit: s.limit,
    sortBy: s.sort as SearchRequest["sortBy"],
  };
  if (s.maxTime < TIME_MAX) req.maxDurationMin = s.maxTime;
  if (s.maxDist < DIST_MAX) req.maxDistanceKm = s.maxDist;
  if (s.province) req.provinces = [s.province];
  if (s.district) req.districts = [s.district];
  if (s.category) req.categories = [s.category];
  if (s.type) req.types = [s.type];
  if (s.designation.trim()) req.designation = s.designation.trim();
  return req;
}

const timeLabel = (s: Settings) => (s.maxTime >= TIME_MAX ? "No limit" : `Up to ${formatDuration(s.maxTime)}`);
const distLabel = (s: Settings) => (s.maxDist >= DIST_MAX ? "No limit" : `Up to ${s.maxDist} km`);
const timePill = (s: Settings) => (s.maxTime >= TIME_MAX ? "Any travel time" : `Up to ${formatDuration(s.maxTime)}`);
const distPill = (s: Settings) => (s.maxDist >= DIST_MAX ? "Any distance" : `Up to ${s.maxDist} km`);
const filtersPill = (s: Settings) => {
  const n = activeFilterCount(s);
  return n === 0 ? "No filters" : n === 1 ? "1 filter" : `${n} filters`;
};

/* ------------------------------------------------------------------ */
/* Search settings (desktop sidebar, tablet drawer, phone sheet)       */
/* ------------------------------------------------------------------ */

interface SettingsFormProps {
  meta: Meta;
  layout: Layout;
  origin: SelectedPlace | null;
  originText: string;
  setOriginText: (t: string) => void;
  onPlaceSelect: (p: SelectedPlace) => void;
  onClearOrigin: () => void;
  onUseLocation: () => void;
  onPickMap: () => void;
  locating: boolean;
  pickMode: boolean;
  settings: Settings;
  update: (patch: Partial<Settings>) => void;
  showRanking?: boolean;
  autoFocusSearch?: boolean;
}

function SettingsForm(p: SettingsFormProps) {
  const { meta, settings: s, update } = p;
  const big = p.layout !== "desktop";
  const btnSize = big ? "btn-lg" : "btn-sm";
  const districtOptions = useMemo(
    () => (s.province ? meta.districts.filter((d) => meta.districtProvince[d.name] === s.province) : meta.districts),
    [meta, s.province],
  );
  const active = activeFilterCount(s);
  const limitOptions = [...new Set([5, 10, 20, 50, s.limit])].sort((a, b) => a - b);

  return (
    <div className={`settings${big ? " big" : ""}`}>
      <section className="settings-section">
        <h2 className="section-title">Starting point</h2>
        {p.origin ? (
          <div className="origin-card">
            <span className="origin-card-icon">
              <LocateIcon size={18} />
            </span>
            <div className="origin-card-text">
              <span className="origin-card-label">{p.origin.label}</span>
              <span className="mono">{fmtCoord(p.origin)}</span>
            </div>
            <button type="button" className="btn btn-text btn-sm" onClick={p.onClearOrigin}>
              Change
            </button>
          </div>
        ) : (
          <PlaceSearch
            text={p.originText}
            onTextChange={p.setOriginText}
            onSelect={p.onPlaceSelect}
            placeholder="Search a town, address or hospital"
            autoFocus={p.autoFocusSearch}
          />
        )}
        <div className={`btn-row${big ? " two" : ""}`}>
          <button type="button" className={`btn btn-outlined ${btnSize}`} onClick={p.onUseLocation} disabled={p.locating}>
            <LocateIcon size={14} />
            {p.locating ? "Locating…" : "Use my location"}
          </button>
          <button type="button" className={`btn btn-outlined ${btnSize}${p.pickMode ? " pressed" : ""}`} onClick={p.onPickMap}>
            <PinIcon size={14} />
            Pick on map
          </button>
        </div>
      </section>

      <section className="settings-section">
        <div>
          <h2 className="section-title">Travel limits</h2>
          <p className="section-hint">Stations beyond either limit are left out.</p>
        </div>
        <div className="range">
          <div className="range-head">
            <label htmlFor="max-time">Max travel time</label>
            <span className="num strong">{timeLabel(s)}</span>
          </div>
          <input
            id="max-time"
            type="range"
            min={10}
            max={TIME_MAX}
            step={5}
            value={s.maxTime}
            onChange={(e) => update({ maxTime: Number(e.target.value) })}
          />
          <div className="range-ticks" aria-hidden="true">
            <span>0</span>
            <span>1 h</span>
            <span>2 h</span>
            <span>3 h</span>
            <span>4 h</span>
            <span>No limit</span>
          </div>
        </div>
        <div className="range">
          <div className="range-head">
            <label htmlFor="max-dist">Max road distance</label>
            <span className="num strong">{distLabel(s)}</span>
          </div>
          <input
            id="max-dist"
            type="range"
            min={5}
            max={DIST_MAX}
            step={5}
            value={s.maxDist}
            onChange={(e) => update({ maxDist: Number(e.target.value) })}
          />
          <div className="range-ticks" aria-hidden="true">
            <span>0</span>
            <span>50 km</span>
            <span>100 km</span>
            <span>150 km</span>
            <span>No limit</span>
          </div>
        </div>
      </section>

      {p.showRanking && (
        <section className="settings-section">
          <h2 className="section-title" id="rank-label">
            Rank results by
          </h2>
          <Segmented
            label="Rank results by"
            stretch
            options={SORTS.map((o) => ({ value: o.value, label: o.label }))}
            value={s.sort}
            onChange={(v) => update({ sort: v })}
          />
          <div className="inline-field">
            <label htmlFor="count">Number of stations</label>
            <select id="count" className="select" value={s.limit} onChange={(e) => update({ limit: Number(e.target.value) })}>
              {limitOptions.map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
            </select>
          </div>
        </section>
      )}

      <section className="settings-section">
        <div className="section-row">
          <h2 className="section-title">Filters</h2>
          {active > 0 ? (
            <span className="section-row-end">
              <span className="section-hint">{active === 1 ? "1 active" : `${active} active`}</span>
              <button
                type="button"
                className="btn btn-text btn-sm"
                onClick={() => update({ province: "", district: "", category: "", type: "", designation: "" })}
              >
                Clear
              </button>
            </span>
          ) : (
            big && <span className="section-hint">None active</span>
          )}
        </div>
        <div className="field-grid">
          <div className="field">
            <label htmlFor="f-province">Province</label>
            <select
              id="f-province"
              className="select"
              value={s.province}
              onChange={(e) => {
                const province = e.target.value;
                const keep = !province || !s.district || meta.districtProvince[s.district] === province;
                update({ province, district: keep ? s.district : "" });
              }}
            >
              <option value="">Any province</option>
              {meta.provinces.map((o) => (
                <option key={o.name} value={o.name}>
                  {titleCase(o.name)}
                </option>
              ))}
            </select>
          </div>
          <div className="field">
            <label htmlFor="f-district">District / RDHS</label>
            <select id="f-district" className="select" value={s.district} onChange={(e) => update({ district: e.target.value })}>
              <option value="">Any district</option>
              {districtOptions.map((o) => (
                <option key={o.name} value={o.name}>
                  {titleCase(o.name)}
                </option>
              ))}
            </select>
          </div>
          <div className="field">
            <label htmlFor="f-category">Station type</label>
            <select id="f-category" className="select" value={s.category} onChange={(e) => update({ category: e.target.value })}>
              <option value="">Any type</option>
              {meta.categories.map((o) => (
                <option key={o.name} value={o.name}>
                  {o.name}
                </option>
              ))}
            </select>
          </div>
          <div className="field">
            <label htmlFor="f-type">Vacancy type</label>
            <select id="f-type" className="select" value={s.type} onChange={(e) => update({ type: e.target.value })}>
              <option value="">Any vacancy type</option>
              {meta.types.map((o) => (
                <option key={o.name} value={o.name}>
                  {o.name}
                </option>
              ))}
            </select>
          </div>
        </div>
        <div className="field">
          <label htmlFor="f-designation">Designation contains</label>
          <input
            id="f-designation"
            className="input"
            list="designations"
            value={s.designation}
            onChange={(e) => update({ designation: e.target.value })}
            placeholder="e.g. MO, SHO, Anaesthesia"
          />
          <datalist id="designations">
            {DESIGNATIONS.map((d) => (
              <option key={d} value={d} />
            ))}
          </datalist>
          <span className="field-help">Matches any part of the post name.</span>
        </div>
      </section>
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Results                                                            */
/* ------------------------------------------------------------------ */

function ResultRow({
  r,
  origin,
  selected,
  onSelect,
}: {
  r: SearchResult;
  origin: LatLng;
  selected: boolean;
  onSelect: () => void;
}) {
  const name = titleCase(r.station.name);
  const dir = directionsUrl(origin, r.location.location!);
  return (
    <li className={`result${selected ? " on" : ""}`}>
      <span className="rank num" aria-hidden="true">
        {r.rank}
      </span>
      <div className="result-main">
        <button type="button" className="result-name stretch" aria-pressed={selected} onClick={onSelect}>
          {name}
        </button>
        <span className="result-meta">
          {r.station.category} · {titleCase(r.station.district)}
        </span>
        <div className="result-chips">
          <QualityChip entry={r.location} />
          <VacancyChips vacancies={r.vacancies} />
        </div>
      </div>
      <div className="result-metrics num">
        <span className="metric-time">{formatDuration(r.durationMin)}</span>
        <span className="metric-road">
          {fmtKm(r.distanceKm)}
          <span className="long"> by road</span>
        </span>
        <span className="metric-direct">{fmtKm(r.straightLineKm)} straight line</span>
      </div>
      <a className="btn btn-outlined btn-sm dir-full" href={dir} target="_blank" rel="noreferrer" aria-label={`Directions to ${name}`}>
        Directions
        <ExternalIcon size={14} />
      </a>
      <a className="icon-btn dir-icon" href={dir} target="_blank" rel="noreferrer" aria-label={`Directions to ${name}`}>
        <NavigateIcon />
      </a>
    </li>
  );
}

function StationPopup({ r, origin }: { r: SearchResult; origin: LatLng }) {
  return (
    <div className="popup-card">
      <div className="popup-head">
        <span className="popup-title">{titleCase(r.station.name)}</span>
        <span className="popup-meta">
          {r.station.category} · {titleCase(r.station.district)}
        </span>
      </div>
      <div className="popup-metrics num">
        <span className="strong">
          <ClockIcon size={14} />
          {formatDuration(r.durationMin)}
        </span>
        <span className="muted">
          <RouteIcon size={14} />
          {fmtKm(r.distanceKm)}
        </span>
      </div>
      <a className="btn btn-outlined btn-sm btn-full" href={directionsUrl(origin, r.location.location!)} target="_blank" rel="noreferrer">
        Directions
        <ExternalIcon size={14} />
      </a>
    </div>
  );
}

/** Phone map view: the selected station as a bottom sheet with previous / next. */
function StationSheet({
  r,
  index,
  total,
  origin,
  onMove,
}: {
  r: SearchResult;
  index: number;
  total: number;
  origin: LatLng;
  onMove: (delta: number) => void;
}) {
  return (
    <div className="station-sheet" role="region" aria-label="Selected station">
      <span className="sheet-handle" aria-hidden="true" />
      <div className="sheet-head">
        <span className="rank num on" aria-hidden="true">
          {r.rank}
        </span>
        <div className="sheet-title">
          <h2>{titleCase(r.station.name)}</h2>
          <span className="result-meta">
            {r.station.category} · {titleCase(r.station.district)}
          </span>
        </div>
        <span className="num sheet-pos">
          {index + 1} of {total}
        </span>
      </div>
      <div className="result-chips">
        <QualityChip entry={r.location} />
        <VacancyChips vacancies={r.vacancies} />
      </div>
      <dl className="sheet-metrics">
        <div>
          <dt>Travel time</dt>
          <dd className="num">{formatDuration(r.durationMin)}</dd>
        </div>
        <div>
          <dt>By road</dt>
          <dd className="num">{fmtKm(r.distanceKm)}</dd>
        </div>
        <div>
          <dt>Straight line</dt>
          <dd className="num">{fmtKm(r.straightLineKm)}</dd>
        </div>
      </dl>
      <div className="sheet-actions">
        <button type="button" className="icon-btn" aria-label="Previous station" onClick={() => onMove(-1)} disabled={total < 2}>
          <ChevronLeft size={18} />
        </button>
        <a className="btn btn-contained btn-lg btn-full" href={directionsUrl(origin, r.location.location!)} target="_blank" rel="noreferrer">
          <NavigateIcon />
          Directions
        </a>
        <button type="button" className="icon-btn" aria-label="Next station" onClick={() => onMove(1)} disabled={total < 2}>
          <ChevronRight size={18} />
        </button>
      </div>
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Search view                                                        */
/* ------------------------------------------------------------------ */

export default function SearchView({ meta }: { meta: Meta }) {
  const layout = useLayout();
  const phone = layout === "phone";

  const [origin, setOrigin] = useState<SelectedPlace | null>(null);
  const [originText, setOriginText] = useState("");
  const [settings, setSettings] = useState<Settings>(() => defaults(meta.defaultLimit || 10));
  const update = (patch: Partial<Settings>) => setSettings((s) => ({ ...s, ...patch }));

  const [pickMode, setPickMode] = useState(false);
  const [locating, setLocating] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [resp, setResp] = useState<SearchResponse | null>(null);
  const [lastKey, setLastKey] = useState("");
  const [searchCount, setSearchCount] = useState(0);
  const [selected, setSelected] = useState<string | null>(null);
  const [showInfo, setShowInfo] = useState(false);
  const [panelOpen, setPanelOpen] = useState(false); // tablet drawer / phone sheet
  const [view, setView] = useState<"list" | "map">("list"); // phone only
  const listRef = useRef<HTMLElement>(null);

  const currentKey = origin ? JSON.stringify(buildRequest(origin, settings)) : "";
  const dirty = !!resp && !!origin && currentKey !== lastKey;
  const results = resp?.results ?? [];

  // Close the drawer / sheet with Escape.
  useEffect(() => {
    if (!panelOpen) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setPanelOpen(false);
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [panelOpen]);

  // The desktop has no drawer.
  useEffect(() => {
    if (layout === "desktop") setPanelOpen(false);
  }, [layout]);

  async function runSearch(o: SelectedPlace | null = origin, s: Settings = settings) {
    setError(null);
    if (!o) {
      setError("Choose a starting point: search a place, use your location, or pick a point on the map.");
      if (layout === "tablet") setPanelOpen(true);
      return;
    }
    if (!meta.apiKeyConfigured) return;
    const req = buildRequest(o, s);
    setLoading(true);
    try {
      const r = await api.search(req);
      setResp(r);
      setLastKey(JSON.stringify(req));
      setSelected(null);
      setSearchCount((c) => c + 1);
      setPanelOpen(false);
      listRef.current?.scrollTo({ top: 0 });
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setLoading(false);
    }
  }

  function chooseOrigin(p: SelectedPlace) {
    setOrigin(p);
    setPickMode(false);
    runSearch(p);
  }

  function useMyLocation() {
    if (!navigator.geolocation) {
      setError("Location is not available in this browser.");
      return;
    }
    setLocating(true);
    navigator.geolocation.getCurrentPosition(
      (pos) => {
        setLocating(false);
        chooseOrigin({ lat: pos.coords.latitude, lng: pos.coords.longitude, label: "My location" });
      },
      (err) => {
        setLocating(false);
        setError(`Location unavailable: ${err.message}`);
      },
      { enableHighAccuracy: true, timeout: 15000 },
    );
  }

  function startPick() {
    if (pickMode) {
      setPickMode(false);
      return;
    }
    setPickMode(true);
    setPanelOpen(false);
    if (phone) setView("map");
  }

  function reset() {
    setSettings(defaults(meta.defaultLimit || 10));
  }

  function changeSort(sort: Sort) {
    const next = { ...settings, sort };
    setSettings(next);
    if (origin && resp) runSearch(origin, next);
  }

  function showMore() {
    const next = { ...settings, limit: results.length + 10 };
    setSettings(next);
    runSearch(origin, next);
  }

  const sortInfo = SORTS.find((o) => o.value === settings.sort) ?? SORTS[0];
  const moreAvailable = resp ? Math.min(10, resp.stats.withinLimits - results.length) : 0;

  // Phone map view shows one station at a time; default to the first.
  const phoneSelIndex = Math.max(0, results.findIndex((r) => r.station.id === selected));
  const effectiveSelected = phone && view === "map" && results.length ? results[phoneSelIndex].station.id : selected;
  const selResult = results.find((r) => r.station.id === effectiveSelected);

  const markers: MapMarker[] = results.map((r) => ({
    id: r.station.id,
    pos: r.location.location!,
    label: String(r.rank),
    tone: "good",
  }));

  const originOnMap = origin ?? (resp ? { lat: resp.origin.lat, lng: resp.origin.lng } : null);
  const originLabel = origin?.label ?? "Starting point";

  const formProps: SettingsFormProps = {
    meta,
    layout,
    origin,
    originText,
    setOriginText,
    onPlaceSelect: chooseOrigin,
    onClearOrigin: () => {
      setOrigin(null);
      setOriginText("");
    },
    onUseLocation: useMyLocation,
    onPickMap: startPick,
    locating,
    pickMode,
    settings,
    update,
  };

  const searchLabel = loading ? "Calculating routes…" : dirty ? "Update results" : "Find stations";
  const dataLine = (
    <span className="data-line">
      Data: {num(meta.vacancyCount)} vacancies at {num(meta.stationCount)} stations
      {meta.geocoding.running && ` · locating ${meta.geocoding.done}/${meta.geocoding.total}…`}
    </span>
  );
  const apiKeyAlert = !meta.apiKeyConfigured && (
    <Alert severity="error" title="Google API key missing">
      Set google.apiKey in config.yaml (or GOOGLE_MAPS_API_KEY) and restart the backend.
    </Alert>
  );

  /* ----- results pane ----- */

  const resultsHeader = resp && (
    <div className="results-head">
      <div className="results-title-row">
        <div>
          <h1 className="results-title">
            {results.length === 0
              ? "No stations found"
              : `${results.length} nearest station${results.length === 1 ? "" : "s"}`}
          </h1>
          {!phone && (
            <p className="results-sub">
              From {originLabel}, ranked by {sortInfo.phrase}
            </p>
          )}
        </div>
        {phone && results.length > 0 && (
          <button
            type="button"
            className="pill-btn"
            onClick={() => changeSort(SORTS[(SORTS.indexOf(sortInfo) + 1) % SORTS.length].value)}
            aria-label={`Sorted by ${sortInfo.label.toLowerCase()}. Sort by ${SORTS[(SORTS.indexOf(sortInfo) + 1) % SORTS.length].label.toLowerCase()} instead`}
          >
            <SortIcon size={14} />
            {sortInfo.label}
          </button>
        )}
      </div>

      <div className="stats stats-pipeline">
        <span>
          <strong className="num">{num(resp.stats.stationsMatchingFilters)}</strong> match your filters
        </span>
        <ChevronRight size={14} />
        <span className="stats-info">
          <span>
            <strong className="num">{resp.stats.routeCandidates}</strong> nearest checked by road
          </span>
          <button
            type="button"
            className="info-btn"
            onClick={() => setShowInfo((v) => !v)}
            aria-expanded={showInfo}
            aria-label={`Why only ${resp.stats.routeCandidates} stations are checked`}
          >
            <InfoIcon size={15} />
          </button>
        </span>
        <ChevronRight size={14} />
        <span>
          <strong className="num">{resp.stats.withinLimits}</strong> within your limits
        </span>
      </div>
      <div className="stats stats-compact">
        <span>
          <strong className="num">{resp.stats.withinLimits}</strong> of {num(resp.stats.stationsMatchingFilters)} matching
          stations are within your limits
        </span>
        <button
          type="button"
          className="info-btn"
          onClick={() => setShowInfo((v) => !v)}
          aria-expanded={showInfo}
          aria-label={`Why only ${resp.stats.routeCandidates} stations are checked`}
        >
          <InfoIcon size={14} />
        </button>
      </div>
      {showInfo && (
        <p className="info-box">
          To keep searches fast, road routes are calculated only for the {resp.stats.routeCandidates} stations closest in a
          straight line. A station that is farther away in a straight line but quicker by road may not appear.
          {resp.stats.withoutLocation > 0 &&
            ` ${resp.stats.withoutLocation} matching station${resp.stats.withoutLocation === 1 ? " has" : "s have"} no location yet and ${resp.stats.withoutLocation === 1 ? "is" : "are"} skipped.`}
        </p>
      )}

      {(!phone || results.length > 0) && (
        <div className="results-tools">
          {!phone && (
            <Segmented
              label="Sort by"
              options={SORTS.map((o) => ({ value: o.value, label: o.label }))}
              value={settings.sort}
              onChange={changeSort}
            />
          )}
          <TypeLegend vacancies={results.flatMap((r) => r.vacancies)} />
        </div>
      )}
    </div>
  );

  const emptyState = !resp && (
    <div className="empty">
      <span className="empty-icon">
        <PinIcon size={22} />
      </span>
      <h1 className="results-title">Find stations near you</h1>
      <p>
        Choose a starting point: search a town, address or hospital, use your location, or pick a point on the map. Stations
        are ranked by real road travel time.
      </p>
      {layout !== "desktop" && (
        <div className="btn-row center">
          <button type="button" className="btn btn-contained btn-lg" onClick={() => setPanelOpen(true)}>
            <SearchIcon />
            Search a place
          </button>
          <button type="button" className="btn btn-outlined btn-lg" onClick={useMyLocation} disabled={locating}>
            <LocateIcon size={15} />
            {locating ? "Locating…" : "Use my location"}
          </button>
        </div>
      )}
    </div>
  );

  const resultsPane = (
    <section className={`results-pane${loading ? " busy" : ""}`} aria-label="Results" ref={listRef}>
      {apiKeyAlert && <div className="pane-alert">{apiKeyAlert}</div>}
      {error && !panelOpen && (
        <div className="pane-alert">
          <Alert severity="error">{error}</Alert>
        </div>
      )}
      {emptyState}
      {resultsHeader}
      {dirty && (
        <div className="pane-alert">
          <Alert severity="info" title="Search settings changed" actionLabel="Update results" onAction={() => runSearch()}>
            The list and map still show results for your previous settings.
          </Alert>
        </div>
      )}
      {/* The routing cap is explained by the (i) button next to the counts. */}
      {resp?.warnings
        ?.filter((w) => !w.startsWith("Only the "))
        .map((w) => (
        <div className="pane-alert" key={w}>
          <Alert severity="warning">{w}</Alert>
        </div>
      ))}
      {resp && results.length === 0 && <p className="empty-results">No stations match. Try relaxing the limits or filters.</p>}
      {resp && results.length > 0 && (
        <ol className="result-list">
          {results.map((r) => (
            <ResultRow
              key={r.station.id}
              r={r}
              origin={resp.origin}
              selected={selected === r.station.id}
              onSelect={() => {
                setSelected(r.station.id);
                if (phone) setView("map");
              }}
            />
          ))}
        </ol>
      )}
      {moreAvailable > 0 && (
        <div className="more">
          <button type="button" className={`btn btn-outlined ${phone ? "btn-lg" : "btn-md"}`} onClick={showMore} disabled={loading}>
            Show {moreAvailable} more station{moreAvailable === 1 ? "" : "s"}
          </button>
        </div>
      )}
      {loading && (
        <div className="busy-overlay" role="status">
          <span className="spinner" />
          Calculating routes…
        </div>
      )}
    </section>
  );

  const mapPane = (
    <section className="map-pane" aria-label="Map of results">
      {pickMode && (
        <div className="map-hint" role="status">
          <span>{phone ? "Tap" : "Click"} the map to set the starting point</span>
          <button type="button" className="btn btn-text btn-sm" onClick={() => setPickMode(false)}>
            Cancel
          </button>
        </div>
      )}
      <div className="map-area">
        <StationMap
          origin={originOnMap}
          originLabel={originLabel}
          markers={markers}
          selectedId={effectiveSelected}
          selectedPopup={!phone && selResult && resp ? <StationPopup r={selResult} origin={resp.origin} /> : undefined}
          onPopupClose={() => setSelected(null)}
          onMarkerClick={setSelected}
          fitKey={`${searchCount}|${origin ? fmtCoord(origin) : ""}|${view}`}
          crosshair={pickMode}
          showLink
          compactControls={phone}
          onMapClick={(p) => {
            if (!pickMode) return;
            chooseOrigin({ ...p, label: "Point on map" });
          }}
        />
      </div>
      {phone && selResult && resp && (
        <StationSheet
          r={selResult}
          index={phoneSelIndex}
          total={results.length}
          origin={resp.origin}
          onMove={(d) => setSelected(results[(phoneSelIndex + d + results.length) % results.length].station.id)}
        />
      )}
    </section>
  );

  /* ----- layouts ----- */

  const footer = (
    <div className="panel-footer">
      <button type="button" className="btn btn-contained btn-lg btn-full" onClick={() => runSearch()} disabled={loading || !meta.apiKeyConfigured}>
        <SearchIcon />
        {searchLabel}
      </button>
      {error && panelOpen && <Alert severity="error">{error}</Alert>}
      <div className="panel-footer-row">
        {dataLine}
        <button type="button" className="btn btn-text btn-sm" onClick={reset}>
          <ResetIcon size={14} />
          Reset all
        </button>
      </div>
    </div>
  );

  if (layout === "desktop") {
    return (
      <div className="sv sv-desktop">
        <aside className="sv-panel" aria-label="Search settings">
          <SettingsForm {...formProps} />
          {footer}
        </aside>
        <div className="sv-body">
          {resultsPane}
          {mapPane}
        </div>
      </div>
    );
  }

  if (layout === "tablet") {
    return (
      <div className="sv sv-tablet">
        <div className="sv-toolbar" role="toolbar" aria-label="Search settings">
          <button type="button" className="pill-btn strong" onClick={() => setPanelOpen(true)}>
            <LocateIcon size={16} className="tint-blue" />
            {origin ? origin.label : "Choose a starting point"}
          </button>
          <button type="button" className="pill-btn" onClick={() => setPanelOpen(true)}>
            <ClockIcon />
            {timePill(settings)}
          </button>
          <button type="button" className="pill-btn" onClick={() => setPanelOpen(true)}>
            <RouteIcon />
            {distPill(settings)}
          </button>
          <button type="button" className="pill-btn" onClick={() => setPanelOpen(true)}>
            <SlidersIcon />
            {filtersPill(settings)}
          </button>
        </div>
        <div className="sv-body">
          {resultsPane}
          {mapPane}
        </div>
        {panelOpen && (
          <>
            <div className="scrim" onClick={() => setPanelOpen(false)} />
            <aside className="drawer" role="dialog" aria-modal="true" aria-label="Search settings">
              <div className="drawer-head">
                <h2>Search settings</h2>
                <button type="button" className="icon-btn plain" aria-label="Close search settings" onClick={() => setPanelOpen(false)}>
                  <CloseIcon size={20} />
                </button>
              </div>
              <div className="drawer-body">
                <SettingsForm {...formProps} autoFocusSearch={!origin} />
              </div>
              {footer}
            </aside>
          </>
        )}
      </div>
    );
  }

  // Phone
  return (
    <div className="sv sv-phone">
      <div className="phone-top">
        <button type="button" className="summary" onClick={() => setPanelOpen(true)}>
          <span className="summary-icon">
            <LocateIcon />
          </span>
          <span className="summary-text">
            <span className="summary-title">{origin ? origin.label : "Choose a starting point"}</span>
            <span className="summary-sub">
              {timePill(settings)} · {distPill(settings)} · {filtersPill(settings)}
            </span>
          </span>
          <span className="summary-action" aria-hidden="true">
            <SlidersIcon />
          </span>
        </button>
        <nav className="view-toggle" aria-label="Results view">
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
      <div className="sv-body">{view === "list" ? resultsPane : mapPane}</div>

      {panelOpen && (
        <div className="sheet" role="dialog" aria-modal="true" aria-label="Search settings">
          <div className="sheet-header">
            <button type="button" className="icon-btn plain" aria-label="Close search settings" onClick={() => setPanelOpen(false)}>
              <CloseIcon size={20} />
            </button>
            <h1>Search settings</h1>
            <button type="button" className="btn btn-text btn-lg" onClick={reset}>
              Reset
            </button>
          </div>
          <div className="sheet-body">
            <SettingsForm {...formProps} showRanking autoFocusSearch={!origin} />
            {error && <Alert severity="error">{error}</Alert>}
            {dataLine}
          </div>
          <div className="sheet-footer">
            <button
              type="button"
              className="btn btn-contained btn-lg btn-full"
              onClick={() => {
                setView("list");
                runSearch();
              }}
              disabled={loading || !meta.apiKeyConfigured}
            >
              <SearchIcon />
              {searchLabel}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

