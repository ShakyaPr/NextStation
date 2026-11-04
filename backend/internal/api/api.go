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
	"strings"
	"time"

	"stationfinder/internal/config"
	"stationfinder/internal/data"
	"stationfinder/internal/geo"
	"stationfinder/internal/google"
	"stationfinder/internal/locations"
	"stationfinder/internal/search"
)

type Server struct {
	cfg      *config.Config
	stations []*data.Station
	byID     map[string]*data.Station
	store    *locations.Store
	search   *search.Service
	google   *google.Client
	bgCtx    context.Context
}

func New(bgCtx context.Context, cfg *config.Config, stations []*data.Station, store *locations.Store, svc *search.Service, gc *google.Client) *Server {
	byID := make(map[string]*data.Station, len(stations))
	for _, s := range stations {
		byID[s.ID] = s
	}
	return &Server{cfg: cfg, stations: stations, byID: byID, store: store, search: svc, google: gc, bgCtx: bgCtx}
}

// routes is the API surface. Keep backend/openapi.yaml in sync: on Choreo the
// gateway only forwards resources listed there (enforced by TestOpenAPIMatchesRoutes).
func (s *Server) routes() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"GET /healthz": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		},
		"GET /api/meta":                      s.meta,
		"GET /api/stations":                  s.listStations,
		"PUT /api/stations/{id}/location":    s.setLocation,
		"DELETE /api/stations/{id}/location": s.clearLocation,
		"POST /api/geocode":                  s.runGeocode,
		"GET /api/geocode/status":            s.geocodeStatus,
		"POST /api/search":                   s.doSearch,
		"GET /api/places/autocomplete":       s.autocomplete,
		"GET /api/places/details":            s.placeDetails,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	for pattern, h := range s.routes() {
		mux.HandleFunc(pattern, h)
	}

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
		if r.URL.Path != "/api/geocode/status" && r.URL.Path != "/healthz" {
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

type stationSuggestion struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	District string     `json:"district"`
	Category string     `json:"category"`
	Location geo.LatLng `json:"location"`
}

// autocomplete returns Google place predictions plus matching stations from the vacancy list.
func (s *Server) autocomplete(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	resp := struct {
		Places   []google.Suggestion `json:"places"`
		Stations []stationSuggestion `json:"stations"`
		Error    string              `json:"error,omitempty"`
	}{Places: []google.Suggestion{}, Stations: []stationSuggestion{}}
	if len([]rune(q)) < 2 {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	upper := strings.ToUpper(q)
	for _, st := range s.stations {
		if len(resp.Stations) >= 5 {
			break
		}
		if !strings.Contains(st.Name, upper) {
			continue
		}
		if e := s.store.Get(st.ID); e != nil && e.Location != nil {
			resp.Stations = append(resp.Stations, stationSuggestion{st.ID, st.Name, st.District, st.Category, *e.Location})
		}
	}

	if s.google.Enabled() {
		places, err := s.google.Autocomplete(r.Context(), q, r.URL.Query().Get("session"))
		if err != nil {
			// Still return station matches; surface the Google error for the UI.
			log.Printf("autocomplete: %v", err)
			resp.Error = err.Error()
		} else {
			resp.Places = places
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) placeDetails(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("placeId")
	if id == "" {
		writeErr(w, http.StatusBadRequest, errors.New("placeId is required"))
		return
	}
	if !s.google.Enabled() {
		writeErr(w, http.StatusServiceUnavailable, errors.New("google api key is not configured"))
		return
	}
	p, err := s.google.PlaceDetails(r.Context(), id, r.URL.Query().Get("session"))
	if errors.Is(err, google.ErrNotFound) {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	if !geo.InSriLanka(p.Location) {
		writeErr(w, http.StatusBadRequest, errors.New("place is outside Sri Lanka"))
		return
	}
	writeJSON(w, http.StatusOK, p)
}
