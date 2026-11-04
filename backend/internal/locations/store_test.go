package locations

import (
	"os"
	"path/filepath"
	"testing"

	"stationfinder/internal/data"
	"stationfinder/internal/geo"
	"stationfinder/internal/google"
)

func TestSeedFilesAreReadOnlyFallbacks(t *testing.T) {
	seedDir, runDir := t.TempDir(), filepath.Join(t.TempDir(), "nested", "run")
	seed := filepath.Join(seedDir, "geocache.json")
	if err := os.WriteFile(seed, []byte(`{"a":{"status":"ok","location":{"lat":7.1,"lng":80.1},"source":"places"}}`), 0o444); err != nil {
		t.Fatal(err)
	}
	p := Paths{
		Cache:         filepath.Join(runDir, "geocache.json"),
		Overrides:     filepath.Join(runDir, "overrides.json"),
		CacheSeed:     seed,
		OverridesSeed: filepath.Join(seedDir, "missing.json"), // missing seed is fine
	}
	s, err := NewStoreWithSeeds(p, google.New(""), true, 75)
	if err != nil {
		t.Fatal(err)
	}
	if e := s.Get("a"); e == nil || e.Location.Lat != 7.1 {
		t.Fatalf("seed not loaded: %+v", e)
	}

	// Writes go to the (not yet existing) writable dir, never to the seed.
	st := &data.Station{ID: "a", District: "KEGALLE"}
	if _, err := s.SetManual(st, geo.LatLng{Lat: 7.2, Lng: 80.3}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.Overrides); err != nil {
		t.Fatalf("overrides not written to writable dir: %v", err)
	}

	// A restart prefers the writable file over the seed.
	s2, err := NewStoreWithSeeds(p, google.New(""), true, 75)
	if err != nil {
		t.Fatal(err)
	}
	if e := s2.Get("a"); e.Source != "manual" {
		t.Fatalf("expected manual override after restart, got %+v", e)
	}
}
