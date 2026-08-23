package cpv

import (
	"strings"
	"testing"
)

func TestVocabularyIsComplete(t *testing.T) {
	n, err := Size()
	if err != nil {
		t.Fatalf("the embedded vocabulary did not load: %v", err)
	}
	// CPV 2008 is a fixed list. A different count means the data was
	// regenerated from something else.
	if n != 9454 {
		t.Errorf("got %d codes, want 9454", n)
	}
}

func TestSearchFindsACodeByItsOwnLanguage(t *testing.T) {
	res, err := Search("raccolta rifiuti", nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	if !hasCode(res.Matches, "90511000") {
		t.Errorf("refuse collection was not found among %d matches", res.Total)
	}
	// The family, not the leaf, is what a search should be expressed as.
	if f := familyOf(res.Families, "905"); f == nil {
		t.Errorf("expected the 905 family in the roll-up, got %+v", res.Families)
	} else if f.Filter != "905*" {
		t.Errorf("filter = %q, want 905*", f.Filter)
	}
}

func TestSearchIsMultilingual(t *testing.T) {
	// The same work, asked for in three languages, must reach the same family.
	for _, q := range []string{"waste collection", "raccolta di rifiuti", "collecte des ordures"} {
		res, err := Search(q, nil, 50)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if familyOf(res.Families, "905") == nil {
			t.Errorf("%q did not reach family 905: %+v", q, res.Families)
		}
	}
}

func TestSearchReportsTheLanguageItMatchedIn(t *testing.T) {
	res, err := Search("ristorazione scolastica", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total == 0 {
		t.Fatal("no match")
	}
	// A hit found in an Italian label must not be presented as an English one:
	// searching every language at once is only safe if each hit says which.
	if res.Matches[0].Language != "it" {
		t.Errorf("language = %q, want it", res.Matches[0].Language)
	}
}

func TestSearchNarrowsByLanguage(t *testing.T) {
	res, err := Search("raccolta rifiuti", []string{"de"}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 0 {
		t.Errorf("an Italian phrase matched %d German labels: %+v", res.Total, res.Matches)
	}
}

func TestSearchRequiresEveryWord(t *testing.T) {
	broad, _ := Search("rifiuti", nil, 500)
	narrow, _ := Search("rifiuti radioattivi", nil, 500)

	if narrow.Total == 0 {
		t.Fatal("the narrower search found nothing")
	}
	if narrow.Total >= broad.Total {
		t.Errorf("two words (%d) should match fewer codes than one (%d)", narrow.Total, broad.Total)
	}
}

func TestSearchIgnoresAccents(t *testing.T) {
	with, err := Search("gestión", nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	without, err := Search("gestion", nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	if with.Total == 0 || with.Total != without.Total {
		t.Errorf("accented %d vs unaccented %d — folding is not working", with.Total, without.Total)
	}
}

func TestSearchOrdersBroadestFirst(t *testing.T) {
	res, err := Search("software", nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(res.Matches); i++ {
		if res.Matches[i].Level < res.Matches[i-1].Level {
			t.Fatalf("levels are not ascending: %d after %d", res.Matches[i].Level, res.Matches[i-1].Level)
		}
	}
}

func TestSearchWithNoHitsIsNotAnError(t *testing.T) {
	res, err := Search("zzzzqqqq", nil, 10)
	if err != nil {
		t.Fatalf("an empty result must not be an error: %v", err)
	}
	if res.Total != 0 || len(res.Families) != 0 {
		t.Errorf("expected nothing, got %+v", res)
	}
	if res.Searched == 0 {
		t.Error("the result should still say how many codes were searched")
	}
}

func TestSearchNeedsWords(t *testing.T) {
	if _, err := Search("   ", nil, 10); err == nil {
		t.Error("an empty query must be an error, not a match against everything")
	}
}

func TestGetReturnsTheAncestry(t *testing.T) {
	entry, ancestors, err := Get("90511000")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Code != "90511000" {
		t.Errorf("code = %q", entry.Code)
	}
	if label, _ := entry.Label("it"); !strings.Contains(strings.ToLower(label), "rifiuti") {
		t.Errorf("Italian label = %q", label)
	}

	// Ancestry is read off the digits, narrowest first.
	var codes []string
	for _, a := range ancestors {
		codes = append(codes, a.Code)
	}
	for _, want := range []string{"90000000", "90500000", "90510000"} {
		if !contains(codes, want) {
			t.Errorf("ancestry %v is missing %s", codes, want)
		}
	}
}

func TestGetAcceptsTheCheckDigitAndShortCodes(t *testing.T) {
	for _, in := range []string{"90511000-2", "90511000", "9051100", "905110"} {
		if _, _, err := Get(in); err != nil {
			t.Errorf("Get(%q): %v", in, err)
		}
	}
}

func TestGetUnknownCodeSaysSo(t *testing.T) {
	if _, _, err := Get("99999999"); err == nil {
		t.Error("an unknown code must be an error rather than an empty entry")
	}
}

func TestLabelFallsBackToEnglish(t *testing.T) {
	entry, _, err := Get("90511000")
	if err != nil {
		t.Fatal(err)
	}
	label, lang := entry.Label("xx")
	if label == "" || lang != "en" {
		t.Errorf("label = %q (%s), want an English fallback", label, lang)
	}
}

// --- helpers ---

func hasCode(matches []Match, code string) bool {
	for _, m := range matches {
		if m.Code == code {
			return true
		}
	}
	return false
}

func familyOf(families []Family, prefix string) *Family {
	for i, f := range families {
		if f.Prefix == prefix {
			return &families[i]
		}
	}
	return nil
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
