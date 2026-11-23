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

func TestLoadListsManifest(t *testing.T) {
	lists, err := LoadLists("../../../data/lists.json")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"vacancies-2025-11-04": 1416, "difficult-stations-2026": 265, "draft-2026": 1300}
	if len(lists) != len(want) {
		t.Fatalf("got %d lists, want %d", len(lists), len(want))
	}
	byID := map[string]map[string]*Station{}
	for _, l := range lists {
		n := 0
		byID[l.ID] = map[string]*Station{}
		for _, st := range l.Stations {
			n += len(st.Vacancies)
			byID[l.ID][st.ID] = st
		}
		if n != want[l.ID] {
			t.Errorf("list %s: %d rows, want %d", l.ID, n, want[l.ID])
		}
	}
	if lists[0].ID != "vacancies-2025-11-04" {
		t.Errorf("default list = %s", lists[0].ID)
	}
	// The same station has the same ID in every list, so it shares one location.
	for _, id := range []string{"kandy-gampola-bh", "kandy-deltota-sisila-mental-rehabilitation-centre"} {
		for l := range want {
			if l == "difficult-stations-2026" && id == "kandy-gampola-bh" {
				continue
			}
			if byID[l][id] == nil {
				t.Errorf("station %s missing from list %s", id, l)
			}
		}
	}
	// Vacancies at difficult stations are marked in the new vacancy list.
	for _, v := range byID["vacancies-2025-11-04"]["kandy-deltota-sisila-mental-rehabilitation-centre"].Vacancies {
		if v.Type != "Difficult station" {
			t.Errorf("vacancy at a difficult station has type %q", v.Type)
		}
	}
	if n := len(AllStations(lists)); n <= len(byID["vacancies-2025-11-04"]) {
		t.Errorf("AllStations = %d, expected the union of all lists", n)
	}
}

func TestPrefixStationTypes(t *testing.T) {
	st := &Station{Name: "AHWC KALUTARA SOUTH (NIHS)", District: "KALUTARA"}
	if c := categorize(st.Name); c != "AHWC" {
		t.Errorf("categorize = %q", c)
	}
	if got, want := st.SearchQuery(), "AHWC Kalutara South (nihs), Kalutara District, Sri Lanka"; got != want {
		t.Errorf("SearchQuery = %q, want %q", got, want)
	}
	tq := st.TownQueries()
	if len(tq) != 2 || tq[0] != "Kalutara South (nihs), Kalutara District, Sri Lanka" || tq[1] != "Kalutara, Kalutara District, Sri Lanka" {
		t.Errorf("TownQueries = %q", tq)
	}
}
