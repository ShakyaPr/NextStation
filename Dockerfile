# Station Finder: single container serving the Go API and the built React UI.
#
#   docker build -t station-finder .
#   docker run -p 8080:8080 -e GOOGLE_MAPS_API_KEY=... station-finder
#
# Works on Choreo (Docker build preset): runs as a non-root UID in 10000-20000 and
# only writes to /tmp, so a read-only root filesystem is fine.

# ---- 1. Frontend build ----
FROM node:22.16.0-alpine AS web
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
RUN npm run build

# ---- 2. Backend build ----
FROM golang:1.26.2-alpine AS api
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/stationfinder ./cmd/server

# ---- 3. Runtime ----
FROM gcr.io/distroless/static-debian12
WORKDIR /app
COPY --from=api /out/stationfinder /app/stationfinder
COPY --from=web /src/frontend/dist /app/web
# vacancies.json plus geocache.json (and overrides.json if present) as read-only seeds.
COPY data/ /app/data/
COPY deploy/config.yaml /app/config.yaml

ENV CONFIG_FILE=/app/config.yaml
# Choreo requires a non-root user with a UID between 10000 and 20000.
USER 10014
EXPOSE 8080
ENTRYPOINT ["/app/stationfinder"]
