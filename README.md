# Station Finder

Finds the most suitable stations for a given starting point and ranks them by real road
travel time or distance, optionally within a time or distance limit. You pick the list
to search in the app:

| List | Source PDF (`source-pdfs/`) | Rows | Stations |
|---|---|---|---|
| **Vacancy list – 2025.11.04 batch** (default) | `PDF345-vac-new2.pdf` | 1,416 vacancies | 537 |
| **Difficult stations – 2026** | `Difficult Vacancy signed.pdf` | 265 stations | 265 |
| **Draft vacancy list – Oct/Nov 2026** | `Draft vacancy list for preliminary grade appointments.pdf` | 1,300 vacancies | 438 |

In the 2025.11.04 list, the 126 vacancies at stations on the 2026 difficult stations
list have the vacancy type **Difficult station**, so you can filter for them.

```
frontend/      React + Vite + Leaflet UI          (http://localhost:5173)
backend/       Go API server                       (http://localhost:8080)
data/          lists.json (which lists exist) + one JSON per list, geocache.json, overrides.json
source-pdfs/   the PDFs the lists were extracted from
tools/         extract_pdf.py (text PDF), extract_scanned_pdf.py (scanned PDF) → data/*.json
deploy/        config.yaml for the image, Caddyfile
docs/          keycloak.md: setting up sign-in and registering users
docker-compose.yml, Dockerfile, .env.example   VPS deployment
```

## How it works

1. **Data.** Each list is a JSON file in `data/`, declared in `data/lists.json`.
   - The draft list was a text PDF; `tools/extract_pdf.py` pulled out its 1,300 rows
     and stripped the "DRAFT" watermark.
   - The two newer lists are scans. `tools/extract_scanned_pdf.py` reads them with
     macOS's OCR plus the PDF's own text layer, and cross-checks the two. Names are
     snapped to the draft list's spellings, so the same station gets the same ID (and
     the same location) in every list. The few cells the two readings disagreed on
     were checked against the scans and fixed in `tools/corrections.json`.
   - The backend groups rows into stations (district + institution): 702 distinct
     stations across the three lists.
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

The first start locates every station that isn't in `data/geocache.json` yet, in the
background. That is 264 stations from the two new lists; the other 438 are already
in the cache. It takes a minute or two and progress shows in the UI. Later starts reuse
the cache. Then open **Station locations**, pick each list, and review *Needs review*.
Commit the updated `data/geocache.json` (and `data/overrides.json`), so the Docker image
starts with every station already located.

### 4. Run the frontend

```bash
cd frontend
npm install
npm run dev
```

Open http://localhost:5173. The Vite dev server proxies `/api` to `:8080`.

To run a single process instead, run `npm run build` in `frontend/`. The backend then
serves the UI at http://localhost:8080 (`server.staticDir` in your local `config.yaml`).

## Deploy on a VPS with Docker

One container serves the UI and the API, and [Caddy](https://caddyserver.com) in
front of it gets an HTTPS certificate automatically. Sign-in goes through your
**Keycloak**: only users you register there can open the app or use your Google key.
(On Dokploy, Traefik takes Caddy's place; see [Deploy on Dokploy](#deploy-on-dokploy).)

```
Browser ──HTTPS──► Caddy (:443, Let's Encrypt) ──► app (:8080, UI + API) ──► Google APIs
   │                                                 │  │
   └──── sign-in page ◄──────── Keycloak ◄───────────┘  └─ volume app-data: locations, fixes, session key
```

### 1. Prepare the server
- Any small Linux VPS (1 vCPU / 512 MB is plenty) with
  [Docker Engine and the compose plugin](https://docs.docker.com/engine/install/).
- A domain or subdomain whose DNS **A record** points at the server's IP, e.g.
  `stations.example.com`. If you have no domain, see "No domain" below.
- Ports **80** and **443** open in the firewall (Caddy needs 80 for the certificate).

### 2. Set up Keycloak
Follow [docs/keycloak.md](docs/keycloak.md) Part 1: create a realm, a `station-finder`
client and an `admin` role. Then register your users (Part 3). Note the client secret.

### 3. Get the code and configure
```bash
git clone <your repo URL> station-finder && cd station-finder
cp .env.example .env
nano .env
```
Fill in:
- `GOOGLE_MAPS_API_KEY`
- `DOMAIN=stations.example.com`
- `OIDC_ISSUER=https://keycloak.example.com/realms/station-finder`
- `OIDC_CLIENT_SECRET=<client secret>`
- `PUBLIC_URL=https://stations.example.com`

### 4. Start
```bash
docker compose up -d --build
docker compose logs -f app       # watch the first start
```
Open `https://<your domain>` and sign in with a Keycloak user. The log should show
`sign-in with https://keycloak…`; see the troubleshooting table in
[docs/keycloak.md](docs/keycloak.md) if sign-in fails.
- On the first start the app locates any stations missing from the committed
  `data/geocache.json`, about 1–2 minutes. Results are kept in the `app-data` volume.
- If the Google key was wrong at that point, fix `.env`, run `docker compose up -d`,
  and press **Locate missing** under *Station locations* (as an admin).

### Updating
```bash
git pull && docker compose up -d --build
```
The `app-data` volume survives updates. Locations and fixes committed in the new
image's `data/` are merged in, and your own fixes on the server win.

### Keeping manual location fixes
Fixes made in the UI are stored in the volume. To back them up, or to put them in git
so every new deployment starts with them:
```bash
docker compose cp app:/data/overrides.json data/overrides.json
docker compose cp app:/data/geocache.json  data/geocache.json
git add data && git commit -m "Update station locations"
```
To back up the whole volume:
```bash
docker run --rm -v station-finder_app-data:/data -v "$PWD":/backup alpine tar czf /backup/app-data.tgz -C /data .
```
The volume name is `<project folder name>_app-data`; `docker volume ls` shows it.

### No domain
Set `DOMAIN=:80` in `.env`. Caddy then serves plain HTTP on the server's IP. Use this
only for testing: passwords and session cookies travel unencrypted.

### Settings
| Variable (`.env`) | |
|---|---|
| `GOOGLE_MAPS_API_KEY` | required; Places API (New), Geocoding API, Routes API |
| `DOMAIN` | domain for HTTPS, or `:80` |
| `OIDC_ISSUER` | Keycloak realm URL. **Empty = no sign-in: anyone can use the app** |
| `OIDC_CLIENT_ID` | default `station-finder` |
| `OIDC_CLIENT_SECRET` | the client's secret (Keycloak → Clients → station-finder → Credentials) |
| `PUBLIC_URL` | the app's address; Keycloak must allow `PUBLIC_URL/auth/callback` |
| `OIDC_ADMIN_ROLE` | default `admin`; users with it can fix station locations |
| `OIDC_REQUIRED_ROLE` | optional; only users with this role may sign in |
| `SESSION_SECRET` | optional, 32+ characters; otherwise generated and kept in the volume |

`docker-compose.yml` also sets `STATE_DIR=/data` (the volume).

Signing in lasts 12 hours (`auth.sessionHours` in `deploy/config.yaml`). After that the
app checks with Keycloak again, silently if the user is still signed in there.
Wrong-password lock-outs are handled by Keycloak's brute force detection.

### Running the image without compose
```bash
docker build -t station-finder .
docker run -p 8080:8080 -e GOOGLE_MAPS_API_KEY=your-key station-finder   # no sign-in
curl localhost:8080/healthz
```
- Runs as UID `10014`. Without `STATE_DIR`, it writes only to `/tmp/stationfinder`, so
  a read-only root filesystem works, but fixes are lost when the container is replaced.
- Health check: `GET /healthz`, or `/app/stationfinder healthcheck` inside the container.

### Deploy on Dokploy
Deploy as an **Application**, not as Compose. Dokploy's Traefik already serves ports
80/443 with HTTPS, so Caddy isn't needed.
1. **Create Service → Application.**
   - Provider: your Git repo, branch `main`.
   - Build Type **Dockerfile**: Dockerfile Path `Dockerfile`, Docker Context Path `.`.
2. **Environment:** the variables from the Settings table above (without `DOMAIN`),
   plus `STATE_DIR=/data`.
3. **Advanced → Volumes/Mounts → Volume Mount:** Volume Name `station-finder-data`,
   Mount Path `/data`. Use a volume, not a bind mount: the app runs as UID 10014 and
   couldn't write to a root-owned host folder.
4. **Domains:** your host, **Container Port `8080`**, HTTPS on, certificate `letsencrypt`.
5. **Deploy.** To copy location fixes out of the volume:
   ```bash
   C=$(docker ps -q --filter name=<app name>)
   docker cp $C:/data/overrides.json data/overrides.json
   ```

## Alternative: deploy on Choreo with managed authentication

The app was first hosted on Choreo; this still works. The same Docker image serves as
the API service. Its built-in UI and sign-in are unused there: Choreo serves the UI
and handles sign-in itself.

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

- **Choose a list.** *Station list* at the top of the search settings (and in
  *Station locations*) switches between the lists above. Your choice is remembered in
  this browser. Filters that don't apply to a list are hidden; the difficult stations
  list, for example, has no posts.
- **Find stations.** Set a starting point in one of three ways:
  - **Search**: start typing a town, address or landmark (e.g. `kegal`). Suggestions
    come from Google Places, restricted to Sri Lanka. Matching stations from any
    list are listed too, so you can start from one of them. Use ↑/↓ and
    Enter, or click a suggestion. Selecting one runs the search straight away.
  - Click *Pick on map*.
  - Use *My location*.

  Optionally set:
  - **Max travel time** and/or **Max road distance**
  - **Number of stations** (default 10)
  - **Rank by**: travel time or distance
  - **Filters**: province, district, vacancy type (New / Difficult station / …),
    station type (DH, BH, PMCU, AHWC, …), and designation (e.g. `SHO`, `ANAESTHESIA`)

  Each result shows road distance, travel time, matching vacancies, and a
  Google Maps *Directions* link.
- **Station locations.** Review the stations flagged *Needs review* and correct them.

## API

| Method | Path | Notes |
|---|---|---|
| `POST` | `/api/search` | see body below |
| `GET` | `/api/meta[?list=<id>]` | available lists; counts, filter options and geocoding progress for one list |
| `GET` | `/api/stations[?list=<id>]` | the list's stations with their location entries |
| `PUT` | `/api/stations/{id}/location` | `{"lat":7.25,"lng":80.34}` manual fix |
| `DELETE` | `/api/stations/{id}/location` | remove manual fix |
| `POST` | `/api/geocode[?retryFailed=true]` | locate missing / retry failed stations |
| `GET` | `/api/places/autocomplete?q=kegal&session=<token>` | Google place suggestions + matching stations |
| `GET` | `/api/places/details?placeId=<id>&session=<token>` | coordinates of a suggested place |

Without `list`, the first list in `data/lists.json` is used. With Keycloak sign-in on,
every `/api/*` call needs the session cookie set after `GET /auth/login`, which
redirects to Keycloak and back to `/auth/callback`. `GET /auth/userinfo` returns the
signed-in user and `GET /auth/logout` signs out of the app and Keycloak.

```bash
curl -s localhost:8080/api/search -H 'Content-Type: application/json' -d '{
  "list": "difficult-stations-2026",
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

- Geocoding the stations is a one-time cost: one Places Text Search request per
  station (438 for the draft list, 264 more for the two new lists), plus Geocoding
  requests for any fallbacks. Results are cached on disk.
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

### Re-extracting or adding a list
Text PDF (like the draft list):
```bash
python3 -m venv .venv && .venv/bin/pip install pdfplumber
.venv/bin/python tools/extract_pdf.py "source-pdfs/Draft vacancy list for preliminary grade appointments.pdf" data/vacancies.json
```
Scanned PDF (macOS only, uses the built-in Vision OCR):
```bash
.venv/bin/pip install pdfplumber pyobjc-framework-Vision pyobjc-framework-Quartz
.venv/bin/python tools/extract_scanned_pdf.py difficult "source-pdfs/Difficult Vacancy signed.pdf" \
    data/difficult-stations-2026.json --reference data/vacancies.json
.venv/bin/python tools/extract_scanned_pdf.py vacancies source-pdfs/PDF345-vac-new2.pdf \
    data/vacancies-2025-11-04.json --reference data/vacancies.json \
    --mark-difficult data/difficult-stations-2026.json
```
- The tool prints rows per page (compare with the PDF) and flags with `!!` any cell
  the two OCR readings disagree on.
- Check those rows against the scan and add fixes to `tools/corrections.json`.
- For a new list, add an entry to `data/lists.json`:
  - `kind` is `vacancies` (rows are posts) or `stations` (rows are stations only).
  - The first entry is the default list.
