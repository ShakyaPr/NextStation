// Package locations geocodes stations and persists the results.
//
// Two files are used:
//   - geocache.json  – results from Google, written automatically
//   - overrides.json – manual corrections; always take precedence
package locations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"stationfinder/internal/data"
	"stationfinder/internal/geo"
	"stationfinder/internal/google"
)

type Entry struct {
	Status             string      `json:"status"` // ok | failed
	Location           *geo.LatLng `json:"location,omitempty"`
	Source             string      `json:"source,omitempty"` // places | geocode | geocode-town | manual
	Query              string      `json:"query,omitempty"`
	MatchedName        string      `json:"matchedName,omitempty"`
	Address            string      `json:"address,omitempty"`
	DistrictDistanceKm float64     `json:"districtDistanceKm,omitempty"`
	Suspect            bool        `json:"suspect,omitempty"`
	Error              string      `json:"error,omitempty"`
	UpdatedAt          time.Time   `json:"updatedAt"`
}

type Progress struct {
	Running bool   `json:"running"`
	Total   int    `json:"total"`
	Done    int    `json:"done"`
	Failed  int    `json:"failed"`
	LastErr string `json:"lastError,omitempty"`
}

type Store struct {
	mu        sync.RWMutex
	cache     map[string]*Entry
	overrides map[string]*Entry
	cachePath string
	overPath  string

	client      *google.Client
	usePlaces   bool
	suspectKm   float64
	progress    Progress
	progressMux sync.Mutex
}

// Paths holds the writable files and their optional read-only seeds.
type Paths struct {
	Cache, Overrides         string
	CacheSeed, OverridesSeed string
}

func NewStore(cachePath, overridesPath string, client *google.Client, usePlaces bool, suspectKm float64) (*Store, error) {
	return NewStoreWithSeeds(Paths{Cache: cachePath, Overrides: overridesPath}, client, usePlaces, suspectKm)
}

func NewStoreWithSeeds(p Paths, client *google.Client, usePlaces bool, suspectKm float64) (*Store, error) {
	cachePath, overridesPath := p.Cache, p.Overrides
	s := &Store{
		cache:     map[string]*Entry{},
		overrides: map[string]*Entry{},
		cachePath: cachePath,
		overPath:  overridesPath,
		client:    client,
		usePlaces: usePlaces,
		suspectKm: suspectKm,
	}
	if err := readWithSeed(cachePath, p.CacheSeed, &s.cache); err != nil {
		return nil, err
	}
	if err := readWithSeed(overridesPath, p.OverridesSeed, &s.overrides); err != nil {
		return nil, err
	}
	return s, nil
}

// readWithSeed reads path, or seed when path does not exist yet.
func readWithSeed(path, seed string, v any) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) && seed != "" {
		log.Printf("%s not found, loading seed %s", path, seed)
		return readJSON(seed, v)
	}
	return readJSON(path, v)
}

func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Get returns the effective entry for a station (override first).
func (s *Store) Get(id string) *Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if e, ok := s.overrides[id]; ok {
		return e
	}
	return s.cache[id]
}

// SetManual stores a manual location for a station.
func (s *Store) SetManual(st *data.Station, loc geo.LatLng) (*Entry, error) {
	e := &Entry{Status: "ok", Location: &loc, Source: "manual", UpdatedAt: time.Now()}
	s.annotate(st, e)
	e.Suspect = false
	s.mu.Lock()
	defer s.mu.Unlock()
	s.overrides[st.ID] = e
	return e, writeJSON(s.overPath, s.overrides)
}

// ClearManual removes a manual override.
func (s *Store) ClearManual(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.overrides, id)
	return writeJSON(s.overPath, s.overrides)
}

func (s *Store) Progress() Progress {
	s.progressMux.Lock()
	defer s.progressMux.Unlock()
	return s.progress
}

func (s *Store) annotate(st *data.Station, e *Entry) {
	if e.Location == nil {
		return
	}
	if c, ok := geo.DistrictCentres[st.District]; ok {
		e.DistrictDistanceKm = geo.HaversineKm(c, *e.Location)
		e.Suspect = e.DistrictDistanceKm > s.suspectKm
	}
}

// Run geocodes every station lacking a cached entry (or, with retryFailed, a failed one).
// It returns immediately if a run is already in progress.
func (s *Store) Run(ctx context.Context, stations []*data.Station, workers int, retryFailed bool) error {
	if !s.client.Enabled() {
		return errors.New("google api key is not configured")
	}
	var todo []*data.Station
	for _, st := range stations {
		e := s.Get(st.ID)
		if e == nil || (retryFailed && e.Status != "ok") {
			todo = append(todo, st)
		}
	}
	s.progressMux.Lock()
	if s.progress.Running {
		s.progressMux.Unlock()
		return errors.New("geocoding already running")
	}
	s.progress = Progress{Running: len(todo) > 0, Total: len(todo)}
	s.progressMux.Unlock()
	if len(todo) == 0 {
		return nil
	}

	go func() {
		log.Printf("geocoding %d stations with %d workers", len(todo), workers)
		jobs := make(chan *data.Station)
		var wg sync.WaitGroup
		for range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for st := range jobs {
					e := s.locate(ctx, st)
					s.mu.Lock()
					s.cache[st.ID] = e
					s.mu.Unlock()
					s.progressMux.Lock()
					s.progress.Done++
					if e.Status != "ok" {
						s.progress.Failed++
						s.progress.LastErr = e.Error
					}
					if s.progress.Done%25 == 0 {
						s.saveCache()
					}
					s.progressMux.Unlock()
				}
			}()
		}
		for _, st := range todo {
			if ctx.Err() != nil {
				break
			}
			jobs <- st
		}
		close(jobs)
		wg.Wait()
		s.saveCache()
		s.progressMux.Lock()
		s.progress.Running = false
		p := s.progress
		s.progressMux.Unlock()
		log.Printf("geocoding finished: %d done, %d failed", p.Done, p.Failed)
	}()
	return nil
}

func (s *Store) saveCache() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := writeJSON(s.cachePath, s.cache); err != nil {
		log.Printf("save geocache: %v", err)
	}
}

// locate tries, in order: Places text search, Geocoding of the full name,
// then Geocoding of the town name. A candidate within the district radius wins;
// otherwise the first result found is kept and flagged suspect.
func (s *Store) locate(ctx context.Context, st *data.Station) *Entry {
	query := st.SearchQuery()
	var fallback *Entry
	var lastErr error

	consider := func(e *Entry) bool {
		s.annotate(st, e)
		if !e.Suspect {
			return true
		}
		if fallback == nil {
			fallback = e
		}
		return false
	}

	if s.usePlaces {
		places, err := s.client.PlacesTextSearch(ctx, query)
		if err != nil && !errors.Is(err, google.ErrNotFound) {
			lastErr = err
		}
		for _, p := range places {
			e := &Entry{Status: "ok", Location: &p.Location, Source: "places", Query: query,
				MatchedName: p.Name, Address: p.Address, UpdatedAt: time.Now()}
			if consider(e) {
				return e
			}
		}
	}

	if p, err := s.client.Geocode(ctx, query); err == nil {
		e := &Entry{Status: "ok", Location: &p.Location, Source: "geocode", Query: query,
			Address: p.Address, UpdatedAt: time.Now()}
		if consider(e) {
			return e
		}
	} else if !errors.Is(err, google.ErrNotFound) {
		lastErr = err
	}

	for _, tq := range st.TownQueries() {
		p, err := s.client.Geocode(ctx, tq)
		if err != nil {
			if !errors.Is(err, google.ErrNotFound) {
				lastErr = err
			}
			continue
		}
		e := &Entry{Status: "ok", Location: &p.Location, Source: "geocode-town", Query: tq,
			Address: p.Address, UpdatedAt: time.Now()}
		if consider(e) {
			return e
		}
	}

	if fallback != nil {
		return fallback
	}
	msg := "not found"
	if lastErr != nil {
		msg = lastErr.Error()
	}
	return &Entry{Status: "failed", Query: query, Error: msg, UpdatedAt: time.Now()}
}
