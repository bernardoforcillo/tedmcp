// Package requirements reads a tender's own documents for the facts a bid team
// needs before deciding whether to bid at all: what turnover and references are
// demanded, which certifications, how big the guarantee is, whether the site
// visit is compulsory, when questions close.
//
// Those facts are rarely in the TED notice — it says "see the disciplinare" —
// and finding them by reading forty pages per tender is the slowest part of
// qualifying a pipeline. This package does not decide anything. It finds the
// passages, pulls out the value each one states (an amount, a date, a list of
// ISO codes) and says which file it came from, so a person can confirm in a
// minute what used to take an hour. A check with no passage is reported as not
// found in the documents read, which is a different statement from "not
// required" — and the caller is told which documents were not read.
package requirements

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// AnyLang keys patterns that apply whatever the document's language.
const AnyLang = "*"

// What a check pulls out of the passages it finds.
const (
	ExtractAmount   = "amount"   // € 1.500.000,00
	ExtractPercent  = "percent"  // 2%
	ExtractDate     = "date"     // 14/08/2026 ore 12:00
	ExtractDuration = "duration" // 36 mesi
	ExtractCodes    = "codes"    // ISO 9001, OG1
	ExtractNone     = ""         // the passage is the finding
)

// Check is one question asked of the documents.
type Check struct {
	ID    string
	Label string
	// Patterns are keyed by language (ISO 639-2/T) and tried only against
	// documents in that language, plus AnyLang patterns against every document.
	Patterns map[string][]*regexp.Regexp
	Extract  []string
	// Flags name a property a passage can state, with the pattern that states
	// it — "mandatory" for a site visit, for instance.
	Flags map[string]*regexp.Regexp
}

// Document is one readable file.
type Document struct {
	Name      string
	Container string
	Text      string
}

// Finding is a passage that answers a check.
type Finding struct {
	Document  string `json:"document"`
	Container string `json:"container,omitempty"`
	Text      string `json:"text"`
}

// Result is everything found for one check across a tender's documents.
type Result struct {
	ID       string    `json:"id"`
	Label    string    `json:"label"`
	Found    bool      `json:"found"`
	Values   []string  `json:"values,omitempty" jsonschema:"the amounts, dates, codes or spans the passages state, deduplicated"`
	Flags    []string  `json:"flags,omitempty" jsonschema:"properties the passages state, e.g. mandatory"`
	Findings []Finding `json:"findings,omitempty"`
	Hits     int       `json:"hits" jsonschema:"how many passages matched in total, before the per-check cap"`
}

const (
	contextBefore = 160
	contextAfter  = 320
	valueWindow   = 320 // how far after a match its value may sit
	maxValues     = 8
)

// Run applies the checks to a tender's documents, which should be ordered most
// important first: the first passages found are the ones kept.
func Run(checks []Check, lang string, docs []Document, perCheck int) []Result {
	if perCheck <= 0 {
		perCheck = 3
	}
	lang = strings.ToUpper(strings.TrimSpace(lang))
	out := make([]Result, 0, len(checks))

	for _, c := range checks {
		r := Result{ID: c.ID, Label: c.Label}
		patterns := c.patternsFor(lang)
		seenText := map[string]bool{}
		seenValue := map[string]bool{}
		seenFlag := map[string]bool{}
		perDoc := map[string]int{}

		for _, d := range docs {
			for _, re := range patterns {
				for _, loc := range re.FindAllStringIndex(d.Text, 50) {
					r.Hits++
					window := after(d.Text, loc[0], valueWindow)
					for _, kind := range c.Extract {
						for _, v := range extract(kind, window) {
							key := strings.ToLower(v)
							if !seenValue[key] && len(r.Values) < maxValues {
								seenValue[key] = true
								r.Values = append(r.Values, v)
							}
						}
					}
					around := around(d.Text, loc[0], loc[1], 60, 200)
					for name, fre := range c.Flags {
						if fre.MatchString(around) && !seenFlag[name] {
							seenFlag[name] = true
							r.Flags = append(r.Flags, name)
						}
					}

					// Spread the kept passages over documents: three quotes
					// from the same page say less than one from each file.
					if len(r.Findings) >= perCheck || perDoc[d.Name] >= 2 {
						continue
					}
					text := passage(d.Text, loc[0], loc[1])
					key := dedupeKey(text)
					if seenText[key] {
						continue
					}
					seenText[key] = true
					perDoc[d.Name]++
					r.Findings = append(r.Findings, Finding{Document: d.Name, Container: d.Container, Text: text})
				}
			}
		}
		r.Found = r.Hits > 0
		sort.Strings(r.Flags)
		out = append(out, r)
	}
	return out
}

func (c Check) patternsFor(lang string) []*regexp.Regexp {
	var out []*regexp.Regexp
	out = append(out, c.Patterns[AnyLang]...)
	if lang != "" {
		return append(out, c.Patterns[lang]...)
	}
	// A document whose language is unknown is tried with every language's
	// patterns: these are whole words and phrases, not stems, so the risk of
	// a collision is small next to the cost of a silent gap.
	langs := make([]string, 0, len(c.Patterns))
	for l := range c.Patterns {
		if l != AnyLang {
			langs = append(langs, l)
		}
	}
	sort.Strings(langs)
	for _, l := range langs {
		out = append(out, c.Patterns[l]...)
	}
	return out
}

// Covers reports whether any check has language-specific patterns for lang, so
// a caller can say when a document was only searched with the generic ones.
func Covers(checks []Check, lang string) bool {
	lang = strings.ToUpper(strings.TrimSpace(lang))
	if lang == "" {
		return true
	}
	for _, c := range checks {
		if len(c.Patterns[lang]) > 0 {
			return true
		}
	}
	return false
}

// Languages lists the languages the checks have patterns for.
func Languages(checks []Check) []string {
	set := map[string]bool{}
	for _, c := range checks {
		for l := range c.Patterns {
			if l != AnyLang {
				set[l] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for l := range set {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// --- value extraction ---

var (
	amountRe = regexp.MustCompile(`(?i)(?:€|\beur\b|\beuro\b)\s?\d{1,3}(?:[.,'\s]\d{3})*(?:[.,]\d{1,2})?|\d{1,3}(?:[.,'\s]\d{3})*(?:[.,]\d{1,2})?\s?(?:€|\beur\b|\beuro\b)`)
	// An amount written without a currency still counts when it is large and
	// grouped (1.500.000,00) — small bare numbers are article numbers.
	bareAmountRe = regexp.MustCompile(`\b\d{1,3}(?:\.\d{3}){1,4},\d{2}\b`)
	percentRe    = regexp.MustCompile(`\d{1,3}(?:[.,]\d{1,2})?\s?%`)
	dateRe       = regexp.MustCompile(`(?i)\b\d{1,2}[/.\-]\d{1,2}[/.\-](?:20)?\d{2}\b(?:[^\n\d]{0,14}\d{1,2}[:.]\d{2})?|\b\d{1,2}\s+(?:` + monthWords + `)\s+20\d{2}\b(?:[^\n\d]{0,14}\d{1,2}[:.]\d{2})?`)
	durationRe   = regexp.MustCompile(`(?i)\b\d{1,3}\s?(?:\(\w+\)\s?)?(?:mesi|mese|anni|anno|giorni|months?|years?|days|mois|ans|jours|monate|monaten|jahre|jahren|tage|meses|años|días)\b`)
	isoCodeRe    = regexp.MustCompile(`(?i)\b(?:UNI\s+)?(?:EN\s+)?ISO(?:/IEC)?\s?\d{4,5}(?:-\d)?(?::\s?\d{4})?|\bSA\s?8000\b|\bEMAS\b|\bOHSAS\s?18001\b|\bUNI\s?(?:PdR\s?)?\d{3,5}(?::\d{4})?\b|\bFSC\b|\bEcolabel\b`)
	soaCodeRe    = regexp.MustCompile(`\b(?:OG|OS)\s?-?\s?\d{1,2}(?:-?[A-C])?\b(?:[^\n]{0,30}?class(?:e|ifica)\s+[IVX]{1,5}(?:-bis)?)?`)
)

const monthWords = `gennaio|febbraio|marzo|aprile|maggio|giugno|luglio|agosto|settembre|ottobre|novembre|dicembre|` +
	`january|february|march|april|may|june|july|august|september|october|november|december|` +
	`janvier|février|mars|avril|mai|juin|juillet|août|septembre|octobre|décembre|` +
	`januar|februar|märz|juni|juli|oktober|dezember|enero|febrero|abril|mayo|junio|julio|septiembre|octubre|noviembre|diciembre`

func extract(kind, window string) []string {
	var found []string
	switch kind {
	case ExtractAmount:
		found = amountRe.FindAllString(window, 3)
		if len(found) == 0 {
			found = bareAmountRe.FindAllString(window, 2)
		}
	case ExtractPercent:
		found = percentRe.FindAllString(window, 2)
	case ExtractDate:
		found = dateRe.FindAllString(window, 2)
	case ExtractDuration:
		found = durationRe.FindAllString(window, 2)
	case ExtractCodes:
		found = append(isoCodeRe.FindAllString(window, 6), soaCodeRe.FindAllString(window, 6)...)
	}
	out := make([]string, 0, len(found))
	for _, f := range found {
		if f = strings.Join(strings.Fields(f), " "); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// --- text windows ---
//
// Offsets come from regexp and are in bytes; the windows are counted in
// characters so a capitolato in Greek is never cut through a letter.

func after(text string, start, chars int) string {
	end := start
	for n := 0; end < len(text) && n < chars; n++ {
		_, size := utf8.DecodeRuneInString(text[end:])
		end += size
	}
	return text[start:end]
}

func around(text string, start, end, before, afterChars int) string {
	s, e := bounds(text, start, end, before, afterChars)
	return text[s:e]
}

// bounds widens [start,end) by a number of characters on each side.
func bounds(text string, start, end, before, afterChars int) (int, int) {
	s := start
	for n := 0; s > 0 && n < before; n++ {
		_, size := utf8.DecodeLastRuneInString(text[:s])
		s -= size
	}
	e := end
	for n := 0; e < len(text) && n < afterChars; n++ {
		_, size := utf8.DecodeRuneInString(text[e:])
		e += size
	}
	return s, e
}

func passage(text string, start, end int) string {
	s, e := bounds(text, start, end, contextBefore, contextAfter)
	out := strings.Join(strings.Fields(text[s:e]), " ")
	if s > 0 {
		out = "…" + out
	}
	if e < len(text) {
		out += "…"
	}
	return out
}

func dedupeKey(s string) string {
	r := []rune(strings.ToLower(strings.Trim(s, "… ")))
	if len(r) > 80 {
		r = r[:80]
	}
	return string(r)
}
