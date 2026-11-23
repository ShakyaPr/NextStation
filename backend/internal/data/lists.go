package data

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ListInfo describes one vacancy or station list. The lists are declared in a
// manifest (data/lists.json); the first entry is the default list.
type ListInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// File holds the extracted rows (relative paths are resolved from the manifest's directory).
	File string `json:"file"`
	// Source is the PDF the list was extracted from (informational).
	Source string `json:"source,omitempty"`
	// Kind is "vacancies" (default: rows are posts) or "stations" (rows are stations,
	// e.g. the difficult stations list; the UI then hides post-related filters).
	Kind string `json:"kind,omitempty"`
}

// StationsOnly reports whether the list names stations rather than posts.
func (l ListInfo) StationsOnly() bool { return l.Kind == "stations" }

// List is a loaded list. A station that appears in several lists has the same ID in
// each, so it shares one location; its vacancies are those of this list only.
type List struct {
	ListInfo
	Stations []*Station
}

// NewList wraps already loaded stations (used by tests and the single-file setup).
func NewList(info ListInfo, stations []*Station) *List {
	return &List{ListInfo: info, Stations: stations}
}

// LoadLists reads the manifest and every list it names.
func LoadLists(manifest string) ([]*List, error) {
	raw, err := os.ReadFile(manifest)
	if err != nil {
		return nil, fmt.Errorf("read lists: %w", err)
	}
	var infos []ListInfo
	if err := json.Unmarshal(raw, &infos); err != nil {
		return nil, fmt.Errorf("parse %s: %w", manifest, err)
	}
	if len(infos) == 0 {
		return nil, fmt.Errorf("%s declares no lists", manifest)
	}
	base := filepath.Dir(manifest)
	seen := map[string]bool{}
	out := make([]*List, 0, len(infos))
	for _, info := range infos {
		if info.ID == "" || info.File == "" {
			return nil, fmt.Errorf("%s: every list needs an id and a file", manifest)
		}
		if seen[info.ID] {
			return nil, fmt.Errorf("%s: duplicate list id %q", manifest, info.ID)
		}
		seen[info.ID] = true
		if info.Name == "" {
			info.Name = info.ID
		}
		if info.Kind != "" && info.Kind != "vacancies" && info.Kind != "stations" {
			return nil, fmt.Errorf("%s: list %q: kind must be \"vacancies\" or \"stations\"", manifest, info.ID)
		}
		path := info.File
		if !filepath.IsAbs(path) {
			path = filepath.Join(base, path)
		}
		stations, err := LoadStations(path)
		if err != nil {
			return nil, fmt.Errorf("list %q: %w", info.ID, err)
		}
		out = append(out, NewList(info, stations))
	}
	return out, nil
}

// LoadConfigured loads the lists named in listsFile, or, when listsFile is empty or
// does not exist, the single vacanciesFile (the original one-list setup).
func LoadConfigured(listsFile, vacanciesFile string) ([]*List, error) {
	if listsFile != "" {
		if _, err := os.Stat(listsFile); err == nil {
			return LoadLists(listsFile)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	stations, err := LoadStations(vacanciesFile)
	if err != nil {
		return nil, err
	}
	return []*List{NewList(ListInfo{ID: "vacancies", Name: "Vacancy list", File: vacanciesFile}, stations)}, nil
}

// AllStations returns one station per ID across all lists (the first one seen), for
// work that is about places rather than vacancies: geocoding and location edits.
func AllStations(lists []*List) []*Station {
	seen := map[string]bool{}
	var out []*Station
	for _, l := range lists {
		for _, st := range l.Stations {
			if !seen[st.ID] {
				seen[st.ID] = true
				out = append(out, st)
			}
		}
	}
	return out
}
