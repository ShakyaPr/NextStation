// Choreo managed authentication (backend-for-frontend).
//
// When `managedAuth` is on, Choreo serves these endpoints next to the web app:
//   /auth/login     - start sign-in (redirects to the identity provider)
//   /auth/logout    - sign out; needs ?session_hint=<session_hint cookie>
//   /auth/userinfo  - 200 + user JSON when signed in, 401 otherwise
// After sign-in Choreo sets a short-lived `userinfo` cookie (base64 JSON claims).
// API calls through /choreo-apis/... are authorised by Choreo's session cookies,
// so the browser never handles an access token.

declare global {
  interface Window {
    configs?: { apiUrl?: string; managedAuth?: boolean };
  }
}

export const runtimeConfig = {
  apiUrl: (window.configs?.apiUrl ?? "").replace(/\/+$/, ""),
  managedAuth: window.configs?.managedAuth === true,
};

export interface User {
  sub?: string;
  name?: string;
  given_name?: string;
  family_name?: string;
  email?: string;
  username?: string;
  [claim: string]: unknown;
}

const USER_KEY = "stationFinder.user";
const REDIRECT_KEY = "stationFinder.loginRedirectAt";

function getCookie(name: string): string | null {
  const m = document.cookie.match(new RegExp(`(?:^|; )${name}=([^;]*)`));
  return m ? decodeURIComponent(m[1]) : null;
}

function deleteCookie(name: string) {
  document.cookie = `${name}=; Max-Age=0; path=/`;
}

function decodeUserInfo(encoded: string): User | null {
  try {
    const bytes = Uint8Array.from(atob(encoded), (c) => c.charCodeAt(0));
    return JSON.parse(new TextDecoder().decode(bytes)) as User;
  } catch {
    return null;
  }
}

function safeSession<T>(fn: () => T): T | null {
  try {
    return fn();
  } catch {
    return null; // storage can be unavailable (private mode, blocked site data)
  }
}

/** Returns the signed-in user, or null if not signed in. Only meaningful with managed auth. */
export async function loadUser(): Promise<User | null> {
  // 1. Right after sign-in: Choreo hands over the claims in the `userinfo` cookie.
  const cookie = getCookie("userinfo");
  if (cookie) {
    deleteCookie("userinfo");
    const user = decodeUserInfo(cookie);
    if (user) {
      safeSession(() => sessionStorage.setItem(USER_KEY, JSON.stringify(user)));
      return user;
    }
  }
  // 2. Ask Choreo whether the session is still valid.
  try {
    const res = await fetch("/auth/userinfo", { credentials: "include" });
    if (res.status === 401) {
      safeSession(() => sessionStorage.removeItem(USER_KEY));
      return null;
    }
    if (res.ok && res.headers.get("content-type")?.includes("json")) {
      const user = (await res.json()) as User;
      safeSession(() => sessionStorage.setItem(USER_KEY, JSON.stringify(user)));
      return user;
    }
  } catch {
    // fall through
  }
  // 3. Fallback: what we stored at sign-in during this browser session.
  const stored = safeSession(() => sessionStorage.getItem(USER_KEY));
  return stored ? (JSON.parse(stored) as User) : null;
}

export function login() {
  safeSession(() => sessionStorage.setItem(REDIRECT_KEY, String(Date.now())));
  window.location.href = "/auth/login";
}

export function logout() {
  safeSession(() => sessionStorage.removeItem(USER_KEY));
  const hint = getCookie("session_hint") ?? "";
  window.location.href = `/auth/logout?session_hint=${encodeURIComponent(hint)}`;
}

/** Error reported by Choreo on the error path (?code=...&message=...). */
export function authErrorFromUrl(): string | null {
  const p = new URLSearchParams(window.location.search);
  const code = p.get("code");
  if (!code) return null;
  return `${p.get("message") ?? "Sign-in failed"} (${code})`;
}

export function displayName(u: User): string {
  const full = [u.given_name, u.family_name].filter(Boolean).join(" ");
  return (u.name as string) || full || u.email || u.username || "Signed in";
}

/**
 * fetch() for backend API paths like "api/meta". Prefixes the configured API base URL.
 * With managed auth, a 401 means the session expired: send the user to sign in again
 * (but not in a loop, if the API keeps rejecting a fresh session).
 */
export async function apiFetch(path: string, init: RequestInit = {}): Promise<Response> {
  const url = runtimeConfig.apiUrl ? `${runtimeConfig.apiUrl}/${path}` : path;
  const res = await fetch(url, { credentials: "same-origin", ...init });
  if (res.status === 401 && runtimeConfig.managedAuth) {
    const last = Number(safeSession(() => sessionStorage.getItem(REDIRECT_KEY)) ?? 0);
    if (Date.now() - last > 60_000) {
      login();
      return new Promise<Response>(() => {}); // navigating away
    }
    throw new Error(
      "The API rejected your session (401). Check that the web app has a connection to the service.",
    );
  }
  if (res.status === 403) {
    throw new Error("You don't have permission to do this.");
  }
  return res;
}
