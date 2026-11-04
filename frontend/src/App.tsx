import { useCallback, useEffect, useState } from "react";
import { api, type Meta } from "./api";
import { authErrorFromUrl, displayName, loadUser, login, logout, runtimeConfig, type User } from "./auth";
import SearchView from "./SearchView";
import StationsView from "./StationsView";

type Tab = "search" | "stations";
type AuthState = { status: "checking" } | { status: "signedOut" } | { status: "signedIn"; user: User | null };

function SignIn({ error }: { error: string | null }) {
  return (
    <div className="signin">
      <div className="signin-card">
        <h1>Station Finder</h1>
        <p className="muted">
          Find the vacancy stations that best match your location, travel time and preferences.
        </p>
        <button className="btn primary wide" onClick={login}>
          Sign in
        </button>
        <p className="muted small">Access is limited to invited users.</p>
        {error && <p className="error">{error}</p>}
      </div>
    </div>
  );
}

export default function App() {
  const [auth, setAuth] = useState<AuthState>(
    runtimeConfig.managedAuth ? { status: "checking" } : { status: "signedIn", user: null },
  );
  const [authError] = useState(authErrorFromUrl);
  const [meta, setMeta] = useState<Meta | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>("search");

  useEffect(() => {
    if (!runtimeConfig.managedAuth) return;
    loadUser().then((user) => setAuth(user ? { status: "signedIn", user } : { status: "signedOut" }));
    // Drop ?code=&message= from the address bar once read.
    if (authErrorFromUrl()) window.history.replaceState(null, "", window.location.pathname);
  }, []);

  const refresh = useCallback(() => {
    api
      .meta()
      .then((m) => {
        setMeta(m);
        setError(null);
      })
      .catch((e) => setError(`Cannot reach the backend: ${e.message}`));
  }, []);

  const signedIn = auth.status === "signedIn";
  useEffect(() => {
    if (signedIn) refresh();
  }, [signedIn, refresh]);

  // Poll while geocoding is running.
  useEffect(() => {
    if (!meta?.geocoding.running) return;
    const t = setInterval(refresh, 2000);
    return () => clearInterval(t);
  }, [meta?.geocoding.running, refresh]);

  if (auth.status === "checking") {
    return <div className="signin muted">Checking sign-in…</div>;
  }
  if (auth.status === "signedOut") {
    return <SignIn error={authError} />;
  }

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
          {runtimeConfig.managedAuth && (
            <div className="user">
              {auth.user && <span className="muted small">{displayName(auth.user)}</span>}
              <button className="btn small" onClick={logout}>
                Sign out
              </button>
            </div>
          )}
        </nav>
      </header>
      {error && <p className="error banner">{error}</p>}
      {authError && <p className="error banner">{authError}</p>}
      {meta && tab === "search" && <SearchView meta={meta} />}
      {meta && tab === "stations" && <StationsView meta={meta} onChanged={refresh} />}
    </div>
  );
}
