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
	if len(lists) != 1 || lists[0].ID != "vacancies-2025-11-04" {
		t.Fatalf("lists = %d, want only the 2025.11.04 vacancy list", len(lists))
	}
	l := lists[0]
	rows, difficult, difficultRows := 0, 0, 0
	byID := map[string]*Station{}
	for _, st := range l.Stations {
		byID[st.ID] = st
		rows += len(st.Vacancies)
		if st.Difficult {
			difficult++
			difficultRows += len(st.Vacancies)
		}
	}
	if rows != 1416 || len(l.Stations) != 537 {
		t.Errorf("%d vacancies at %d stations, want 1416 at 537", rows, len(l.Stations))
	}
	// The stations of PDF345 that are on the 2026 difficult stations list.
	if difficult != 115 || difficultRows != 126 {
		t.Errorf("%d difficult stations with %d vacancies, want 115 with 126", difficult, difficultRows)
	}
	if st := byID["kandy-deltota-sisila-mental-rehabilitation-centre"]; st == nil || !st.Difficult {
		t.Errorf("Deltota Sisila Mental Rehabilitation Centre should be difficult: %+v", st)
	}
	if st := byID["kandy-gampola-bh"]; st == nil || st.Difficult {
		t.Errorf("Gampola BH should not be difficult: %+v", st)
	}
	// The difficult list itself must still load (it is the source of the marking).
	hard, err := LoadStations("../../../data/difficult-stations-2026.json")
	if err != nil || len(hard) != 265 {
		t.Fatalf("difficult list: %d stations, err %v", len(hard), err)
	}
	inPDF345 := 0
	for _, st := range hard {
		if byID[st.ID] != nil {
			inPDF345++
			if !byID[st.ID].Difficult {
				t.Errorf("%s is on the difficult list but not marked in PDF345", st.ID)
			}
		}
	}
	if inPDF345 != difficult {
		t.Errorf("%d difficult-list stations are in PDF345 but %d are marked", inPDF345, difficult)
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
