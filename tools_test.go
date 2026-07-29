package main

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

// Notices are searched in every EU language. Slicing their text on byte
// offsets splits a Greek or Cyrillic character in half and yields U+FFFD,
// which corrupts the output silently — it only shows up as a stray "�".
func TestSnippetKeepsCharactersIntact(t *testing.T) {
	texts := []string{
		"Дружеството трябва да има писмени процедури, описващи най-добрите практики за предотвратяване на генерирането на хранителни отпадъци, в съответствие с Наредбата за управление на отпадъците.",
		"Ο ανάδοχος οφείλει να εφαρμόζει διαδικασίες για τη μείωση της σπατάλης τροφίμων στο εστιατόριο του ιδρύματος, σύμφωνα με την ισχύουσα νομοθεσία περί αποβλήτων.",
		"Le titulaire met en place un dispositif de lutte contre le gaspillage alimentaire, incluant le don des excédents aux associations caritatives du territoire.",
	}
	patterns := []string{"хранителни отпадъци", "σπατάλη", "gaspillage"}

	for i, text := range texts {
		re := regexp.MustCompile("(?i)" + patterns[i])
		loc := re.FindStringIndex(text)
		if loc == nil {
			t.Fatalf("case %d: pattern %q not found", i, patterns[i])
		}

		got := snippet(text, loc)
		if strings.ContainsRune(got, utf8.RuneError) {
			t.Errorf("case %d: snippet split a character:\n%s", i, got)
		}
		if !utf8.ValidString(got) {
			t.Errorf("case %d: snippet is not valid UTF-8", i)
		}
		if !strings.Contains(got, patterns[i]) {
			t.Errorf("case %d: snippet lost the match itself: %s", i, got)
		}
	}
}

func TestSnippetAddsEllipsesOnlyWhenCut(t *testing.T) {
	text := "evitar el desperdicio de alimentos."
	loc := regexp.MustCompile("desperdicio").FindStringIndex(text)

	got := snippet(text, loc)
	if got != text {
		t.Fatalf("a text shorter than the context window must come back whole, got %q", got)
	}

	long := strings.Repeat("α", 400) + "gaspillage" + strings.Repeat("β", 400)
	cut := snippet(long, regexp.MustCompile("gaspillage").FindStringIndex(long))
	if !strings.HasPrefix(cut, "…") || !strings.HasSuffix(cut, "…") {
		t.Errorf("a cut snippet must be marked at both ends: %.40s…", cut)
	}
	if strings.ContainsRune(cut, utf8.RuneError) {
		t.Error("cutting a multi-byte text produced a replacement character")
	}
}

func TestTruncateCountsCharacters(t *testing.T) {
	// Ten Cyrillic characters occupy twenty bytes; a byte-based truncation to
	// 10 would cut the fifth character in half.
	s := "аабвгдежзи"
	got := truncate(s, 10)
	if got != s {
		t.Errorf("truncate cut a string that is exactly at the limit: %q", got)
	}

	got = truncate(s, 5)
	if strings.ContainsRune(got, utf8.RuneError) {
		t.Errorf("truncate split a character: %q", got)
	}
	if n := utf8.RuneCountInString(strings.TrimSuffix(got, "…")); n != 5 {
		t.Errorf("truncate kept %d characters, want 5", n)
	}
}

func TestBuildMatcherTermsAreLiteral(t *testing.T) {
	m, err := buildMatcher(ScanInput{Terms: []string{"c.t", "spreco"}})
	if err != nil {
		t.Fatal(err)
	}
	// A dot in a term is a character the buyer wrote, not a wildcard.
	if _, ok := m.find("", "cat"); ok {
		t.Error("terms must be matched literally, not as regular expressions")
	}
	if _, ok := m.find("", "lo Spreco alimentare"); !ok {
		t.Error("term matching must ignore case")
	}
}

func TestBuildMatcherNoTermsMeansNoSearch(t *testing.T) {
	m, err := buildMatcher(ScanInput{})
	if err != nil || m != nil {
		t.Fatalf("expected no matcher and no error, got %v, %v", m, err)
	}
}

func TestBuildMatcherPrefersLanguageAwareTerms(t *testing.T) {
	// Terms tied to a language know what text they may be tested against; a
	// regex applied to every notice does not.
	m, err := buildMatcher(ScanInput{
		TermsByLanguage: map[string][]string{"ITA": {"sprec"}},
		Regex:           "ignored-because-terms-were-given",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.find("DEU", "Gemäß den entsprechenden gesetzlichen Vorgaben."); ok {
		t.Error("an Italian term must not match German boilerplate")
	}
	if _, ok := m.find("ITA", "lo spreco alimentare"); !ok {
		t.Error("the Italian requirement must still match")
	}
}

func TestBuildMatcherFallsBackToRegex(t *testing.T) {
	m, err := buildMatcher(ScanInput{Regex: "gaspill\\w+"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.find("FRA", "lutte contre le gaspillage alimentaire"); !ok {
		t.Error("regex should match when no terms were given")
	}
}

func TestBuildMatcherRejectsInvalidRegex(t *testing.T) {
	if _, err := buildMatcher(ScanInput{Regex: "[unclosed"}); err == nil {
		t.Fatal("an invalid regex must be an error, not an empty search")
	}
}
