package requirements

import (
	"fmt"
	"regexp"
	"strings"
)

// The built-in checks are the questions every go/no-go meeting asks, phrased
// the way European tender documents phrase them. They are deliberately whole
// words and set phrases rather than stems — "sopralluog", "garanzia
// provvisoria" — because a requirement check that fires on ordinary
// vocabulary would bury the one passage that matters. Teams add their own with
// CustomCheck; nothing here is meant to be the last word.

type spec struct {
	id, label string
	extract   []string
	flags     map[string]string
	patterns  map[string][]string
}

var builtin = []spec{
	{
		id: "questions_deadline", label: "Deadline for questions / clarifications",
		extract: []string{ExtractDate},
		patterns: map[string][]string{
			"ITA": {`richiest[ae] di chiariment`, `chiarimenti`, `quesiti`},
			"ENG": {`requests? for clarification`, `clarification questions`, `questions (?:must|should|may|shall) be (?:sent|submitted|received)`},
			"FRA": {`demandes? de renseignements`, `questions? (?:doivent|peuvent) être`},
			"DEU": {`Bieterfragen`, `Fragen (?:sind|können) bis`},
			"ESP": {`solicitudes? de aclaraci[oó]n`, `consultas (?:deberán|podrán)`},
		},
	},
	{
		id: "submission_deadline", label: "Submission deadline",
		extract: []string{ExtractDate},
		patterns: map[string][]string{
			"ITA": {`termine (?:ultimo |perentorio )?(?:di|per (?:la )?)?(?:presentazione|ricezione) delle offerte`, `scadenza (?:del termine )?(?:di|per la) presentazione`},
			"ENG": {`(?:time limit|deadline) for (?:the )?(?:receipt|submission) of tenders`, `tenders must be (?:received|submitted) by`},
			"FRA": {`date limite de (?:réception|remise) des offres`},
			"DEU": {`Angebotsfrist`, `Ablauf der Angebotsfrist`},
			"ESP": {`plazo de presentaci[oó]n de (?:las )?ofertas`},
		},
	},
	{
		id: "opening_session", label: "Tender opening / public session",
		extract: []string{ExtractDate},
		patterns: map[string][]string{
			"ITA": {`prima seduta pubblica`, `seduta pubblica`, `apertura delle (?:buste|offerte)`},
			"ENG": {`opening of (?:the )?tenders`, `public opening`},
			"FRA": {`ouverture des plis`},
			"DEU": {`Öffnung der Angebote`, `Submissionstermin`},
			"ESP": {`apertura de (?:las )?(?:ofertas|proposiciones)`},
		},
	},
	{
		id: "turnover", label: "Minimum turnover (economic and financial standing)",
		extract: []string{ExtractAmount},
		patterns: map[string][]string{
			"ITA": {`fatturato (?:globale|specifico|minimo|medio|annuo|complessivo)`, `fatturato`},
			"ENG": {`(?:minimum |annual |average |general |specific )turnover`, `turnover`},
			"FRA": {`chiffre d'affaires`},
			"DEU": {`(?:Mindest)?[Uu]msatz`, `Jahresumsatz`},
			"ESP": {`volumen (?:anual )?de negocios?`},
		},
	},
	{
		id: "similar_contracts", label: "References / similar contracts required",
		extract: []string{ExtractAmount, ExtractDuration},
		patterns: map[string][]string{
			"ITA": {`servizi analoghi`, `forniture analoghe`, `lavori analoghi`, `contratti analoghi`, `servizi? di punta`, `esperienza pregressa`},
			"ENG": {`similar (?:services|contracts|supplies|works)`, `(?:list of )?(?:principal )?references`},
			"FRA": {`prestations similaires`, `références`},
			"DEU": {`Referenzen`, `vergleichbare (?:Leistungen|Aufträge)`},
			"ESP": {`(?:servicios|suministros|trabajos) similares`},
		},
	},
	{
		id: "certifications", label: "Certifications required or rewarded",
		extract: []string{ExtractCodes},
		patterns: map[string][]string{
			AnyLang: {`\bISO\s?\d{4,5}`, `\bSA\s?8000\b`, `\bEMAS\b`},
			"ITA":   {`certificazion[ei] (?:di qualità|ambientale|UNI|ISO)`, `possesso della certificazione`},
			"ENG":   {`certification (?:to|of|under) (?:ISO|EN)`, `quality (?:management|assurance) (?:system|certificate)`},
			"FRA":   {`certification (?:ISO|qualité)`},
			"DEU":   {`Zertifizierung`, `Zertifikat`},
			"ESP":   {`certificaci[oó]n (?:ISO|de calidad)`},
		},
	},
	{
		id: "soa_qualification", label: "SOA qualification (works categories)",
		extract: []string{ExtractCodes},
		patterns: map[string][]string{
			"ITA": {`attestazione SOA`, `qualificazione SOA`, `\bSOA\b[^.\n]{0,40}categori`},
		},
	},
	{
		id: "bid_guarantee", label: "Bid guarantee (garanzia provvisoria)",
		extract: []string{ExtractAmount, ExtractPercent},
		patterns: map[string][]string{
			"ITA": {`garanzia provvisoria`, `cauzione provvisoria`},
			"ENG": {`tender guarantee`, `bid (?:bond|security|guarantee)`},
			"FRA": {`garantie (?:de soumission|provisoire)`},
			"DEU": {`Bietersicherheit`, `Angebotsbürgschaft`},
			"ESP": {`garant[ií]a provisional`},
		},
	},
	{
		id: "performance_guarantee", label: "Performance guarantee (garanzia definitiva)",
		extract: []string{ExtractPercent, ExtractAmount},
		patterns: map[string][]string{
			"ITA": {`garanzia definitiva`, `cauzione definitiva`},
			"ENG": {`performance (?:bond|guarantee|security)`},
			"FRA": {`garantie (?:de bonne exécution|définitive)`},
			"DEU": {`Vertragserfüllungs(?:sicherheit|bürgschaft)`},
			"ESP": {`garant[ií]a definitiva`},
		},
	},
	{
		id: "site_visit", label: "Site visit",
		extract: []string{ExtractDate},
		flags: map[string]string{
			"mandatory": `(?i)obbligatori|mandatory|compulsory|obligatoire|verpflichtend|zwingend|obligatori`,
			"optional":  `(?i)facoltativ|optional|facultati|nicht verpflichtend`,
		},
		patterns: map[string][]string{
			"ITA": {`sopralluog[oh]i?`},
			"ENG": {`site visits?`, `site inspection`},
			"FRA": {`visite (?:des lieux|sur site|obligatoire)`},
			"DEU": {`Ortsbesichtigung`, `Besichtigungstermin`},
			"ESP": {`visita (?:al lugar|a las instalaciones|obligatoria)`},
		},
	},
	{
		id: "subcontracting", label: "Subcontracting rules",
		extract: []string{ExtractPercent},
		patterns: map[string][]string{
			"ITA": {`subappalto`},
			"ENG": {`subcontracting`},
			"FRA": {`sous-traitance`},
			"DEU": {`Unterauftrag`, `Nachunternehmer`},
			"ESP": {`subcontrataci[oó]n`},
		},
	},
	{
		id: "reliance", label: "Reliance on other entities (avvalimento)",
		patterns: map[string][]string{
			"ITA": {`avvalimento`},
			"ENG": {`reliance on (?:the )?capacit`, `rely on the capacities`},
			"FRA": {`capacités d'autres (?:entités|opérateurs)`},
			"DEU": {`Eignungsleihe`},
			"ESP": {`integraci[oó]n de la solvencia`, `capacidades de otras entidades`},
		},
	},
	{
		id: "lot_limits", label: "Limits on lots per bidder",
		patterns: map[string][]string{
			"ITA": {`vincolo di aggiudicazione`, `numero massimo di lotti`, `(?:aggiudicat\w+|partecipare) (?:a )?(?:un )?(?:massimo|non più) di \w+ lott`},
			"ENG": {`maximum number of lots`},
			"FRA": {`nombre maximal de lots`},
			"DEU": {`Höchstzahl (?:der )?Lose`},
			"ESP": {`n[uú]mero m[aá]ximo de lotes`},
		},
	},
	{
		id: "base_value", label: "Base value of the contract (importo a base di gara)",
		extract: []string{ExtractAmount},
		patterns: map[string][]string{
			"ITA": {`importo (?:complessivo )?(?:a )?base (?:d'asta|di gara)`, `valore (?:stimato|complessivo) dell'appalto`, `importo (?:complessivo )?dell'appalto`},
			"ENG": {`estimated (?:total )?value`, `contract value`},
			"FRA": {`montant (?:estimé|total) du marché`, `valeur estimée`},
			"DEU": {`geschätzte[rn]? (?:Gesamt)?[Ww]ert`, `Auftragswert`},
			"ESP": {`presupuesto base de licitaci[oó]n`, `valor estimado`},
		},
	},
	{
		id: "duration", label: "Contract duration and renewals",
		extract: []string{ExtractDuration},
		patterns: map[string][]string{
			"ITA": {`durata (?:del contratto|dell'appalto|del servizio|della concessione)`, `rinnovo`, `proroga tecnica`},
			"ENG": {`(?:duration|term) of the contract`, `contract (?:duration|period)`, `renewal`},
			"FRA": {`durée du marché`, `reconduction`},
			"DEU": {`Vertragslaufzeit`, `Laufzeit`, `Verlängerung`},
			"ESP": {`duraci[oó]n del contrato`, `plazo de ejecuci[oó]n`, `pr[oó]rroga`},
		},
	},
	{
		id: "social_clause", label: "Staff transfer / social clause",
		patterns: map[string][]string{
			"ITA": {`clausola sociale`, `riassorbimento del personale`, `assorbimento del personale`},
			"ENG": {`transfer of undertakings`, `\bTUPE\b`, `transfer of (?:the )?(?:existing )?staff`},
			"FRA": {`reprise du personnel`},
			"DEU": {`Betriebsübergang`, `Personalübernahme`},
			"ESP": {`subrogaci[oó]n (?:del personal|de los trabajadores)`},
		},
	},
	{
		id: "green_criteria", label: "Green criteria (CAM / GPP)",
		patterns: map[string][]string{
			"ITA": {`criteri ambientali minimi`, `\bC\.?A\.?M\.?\b`},
			"ENG": {`green public procurement`, `environmental criteria`},
			"FRA": {`critères environnementaux`},
			"DEU": {`Umweltkriterien`, `umweltfreundliche Beschaffung`},
			"ESP": {`criterios (?:medio)?ambientales`},
		},
	},
	{
		id: "penalties", label: "Penalties",
		extract: []string{ExtractAmount, ExtractPercent},
		patterns: map[string][]string{
			"ITA": {`penali\b`, `penale\b`},
			"ENG": {`liquidated damages`, `penalt(?:y|ies)`},
			"FRA": {`pénalités`},
			"DEU": {`Vertragsstrafe`},
			"ESP": {`penalidades`},
		},
	},
	{
		id: "anac_fee", label: "ANAC contribution",
		extract: []string{ExtractAmount},
		patterns: map[string][]string{
			"ITA": {`contributo (?:a favore dell'|all')?(?:ANAC|Autorità Nazionale Anticorruzione)`},
		},
	},
}

// compiled holds the built-in checks once, since their patterns never change.
var compiled = mustCompile(builtin)

// Builtin returns the built-in checks, optionally narrowed to some IDs.
func Builtin(ids ...string) []Check {
	if len(ids) == 0 {
		return append([]Check(nil), compiled...)
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[strings.TrimSpace(id)] = true
	}
	var out []Check
	for _, c := range compiled {
		if want[c.ID] {
			out = append(out, c)
		}
	}
	return out
}

// BuiltinIDs lists the IDs of the built-in checks with their labels.
func BuiltinIDs() map[string]string {
	out := map[string]string{}
	for _, c := range compiled {
		out[c.ID] = c.Label
	}
	return out
}

// CustomCheck builds a check from caller-supplied terms. Terms are matched as
// literal, case-insensitive phrases; a regex is used as given.
func CustomCheck(id, label string, termsByLanguage map[string][]string, regex string, extractKinds []string) (Check, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Check{}, fmt.Errorf("a custom check needs an id")
	}
	c := Check{ID: id, Label: firstNonEmpty(label, id), Patterns: map[string][]*regexp.Regexp{}}
	for lang, terms := range termsByLanguage {
		key := strings.ToUpper(strings.TrimSpace(lang))
		if key == "" {
			key = AnyLang
		}
		for _, t := range terms {
			if t = strings.TrimSpace(t); t != "" {
				c.Patterns[key] = append(c.Patterns[key], regexp.MustCompile(`(?i)`+regexp.QuoteMeta(t)))
			}
		}
	}
	if r := strings.TrimSpace(regex); r != "" {
		if !strings.HasPrefix(r, "(?") {
			r = "(?i)" + r
		}
		re, err := regexp.Compile(r)
		if err != nil {
			return Check{}, fmt.Errorf("custom check %s: invalid regex: %w", id, err)
		}
		c.Patterns[AnyLang] = append(c.Patterns[AnyLang], re)
	}
	if len(c.Patterns) == 0 {
		return Check{}, fmt.Errorf("custom check %s has no terms or regex", id)
	}
	for _, k := range extractKinds {
		switch k = strings.ToLower(strings.TrimSpace(k)); k {
		case ExtractAmount, ExtractPercent, ExtractDate, ExtractDuration, ExtractCodes:
			c.Extract = append(c.Extract, k)
		case "":
		default:
			return Check{}, fmt.Errorf("custom check %s: unknown extract %q (use amount, percent, date, duration or codes)", id, k)
		}
	}
	return c, nil
}

func mustCompile(specs []spec) []Check {
	out := make([]Check, 0, len(specs))
	for _, s := range specs {
		c := Check{ID: s.id, Label: s.label, Extract: s.extract, Patterns: map[string][]*regexp.Regexp{}}
		for lang, list := range s.patterns {
			for _, p := range list {
				c.Patterns[lang] = append(c.Patterns[lang], regexp.MustCompile(`(?i)`+p))
			}
		}
		if len(s.flags) > 0 {
			c.Flags = map[string]*regexp.Regexp{}
			for name, p := range s.flags {
				c.Flags[name] = regexp.MustCompile(p)
			}
		}
		out = append(out, c)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
