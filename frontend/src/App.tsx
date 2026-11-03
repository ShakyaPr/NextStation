import { useCallback, useEffect, useState } from "react";
import { api, type Meta } from "./api";
import SearchView from "./SearchView";
import StationsView from "./StationsView";

type Tab = "search" | "stations";

export default function App() {
  const [meta, setMeta] = useState<Meta | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>("search");

  const refresh = useCallback(() => {
    api
      .meta()
      .then((m) => {
        setMeta(m);
        setError(null);
      })
      .catch((e) => setError(`Cannot reach backend: ${e.message}`));
  }, []);

  useEffect(refresh, [refresh]);

  // Poll while geocoding is running.
  useEffect(() => {
    if (!meta?.geocoding.running) return;
    const t = setInterval(refresh, 2000);
    return () => clearInterval(t);
  }, [meta?.geocoding.running, refresh]);

  return (
    <div className="app">
      <header className="topbar">
        <div className="brand">
          Station Finder
          {meta && (
            <span className="muted small">
              {meta.vacancyCount} vacancies · {meta.stationCount} stations · {meta.locatedCount} located
              {meta.geocoding.running && ` · locating ${meta.geocoding.done}/${meta.geocoding.total}…`}
            </span>
          )}
        </div>
        <nav className="tabs">
          <button className={tab === "search" ? "active" : ""} onClick={() => setTab("search")}>
            Find stations
          </button>
          <button className={tab === "stations" ? "active" : ""} onClick={() => setTab("stations")}>
            Station locations
            {meta && meta.suspectCount + (meta.stationCount - meta.locatedCount) > 0 && (
              <span className="dot" title="Some stations need review" />
            )}
          </button>
        </nav>
      </header>
      {error && <p className="error banner">{error}</p>}
      {meta && tab === "search" && <SearchView meta={meta} />}
      {meta && tab === "stations" && <StationsView meta={meta} onChanged={refresh} />}
    </div>
  );
}
