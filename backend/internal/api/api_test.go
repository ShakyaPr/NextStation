package api

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

	"gopkg.in/yaml.v3"

	"stationfinder/internal/config"
	"stationfinder/internal/data"
	"stationfinder/internal/geo"
	"stationfinder/internal/google"
	"stationfinder/internal/locations"
	"stationfinder/internal/search"
)

func newTestServer(t *testing.T) (http.Handler, *[]string) {
	t.Helper()
	var calls []string
	fake := http.NewServeMux()
	fake.HandleFunc("/autocomplete", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		calls = append(calls, fmt.Sprintf("autocomplete:%v:%v:%v", body["input"], body["sessionToken"], body["includedRegionCodes"]))
		fmt.Fprint(w, `{"suggestions":[
			{"placePrediction":{"placeId":"P1","text":{"text":"Kegalle, Sri Lanka"},
			 "structuredFormat":{"mainText":{"text":"Kegalle"},"secondaryText":{"text":"Sri Lanka"}}}},
			{"queryPrediction":{"text":{"text":"kegalle hotels"}}}]}`)
	})
	fake.HandleFunc("/details/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/details/")
		calls = append(calls, fmt.Sprintf("details:%s:%s:%s", id, r.URL.Query().Get("sessionToken"), r.Header.Get("X-Goog-FieldMask")))
		switch id {
		case "P1":
			fmt.Fprint(w, `{"id":"P1","displayName":{"text":"Kegalle"},"formattedAddress":"Kegalle, Sri Lanka","location":{"latitude":7.2513,"longitude":80.3464}}`)
		case "LONDON":
			fmt.Fprint(w, `{"id":"LONDON","displayName":{"text":"London"},"location":{"latitude":51.5,"longitude":-0.12}}`)
		default:
			w.WriteHeader(404)
			fmt.Fprint(w, `{"error":{"code":404,"message":"Not found","status":"NOT_FOUND"}}`)
		}
	})
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	vac := []data.Vacancy{
		{Province: "SABARAGAMUWA PROVINCE", District: "KEGALLE", Institution: "KEGALLE NARANGODA PMCU", Designation: "MO", Type: "New"},
		{Province: "SABARAGAMUWA PROVINCE", District: "KEGALLE", Institution: "KEGALLE UNLOCATED DH", Designation: "MO", Type: "New"},
	}
	raw, _ := json.Marshal(vac)
	vacPath := filepath.Join(dir, "v.json")
	if err := os.WriteFile(vacPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	stations, err := data.LoadStations(vacPath)
	if err != nil {
		t.Fatal(err)
	}
	client := google.NewWithEndpoints("test-key", google.Endpoints{
		PlacesAutocomplete: srv.URL + "/autocomplete",
		PlaceDetails:       srv.URL + "/details/",
	})
	store, err := locations.NewStore(filepath.Join(dir, "c.json"), filepath.Join(dir, "o.json"), client, true, 75)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetManual(stations[0], geo.LatLng{Lat: 7.27, Lng: 80.36}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	svc := search.NewService(cfg, stations, store, client)
	return New(context.Background(), cfg, stations, store, svc, client).Handler(), &calls
}

func get(t *testing.T, h http.Handler, path string, out any) int {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("%s: bad json %q", path, rec.Body.String())
		}
	}
	return rec.Code
}

func TestAutocompleteAndDetails(t *testing.T) {
	h, calls := newTestServer(t)

	var ac struct {
		Places   []google.Suggestion `json:"places"`
		Stations []stationSuggestion `json:"stations"`
	}
	if code := get(t, h, "/api/places/autocomplete?q=kegalle&session=S1", &ac); code != 200 {
		t.Fatalf("autocomplete status %d", code)
	}
	if len(ac.Places) != 1 || ac.Places[0].PlaceID != "P1" || ac.Places[0].MainText != "Kegalle" || ac.Places[0].SecondaryText != "Sri Lanka" {
		t.Errorf("places = %+v", ac.Places)
	}
	// Only the station with a location is suggested.
	if len(ac.Stations) != 1 || ac.Stations[0].Name != "KEGALLE NARANGODA PMCU" || ac.Stations[0].Location.Lat != 7.27 {
		t.Errorf("stations = %+v", ac.Stations)
	}
	if (*calls)[0] != "autocomplete:kegalle:S1:[lk]" {
		t.Errorf("autocomplete request = %s", (*calls)[0])
	}

	// Too-short input: no Google call.
	n := len(*calls)
	get(t, h, "/api/places/autocomplete?q=k", &ac)
	if len(*calls) != n || len(ac.Places) != 0 {
		t.Error("expected no google call for 1-char input")
	}

	var p google.Place
	if code := get(t, h, "/api/places/details?placeId=P1&session=S1", &p); code != 200 {
		t.Fatalf("details status %d", code)
	}
	if p.Location.Lat != 7.2513 || p.Name != "Kegalle" || p.Address != "Kegalle, Sri Lanka" {
		t.Errorf("place = %+v", p)
	}
	if last := (*calls)[len(*calls)-1]; last != "details:P1:S1:id,displayName,formattedAddress,location" {
		t.Errorf("details request = %s", last)
	}

	if code := get(t, h, "/api/places/details?placeId=LONDON", nil); code != 400 {
		t.Errorf("outside Sri Lanka: status %d, want 400", code)
	}
	if code := get(t, h, "/api/places/details?placeId=NOPE", nil); code != 502 {
		t.Errorf("unknown place: status %d, want 502", code)
	}
	if code := get(t, h, "/api/places/details", nil); code != 400 {
		t.Errorf("missing placeId: status %d, want 400", code)
	}
}

func TestOpenAPIMatchesRoutes(t *testing.T) {
	raw, err := os.ReadFile("../../openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	inSpec := map[string]bool{}
	for path, ops := range spec.Paths {
		for method := range ops {
			if method == "parameters" {
				continue
			}
			inSpec[strings.ToUpper(method)+" "+path] = true
		}
	}
	s := &Server{}
	for pattern := range s.routes() {
		if !inSpec[pattern] {
			t.Errorf("route %q is missing from openapi.yaml (the Choreo gateway would block it)", pattern)
		}
		delete(inSpec, pattern)
	}
	for op := range inSpec {
		t.Errorf("openapi.yaml lists %q but the server has no such route", op)
	}
}
