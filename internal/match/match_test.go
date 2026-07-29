package match

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// foodWaste is a caller's search, written here to exercise the engine. The
// package ships no vocabulary; this one exists only in the test.
func foodWaste() Spec {
	return Spec{
		Terms: map[string][]string{
			"ITA": {"eccedenz", "spreco", "sprechi", "avanzi", "non somministrat"},
			"ENG": {"food waste", "leftover", "surplus food"},
			"FRA": {"gaspillage", "invendus"},
			"DEU": {"lebensmittelverschwendung", "speisereste"},
			"SPA": {"desperdicio"},
			"BUL": {"хранителни отпадъци"},
			"HUN": {"ételhulladék", "ételmaradék"},
		},
		Near: &Near{
			Window: 60,
			A: map[string][]string{
				"ENG": {"food", "meal"},
				"DEU": {"lebensmittel", "speise"},
			},
			B: map[string][]string{
				"ENG": {"waste", "minimizing", "reduction"},
				"DEU": {"reduzierung", "vermeidung", "abfall"},
			},
		},
	}.Normalize()
}

// The cases below come from real notices among the 338 open European catering
// tenders. The negatives are what a single multilingual regular expression got
// wrong; the last two positives are what it missed.
func TestFindAgainstRealNotices(t *testing.T) {
	spec := foodWaste()

	tests := []struct {
		name string
		lang string
		text string
		want bool
	}{
		{"italian scored criterion", "ITA",
			"GESTIONE DELLE ECCEDENZE ALIMENTARI: progetto per il recupero degli avanzi alimentari.", true},
		{"italian unserved products", "ITA",
			"Progetto di recupero dei prodotti non somministrati", true},
		{"french award criterion", "FRA",
			"Performance environnementale: dispositif de lutte contre le gaspillage alimentaire.", true},
		{"bulgarian technical requirement", "BUL",
			"Участникът трябва да има писмени процедури за предотвратяване на генерирането на хранителни отпадъци.", true},
		{"english green procurement statement", "ENG",
			"The authority seeks to minimise the environmental impact through reduction of food waste.", true},
		{"spanish description", "SPA",
			"El servicio de cocina deberá evitar el desperdicio de alimentos.", true},

		// Compositional phrasings, which a phrase list alone cannot enumerate.
		{"german food-reduction plan, found by pairing roots", "DEU",
			"Die Vorlage eines Konzeptes zur Lebensmittelreduzierung das während der Vertragslaufzeit umgesetzt wird.", true},
		{"maltese notice where food and waste are not adjacent", "ENG",
			"Supply and Delivery of Midday Snacks and Party Food for Day Centres taking into Consideration Seasonality and Minimizing Waste Generation.", true},
		{"hungarian compound noun", "HUN",
			"Közétkeztetési szolgáltatás biztosítása az ételhulladék csökkentése mellett.", true},

		// What the single regex got wrong: an Italian stem inside German words.
		{"german boilerplate containing the italian stem", "DEU",
			"Gemäß den entsprechenden gesetzlichen Vorgaben des WVRG 2020.", false},
		{"german contact person", "DEU",
			"Referenzen mit Angabe des Umfanges sowie der Ansprechpartner auf dem Fachgebiet.", false},
		{"german conversation noun", "DEU",
			"Die Ergebnisse des Gesprächs werden protokolliert.", false},
		{"spanish advanced technology, not leftovers", "SPA",
			"Se requiere tecnología avanzada para la cocina central.", false},
		{"plain catering description", "FRA",
			"Fourniture et livraison de repas en liaison froide pour les services de restauration.", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, got := spec.Find(tt.lang, tt.text)
			if got != tt.want {
				if got {
					t.Fatalf("matched on %q but should not have", m.Term)
				}
				t.Fatal("did not match, but the requirement is present")
			}
			if got {
				if m.Start < 0 || m.End > len(tt.text) || m.Start >= m.End {
					t.Fatalf("match offsets out of range: %+v", m)
				}
				if s := tt.text[m.Start:m.End]; !utf8.ValidString(s) {
					t.Errorf("match does not land on character boundaries: %q", s)
				}
			}
		})
	}
}

// The whole point of language-aware matching: a term is never offered text it
// cannot belong to.
func TestTermsAreScopedToTheirLanguage(t *testing.T) {
	spec := Spec{Terms: map[string][]string{
		"ITA": {"sprec"},
		"DEU": {"lebensmittelverschwendung"},
	}}.Normalize()

	if _, ok := spec.Find("DEU", "Gemäß den entsprechenden gesetzlichen Vorgaben."); ok {
		t.Error("an Italian term must not be tested against German text")
	}
	if _, ok := spec.Find("ITA", "lo spreco alimentare va ridotto"); !ok {
		t.Error("Italian text should match Italian terms")
	}
}

func TestAnyLanguageTermsApplyEverywhere(t *testing.T) {
	spec := Spec{Terms: map[string][]string{
		AnyLang: {"egalim"},
		"ITA":   {"eccedenz"},
	}}.Normalize()

	for _, lang := range []string{"FRA", "DEU", "BUL"} {
		if _, ok := spec.Find(lang, "conforme à la loi EGalim"); !ok {
			t.Errorf("a language-neutral term should match %s text", lang)
		}
	}
}

func TestUntaggedTextIsSearchedWithEveryTerm(t *testing.T) {
	spec := foodWaste()

	// A text carrying no language must still be searched: there the
	// alternative to a possible collision is a silent gap.
	if _, ok := spec.Find("", "reduction of food waste in the school canteen"); !ok {
		t.Error("an untagged text should still be searched")
	}
	if _, ok := spec.Find("", "lotta allo spreco alimentare"); !ok {
		t.Error("an untagged text should be matched against every language's terms")
	}
}

// A declared language the spec does not cover is the case that matters: it
// must NOT fall back to other languages, or the collisions come straight back.
func TestUncoveredLanguageGetsOnlyNeutralTerms(t *testing.T) {
	spec := Spec{Terms: map[string][]string{
		"ITA":   {"sprec"},
		AnyLang: {"egalim"},
	}}.Normalize()

	if _, ok := spec.Find("DEU", "Gemäß den entsprechenden gesetzlichen Vorgaben."); ok {
		t.Error("a language with no terms of its own must not borrow another's")
	}
	if _, ok := spec.Find("DEU", "Die Vorgaben der EGalim-Richtlinie gelten."); !ok {
		t.Error("language-neutral terms should still apply")
	}

	if spec.Covers("DEU") != true {
		t.Error("a spec with neutral terms does cover every language")
	}
	if (Spec{Terms: map[string][]string{"ITA": {"sprec"}}}).Normalize().Covers("DEU") {
		t.Error("a spec with only Italian terms does not cover German")
	}
}

func TestNearRespectsTheWindow(t *testing.T) {
	spec := foodWaste()

	near := "Minimizing Waste Generation in the supply of Food to day centres"
	if _, ok := spec.Find("ENG", near); !ok {
		t.Error("roots within the window should match")
	}

	far := "Food is delivered daily. " + strings.Repeat("Lorem ipsum dolor sit amet. ", 8) + "Waste containers are emptied weekly."
	if m, ok := spec.Find("ENG", far); ok && m.Pair {
		t.Errorf("roots %q are too far apart to be one requirement", m.Term)
	}
}

func TestEmptyAndNormalize(t *testing.T) {
	if !(Spec{}).Empty() {
		t.Error("a spec with nothing in it should be empty")
	}
	if (Spec{Terms: map[string][]string{"ITA": {}}}).Empty() != true {
		t.Error("a language with no terms should not count as a search")
	}

	// Callers write terms however is natural; matching is case-folded.
	spec := Spec{Terms: map[string][]string{"ita": {"  Spreco  ", ""}}}.Normalize()
	if got := spec.Terms["ITA"]; len(got) != 1 || got[0] != "spreco" {
		t.Errorf("normalize = %q, want [spreco] under key ITA", got)
	}
	if _, ok := spec.Find("ITA", "Lo SPRECO va ridotto"); !ok {
		t.Error("matching should ignore case on both sides")
	}
}

func TestSnippetKeepsCharacterBoundaries(t *testing.T) {
	spec := foodWaste()
	text := "Участникът трябва да има писмени процедури за предотвратяване на генерирането на хранителни отпадъци съгласно Наредбата."

	m, ok := spec.Find("BUL", text)
	if !ok {
		t.Fatal("expected a match")
	}
	s := Snippet(text, m, 20)
	if strings.ContainsRune(s, utf8.RuneError) {
		t.Errorf("snippet split a character: %q", s)
	}
	if !strings.Contains(s, "хранителни отпадъци") {
		t.Errorf("snippet lost the match: %q", s)
	}
}
