package locations

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

	"stationfinder/internal/data"
	"stationfinder/internal/google"
)

func TestNameMatches(t *testing.T) {
	cases := []struct {
		station, google string
		want            bool
	}{
		// Right place, possibly spelled differently.
		{"DELTOTA DH", "Divisional Hospital Deltota, Deltota", true},
		{"KANDY NATIONAL HOSPITAL", "National Hospital Kandy, William Gopallawa Mawatha, Kandy", true},
		{"NABODA DH", "Divisional Hospital Neboda, Neboda - Wellatha Rd", true},
		{"MULOYA DH", "Divisional Hospital Mooloya", true},
		{"PUNDALUOYA PMCU", "Primary Medical Care Unit Punduloya, Talawakelle", true},
		{"AKKARAYAN KULAM DH", "Divisional Hospital - Akkarayankulam, Kilinochchi", true},
		{"PAHALAGIRIBAWA DH", "Pahala Giribawa Divisional Hospital", true},
		{"RATHNAPURA RDHS", "Regional Directorate of Health Services, Ratnapura", true},
		{"AHWC GALATHURA ESTATE", "Galathura Estate Senior Assistant Superintendent Bungalow, Kehelowitigama", true},
		// Some other place: what Google returns for a facility it doesn't know.
		{"AHWC GALATHURA ESTATE", "Health Department Municipal Council Rathnapura, MCH3+F78, Ratnapura", false},
		{"KERKIL DH", "Teaching Hospital Badulla, Daya Gunasekara Mawatha, Badulla", false},
		{"JAYAPURAM PMCU", "Teaching Hospital Jaffna, Hospital St, Jaffna 40000", false},
		{"ELLAWALA PMCU", "Ellagawa Primary Medical Care Unit", false},
	}
	for _, c := range cases {
		if got := NameMatches(c.station, c.google); got != c.want {
			t.Errorf("NameMatches(%q, %q) = %v, want %v", c.station, c.google, got, c.want)
		}
	}
}

// fakeGoogleRatnapura knows the town of Galathura but not its health centre: Places
// answers a query for the centre with a health office in Ratnapura town, like the real API.
func fakeGoogleRatnapura(t *testing.T) *google.Client {
	mux := http.NewServeMux()
	place := func(name, addr string, lat, lng float64, types ...string) string {
		tj, _ := json.Marshal(types)
		return fmt.Sprintf(`{"id":"x","displayName":{"text":%q},"formattedAddress":%q,"location":{"latitude":%f,"longitude":%f},"types":%s}`,
			name, addr, lat, lng, tj)
	}
	mux.HandleFunc("/places", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ TextQuery string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case strings.HasPrefix(body.TextQuery, "AHWC"):
			// A health office in another town, and a similarly named place 45 km away.
			fmt.Fprintf(w, `{"places":[%s,%s]}`,
				place("Health Department Municipal Council Rathnapura", "MCH3+F78, Ratnapura", 6.6787, 80.4032, "government_office"),
				place("Galathara Tea Factory", "B114", 6.4058, 80.1035, "establishment"))
		case strings.HasPrefix(body.TextQuery, "Galathura"):
			fmt.Fprintf(w, `{"places":[%s]}`, place("Galathura", "Galathura", 6.7038, 80.2907, "administrative_area_level_4", "political"))
		default:
			fmt.Fprint(w, `{}`)
		}
	})
	// Geocoding only knows the district town (what the real API answers for unknown
	// villages), so the town has to come from Places.
	mux.HandleFunc("/geocode", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"OK","results":[{"formatted_address":"Ratnapura, Sri Lanka","types":["locality","political"],"geometry":{"location":{"lat":6.6828,"lng":80.3992}}}]}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return google.NewWithEndpoints("test-key", google.Endpoints{PlacesSearchText: srv.URL + "/places", Geocode: srv.URL + "/geocode"})
}

func runAndWait(t *testing.T, s *Store, stations []*data.Station) {
	t.Helper()
	if err := s.Run(context.Background(), stations, 2, false); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); s.Progress().Running; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("geocoding did not finish")
		}
	}
}

func TestLocateFallsBackToTownAndDistrict(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "c.json")
	// An earlier version had accepted the wrong health office for Galathura.
	os.WriteFile(cachePath, []byte(`{"rathnapura-ahwc-galathura-estate":{"status":"ok","source":"places",
		"location":{"lat":6.6787,"lng":80.4032},"matchedName":"Health Department Municipal Council Rathnapura",
		"address":"MCH3+F78, Ratnapura"}}`), 0o644)
	s, err := NewStore(cachePath, filepath.Join(dir, "o.json"), fakeGoogleRatnapura(t), true, 75)
	if err != nil {
		t.Fatal(err)
	}
	galathura := &data.Station{ID: "rathnapura-ahwc-galathura-estate", Name: "AHWC GALATHURA ESTATE", District: "RATHNAPURA"}
	unknown := &data.Station{ID: "rathnapura-ahwc-nowhereville", Name: "AHWC NOWHEREVILLE", District: "RATHNAPURA"}
	runAndWait(t, s, []*data.Station{galathura, unknown})

	// The wrong cached match was re-checked; the town is used instead.
	e := s.Get(galathura.ID)
	if e == nil || e.Source != "geocode-town" || e.MatchedName != "Galathura" || e.Location.Lat != 6.7038 {
		t.Fatalf("Galathura = %+v, want the town of Galathura", e)
	}
	// Nothing found at all: the district centre, so the station can still be searched.
	e = s.Get(unknown.ID)
	if e == nil || e.Status != "ok" || e.Source != "district" || e.Location == nil {
		t.Fatalf("unknown station = %+v, want the district centre", e)
	}
	if p := s.Progress(); p.Failed != 0 || p.Done != 2 {
		t.Errorf("progress = %+v", p)
	}

	// Once fixed, nothing is looked up again.
	runAndWait(t, s, []*data.Station{galathura, unknown})
	if p := s.Progress(); p.Total != 0 {
		t.Errorf("second run looked up %d stations again", p.Total)
	}
}
