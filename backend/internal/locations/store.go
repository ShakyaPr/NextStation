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
	"strings"
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

// errNotFound is the Entry.Error of a station Google has no result for.
const errNotFound = "not found"

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
	if err := readWithSeed(cachePath, p.CacheSeed, s.cache); err != nil {
		return nil, err
	}
	if err := readWithSeed(overridesPath, p.OverridesSeed, s.overrides); err != nil {
		return nil, err
	}
	return s, nil
}

// readWithSeed loads the read-only seed (if any) and then the writable file on top of
// it. Entries in the writable file win, except that a failed lookup never hides a
// located seed entry. So a persistent volume keeps its own results and edits, and
// still picks up stations located in a newer image.
func readWithSeed(path, seed string, into map[string]*Entry) error {
	if seed != "" && seed != path {
		if err := readJSON(seed, &into); err != nil {
			return err
		}
	}
	own := map[string]*Entry{}
	if err := readJSON(path, &own); err != nil {
		return err
	}
	if len(own) == 0 && seed != "" {
		log.Printf("%s not found or empty, starting from seed %s (%d entries)", path, seed, len(into))
	}
	for id, e := range own {
		if prev, ok := into[id]; ok && e.Status != "ok" && prev.Status == "ok" {
			continue
		}
		into[id] = e
	}
	return nil
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
		if e == nil || (retryFailed && e.Status != "ok") || needsRecheck(st, e) {
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
					// Remember results and genuine "not found"s. A failure caused by the API
					// (bad key, quota, network) is not cached, so the next run retries it.
					if e.Status == "ok" || e.Error == errNotFound {
						s.mu.Lock()
						s.cache[st.ID] = e
						s.mu.Unlock()
					}
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

// nearTownKm: a facility named after a town is within this distance of the town.
const nearTownKm = 15

// locate finds a station, from most to least precise:
//  1. Places Text Search for the station itself. A result only counts if its name or
//     address mentions the station's place (NameMatches) and, when the town is known,
//     it lies near that town: Google happily returns another health office, or a
//     similarly named place elsewhere, for a facility it doesn't know.
//  2. Geocoding of the full name, if Google returns an actual facility.
//  3. The town the station is named after (a town-level location).
//  4. The centre of the district, so the station is still searchable (marked as such).
//
// A candidate within the district radius wins; otherwise the first one found is kept
// and flagged suspect. API errors (bad key, quota) skip step 4, so the station is
// retried on the next run instead of being parked at the district centre.
func (s *Store) locate(ctx context.Context, st *data.Station) *Entry {
	query := st.SearchQuery()
	var fallback *Entry
	var lastErr error
	note := func(err error) {
		if err != nil && !errors.Is(err, google.ErrNotFound) {
			lastErr = err
		}
	}
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
	entry := func(p google.Place, source, q string) *Entry {
		loc := p.Location
		return &Entry{Status: "ok", Location: &loc, Source: source, Query: q,
			MatchedName: p.Name, Address: p.Address, UpdatedAt: time.Now()}
	}

	town := s.findTown(ctx, st, note)
	nearTown := func(p google.Place) bool {
		return town == nil || geo.HaversineKm(p.Location, *town.Location) <= nearTownKm
	}

	// 1. The station itself.
	if s.usePlaces {
		places, err := s.client.PlacesTextSearch(ctx, query)
		note(err)
		for _, p := range places {
			if !NameMatches(st.Name, p.Name+" "+p.Address) || !nearTown(p) {
				continue
			}
			if e := entry(p, "places", query); consider(e) {
				return e
			}
		}
	}

	// 2. Geocoding of the full name. When Google doesn't know the station it answers
	// with a town or area, which step 3 handles more carefully.
	if p, err := s.client.Geocode(ctx, query); err == nil {
		if !p.IsArea() && nearTown(*p) {
			if e := entry(*p, "geocode", query); consider(e) {
				return e
			}
		}
	} else {
		note(err)
	}

	// 3. The town.
	if town != nil && consider(town) {
		return town
	}

	if fallback != nil {
		return fallback
	}
	// 4. The district centre.
	if c, ok := geo.DistrictCentres[st.District]; ok && lastErr == nil {
		e := &Entry{Status: "ok", Location: &c, Source: "district", Query: st.District + " district",
			Address: "Centre of " + st.District + " district", UpdatedAt: time.Now()}
		s.annotate(st, e)
		return e
	}
	msg := errNotFound
	if lastErr != nil {
		msg = lastErr.Error()
	}
	return &Entry{Status: "failed", Query: query, Error: msg, UpdatedAt: time.Now()}
}

// findTown locates the town a station is named after (Geocoding, then Places). A result
// only counts if it names that town: for an unknown village Google answers with the
// district town instead.
func (s *Store) findTown(ctx context.Context, st *data.Station, note func(error)) *Entry {
	for _, tq := range st.TownQueries() {
		name, _, _ := strings.Cut(tq, ",")
		town := func(p google.Place) *Entry {
			loc := p.Location
			return &Entry{Status: "ok", Location: &loc, Source: "geocode-town", Query: tq,
				MatchedName: p.Name, Address: p.Address, UpdatedAt: time.Now()}
		}
		if p, err := s.client.Geocode(ctx, tq); err == nil {
			if NameMatches(name, p.Name+" "+p.Address) {
				return town(*p)
			}
		} else {
			note(err)
		}
		if !s.usePlaces {
			continue
		}
		places, err := s.client.PlacesTextSearch(ctx, tq)
		note(err)
		for _, p := range places {
			if NameMatches(name, p.Name+" "+p.Address) {
				return town(p)
			}
		}
	}
	return nil
}

// needsRecheck finds cached matches made before NameMatches existed that point at a
// different place (e.g. "AHWC Galathura Estate" pinned to a health office in Ratnapura).
func needsRecheck(st *data.Station, e *Entry) bool {
	return e.Source == "places" && !NameMatches(st.Name, e.MatchedName+" "+e.Address)
}
