# Station Finder: one image with the Go API and the built React UI.
#
#   docker compose up -d --build          # VPS: app + Caddy (HTTPS), see README
#   docker build -t station-finder . && docker run -p 8080:8080 -e GOOGLE_MAPS_API_KEY=... station-finder
#
# Runs as a non-root UID and, by default, writes only to /tmp/stationfinder, so a
# read-only root filesystem works (Choreo). With STATE_DIR=/data and a volume there,
# location fixes, geocoding results and the session key survive restarts.

# ---- 1. UI ----
FROM node:22.23.3-alpine AS ui
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
RUN npm run build

# ---- 2. API ----
FROM golang:1.26.2-alpine AS api
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/stationfinder ./cmd/server \
    && mkdir -p /out/state

# ---- 3. Runtime ----
FROM gcr.io/distroless/static-debian12
WORKDIR /app
COPY --from=api /out/stationfinder /app/stationfinder
COPY --from=ui /src/frontend/dist /app/static
# The lists (lists.json + one JSON per list) and the committed locations, used as
# read-only seeds for the writable state.
COPY data/ /app/data/
COPY deploy/config.yaml /app/config.yaml
# /data is owned by the app user, so a new named volume mounted there is writable.
COPY --from=api --chown=10014:10014 /out/state /data

ENV CONFIG_FILE=/app/config.yaml
# Non-root; Choreo also requires a UID between 10000 and 20000.
USER 10014
EXPOSE 8080
ENTRYPOINT ["/app/stationfinder"]
