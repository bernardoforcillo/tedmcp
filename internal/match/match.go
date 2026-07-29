// Package match searches notice text across the languages of the EU.
//
// Two things make this different from applying one pattern to everything.
//
// First, matching is per language. Every eForms notice declares the language of
// its text, so a caller's Italian terms are only ever tested against Italian
// text. That removes cross-language collisions by construction: the Italian
// stem for waste, "sprec", cannot match the German "entsprechenden", because it
// is never offered German text to match.
//
// Second, a search is not only a list of phrases. The same requirement gets
// written compositionally — "minimizing waste generation" in a food contract,
// "Konzept zur Lebensmittelreduzierung" — so a spec can pair two groups of
// roots and match when a member of each appears close to the other.
//
// The package ships no vocabulary of its own. What to look for, and in which
// languages, is the caller's to say.
package match

import (
	"fmt"
	"strings"
)

// AnyLang is the language key for terms that hold whatever the notice is
// written in — a phrase buyers write in English inside a national notice, a
// product name, a legal reference.
const AnyLang = "*"

// DefaultWindow is how far apart paired roots may sit, in bytes, when the
// caller does not say.
const DefaultWindow = 60

// Near pairs two groups of roots: a match needs one member of each, within
// Window of the other.
type Near struct {
	A      map[string][]string `json:"a,omitempty" jsonschema:"first group of roots, keyed by language code (ISO 639-2/T, e.g. ITA) or * for any language"`
	B      map[string][]string `json:"b,omitempty" jsonschema:"second group of roots, keyed the same way"`
	Window int                 `json:"window,omitempty" jsonschema:"how many characters may separate the two roots (default 60)"`
}

// Spec is what to look for.
type Spec struct {
	// Terms are phrases that carry the meaning on their own, keyed by language
	// code (ISO 639-2/T upper case, e.g. ITA, DEU) or AnyLang.
	Terms map[string][]string

	// Near optionally matches two roots appearing close together.
	Near *Near
}

// Match is where and how a spec was found in a text.
type Match struct {
	Term  string // the entry that matched, or "a + b" for a pair
	Start int    // byte offset in the original text
	End   int
	Pair  bool // true when the match came from root co-occurrence
}

// Empty reports whether the spec would search for nothing.
func (s Spec) Empty() bool {
	for _, terms := range s.Terms {
		if len(terms) > 0 {
			return false
		}
	}
	if s.Near != nil {
		for _, terms := range s.Near.A {
			if len(terms) > 0 {
				return false
			}
		}
	}
	return true
}

// Languages lists the languages the spec has terms for.
func (s Spec) Languages() []string {
	seen := map[string]bool{}
	for _, set := range []map[string][]string{s.Terms, nearMap(s.Near, true), nearMap(s.Near, false)} {
		for lang := range set {
			seen[lang] = true
		}
	}
	out := make([]string, 0, len(seen))
	for lang := range seen {
		out = append(out, lang)
	}
	return out
}

// Describe renders the spec for display.
func (s Spec) Describe() string {
	var parts []string
	if n := countTerms(s.Terms); n > 0 {
		parts = append(parts, fmt.Sprintf("%d term(s)", n))
	}
	if s.Near != nil {
		window := s.Near.Window
		if window <= 0 {
			window = DefaultWindow
		}
		parts = append(parts, fmt.Sprintf("%d+%d paired root(s) within %d chars",
			countTerms(s.Near.A), countTerms(s.Near.B), window))
	}
	if len(parts) == 0 {
		return "empty search"
	}
	return fmt.Sprintf("%s across %d language(s)", strings.Join(parts, ", "), len(s.Languages()))
}

// Normalize lower-cases every term and upper-cases every language key, so a
// caller can write them however is natural.
func (s Spec) Normalize() Spec {
	out := Spec{Terms: normalizeSet(s.Terms)}
	if s.Near != nil {
		out.Near = &Near{
			A:      normalizeSet(s.Near.A),
			B:      normalizeSet(s.Near.B),
			Window: s.Near.Window,
		}
	}
	return out
}

// Find reports the first place the spec appears in text written in lang.
//
// An unknown or empty language falls back to every term in the spec: a
// collision the caller can see and dismiss beats a notice silently skipped
// because its language tag was missing.
func (s Spec) Find(lang, text string) (Match, bool) {
	if text == "" {
		return Match{}, false
	}
	lower := strings.ToLower(text)
	lang = strings.ToUpper(strings.TrimSpace(lang))

	if m, ok := findTerms(s.Terms, lang, lower); ok {
		return m, true
	}
	return s.findNear(lang, lower)
}

func findTerms(set map[string][]string, lang, lower string) (Match, bool) {
	best := Match{Start: -1}
	for _, term := range termsFor(set, lang) {
		if i := strings.Index(lower, term); i >= 0 && (best.Start < 0 || i < best.Start) {
			best = Match{Term: term, Start: i, End: i + len(term)}
		}
	}
	if best.Start < 0 {
		return Match{}, false
	}
	return best, true
}

func (s Spec) findNear(lang, lower string) (Match, bool) {
	if s.Near == nil {
		return Match{}, false
	}
	window := s.Near.Window
	if window <= 0 {
		window = DefaultWindow
	}

	as := findAll(lower, termsFor(s.Near.A, lang))
	if len(as) == 0 {
		return Match{}, false
	}
	bs := findAll(lower, termsFor(s.Near.B, lang))
	if len(bs) == 0 {
		return Match{}, false
	}

	best := Match{Start: -1}
	for _, a := range as {
		for _, b := range bs {
			gap := b.Start - a.End
			if b.Start < a.Start {
				gap = a.Start - b.End
			}
			if gap < 0 || gap > window {
				continue
			}
			start, end := min(a.Start, b.Start), max(a.End, b.End)
			if best.Start < 0 || start < best.Start {
				best = Match{Term: a.Term + " + " + b.Term, Start: start, End: end, Pair: true}
			}
		}
	}
	if best.Start < 0 {
		return Match{}, false
	}
	return best, true
}

// termsFor collects the terms that apply to a language: its own plus the
// language-neutral ones.
//
// A text that declares a language the spec says nothing about gets only the
// language-neutral terms — never another language's. Falling back to
// everything there would reintroduce the collisions this design exists to
// prevent: it is how an Italian stem ends up matching German boilerplate.
//
// Only a text carrying no language at all falls back to every term, because
// there the alternative is not a collision but a silent gap.
func termsFor(set map[string][]string, lang string) []string {
	if len(set) == 0 {
		return nil
	}
	if lang == "" {
		var all []string
		for _, terms := range set {
			all = append(all, terms...)
		}
		return all
	}
	return append(append([]string{}, set[lang]...), set[AnyLang]...)
}

// Covers reports whether the spec has anything to look for in text written in
// lang. A caller can use it to see which notices their terms cannot speak to.
func (s Spec) Covers(lang string) bool {
	lang = strings.ToUpper(strings.TrimSpace(lang))
	if lang == "" {
		return true // an untagged text is searched with everything
	}
	for _, set := range []map[string][]string{s.Terms, nearMap(s.Near, true)} {
		if len(set[lang]) > 0 || len(set[AnyLang]) > 0 {
			return true
		}
	}
	return false
}

type located struct {
	Term       string
	Start, End int
}

func findAll(lower string, terms []string) []located {
	var out []located
	for _, t := range terms {
		if i := strings.Index(lower, t); i >= 0 {
			out = append(out, located{Term: t, Start: i, End: i + len(t)})
		}
	}
	return out
}

// Snippet renders the matched passage with surrounding context, counted in
// characters so Greek or Cyrillic text is never cut mid-character.
func Snippet(text string, m Match, context int) string {
	runes := []rune(text)
	start := len([]rune(text[:m.Start])) - context
	end := len([]rune(text[:m.End])) + context
	if start < 0 {
		start = 0
	}
	if end > len(runes) {
		end = len(runes)
	}

	out := strings.TrimSpace(string(runes[start:end]))
	if start > 0 {
		out = "…" + out
	}
	if end < len(runes) {
		out += "…"
	}
	return out
}

// --- helpers ---

func normalizeSet(set map[string][]string) map[string][]string {
	if len(set) == 0 {
		return nil
	}
	out := make(map[string][]string, len(set))
	for lang, terms := range set {
		key := strings.ToUpper(strings.TrimSpace(lang))
		if key == "" {
			key = AnyLang
		}
		for _, t := range terms {
			if t = strings.ToLower(strings.TrimSpace(t)); t != "" {
				out[key] = append(out[key], t)
			}
		}
	}
	return out
}

func countTerms(set map[string][]string) int {
	n := 0
	for _, terms := range set {
		n += len(terms)
	}
	return n
}

func nearMap(n *Near, first bool) map[string][]string {
	if n == nil {
		return nil
	}
	if first {
		return n.A
	}
	return n.B
}
