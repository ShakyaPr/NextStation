# Station Finder API service (Go). The React UI is deployed separately as a Choreo
# Web Application with managed authentication (see README).
#
#   docker build -t station-finder-api .
#   docker run -p 8080:8080 -e GOOGLE_MAPS_API_KEY=... station-finder-api
#
# Choreo-ready: runs as a non-root UID in 10000-20000 and only writes to /tmp, so a
# read-only root filesystem is fine.

# ---- 1. Build ----
FROM golang:1.26.2-alpine AS build
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/stationfinder ./cmd/server

# ---- 2. Runtime ----
FROM gcr.io/distroless/static-debian12
WORKDIR /app
COPY --from=build /out/stationfinder /app/stationfinder
# vacancies.json plus geocache.json (and overrides.json if present) as read-only seeds.
COPY data/ /app/data/
COPY deploy/config.yaml /app/config.yaml

ENV CONFIG_FILE=/app/config.yaml
# Choreo requires a non-root user with a UID between 10000 and 20000.
USER 10014
EXPOSE 8080
ENTRYPOINT ["/app/stationfinder"]
