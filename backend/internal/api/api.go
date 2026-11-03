// Package api exposes the HTTP endpoints.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"stationfinder/internal/config"
	"stationfinder/internal/data"
	"stationfinder/internal/geo"
	"stationfinder/internal/locations"
	"stationfinder/internal/search"
)

type Server struct {
	cfg      *config.Config
	stations []*data.Station
	byID     map[string]*data.Station
	store    *locations.Store
	search   *search.Service
	bgCtx    context.Context
}

func New(bgCtx context.Context, cfg *config.Config, stations []*data.Station, store *locations.Store, svc *search.Service) *Server {
	byID := make(map[string]*data.Station, len(stations))
	for _, s := range stations {
		byID[s.ID] = s
	}
	return &Server{cfg: cfg, stations: stations, byID: byID, store: store, search: svc, bgCtx: bgCtx}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/meta", s.meta)
	mux.HandleFunc("GET /api/stations", s.listStations)
	mux.HandleFunc("PUT /api/stations/{id}/location", s.setLocation)
	mux.HandleFunc("DELETE /api/stations/{id}/location", s.clearLocation)
	mux.HandleFunc("POST /api/geocode", s.runGeocode)
	mux.HandleFunc("GET /api/geocode/status", s.geocodeStatus)
	mux.HandleFunc("POST /api/search", s.doSearch)

	if dir := s.cfg.Server.StaticDir; dir != "" {
		if _, err := os.Stat(filepath.Join(dir, "index.html")); err == nil {
			fs := http.FileServer(http.Dir(dir))
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				if _, err := os.Stat(filepath.Join(dir, filepath.Clean(r.URL.Path))); err != nil {
					http.ServeFile(w, r, filepath.Join(dir, "index.html"))
					return
				}
				fs.ServeHTTP(w, r)
			})
			log.Printf("serving frontend from %s", dir)
		}
	}
	return withCORS(withLogging(mux))
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if r.URL.Path != "/api/geocode/status" {
			log.Printf("%s %s (%s)", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
		}
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

type countItem struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func sortedCounts(m map[string]int) []countItem {
	out := make([]countItem, 0, len(m))
	for k, v := range m {
		out = append(out, countItem{k, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Server) meta(w http.ResponseWriter, _ *http.Request) {
	provinces, districts, types, cats := map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}
	districtProvince := map[string]string{}
	located, suspect, vacancies := 0, 0, 0
	for _, st := range s.stations {
		provinces[st.Province]++
		districts[st.District]++
		districtProvince[st.District] = st.Province
		cats[st.Category]++
		for _, v := range st.Vacancies {
			types[v.Type]++
			vacancies++
		}
		if e := s.store.Get(st.ID); e != nil && e.Location != nil {
			located++
			if e.Suspect {
				suspect++
			}
		}
	}
	catList := make([]countItem, 0, len(cats))
	for _, c := range data.Categories {
		if n := cats[c]; n > 0 {
			catList = append(catList, countItem{c, n})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"apiKeyConfigured": s.cfg.Google.APIKey != "",
		"travelMode":       s.cfg.Google.TravelMode,
		"defaultLimit":     s.cfg.Search.DefaultLimit,
		"stationCount":     len(s.stations),
		"vacancyCount":     vacancies,
		"locatedCount":     located,
		"suspectCount":     suspect,
		"provinces":        sortedCounts(provinces),
		"districts":        sortedCounts(districts),
		"districtProvince": districtProvince,
		"types":            sortedCounts(types),
		"categories":       catList,
		"geocoding":        s.store.Progress(),
	})
}

type stationView struct {
	*data.Station
	Location *locations.Entry `json:"location"`
}

func (s *Server) listStations(w http.ResponseWriter, _ *http.Request) {
	out := make([]stationView, len(s.stations))
	for i, st := range s.stations {
		out[i] = stationView{st, s.store.Get(st.ID)}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) setLocation(w http.ResponseWriter, r *http.Request) {
	st, ok := s.byID[r.PathValue("id")]
	if !ok {
		writeErr(w, http.StatusNotFound, errors.New("unknown station"))
		return
	}
	var p geo.LatLng
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if !geo.InSriLanka(p) {
		writeErr(w, http.StatusBadRequest, errors.New("location is outside Sri Lanka"))
		return
	}
	e, err := s.store.SetManual(st, p)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) clearLocation(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ClearManual(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, s.store.Get(r.PathValue("id")))
}

func (s *Server) runGeocode(w http.ResponseWriter, r *http.Request) {
	retry := r.URL.Query().Get("retryFailed") == "true"
	if err := s.store.Run(s.bgCtx, s.stations, s.cfg.Geocoding.Workers, retry); err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusAccepted, s.store.Progress())
}

func (s *Server) geocodeStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Progress())
}

func (s *Server) doSearch(w http.ResponseWriter, r *http.Request) {
	var req search.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.search.Search(r.Context(), req)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, search.ErrBadRequest) {
			status = http.StatusBadRequest
		}
		writeErr(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
