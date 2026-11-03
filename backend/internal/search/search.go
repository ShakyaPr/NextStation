// Package search ranks stations by road distance / travel time from an origin.
package search

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"stationfinder/internal/config"
	"stationfinder/internal/data"
	"stationfinder/internal/geo"
	"stationfinder/internal/google"
	"stationfinder/internal/locations"
)

type Origin struct {
	Query string   `json:"query,omitempty"`
	Lat   *float64 `json:"lat,omitempty"`
	Lng   *float64 `json:"lng,omitempty"`
	// Label is a display name for a lat/lng origin (e.g. the place picked from search).
	Label string `json:"label,omitempty"`
}

type Request struct {
	Origin         Origin   `json:"origin"`
	MaxDistanceKm  float64  `json:"maxDistanceKm,omitempty"`  // road distance limit, 0 = none
	MaxDurationMin float64  `json:"maxDurationMin,omitempty"` // travel time limit, 0 = none
	Limit          int      `json:"limit,omitempty"`
	SortBy         string   `json:"sortBy,omitempty"` // duration | distance
	Provinces      []string `json:"provinces,omitempty"`
	Districts      []string `json:"districts,omitempty"`
	Types          []string `json:"types,omitempty"`      // vacancy type: New, Difficult station, ...
	Categories     []string `json:"categories,omitempty"` // station category
	Designation    string   `json:"designation,omitempty"`
}

type ResolvedOrigin struct {
	geo.LatLng
	Address string `json:"address,omitempty"`
}

type Result struct {
	Rank           int              `json:"rank"`
	Station        *data.Station    `json:"station"`
	Location       *locations.Entry `json:"location"`
	DistanceKm     float64          `json:"distanceKm"`
	DurationMin    float64          `json:"durationMin"`
	StraightLineKm float64          `json:"straightLineKm"`
	Vacancies      []data.Vacancy   `json:"vacancies"`
}

type Stats struct {
	StationsMatchingFilters int `json:"stationsMatchingFilters"`
	WithoutLocation         int `json:"withoutLocation"`
	RouteCandidates         int `json:"routeCandidates"`
	WithinLimits            int `json:"withinLimits"`
	CacheHits               int `json:"cacheHits"`
}

type Response struct {
	Origin   ResolvedOrigin `json:"origin"`
	Results  []Result       `json:"results"`
	Stats    Stats          `json:"stats"`
	Warnings []string       `json:"warnings,omitempty"`
}

type Service struct {
	cfg      *config.Config
	stations []*data.Station
	store    *locations.Store
	client   *google.Client

	mu         sync.Mutex
	routeCache map[string]google.Route
}

func NewService(cfg *config.Config, stations []*data.Station, store *locations.Store, client *google.Client) *Service {
	return &Service{cfg: cfg, stations: stations, store: store, client: client, routeCache: map[string]google.Route{}}
}

var ErrBadRequest = errors.New("bad request")

func badRequest(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrBadRequest, fmt.Sprintf(format, a...))
}

// ResolveOrigin turns a query or coordinates into a point.
func (s *Service) ResolveOrigin(ctx context.Context, o Origin) (*ResolvedOrigin, error) {
	if o.Lat != nil && o.Lng != nil {
		p := geo.LatLng{Lat: *o.Lat, Lng: *o.Lng}
		if !geo.InSriLanka(p) {
			return nil, badRequest("origin %.5f,%.5f is outside Sri Lanka", p.Lat, p.Lng)
		}
		return &ResolvedOrigin{LatLng: p, Address: o.Label}, nil
	}
	q := strings.TrimSpace(o.Query)
	if q == "" {
		return nil, badRequest("origin is required (query or lat/lng)")
	}
	if !strings.Contains(strings.ToLower(q), "sri lanka") {
		q += ", Sri Lanka"
	}
	p, gerr := s.client.Geocode(ctx, q)
	if gerr == nil {
		return &ResolvedOrigin{LatLng: p.Location, Address: p.Address}, nil
	}
	places, perr := s.client.PlacesTextSearch(ctx, q)
	if perr == nil {
		return &ResolvedOrigin{LatLng: places[0].Location, Address: places[0].Name + ", " + places[0].Address}, nil
	}
	if errors.Is(gerr, google.ErrNotFound) && errors.Is(perr, google.ErrNotFound) {
		return nil, badRequest("could not find %q", o.Query)
	}
	return nil, fmt.Errorf("resolve origin: geocoding: %v; places: %v", gerr, perr)
}

func upperSet(xs []string) map[string]bool {
	if len(xs) == 0 {
		return nil
	}
	m := map[string]bool{}
	for _, x := range xs {
		m[strings.ToUpper(strings.TrimSpace(x))] = true
	}
	return m
}

type candidate struct {
	st       *data.Station
	loc      *locations.Entry
	vacs     []data.Vacancy
	straight float64
	route    google.Route
}

func (s *Service) Search(ctx context.Context, req Request) (*Response, error) {
	if !s.client.Enabled() {
		return nil, errors.New("google api key is not configured")
	}
	if req.Limit <= 0 {
		req.Limit = s.cfg.Search.DefaultLimit
	}
	req.Limit = min(req.Limit, 200)
	if req.SortBy == "" {
		req.SortBy = "duration"
	}
	if req.SortBy != "duration" && req.SortBy != "distance" {
		return nil, badRequest("sortBy must be duration or distance")
	}

	origin, err := s.ResolveOrigin(ctx, req.Origin)
	if err != nil {
		return nil, err
	}

	provinces, districts := upperSet(req.Provinces), upperSet(req.Districts)
	types, cats := upperSet(req.Types), upperSet(req.Categories)
	desig := strings.ToUpper(strings.TrimSpace(req.Designation))

	resp := &Response{Origin: *origin}
	var cands []*candidate
	for _, st := range s.stations {
		if provinces != nil && !provinces[st.Province] {
			continue
		}
		if districts != nil && !districts[st.District] {
			continue
		}
		if cats != nil && !cats[strings.ToUpper(st.Category)] {
			continue
		}
		var vacs []data.Vacancy
		for _, v := range st.Vacancies {
			if types != nil && !types[strings.ToUpper(v.Type)] {
				continue
			}
			if desig != "" && !strings.Contains(v.Designation, desig) {
				continue
			}
			vacs = append(vacs, v)
		}
		if len(vacs) == 0 {
			continue
		}
		resp.Stats.StationsMatchingFilters++
		loc := s.store.Get(st.ID)
		if loc == nil || loc.Location == nil {
			resp.Stats.WithoutLocation++
			continue
		}
		cands = append(cands, &candidate{st: st, loc: loc, vacs: vacs, straight: geo.HaversineKm(origin.LatLng, *loc.Location)})
	}
	if resp.Stats.WithoutLocation > 0 {
		resp.Warnings = append(resp.Warnings, fmt.Sprintf(
			"%d matching station(s) have no location yet and were skipped (see the Stations tab).", resp.Stats.WithoutLocation))
	}

	// Road distance is always >= straight-line distance, so these pre-filters are safe.
	maxStraight := 0.0
	if req.MaxDistanceKm > 0 {
		maxStraight = req.MaxDistanceKm
	}
	if req.MaxDurationMin > 0 {
		byTime := req.MaxDurationMin / 60 * s.cfg.Search.MaxSpeedKmh
		if maxStraight == 0 || byTime < maxStraight {
			maxStraight = byTime
		}
	}
	if maxStraight > 0 {
		cands = slices.DeleteFunc(cands, func(c *candidate) bool { return c.straight > maxStraight })
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].straight < cands[j].straight })

	maxCands := max(s.cfg.Search.MaxRouteCandidates, req.Limit*3)
	if len(cands) > maxCands {
		cands = cands[:maxCands]
		resp.Warnings = append(resp.Warnings, fmt.Sprintf(
			"Only the %d stations closest in straight line were routed (search.maxRouteCandidates).", maxCands))
	}
	resp.Stats.RouteCandidates = len(cands)

	hits, err := s.route(ctx, origin.LatLng, cands)
	if err != nil {
		return nil, err
	}
	resp.Stats.CacheHits = hits

	var within []*candidate
	for _, c := range cands {
		if !c.route.OK {
			continue
		}
		if req.MaxDistanceKm > 0 && c.route.DistanceKm > req.MaxDistanceKm {
			continue
		}
		if req.MaxDurationMin > 0 && c.route.DurationMin > req.MaxDurationMin {
			continue
		}
		within = append(within, c)
	}
	resp.Stats.WithinLimits = len(within)
	sort.SliceStable(within, func(i, j int) bool {
		a, b := within[i].route, within[j].route
		if req.SortBy == "distance" {
			return a.DistanceKm < b.DistanceKm
		}
		return a.DurationMin < b.DurationMin
	})
	if len(within) > req.Limit {
		within = within[:req.Limit]
	}
	resp.Results = make([]Result, 0, len(within))
	for i, c := range within {
		resp.Results = append(resp.Results, Result{
			Rank:           i + 1,
			Station:        c.st,
			Location:       c.loc,
			DistanceKm:     c.route.DistanceKm,
			DurationMin:    c.route.DurationMin,
			StraightLineKm: c.straight,
			Vacancies:      c.vacs,
		})
	}
	return resp, nil
}

func routeKey(o geo.LatLng, d geo.LatLng) string {
	return fmt.Sprintf("%.4f,%.4f>%.5f,%.5f", o.Lat, o.Lng, d.Lat, d.Lng)
}

// route fills c.route for each candidate, using an in-memory cache keyed by
// (origin rounded to ~10 m, destination).
func (s *Service) route(ctx context.Context, origin geo.LatLng, cands []*candidate) (int, error) {
	var missing []*candidate
	hits := 0
	s.mu.Lock()
	for _, c := range cands {
		if r, ok := s.routeCache[routeKey(origin, *c.loc.Location)]; ok {
			c.route = r
			hits++
		} else {
			missing = append(missing, c)
		}
	}
	s.mu.Unlock()
	if len(missing) == 0 {
		return hits, nil
	}
	dests := make([]geo.LatLng, len(missing))
	for i, c := range missing {
		dests[i] = *c.loc.Location
	}
	routes, err := s.client.RouteMatrix(ctx, origin, dests, s.cfg.Google.TravelMode)
	if err != nil {
		return hits, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.routeCache) > 200_000 {
		s.routeCache = map[string]google.Route{}
	}
	for i, c := range missing {
		c.route = routes[i]
		s.routeCache[routeKey(origin, *c.loc.Location)] = routes[i]
	}
	return hits, nil
}
