import { useCallback, useEffect, useRef, useState } from "react";
import { api, type Meta } from "./api";
import { authErrorFromUrl, displayName, loadUser, login, logout, runtimeConfig, type User } from "./auth";
import SearchView from "./SearchView";
import StationsView from "./StationsView";
import { useIsPhone } from "./useMediaQuery";
import { Alert, Avatar, BrandMark } from "./ui";
import { PinIcon, SearchIcon } from "./icons";

type Tab = "search" | "stations";
type AuthState = { status: "checking" } | { status: "signedOut" } | { status: "signedIn"; user: User | null };

function SignIn({ error }: { error: string | null }) {
  return (
    <div className="signin">
      <div className="signin-card">
        <div className="brand">
          <BrandMark />
          <span className="brand-name">Station Finder</span>
        </div>
        <h1>Sign in to continue</h1>
        <p className="muted">Find the vacancy stations that best match your location, travel time and preferences.</p>
        <button className="btn btn-contained btn-lg btn-full" onClick={login}>
          Sign in
        </button>
        <p className="muted small">Access is limited to invited users.</p>
        {error && <Alert severity="error">{error}</Alert>}
      </div>
    </div>
  );
}

function AccountMenu({ user }: { user: User | null }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const name = user ? displayName(user) : "Account";
  useEffect(() => {
    if (!open) return;
    const close = (e: Event) => !ref.current?.contains(e.target as Node) && setOpen(false);
    document.addEventListener("pointerdown", close);
    return () => document.removeEventListener("pointerdown", close);
  }, [open]);
  return (
    <div className="account" ref={ref}>
      <button type="button" className="avatar-btn" aria-label={`Account: ${name}`} aria-expanded={open} onClick={() => setOpen((o) => !o)}>
        <Avatar name={name} />
      </button>
      {open && (
        <div className="account-menu" role="menu">
          <span className="account-name">{name}</span>
          <button type="button" className="btn btn-outlined btn-lg btn-full" role="menuitem" onClick={logout}>
            Sign out
          </button>
        </div>
      )}
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
  const phone = useIsPhone();

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
    return (
      <div className="signin">
        <span className="muted">
          <span className="spinner" /> Checking sign-in…
        </span>
      </div>
    );
  }
  if (auth.status === "signedOut") {
    return <SignIn error={authError} />;
  }

  const needsReview = !!meta && meta.suspectCount + (meta.stationCount - meta.locatedCount) > 0;
  const user = auth.user;
  const name = user ? displayName(user) : "";

  return (
    <div className="app">
      <header className="topbar">
        <div className="topbar-start">
          <div className="brand">
            <BrandMark />
            <span className="brand-name">Station Finder</span>
          </div>
          {!phone && (
            <nav className="tabs" aria-label="Main">
              <button type="button" aria-current={tab === "search" ? "page" : undefined} onClick={() => setTab("search")}>
                Find stations
              </button>
              <button type="button" aria-current={tab === "stations" ? "page" : undefined} onClick={() => setTab("stations")}>
                Station locations
                {needsReview && <span className="dot" title="Some stations need review" />}
              </button>
            </nav>
          )}
        </div>
        {runtimeConfig.managedAuth &&
          (phone ? (
            <AccountMenu user={user} />
          ) : (
            <div className="user">
              {name && <Avatar name={name} />}
              {name && <span className="user-name">{name}</span>}
              <button type="button" className="btn btn-text btn-sm" onClick={logout}>
                Sign out
              </button>
            </div>
          ))}
      </header>

      {(error || authError) && (
        <div className="banner">
          {error && <Alert severity="error">{error}</Alert>}
          {authError && <Alert severity="error">{authError}</Alert>}
        </div>
      )}

      <main className="app-main">
        {/* The search stays mounted so its results survive a tab switch. */}
        {meta && (
          <div className="tab-panel" hidden={tab !== "search"}>
            <SearchView meta={meta} />
          </div>
        )}
        {meta && tab === "stations" && (
          <div className="tab-panel">
            <StationsView meta={meta} onChanged={refresh} />
          </div>
        )}
        {!meta && !error && (
          <div className="loading">
            <span className="spinner" /> Loading…
          </div>
        )}
      </main>

      {phone && (
        <nav className="bottom-nav" aria-label="Main">
          <button type="button" aria-current={tab === "search" ? "page" : undefined} onClick={() => setTab("search")}>
            <span className="bottom-nav-icon">
              <SearchIcon size={18} />
            </span>
            Find stations
          </button>
          <button type="button" aria-current={tab === "stations" ? "page" : undefined} onClick={() => setTab("stations")}>
            <span className="bottom-nav-icon">
              <PinIcon size={18} />
              {needsReview && <span className="dot" aria-hidden="true" />}
            </span>
            Station locations
          </button>
        </nav>
      )}
    </div>
  );
}
