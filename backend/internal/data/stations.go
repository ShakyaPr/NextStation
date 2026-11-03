// Package data loads the vacancy list and groups it into stations.
package data

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

type Vacancy struct {
	Province    string `json:"province"`
	District    string `json:"district"`
	Institution string `json:"institution"`
	Designation string `json:"designation"`
	Type        string `json:"type"`
	Page        int    `json:"page"`
}

// Station is a unique institution (district + institution name) with its vacancies.
type Station struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	District  string    `json:"district"`
	Province  string    `json:"province"`
	Category  string    `json:"category"`
	Vacancies []Vacancy `json:"vacancies"`
}

// Categories in display order.
var Categories = []string{
	"Teaching / Specialised Hospital",
	"District General Hospital",
	"Base Hospital",
	"Divisional Hospital",
	"PMCU",
	"MOH Office",
	"RDHS",
	"Other",
}

var suffixExpansions = map[string]string{
	"DH":   "Divisional Hospital",
	"BH":   "Base Hospital",
	"DGH":  "District General Hospital",
	"TH":   "Teaching Hospital",
	"PMCU": "Primary Medical Care Unit",
	"MOH":  "MOH Office",
	"RDHS": "RDHS Office",
}

func lastWord(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return f[len(f)-1]
}

func categorize(name string) string {
	switch lastWord(name) {
	case "DH":
		return "Divisional Hospital"
	case "BH":
		return "Base Hospital"
	case "DGH":
		return "District General Hospital"
	case "TH", "HOSPITAL":
		return "Teaching / Specialised Hospital"
	case "PMCU":
		return "PMCU"
	case "MOH":
		return "MOH Office"
	case "RDHS":
		return "RDHS"
	}
	return "Other"
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(nonAlnum.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

func titleCase(s string) string {
	words := strings.Fields(strings.ToLower(s))
	for i, w := range words {
		upper := strings.ToUpper(w)
		if _, ok := suffixExpansions[upper]; ok || upper == "STD" || upper == "ICU" {
			words[i] = upper
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// queryDistrict maps RDHS areas that are not administrative districts.
func queryDistrict(d string) string {
	if d == "KALMUNAI" {
		return "Ampara"
	}
	return titleCase(d)
}

// SearchQuery is the free-text query used to locate the station (abbreviations expanded).
func (s *Station) SearchQuery() string {
	words := strings.Fields(s.Name)
	if exp, ok := suffixExpansions[words[len(words)-1]]; ok {
		words[len(words)-1] = exp
	}
	name := titleCase(strings.Join(words, " "))
	return fmt.Sprintf("%s, %s District, Sri Lanka", name, queryDistrict(s.District))
}

// TownQueries are fallback queries (town name only) when the station itself is not found.
func (s *Station) TownQueries() []string {
	words := strings.Fields(s.Name)
	if _, ok := suffixExpansions[words[len(words)-1]]; ok && len(words) > 1 {
		words = words[:len(words)-1]
	}
	district := queryDistrict(s.District)
	var qs []string
	if len(words) > 1 {
		qs = append(qs, fmt.Sprintf("%s, %s District, Sri Lanka", titleCase(strings.Join(words, " ")), district))
	}
	qs = append(qs, fmt.Sprintf("%s, %s District, Sri Lanka", titleCase(words[0]), district))
	return qs
}

// LoadStations reads vacancies.json and groups rows into stations.
func LoadStations(path string) ([]*Station, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read vacancies: %w", err)
	}
	var rows []Vacancy
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("parse vacancies: %w", err)
	}
	byID := map[string]*Station{}
	var out []*Station
	for _, v := range rows {
		id := slug(v.District + " " + v.Institution)
		st, ok := byID[id]
		if !ok {
			st = &Station{
				ID:       id,
				Name:     v.Institution,
				District: v.District,
				Province: v.Province,
				Category: categorize(v.Institution),
			}
			byID[id] = st
			out = append(out, st)
		}
		st.Vacancies = append(st.Vacancies, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].District != out[j].District {
			return out[i].District < out[j].District
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}
