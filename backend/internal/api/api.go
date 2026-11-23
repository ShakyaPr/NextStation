// Package api exposes the HTTP endpoints.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"stationfinder/internal/auth"
	"stationfinder/internal/config"
	"stationfinder/internal/data"
	"stationfinder/internal/geo"
	"stationfinder/internal/google"
	"stationfinder/internal/locations"
	"stationfinder/internal/search"
)

type Server struct {
	cfg    *config.Config
	lists  []*data.List
	all    []*data.Station // one per station ID across all lists
	byID   map[string]*data.Station
	store  *locations.Store
	search *search.Service
	google *google.Client
	auth   *auth.Auth // nil = sign-in off
	bgCtx  context.Context
}

func New(bgCtx context.Context, cfg *config.Config, lists []*data.List, store *locations.Store, svc *search.Service, gc *google.Client, au *auth.Auth) *Server {
	all := data.AllStations(lists)
	byID := make(map[string]*data.Station, len(all))
	for _, s := range all {
		byID[s.ID] = s
	}
	return &Server{cfg: cfg, lists: lists, all: all, byID: byID, store: store, search: svc, google: gc, auth: au, bgCtx: bgCtx}
}

// adminRoutes change shared data. With Keycloak sign-in only users with the admin role
// may call them; on Choreo they are tagged "admin" in openapi.yaml for the same purpose.
var adminRoutes = map[string]bool{
	"PUT /api/stations/{id}/location":    true,
	"DELETE /api/stations/{id}/location": true,
	"POST /api/geocode":                  true,
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
		mux.HandleFunc(pattern, s.guard(pattern, h))
	}
	if s.auth != nil {
		// Sign-in with Keycloak; same paths as Choreo's managed authentication.
		mux.HandleFunc("GET /auth/login", s.auth.Login)
		mux.HandleFunc("GET /auth/callback", s.auth.Callback)
		mux.HandleFunc("GET /auth/userinfo", s.auth.UserInfo)
		mux.HandleFunc("GET /auth/logout", s.auth.Logout)
	}

	if dir := s.cfg.Server.StaticDir; dir != "" {
		if _, err := os.Stat(filepath.Join(dir, "index.html")); err == nil {
			// Runtime config for the UI served from here: same origin, and sign-in on
			// when Keycloak is configured. (On Choreo a file mount provides it.)
			mux.HandleFunc("GET /config.js", func(w http.ResponseWriter, _ *http.Request) {
				cfg, _ := json.Marshal(map[string]any{"apiUrl": "", "managedAuth": s.auth != nil, "authMode": "oidc"})
				w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
				w.Header().Set("Cache-Control", "no-store")
				fmt.Fprintf(w, "window.configs = %s;\n", cfg)
			})
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

// guard enforces sign-in on API routes when built-in auth is on.
func (s *Server) guard(pattern string, h http.HandlerFunc) http.HandlerFunc {
	if s.auth == nil || !strings.Contains(pattern, " /api/") {
		return h
	}
	admin := adminRoutes[pattern]
	return func(w http.ResponseWriter, r *http.Request) {
		u := s.auth.User(r)
		if u == nil {
			writeErr(w, http.StatusUnauthorized, errors.New("sign in required"))
			return
		}
		if admin && !s.auth.IsAdmin(u) {
			writeErr(w, http.StatusForbidden, errors.New("only administrators can do this"))
			return
		}
		h(w, r)
	}
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

// list resolves the ?list= query parameter ("" = the default list).
func (s *Server) list(w http.ResponseWriter, r *http.Request) (*data.List, bool) {
	l, err := s.search.List(r.URL.Query().Get("list"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return nil, false
	}
	return l, true
}

type listSummary struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	Source          string `json:"source,omitempty"`
	StationCount    int    `json:"stationCount"`
	VacancyCount    int    `json:"vacancyCount"`
	HasDesignations bool   `json:"hasDesignations"` // false for station-only lists
}

func summarize(l *data.List) listSummary {
	sum := listSummary{ID: l.ID, Name: l.Name, Description: l.Description, Source: l.Source, StationCount: len(l.Stations)}
	for _, st := range l.Stations {
		sum.VacancyCount += len(st.Vacancies)
		for _, v := range st.Vacancies {
			if v.Designation != "" && !l.StationsOnly() {
				sum.HasDesignations = true
			}
		}
	}
	return sum
}

func (s *Server) meta(w http.ResponseWriter, r *http.Request) {
	list, ok := s.list(w, r)
	if !ok {
		return
	}
	provinces, districts, types, cats := map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}
	districtProvince := map[string]string{}
	located, suspect, vacancies, difficult := 0, 0, 0, 0
	for _, st := range list.Stations {
		if st.Difficult {
			difficult++
		}
		provinces[st.Province]++
		districts[st.District]++
		districtProvince[st.District] = st.Province
		cats[st.Category]++
		for _, v := range st.Vacancies {
			// Difficult stations have their own filter (difficultOnly), not a vacancy type.
			if v.Type != "" && v.Type != data.DifficultType {
				types[v.Type]++
			}
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
	lists := make([]listSummary, len(s.lists))
	for i, l := range s.lists {
		lists[i] = summarize(l)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"list":             list.ID,
		"lists":            lists,
		"hasDesignations":  summarize(list).HasDesignations,
		"apiKeyConfigured": s.cfg.Google.APIKey != "",
		"travelMode":       s.cfg.Google.TravelMode,
		"defaultLimit":     s.cfg.Search.DefaultLimit,
		"stationCount":     len(list.Stations),
		"difficultCount":   difficult,
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

func (s *Server) listStations(w http.ResponseWriter, r *http.Request) {
	list, ok := s.list(w, r)
	if !ok {
		return
	}
	out := make([]stationView, len(list.Stations))
	for i, st := range list.Stations {
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
	if err := s.store.Run(s.bgCtx, s.all, s.cfg.Geocoding.Workers, retry); err != nil {
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

// autocomplete returns Google place predictions plus matching stations from any list.
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
	for _, st := range s.all {
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
