package requirements

import (
	"strings"
	"testing"
)

// disciplinare reads like the Italian documents the checks are written for:
// requirements scattered over articles, values a line or two after the words
// that introduce them.
const disciplinare = `Art. 6 — Requisiti di capacità economica e finanziaria
Il concorrente deve possedere un fatturato globale minimo annuo riferito agli ultimi tre esercizi
non inferiore a € 1.500.000,00 IVA esclusa.
Art. 7 — Requisiti di capacità tecnica
Esecuzione negli ultimi tre anni di servizi analoghi per un importo complessivo minimo di euro 800.000,00.
Il concorrente deve essere in possesso della certificazione UNI EN ISO 9001:2015 e ISO 22000:2018.
Art. 9 — Garanzia provvisoria
L'offerta è corredata da una garanzia provvisoria pari al 2% del prezzo base, ossia € 89.012,00.
Art. 10 — Sopralluogo
Il sopralluogo è obbligatorio e potrà essere effettuato entro il 10/08/2026.
Art. 11 — Chiarimenti
È possibile ottenere chiarimenti mediante la proposizione di quesiti scritti entro le ore 12:00 del 14/08/2026.
Art. 15 — Clausola sociale
Al fine di promuovere la stabilità occupazionale si applica la clausola sociale.`

func TestRunExtractsValuesFromItalianDocument(t *testing.T) {
	docs := []Document{{Name: "disciplinare.pdf", Text: disciplinare}}
	results := Run(Builtin(), "ITA", docs, 3)

	byID := map[string]Result{}
	for _, r := range results {
		byID[r.ID] = r
	}

	expect := map[string]string{
		"turnover":           "1.500.000,00",
		"similar_contracts":  "800.000,00",
		"certifications":     "ISO 9001:2015",
		"bid_guarantee":      "2%",
		"site_visit":         "10/08/2026",
		"questions_deadline": "14/08/2026",
	}
	for id, want := range expect {
		r := byID[id]
		if !r.Found {
			t.Errorf("%s: not found", id)
			continue
		}
		if !strings.Contains(strings.Join(r.Values, " | "), want) {
			t.Errorf("%s: values %q do not contain %q", id, r.Values, want)
		}
		if len(r.Findings) == 0 || r.Findings[0].Document != "disciplinare.pdf" {
			t.Errorf("%s: finding must name its document: %+v", id, r.Findings)
		}
	}

	if f := byID["site_visit"].Flags; len(f) != 1 || f[0] != "mandatory" {
		t.Errorf("site visit flags = %v, want [mandatory]", f)
	}
	if !byID["social_clause"].Found {
		t.Error("social clause not found")
	}
	if byID["soa_qualification"].Found {
		t.Error("a services tender with no SOA wording must not report one")
	}
	if byID["penalties"].Found {
		t.Error("penalties are not mentioned in the sample")
	}
}

// German text must not be tested with Italian patterns when its language is
// known — and must still be read with the German ones.
func TestRunRespectsDocumentLanguage(t *testing.T) {
	text := "Eine Ortsbesichtigung ist verpflichtend. Der Mindestumsatz beträgt 2.000.000,00 EUR."
	results := Run(Builtin("site_visit", "turnover"), "DEU", []Document{{Name: "Vergabeunterlagen.pdf", Text: text}}, 2)
	for _, r := range results {
		if !r.Found {
			t.Errorf("%s not found in German text", r.ID)
		}
		if r.ID == "turnover" && !strings.Contains(strings.Join(r.Values, " "), "2.000.000,00 EUR") {
			t.Errorf("turnover value: %v", r.Values)
		}
	}
	// Italian patterns are not offered German text.
	if r := Run(Builtin("site_visit"), "DEU", []Document{{Name: "x", Text: "sopralluogo obbligatorio"}}, 1); r[0].Found {
		t.Error("an Italian phrase matched in a document declared German")
	}
}

func TestCustomCheck(t *testing.T) {
	c, err := CustomCheck("food_waste", "Food waste measures", map[string][]string{"ITA": {"eccedenze alimentari"}}, "", []string{"percent"})
	if err != nil {
		t.Fatal(err)
	}
	r := Run([]Check{c}, "ITA", []Document{{Name: "capitolato.pdf", Text: "recupero delle eccedenze alimentari pari ad almeno il 30% dei pasti"}}, 1)
	if !r[0].Found || len(r[0].Values) != 1 || r[0].Values[0] != "30%" {
		t.Fatalf("custom check: %+v", r[0])
	}
	if _, err := CustomCheck("x", "", nil, "", nil); err == nil {
		t.Error("a check with nothing to look for must be rejected")
	}
	if _, err := CustomCheck("x", "", map[string][]string{"ITA": {"a"}}, "", []string{"weight"}); err == nil {
		t.Error("unknown extract kinds must be rejected")
	}
}

func TestPassageMarksCuts(t *testing.T) {
	long := strings.Repeat("parola ", 200) + "sopralluogo obbligatorio" + strings.Repeat(" testo", 200)
	r := Run(Builtin("site_visit"), "ITA", []Document{{Name: "a", Text: long}}, 1)
	f := r[0].Findings[0].Text
	if !strings.HasPrefix(f, "…") || !strings.HasSuffix(f, "…") || !strings.Contains(f, "sopralluogo") {
		t.Errorf("passage: %q", f)
	}
}
