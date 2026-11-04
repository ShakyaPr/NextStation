// Local stand-in for a Choreo web app with managed authentication.
//
//   cd frontend && npm run build && npm run mock:choreo
//   open http://localhost:5174   (backend must be running on :8080)
//
// It imitates what Choreo provides so the sign-in flow can be tried before deploying:
//   /config.js        managed-auth runtime config (instead of the file mount)
//   /auth/login       "signs in" (?user=admin for an admin) and sets the cookies
//   /auth/userinfo    200 + claims when signed in, 401 otherwise
//   /auth/logout      clears the session
//   /choreo-apis/...  proxied to the API; 401 without a session, 403 for admin-only
//                     operations when the user is not an admin (like Choreo's gateway)
// Not a security implementation - local testing only.
import http from "node:http";
import { readFile } from "node:fs/promises";
import { extname, join, normalize } from "node:path";
import { fileURLToPath } from "node:url";

const PORT = Number(process.env.PORT ?? 5174);
const API = process.env.API_URL ?? "http://localhost:8080";
const API_PREFIX = "/choreo-apis/station-finder/api/v1.0";
const DIST = fileURLToPath(new URL("../dist/", import.meta.url));
const ADMIN_ONLY = [/^PUT \/api\/stations\/[^/]+\/location$/, /^DELETE \/api\/stations\/[^/]+\/location$/, /^POST \/api\/geocode$/];
const TYPES = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" };

const sessions = new Map(); // session id -> user claims

function cookies(req) {
  return Object.fromEntries(
    (req.headers.cookie ?? "").split(";").filter(Boolean).map((c) => {
      const i = c.indexOf("=");
      return [c.slice(0, i).trim(), decodeURIComponent(c.slice(i + 1))];
    }),
  );
}

function userOf(req) {
  return sessions.get(cookies(req).session) ?? null;
}

function send(res, status, body, headers = {}) {
  res.writeHead(status, { "Content-Type": "application/json", ...headers });
  res.end(typeof body === "string" ? body : JSON.stringify(body));
}

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, `http://${req.headers.host}`);
  const path = url.pathname;

  if (path === "/config.js") {
    res.writeHead(200, { "Content-Type": "text/javascript" });
    return res.end(`window.configs = { apiUrl: "${API_PREFIX}", managedAuth: true };`);
  }

  if (path === "/auth/login") {
    const admin = url.searchParams.get("user") === "admin";
    const user = admin
      ? { sub: "u-admin", name: "Admin User", email: "admin@example.com", groups: ["admins"] }
      : { sub: "u-1", name: "Test User", email: "user@example.com", groups: [] };
    const id = crypto.randomUUID();
    sessions.set(id, user);
    const info = Buffer.from(JSON.stringify(user)).toString("base64");
    res.writeHead(302, {
      Location: "/",
      "Set-Cookie": [
        `session=${id}; Path=/; HttpOnly; SameSite=Lax`,
        `session_hint=${id}; Path=/; SameSite=Lax`,
        `userinfo=${encodeURIComponent(info)}; Path=/; Max-Age=120; SameSite=Lax`,
      ],
    });
    return res.end();
  }

  if (path === "/auth/userinfo") {
    const user = userOf(req);
    return user ? send(res, 200, user) : send(res, 401, { error: "not signed in" });
  }

  if (path === "/auth/logout") {
    sessions.delete(url.searchParams.get("session_hint") ?? "");
    res.writeHead(302, {
      Location: "/",
      "Set-Cookie": ["session=; Path=/; Max-Age=0", "session_hint=; Path=/; Max-Age=0"],
    });
    return res.end();
  }

  if (path.startsWith(API_PREFIX + "/")) {
    const user = userOf(req);
    if (!user) return send(res, 401, { error: "invalid credentials" });
    const apiPath = path.slice(API_PREFIX.length);
    if (ADMIN_ONLY.some((re) => re.test(`${req.method} ${apiPath}`)) && !user.groups.includes("admins")) {
      return send(res, 403, { error: "insufficient scope" });
    }
    const chunks = [];
    for await (const c of req) chunks.push(c);
    try {
      const upstream = await fetch(API + apiPath + url.search, {
        method: req.method,
        headers: { "Content-Type": req.headers["content-type"] ?? "application/json" },
        body: chunks.length ? Buffer.concat(chunks) : undefined,
      });
      res.writeHead(upstream.status, { "Content-Type": upstream.headers.get("content-type") ?? "application/json" });
      return res.end(Buffer.from(await upstream.arrayBuffer()));
    } catch (e) {
      return send(res, 502, { error: `API unreachable at ${API}: ${e.message}` });
    }
  }

  // Static files from dist/, falling back to index.html.
  const file = normalize(join(DIST, path === "/" ? "index.html" : path));
  if (!file.startsWith(DIST)) return send(res, 400, { error: "bad path" });
  try {
    const body = await readFile(file);
    res.writeHead(200, { "Content-Type": TYPES[extname(file)] ?? "application/octet-stream" });
    res.end(body);
  } catch {
    res.writeHead(200, { "Content-Type": "text/html" });
    res.end(await readFile(join(DIST, "index.html")));
  }
});

server.listen(PORT, () => {
  console.log(`Mock Choreo web app on http://localhost:${PORT} (API: ${API})`);
  console.log(`Sign in as a normal user via the UI, or as admin: http://localhost:${PORT}/auth/login?user=admin`);
});
