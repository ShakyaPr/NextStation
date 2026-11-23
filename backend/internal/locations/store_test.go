package locations

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestSeedEntriesMergeWithWritableFile(t *testing.T) {
	seedDir, runDir := t.TempDir(), t.TempDir()
	seed := filepath.Join(seedDir, "geocache.json")
	own := filepath.Join(runDir, "geocache.json")
	// The seed (newer image) knows a, b and c; the volume has its own a, a failed b and d.
	os.WriteFile(seed, []byte(`{
		"a":{"status":"ok","location":{"lat":7.1,"lng":80.1}},
		"b":{"status":"ok","location":{"lat":7.2,"lng":80.2}},
		"c":{"status":"ok","location":{"lat":7.3,"lng":80.3}}}`), 0o444)
	os.WriteFile(own, []byte(`{
		"a":{"status":"ok","location":{"lat":8.1,"lng":81.1}},
		"b":{"status":"failed","error":"not found"},
		"d":{"status":"ok","location":{"lat":8.4,"lng":81.4}}}`), 0o644)
	s, err := NewStoreWithSeeds(Paths{Cache: own, Overrides: filepath.Join(runDir, "o.json"), CacheSeed: seed}, google.New(""), true, 75)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{"a": 8.1, "b": 7.2, "c": 7.3, "d": 8.4}
	for id, lat := range want {
		if e := s.Get(id); e == nil || e.Location == nil || e.Location.Lat != lat {
			t.Errorf("%s = %+v, want lat %v", id, e, lat)
		}
	}
}

func TestAPIErrorsAreNotCached(t *testing.T) {
	// Google rejects every call (e.g. a wrong key on first start).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"status":"REQUEST_DENIED","error_message":"bad key","error":{"code":403,"message":"bad key","status":"PERMISSION_DENIED"}}`)
	}))
	defer srv.Close()
	client := google.NewWithEndpoints("wrong-key", google.Endpoints{PlacesSearchText: srv.URL, Geocode: srv.URL})
	dir := t.TempDir()
	s, err := NewStore(filepath.Join(dir, "c.json"), filepath.Join(dir, "o.json"), client, true, 75)
	if err != nil {
		t.Fatal(err)
	}
	st := &data.Station{ID: "kegalle-x-dh", Name: "X DH", District: "KEGALLE"}
	if err := s.Run(context.Background(), []*data.Station{st}, 1, false); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); s.Progress().Running; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("geocoding did not finish")
		}
	}
	if p := s.Progress(); p.Failed != 1 || p.LastErr == "" {
		t.Errorf("progress = %+v, want 1 failure with an error", p)
	}
	if e := s.Get(st.ID); e != nil {
		t.Errorf("API failure was cached: %+v (it would never be retried)", e)
	}
}
