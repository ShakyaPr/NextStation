package locations

import (
	"regexp"
	"strings"
)

// Words that say what kind of place a station is, not where it is.
var genericWords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`DH BH DGH TH PMCU MOH RDHS AHWC MH HOSPITAL HOSPITALS BASE DIVISIONAL
		DISTRICT GENERAL TEACHING NATIONAL PRIMARY MEDICAL CARE UNIT UNITS OFFICE CENTRE CENTER CLINIC CHEST
		ESTATE ESTATES NORTH SOUTH EAST WEST CENTRAL NEW OLD UPPER LOWER MENTAL REHABILITATION SPECIAL
		CHILDREN S OF AND THE ONLY RELEIF RELIEF POST CAMP NIHS MEMORIAL STD`) {
		genericWords[w] = true
	}
}

var letters = regexp.MustCompile(`[A-Za-z]+`)

// placeWords are the words of a station name that identify the place, e.g.
// "AHWC GALATHURA ESTATE" -> [galathura].
func placeWords(name string) []string {
	var out []string
	for _, w := range letters.FindAllString(strings.ToUpper(name), -1) {
		if len(w) >= 3 && !genericWords[w] {
			out = append(out, normWord(w))
		}
	}
	return out
}

// normWord smooths over common transliteration differences in Sri Lankan place
// names: Rathnapura ~ Ratnapura, Thalawa ~ Talawa, Elle ~ Ele, Wela ~ Vela.
func normWord(w string) string {
	w = strings.ToLower(w)
	w = strings.ReplaceAll(w, "h", "")
	w = strings.ReplaceAll(w, "w", "v")
	var b strings.Builder
	for i := 0; i < len(w); i++ {
		if i == 0 || w[i] != w[i-1] {
			b.WriteByte(w[i])
		}
	}
	return b.String()
}

// withPairs adds each pair of neighbouring words joined: "akkarayan kulam" also
// matches "akkarayankulam" and "pahala giribawa" matches "pahalagiribawa".
func withPairs(ws []string) []string {
	out := append([]string{}, ws...)
	for i := 0; i+1 < len(ws); i++ {
		out = append(out, ws[i]+ws[i+1])
	}
	return out
}

func consonants(w string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune("aeiouy", r) {
			return -1
		}
		return r
	}, w)
}

func similar(a, b string) bool {
	if a == b {
		return true
	}
	if a[0] != b[0] || min(len(a), len(b)) < 4 {
		return false
	}
	if ratio(a, b) >= 0.8 {
		return true
	}
	ca, cb := consonants(a), consonants(b)
	return len(ca) >= 3 && ca == cb // Naboda ~ Neboda, Muloya ~ Mooloya
}

// ratio is difflib's similarity: 2 * matching characters / total length.
func ratio(a, b string) float64 {
	return 2 * float64(matching(a, b)) / float64(len(a)+len(b))
}

// matching counts the characters in matching blocks (Ratcliff/Obershelp).
func matching(a, b string) int {
	if a == "" || b == "" {
		return 0
	}
	// longest common substring
	best, ai, bi := 0, 0, 0
	prev := make([]int, len(b)+1)
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				cur[j] = prev[j-1] + 1
				if cur[j] > best {
					best, ai, bi = cur[j], i-cur[j], j-cur[j]
				}
			}
		}
		prev = cur
	}
	if best == 0 {
		return 0
	}
	return best + matching(a[:ai], b[:bi]) + matching(a[ai+best:], b[bi+best:])
}

// NameMatches reports whether text (a place's name and address from Google) mentions
// the place a station is named after. It stops a search for "AHWC Galathura Estate"
// from accepting "Health Department Municipal Council Rathnapura".
func NameMatches(stationName, text string) bool {
	keys := withPairs(placeWords(stationName))
	if len(keys) == 0 {
		return true // nothing to check against
	}
	var toks []string
	for _, w := range letters.FindAllString(text, -1) {
		if len(w) >= 3 {
			toks = append(toks, normWord(w))
		}
	}
	toks = withPairs(toks)
	for _, k := range keys {
		for _, t := range toks {
			if similar(k, t) {
				return true
			}
		}
	}
	return false
}
