package data

import "testing"

func TestLoadStationsAndQueries(t *testing.T) {
	stations, err := LoadStations("../../../data/vacancies.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(stations) < 400 {
		t.Fatalf("expected ~438 stations, got %d", len(stations))
	}
	total := 0
	ids := map[string]bool{}
	for _, s := range stations {
		total += len(s.Vacancies)
		if ids[s.ID] {
			t.Fatalf("duplicate id %s", s.ID)
		}
		ids[s.ID] = true
	}
	if total != 1300 {
		t.Fatalf("expected 1300 vacancies, got %d", total)
	}

	st := &Station{Name: "DELTOTA DH", District: "KANDY"}
	if got, want := st.SearchQuery(), "Deltota Divisional Hospital, Kandy District, Sri Lanka"; got != want {
		t.Errorf("SearchQuery = %q, want %q", got, want)
	}
	st = &Station{Name: "GAMPOLA UDAPALATHA MOH", District: "KANDY"}
	if got, want := st.SearchQuery(), "Gampola Udapalatha MOH Office, Kandy District, Sri Lanka"; got != want {
		t.Errorf("SearchQuery = %q, want %q", got, want)
	}
	tq := st.TownQueries()
	if len(tq) != 2 || tq[0] != "Gampola Udapalatha, Kandy District, Sri Lanka" || tq[1] != "Gampola, Kandy District, Sri Lanka" {
		t.Errorf("TownQueries = %q", tq)
	}
	st = &Station{Name: "THIRUKKOVIL BH", District: "KALMUNAI"}
	if got, want := st.SearchQuery(), "Thirukkovil Base Hospital, Ampara District, Sri Lanka"; got != want {
		t.Errorf("SearchQuery = %q, want %q", got, want)
	}
	if c := categorize("KANDY NATIONAL HOSPITAL"); c != "Teaching / Specialised Hospital" {
		t.Errorf("categorize = %q", c)
	}
}
