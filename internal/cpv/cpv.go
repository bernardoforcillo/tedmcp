// Package cpv answers the question every procurement search starts with:
// which codes describe what this company does.
//
// The Common Procurement Vocabulary is how TED is indexed, and it is also the
// place a search fails most quietly. There are 9,454 codes; pick the wrong
// family and the result is an empty list, which reads exactly like "there are
// no such tenders". Nothing downstream can recover from that, because
// everything downstream is conditioned on the code.
//
// So this package exists to be consulted before searching, not after. It looks
// the vocabulary up by words in any of the 24 official languages and reports
// two things: the codes that matched, and — more useful — the families they
// fall into, with the filter value to search them by. A bid office working
// across sectors it does not know is the case this is for.
//
// The vocabulary is embedded rather than fetched. It is a published, stable
// list, and a tool whose job is to stop silent failure should not itself
// depend on a network call to work.
package cpv

import (
	"compress/gzip"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// The vocabulary is Commission Regulation (EC) No 213/2008 (CPV 2008), taken
// from the MIT-licensed cpv-eu package. See doc.md in this directory for how
// to regenerate it.
//
//go:embed cpv.json.gz
var vocabulary embed.FS

// Entry is one code and what it is called in each language.
type Entry struct {
	Code   string            `json:"code"`
	Level  int               `json:"level" jsonschema:"1 division, 2 group, 3 class, 4 category"`
	Labels map[string]string `json:"labels,omitempty" jsonschema:"the code's name, keyed by ISO 639-1 language"`
}

// Label returns the entry's name in the first language that has one, trying
// the caller's preference before falling back to English.
func (e Entry) Label(langs ...string) (string, string) {
	for _, l := range append(append([]string{}, langs...), "en") {
		if s := e.Labels[strings.ToLower(l)]; s != "" {
			return s, strings.ToLower(l)
		}
	}
	for l, s := range e.Labels {
		return s, l
	}
	return "", ""
}

// Match is one code whose name contains the words searched for.
type Match struct {
	Code     string `json:"code"`
	Level    int    `json:"level"`
	Label    string `json:"label"`
	Language string `json:"language" jsonschema:"the language the label matched in"`
	Family   string `json:"family,omitempty" jsonschema:"the 3-digit family this code belongs to"`
}

// Family is a group of matches that share a 3-digit prefix, with the filter
// value that searches all of it.
//
// This is the part worth reading. TED matches child codes automatically, so a
// search is nearly always better expressed as the family than as the handful
// of exact codes that happened to match a word — and a family is also what a
// person who knows the sector can confirm or correct at a glance.
type Family struct {
	Prefix  string `json:"prefix" jsonschema:"3-digit CPV prefix"`
	Label   string `json:"label,omitempty" jsonschema:"what the family is called"`
	Matches int    `json:"matches" jsonschema:"codes under this family that matched"`
	Filter  string `json:"filter" jsonschema:"pass this as a cpv value to search_tenders or scan_tenders"`
}

// Result is what a lookup found.
type Result struct {
	Matches  []Match     `json:"matches,omitempty"`
	Families []Family    `json:"families,omitempty"`
	Total    int         `json:"total" jsonschema:"codes that matched, before any limit"`
	Searched int         `json:"searched" jsonschema:"codes in the vocabulary"`
	Words    []WordCount `json:"words,omitempty" jsonschema:"when nothing matched, how many codes carry each word on its own — the rarest first, since that is the one worth searching"`
}

// WordCount is how many of the vocabulary's names carry one word.
type WordCount struct {
	Word  string `json:"word"`
	Codes int    `json:"codes"`
}

var (
	once    sync.Once
	entries []Entry
	folded  []map[string]string // the same labels, accent-folded, for searching
	byCode  map[string]int
	loadErr error
)

// load reads the vocabulary the first time it is needed. A server that never
// looks a code up never pays for it.
func load() ([]Entry, map[string]int, error) {
	once.Do(func() {
		f, err := vocabulary.Open("cpv.json.gz")
		if err != nil {
			loadErr = err
			return
		}
		defer f.Close()

		zr, err := gzip.NewReader(f)
		if err != nil {
			loadErr = err
			return
		}
		defer zr.Close()

		raw, err := io.ReadAll(zr)
		if err != nil {
			loadErr = err
			return
		}
		if err := json.Unmarshal(raw, &entries); err != nil {
			loadErr = err
			return
		}
		// Fold every label once here rather than on every search. The
		// vocabulary is read far more often than it is loaded, and folding
		// 227,000 labels per query was most of a lookup's cost.
		byCode = make(map[string]int, len(entries))
		folded = make([]map[string]string, len(entries))
		for i, e := range entries {
			byCode[e.Code] = i
			f := make(map[string]string, len(e.Labels))
			for lang, label := range e.Labels {
				f[lang] = fold(label)
			}
			folded[i] = f
		}
	})
	return entries, byCode, loadErr
}

// Size reports how many codes the vocabulary holds.
func Size() (int, error) {
	all, _, err := load()
	return len(all), err
}

// Get returns one code, with the codes it sits under.
//
// Ancestry is read off the digits rather than stored: CPV is hierarchical by
// construction, so 90511000 sits under 90510000, 90500000 and 90000000, and a
// stored parent link could only ever disagree with that.
func Get(code string) (Entry, []Entry, error) {
	all, index, err := load()
	if err != nil {
		return Entry{}, nil, err
	}
	code = normalize(code)
	i, ok := index[code]
	if !ok {
		return Entry{}, nil, fmt.Errorf("no CPV code %q in the vocabulary", code)
	}

	var ancestors []Entry
	for n := 2; n < 8; n++ {
		parent := code[:n] + strings.Repeat("0", 8-n)
		if parent == code {
			continue
		}
		if j, ok := index[parent]; ok {
			ancestors = append(ancestors, all[j])
		}
	}
	return all[i], ancestors, nil
}

// Search finds the codes whose name contains every word given.
//
// Languages narrow which names are looked at; with none, all 24 are searched.
// That is the right default here even though it can cross languages: unlike a
// term matched against a notice, every hit comes back labelled with the
// language it matched in, so a coincidence is visible rather than silent.
func Search(text string, languages []string, limit int) (*Result, error) {
	all, _, err := load()
	if err != nil {
		return nil, err
	}

	words := foldFields(text)
	if len(words) == 0 {
		return nil, fmt.Errorf("nothing to look up: give some words, e.g. \"waste collection\" or \"raccolta rifiuti\"")
	}
	wanted := map[string]bool{}
	for _, l := range languages {
		if l = strings.ToLower(strings.TrimSpace(l)); l != "" {
			wanted[l] = true
		}
	}

	// Preference decides which language's label is shown when a code matches in
	// several. English last-but-one, because several languages carry untranslated
	// English text and showing it under their tag would misreport the source.
	pref := append(append([]string{}, languages...), "en")

	res, perFamily, perLanguage := searchWords(all, words, wanted, pref)

	// CPV names work in the language of a contract, not of a trade: "manutenzione
	// verde" is filed under the maintenance of parks, so a phrase that reads
	// naturally can carry no codes at all.
	//
	// Widening to the codes carrying *some* of the words was tried and removed.
	// These labels are short official phrases, so an either-or search over 9,454
	// of them returns noise however it is ranked — ranked by rarity it leads with
	// green tea, ranked by commonness with computer repair. Both are confident
	// and wrong, which is worse than nothing.
	//
	// What helps is telling the caller which of their words the vocabulary knows.
	// Seeing that one word appears in two hundred codes and the other in twelve
	// says immediately which one to search, and says it without pretending to
	// have found an answer.
	if res.Total == 0 && len(words) > 1 {
		res.Words = documentFrequency(all, words, wanted)
	}

	if limit > 0 && len(res.Matches) > limit {
		res.Matches = res.Matches[:limit]
	}

	// Name the families in whichever language the matches themselves came back
	// in: a list of Italian codes under English headings reads as two answers.
	res.Families = rollUp(perFamily, append([]string{dominant(perLanguage)}, pref...))
	return res, nil
}

// searchWords scans the vocabulary once, keeping the codes whose name carries
// every word.
func searchWords(all []Entry, words []string, wanted map[string]bool, pref []string) (*Result, map[string]int, map[string]int) {
	res := &Result{Searched: len(all)}
	perFamily := map[string]int{}
	perLanguage := map[string]int{}

	for i, e := range all {
		label, lang := "", ""
		for l, text := range e.Labels {
			if len(wanted) > 0 && !wanted[l] {
				continue
			}
			if !containsAll(folded[i][l], words) {
				continue
			}
			// A code matching in several languages is still one code; keep the
			// caller's first preference.
			if lang == "" || preferred(pref, l, lang) {
				label, lang = text, l
			}
		}
		if lang == "" {
			continue
		}

		res.Total++
		perLanguage[lang]++
		family := e.Code[:3]
		perFamily[family]++
		res.Matches = append(res.Matches, Match{
			Code: e.Code, Level: e.Level, Label: label, Language: lang, Family: family,
		})
	}

	// Broadest first: the guidance everywhere else is to search a family rather
	// than a leaf, so the codes that are families should be read first.
	sort.SliceStable(res.Matches, func(i, j int) bool {
		if res.Matches[i].Level != res.Matches[j].Level {
			return res.Matches[i].Level < res.Matches[j].Level
		}
		return res.Matches[i].Code < res.Matches[j].Code
	})
	return res, perFamily, perLanguage
}

func containsAll(haystack string, words []string) bool {
	for _, w := range words {
		if !strings.Contains(haystack, w) {
			return false
		}
	}
	return true
}

// documentFrequency counts how many codes carry each word on its own, rarest
// first — which is the order they are worth searching in.
func documentFrequency(all []Entry, words []string, wanted map[string]bool) []WordCount {
	df := make(map[string]int, len(words))
	for i := range all {
		for _, w := range words {
			for l := range folded[i] {
				if len(wanted) > 0 && !wanted[l] {
					continue
				}
				if strings.Contains(folded[i][l], w) {
					df[w]++
					break // count the code once, not once per language
				}
			}
		}
	}

	out := make([]WordCount, 0, len(words))
	for _, w := range words {
		out = append(out, WordCount{Word: w, Codes: df[w]})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Codes < out[j].Codes })
	return out
}

// rollUp turns the matched codes into the families worth searching, commonest
// first.
func rollUp(counts map[string]int, languages []string) []Family {
	all, index, err := load()
	if err != nil {
		return nil
	}

	out := make([]Family, 0, len(counts))
	for prefix, n := range counts {
		f := Family{Prefix: prefix, Matches: n, Filter: prefix + "*"}
		if i, ok := index[prefix+"00000"]; ok {
			f.Label, _ = all[i].Label(languages...)
		}
		out = append(out, f)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Matches != out[j].Matches {
			return out[i].Matches > out[j].Matches
		}
		return out[i].Prefix < out[j].Prefix
	})
	return out
}

// dominant returns the language most of the matches were found in.
func dominant(counts map[string]int) string {
	best, n := "", 0
	for lang, c := range counts {
		if c > n || (c == n && lang < best) {
			best, n = lang, c
		}
	}
	return best
}

// preferred reports whether language a should win over b, by the caller's order.
func preferred(order []string, a, b string) bool {
	rank := func(l string) int {
		for i, o := range order {
			if strings.EqualFold(o, l) {
				return i
			}
		}
		return len(order)
	}
	return rank(a) < rank(b)
}

// normalize accepts a code however it was written: with its check digit, with
// spaces, or padded short.
func normalize(code string) string {
	var digits []rune
	for _, r := range code {
		if unicode.IsDigit(r) {
			digits = append(digits, r)
		}
		if r == '-' {
			break // the check digit follows; it is not part of the code
		}
	}
	s := string(digits)
	if len(s) > 8 {
		s = s[:8]
	}
	return s + strings.Repeat("0", 8-len(s))
}

// fold lower-cases and strips the accents of the Latin alphabets, so a search
// for "gestion" finds "gestión" and one for "elettrico" is not defeated by a
// stray accent. Greek and Cyrillic pass through unchanged: their letters are
// letters, not accented forms of other letters.
func fold(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if repl, ok := accents[r]; ok {
			b.WriteString(repl)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func foldFields(s string) []string {
	return strings.Fields(fold(s))
}

var accents = map[rune]string{
	'á': "a", 'à': "a", 'â': "a", 'ä': "a", 'ã': "a", 'å': "a", 'ā': "a", 'ą': "a", 'ă': "a",
	'é': "e", 'è': "e", 'ê': "e", 'ë': "e", 'ē': "e", 'ė': "e", 'ę': "e", 'ě': "e",
	'í': "i", 'ì': "i", 'î': "i", 'ï': "i", 'ī': "i", 'į': "i",
	'ó': "o", 'ò': "o", 'ô': "o", 'ö': "o", 'õ': "o", 'ø': "o", 'ō': "o", 'ő': "o",
	'ú': "u", 'ù': "u", 'û': "u", 'ü': "u", 'ū': "u", 'ų': "u", 'ű': "u", 'ů': "u",
	'ý': "y", 'ÿ': "y",
	'ç': "c", 'ć': "c", 'č': "c", 'ĉ': "c",
	'ñ': "n", 'ń': "n", 'ň': "n",
	'š': "s", 'ś': "s", 'ş': "s", 'ș': "s",
	'ž': "z", 'ź': "z", 'ż': "z",
	'ť': "t", 'ţ': "t", 'ț': "t",
	'ď': "d", 'đ': "d", 'ð': "d",
	'ł': "l", 'ľ': "l",
	'ř': "r", 'ŕ': "r",
	'ğ': "g", 'ģ': "g",
	'ķ': "k", 'ļ': "l", 'ņ': "n",
	'ß': "ss", 'æ': "ae", 'œ': "oe", 'þ': "th",
}
