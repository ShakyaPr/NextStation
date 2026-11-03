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
serves the UI at http://localhost:8080 (`server.staticDir`).

## Using it

- **Find stations.** Enter a starting point (e.g. `Kegalle`), click *Pick on map*, or
  use *My location*. Optionally set:
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
`origin` can also be `{"lat": 7.25, "lng": 80.35}`. All fields except `origin` are optional;
`sortBy` is `duration` or `distance`.

## Cost notes

- Geocoding the stations is a one-time cost: about 438 Places Text Search requests,
  plus Geocoding requests for any fallbacks. Results are cached on disk.
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
