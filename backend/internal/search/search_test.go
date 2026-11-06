package search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"stationfinder/internal/config"
	"stationfinder/internal/data"
	"stationfinder/internal/geo"
	"stationfinder/internal/google"
	"stationfinder/internal/locations"
)

var towns = map[string]geo.LatLng{
	"Kegalle":    {Lat: 7.2513, Lng: 80.3464},
	"Mawanella":  {Lat: 7.2526, Lng: 80.4455},
	"Warakapola": {Lat: 7.2266, Lng: 80.1978},
	"Kandy":      {Lat: 7.2906, Lng: 80.6337},
	"Ratnapura":  {Lat: 6.6828, Lng: 80.3992},
	"Jaffna":     {Lat: 9.6615, Lng: 80.0255},
	"Nowhere":    {Lat: 0, Lng: 0},
}

func fakeGoogle(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	find := func(q string) (geo.LatLng, bool) {
		for name, p := range towns {
			if strings.HasPrefix(q, name) && p.Lat != 0 {
				return p, true
			}
		}
		return geo.LatLng{}, false
	}
	mux.HandleFunc("/places", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Goog-Api-Key") != "test-key" || r.Header.Get("X-Goog-FieldMask") == "" {
			http.Error(w, `{"error":{"code":403,"message":"bad key","status":"PERMISSION_DENIED"}}`, 403)
			return
		}
		var body struct{ TextQuery string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		p, ok := find(body.TextQuery)
		if !ok {
			fmt.Fprint(w, `{}`)
			return
		}
		fmt.Fprintf(w, `{"places":[{"id":"x","displayName":{"text":%q},"formattedAddress":"addr","location":{"latitude":%f,"longitude":%f}}]}`,
			body.TextQuery, p.Lat, p.Lng)
	})
	mux.HandleFunc("/geocode", func(w http.ResponseWriter, r *http.Request) {
		p, ok := find(r.URL.Query().Get("address"))
		if !ok {
			fmt.Fprint(w, `{"status":"ZERO_RESULTS","results":[]}`)
			return
		}
		fmt.Fprintf(w, `{"status":"OK","results":[{"formatted_address":"addr","types":["locality"],"geometry":{"location":{"lat":%f,"lng":%f}}}]}`, p.Lat, p.Lng)
	})
	mux.HandleFunc("/matrix", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Origins []struct {
				Waypoint struct {
					Location struct {
						LatLng struct{ Latitude, Longitude float64 } `json:"latLng"`
					} `json:"location"`
				} `json:"waypoint"`
			} `json:"origins"`
			Destinations []struct {
				Waypoint struct {
					Location struct {
						LatLng struct{ Latitude, Longitude float64 } `json:"latLng"`
					} `json:"location"`
				} `json:"waypoint"`
			} `json:"destinations"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		o := body.Origins[0].Waypoint.Location.LatLng
		var parts []string
		// Return in reverse order and omit zero indexes, like the real proto3 JSON.
		for i := len(body.Destinations) - 1; i >= 0; i-- {
			d := body.Destinations[i].Waypoint.Location.LatLng
			km := geo.HaversineKm(geo.LatLng{Lat: o.Latitude, Lng: o.Longitude}, geo.LatLng{Lat: d.Latitude, Lng: d.Longitude}) * 1.3
			secs := int(km / 40 * 3600)
			idx := ""
			if i > 0 {
				idx = fmt.Sprintf(`"destinationIndex":%d,`, i)
			}
			parts = append(parts, fmt.Sprintf(`{%s"status":{},"distanceMeters":%d,"duration":"%ds","condition":"ROUTE_EXISTS"}`, idx, int(km*1000), secs))
		}
		fmt.Fprint(w, "["+strings.Join(parts, ",")+"]")
	})
	return httptest.NewServer(mux)
}

func TestSearchEndToEnd(t *testing.T) {
	srv := fakeGoogle(t)
	defer srv.Close()
	dir := t.TempDir()
	vac := []data.Vacancy{
		{Province: "SABARAGAMUWA PROVINCE", District: "KEGALLE", Institution: "MAWANELLA BH", Designation: "MO OPD", Type: "New"},
		{Province: "SABARAGAMUWA PROVINCE", District: "KEGALLE", Institution: "WARAKAPOLA DH", Designation: "MO", Type: "Difficult station"},
		{Province: "CENTRAL PROVINCE", District: "KANDY", Institution: "KANDY NATIONAL HOSPITAL", Designation: "SHO SURGERY", Type: "New"},
		{Province: "SABARAGAMUWA PROVINCE", District: "RATHNAPURA", Institution: "RATNAPURA TH", Designation: "MO", Type: "New"},
		{Province: "NORTHERN PROVINCE", District: "JAFFNA", Institution: "JAFFNA TH", Designation: "MO", Type: "New"},
		{Province: "NORTHERN PROVINCE", District: "JAFFNA", Institution: "NOWHERE PMCU", Designation: "MO", Type: "New"},
	}
	raw, _ := json.Marshal(vac)
	vacPath := filepath.Join(dir, "v.json")
	if err := os.WriteFile(vacPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Google.TravelMode = "DRIVE"
	cfg.Search = config.SearchConfig{DefaultLimit: 10, MaxRouteCandidates: 60, MaxSpeedKmh: 80, SuspectDistanceKm: 75}

	stations, err := data.LoadStations(vacPath)
	if err != nil {
		t.Fatal(err)
	}
	client := google.NewWithEndpoints("test-key", google.Endpoints{
		PlacesSearchText: srv.URL + "/places", Geocode: srv.URL + "/geocode", RouteMatrix: srv.URL + "/matrix",
	})
	store, err := locations.NewStore(filepath.Join(dir, "cache.json"), filepath.Join(dir, "over.json"), client, true, 75)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Run(context.Background(), stations, 2, false); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); store.Progress().Running; {
		if time.Now().After(deadline) {
			t.Fatal("geocoding did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if p := store.Progress(); p.Failed != 1 || p.Done != 6 {
		t.Fatalf("progress = %+v, want 6 done / 1 failed", p)
	}
	if _, err := os.Stat(filepath.Join(dir, "cache.json")); err != nil {
		t.Fatal("geocache not written")
	}

	svc := NewService(cfg, stations, store, client)

	// 1. Closest to Kegalle, no limits.
	resp, err := svc.Search(context.Background(), Request{Origin: Origin{Query: "Kegalle"}})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range resp.Results {
		names = append(names, r.Station.Name)
	}
	want := "MAWANELLA BH,WARAKAPOLA DH,KANDY NATIONAL HOSPITAL,RATNAPURA TH,JAFFNA TH"
	if strings.Join(names, ",") != want {
		t.Fatalf("results = %v, want %s", names, want)
	}
	if resp.Stats.WithoutLocation != 1 || len(resp.Warnings) == 0 {
		t.Errorf("expected 1 station without location + warning, got %+v %v", resp.Stats, resp.Warnings)
	}
	if resp.Results[0].DistanceKm <= 0 || resp.Results[0].DurationMin <= 0 {
		t.Errorf("missing route data: %+v", resp.Results[0])
	}

	// 2. Time limit of 75 min and only "New" vacancies.
	resp, err = svc.Search(context.Background(), Request{
		Origin: Origin{Query: "Kegalle"}, MaxDurationMin: 75, Types: []string{"New"},
	})
	if err != nil {
		t.Fatal(err)
	}
	names = names[:0]
	for _, r := range resp.Results {
		names = append(names, r.Station.Name)
		if r.DurationMin > 75 {
			t.Errorf("%s exceeds time limit: %.1f", r.Station.Name, r.DurationMin)
		}
	}
	if strings.Join(names, ",") != "MAWANELLA BH,KANDY NATIONAL HOSPITAL" {
		t.Fatalf("time-limited results = %v", names)
	}
	if resp.Stats.CacheHits == 0 {
		t.Error("expected route cache hits on second search")
	}

	// 3. Coordinates origin + distance limit + designation filter.
	lat, lng := 7.2513, 80.3464
	resp, err = svc.Search(context.Background(), Request{
		Origin: Origin{Lat: &lat, Lng: &lng}, MaxDistanceKm: 200, Designation: "sho", SortBy: "distance",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Station.Name != "KANDY NATIONAL HOSPITAL" {
		t.Fatalf("designation-filtered results = %+v", resp.Results)
	}

	// 3b. Straight-line ranking orders by the direct distance.
	resp, err = svc.Search(context.Background(), Request{Origin: Origin{Lat: &lat, Lng: &lng}, SortBy: "straight"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(resp.Results); i++ {
		if resp.Results[i-1].StraightLineKm > resp.Results[i].StraightLineKm {
			t.Fatalf("not sorted by straight line: %+v", resp.Results)
		}
	}
	if _, err := svc.Search(context.Background(), Request{Origin: Origin{Lat: &lat, Lng: &lng}, SortBy: "bogus"}); err == nil {
		t.Error("expected error for unknown sortBy")
	}

	// 4. Manual override wins over geocoded location.
	st := stations[0]
	if _, err := store.SetManual(st, geo.LatLng{Lat: 7.0, Lng: 80.0}); err != nil {
		t.Fatal(err)
	}
	if e := store.Get(st.ID); e.Source != "manual" {
		t.Errorf("override not applied: %+v", e)
	}

	// 5. Bad origin.
	if _, err := svc.Search(context.Background(), Request{Origin: Origin{Query: "Atlantis"}}); err == nil {
		t.Error("expected error for unknown origin")
	}
}
