# Station Finder

Finds the most suitable stations from the *Draft vacancy list for preliminary grade
appointments (Oct/Nov 2026)* for a given starting point. It ranks them by real road
travel time or distance, optionally within a time or distance limit.

```
frontend/  React + Vite + Leaflet UI         (http://localhost:5173)
backend/   Go API server                      (http://localhost:8080)
data/      vacancies.json (from the PDF), geocache.json, overrides.json
tools/     extract_pdf.py  (PDF → data/vacancies.json)
```

## How it works

1. **Data.** `tools/extract_pdf.py` pulled 1,300 vacancy rows out of the PDF and
   stripped the "DRAFT" watermark. The backend groups them into 438 unique stations
   (district + institution).
2. **Station locations.** On first start, the backend looks up each station once and
   caches the result in `data/geocache.json`. It tries these in order:
   - Places API (New) Text Search, with abbreviations expanded
     (e.g. `DELTOTA DH` → "Deltota Divisional Hospital, Kandy District, Sri Lanka")
   - Geocoding API with the same query
   - Geocoding API with the town name only. These are marked **Town centre**.

   Any result more than 75 km from its district centre is flagged **Check location**.
   You can fix it in the UI's *Station locations* tab: select the station, click
   *Set location manually*, then click the map. Manual fixes are saved to
   `data/overrides.json` and always win over the cache.
3. **Search.** The backend:
   - resolves the origin (a place name, or a point picked on the map)
   - applies the filters
   - pre-filters by straight-line distance, which is safe because road distance is
     always at least the straight-line distance
   - sends the nearest candidates (up to `maxRouteCandidates`) to the Routes API
     `computeRouteMatrix`
   - applies the time/distance limits and returns the top N

   Route results are cached in memory.

## Setup

### 1. Google API key

In Google Cloud Console, enable these APIs on the key's project:
- **Places API (New)**
- **Geocoding API**
- **Routes API**

The key is used only by the backend and never reaches the browser. The map uses
OpenStreetMap tiles, so the frontend needs no key.

### 2. Config

```bash
cp config.example.yaml config.yaml
# edit config.yaml → google.apiKey: "AIza..."
```

(`config.yaml` is git-ignored. You can also set the key with `GOOGLE_MAPS_API_KEY=...`.)

### 3. Run the backend

```bash
cd backend
go run ./cmd/server -config ../config.yaml
```

The first start geocodes all 438 stations in the background, which takes about a
minute. Progress shows in the UI header. Later starts use `data/geocache.json`, so
those lookups are not repeated.

### 4. Run the frontend

```bash
cd frontend
npm install
npm run dev
```

Open http://localhost:5173. The Vite dev server proxies `/api` to `:8080`.

To run a single process instead, run `npm run build` in `frontend/`. The backend then
serves the UI at http://localhost:8080 (`server.staticDir` in your local `config.yaml`).

## Docker (API service)

```bash
docker build -t station-finder-api .
docker run -p 8080:8080 -e GOOGLE_MAPS_API_KEY=your-key station-finder-api
curl localhost:8080/healthz
```

The image contains only the Go API (distroless, ~10 MB). It doesn't include the UI,
which is deployed separately on Choreo. It uses `deploy/config.yaml`. Your local
`config.yaml` is excluded by `.dockerignore`, so the API key is never baked into the
image; pass it with `GOOGLE_MAPS_API_KEY`.

Container behaviour:
- Runs as UID `10014` and only writes to `/tmp`, so a read-only root filesystem works.
- Starts from the committed `data/geocache.json` (and `data/overrides.json` if present),
  so it makes no geocoding calls on startup.
- Manual location fixes are saved to `/tmp/stationfinder/` and are **lost on restart**
  (Choreo also scales idle apps to zero). To keep them, copy them into
  `data/overrides.json` and commit.
- Environment variables:
  - `GOOGLE_MAPS_API_KEY` (required)
  - `PORT` (optional)
  - `CONFIG_FILE` (optional)
- Health check: `GET /healthz`

## Deploy on Choreo with sign-in (managed authentication)

```
Browser ─► Web App (React preset, managed auth) ─► /choreo-apis/... ─► gateway (OAuth2 on) ─► API service (Docker)
             /auth/login → identity provider       session cookies        401 without a valid session
```

Only signed-in users can open the UI or call the API, and the browser never holds an
access token. You control who can sign in by choosing which users exist in the
identity provider.

### 1. API service component
1. Push the branch to GitHub.
2. Create a **Service** component from the repository:
   - **Component directory:** `/`
   - **Buildpack:** Docker
   - **Docker context:** `/`
   - **Dockerfile:** `Dockerfile`
3. Before deploying, add the Google API key as a **secret** environment variable named
   `GOOGLE_MAPS_API_KEY`, under **Deploy → Configure & Deploy** (or **Configs &
   Secrets**). It isn't declared in `.choreo/component.yaml` because Choreo's
   configuration form isn't available for Docker builds. Then build and deploy.
4. Keep the endpoint **Public** and leave its **OAuth2 security on**.
   - The gateway only forwards the resources listed in `backend/openapi.yaml`. A test
     (`TestOpenAPIMatchesRoutes`) fails if the spec and the Go routes drift apart.

### 2. Web application component
1. Create a **Web Application** component from the same repository:
   - **Component directory:** `frontend`
   - **Build preset:** React
   - **Build command:** `npm ci && npm run build`
   - **Build path:** `dist`
   - **Node version:** 22
2. **Connections → + Create**: select the API service and copy the service URL it
   shows.
3. **Deploy → Configure & Deploy**: add a **file mount** that replaces `config.js`
   (it sits next to `index.html`; the repo's default is `frontend/public/config.js`):
   ```js
   window.configs = {
     apiUrl: "/choreo-apis/<path part of the service URL>",
     managedAuth: true,
   };
   ```
   - `apiUrl` must be the **relative** `/choreo-apis/...` path, not the full
     `https://...` URL, so the session cookies are sent. Confirm the exact prefix
     in your console.
4. In **Authentication**:
   - Turn on **Managed Authentication**.
   - Set Post Login Path, Post Logout Path and Error Path to `/`.
   - Deploy.

### 3. Who can sign in
- **Built-in identity provider** (any plan): go to **Organization Settings →
  Application Security → Identity Providers → Manage**, choose the environment, and
  upload a CSV of users and groups. Choreo recommends it for testing rather than
  production.
- **Asgardeo** (if your plan offers it under **Settings → Authentication Keys**):
  1. Create an OIDC app with the `Code` and `Refresh Token` grants and JWT access
     tokens.
  2. Add redirect URLs `<web-app-url>/auth/login/callback` and
     `<web-app-url>/auth/logout/callback`.
  3. Paste the client ID and secret into Choreo.
  4. Turn off self sign-up and invite only the people you want.

### 4. Optional: admin-only editing
The write operations are tagged `admin` in `backend/openapi.yaml`:
- `PUT /api/stations/{id}/location`
- `DELETE /api/stations/{id}/location`
- `POST /api/geocode`

To restrict them:
1. On the API component, go to **Deploy → Configure & Deploy → Endpoint Details →
   Operation Level Security**.
2. Create a permission (e.g. `admin`), attach it to those three operations, and
   redeploy.
3. Create a role with that permission under **Settings → Application Security**.
4. Map the role to your admin group under **Role Management**.

Other users get "You don't have permission to do this." in the UI. Choreo's console
labels change over time, so check the RBAC docs if a menu has moved.

### Try the sign-in flow locally
`frontend/dev/mock-choreo.mjs` imitates Choreo's managed auth: the `/auth/*`
endpoints, the `/choreo-apis` proxy with 401/403, and the `config.js` mount.
```bash
cd backend && go run ./cmd/server -config ../config.yaml        # API on :8080
cd frontend && npm run build && npm run mock:choreo             # http://localhost:5174
```
- Click **Sign in** for a normal user.
- To sign in as an admin, open http://localhost:5174/auth/login?user=admin.

Plain `npm run dev` still runs without sign-in, because the default `config.js` has
`managedAuth: false`.

### Also protect the Google key
Restrict the key to the Places, Geocoding and Routes APIs, and set daily quotas and a
budget alert in Google Cloud.

## Using it

- **Find stations.** Set a starting point in one of three ways:
  - **Search**: start typing a town, address or landmark (e.g. `kegal`). Suggestions
    come from Google Places, restricted to Sri Lanka. Matching stations from the
    vacancy list are listed too, so you can start from one of them. Use ↑/↓ and
    Enter, or click a suggestion. Selecting one runs the search straight away.
  - Click *Pick on map*.
  - Use *My location*.

  Optionally set:
  - **Max travel time** and/or **Max road distance**
  - **Number of stations** (default 10)
  - **Rank by**: travel time or distance
  - **Filters**: province, district, vacancy type (New / Difficult station / …),
    station type (DH, BH, PMCU, …), and designation (e.g. `SHO`, `ANAESTHESIA`)

  Each result shows road distance, travel time, matching vacancies, and a
  Google Maps *Directions* link.
- **Station locations.** Review the stations flagged *Needs review* and correct them.

## API

| Method | Path | Notes |
|---|---|---|
| `POST` | `/api/search` | see body below |
| `GET` | `/api/meta` | counts, filter options, geocoding progress |
| `GET` | `/api/stations` | all stations with their location entries |
| `PUT` | `/api/stations/{id}/location` | `{"lat":7.25,"lng":80.34}` manual fix |
| `DELETE` | `/api/stations/{id}/location` | remove manual fix |
| `POST` | `/api/geocode[?retryFailed=true]` | locate missing / retry failed stations |
| `GET` | `/api/places/autocomplete?q=kegal&session=<token>` | Google place suggestions + matching stations |
| `GET` | `/api/places/details?placeId=<id>&session=<token>` | coordinates of a suggested place |

```bash
curl -s localhost:8080/api/search -H 'Content-Type: application/json' -d '{
  "origin": {"query": "Kegalle"},
  "maxDurationMin": 90,
  "maxDistanceKm": 80,
  "limit": 10,
  "sortBy": "duration",
  "types": ["New"],
  "categories": ["Base Hospital", "Divisional Hospital"],
  "designation": "MO"
}'
```
`origin` can also be `{"lat": 7.25, "lng": 80.35, "label": "Kegalle Hospital"}`. All fields except `origin` are optional;
`sortBy` is `duration` or `distance`.

## Cost notes

- Geocoding the stations is a one-time cost: about 438 Places Text Search requests,
  plus Geocoding requests for any fallbacks. Results are cached on disk.
- Place search uses Places Autocomplete with session tokens: the keystrokes and the
  final details lookup are billed as one session. Picking a station suggestion makes
  no Google call.
- Each search makes 1 Geocoding call for the origin, plus at most `maxRouteCandidates`
  (default 60) Route Matrix elements. Repeating a search from the same origin uses the
  in-memory cache.
- Check Google Maps Platform pricing for the current free monthly allowances.
  Restricting the API key to these three APIs is recommended.

## Development

```bash
cd backend && go test ./...    # includes an end-to-end test against a fake Google API
```

To re-extract the data from a new PDF:
```bash
python3 -m venv .venv && .venv/bin/pip install pdfplumber
.venv/bin/python tools/extract_pdf.py "Draft vacancy list for preliminary grade appointments.pdf" data/vacancies.json
```
