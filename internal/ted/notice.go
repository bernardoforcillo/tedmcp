package ted

import (
	"sort"
	"strconv"
	"strings"
)

// SummaryFields is the compact field set requested for search results.
// Every entry is a confirmed-valid TED field identifier.
var SummaryFields = []string{
	"publication-number",
	"notice-title",
	"notice-type",
	"buyer-name",
	"buyer-country",
	"classification-cpv",
	"publication-date",
	"deadline-receipt-tender-date-lot",
	"place-of-performance",
	"total-value",
	"links",
}

// DetailFields extends SummaryFields with a few more descriptive fields for
// single-notice lookups.
var DetailFields = append(append([]string{}, SummaryFields...),
	"contract-nature",
	"winner-name",
)

// preferredLangs is the priority order used when a field is a multilingual map.
// TED uses ISO 639-2/T three-letter codes; a few extras are tolerated.
var preferredLangs = []string{"ENG", "eng", "en", "MUL", "mul", "MULTI"}

// pickLangValue selects the best language variant from a multilingual map,
// falling back to the alphabetically-first key for deterministic output.
func pickLangValue(m map[string]any) any {
	for _, l := range preferredLangs {
		if v, ok := m[l]; ok {
			return v
		}
	}
	for _, k := range sortedKeys(m) {
		return m[k]
	}
	return nil
}

// asStrings flattens a heterogeneous field value into a slice of strings.
func asStrings(v any) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	case bool:
		return []string{strconv.FormatBool(t)}
	case float64:
		return []string{strconv.FormatFloat(t, 'f', -1, 64)}
	case []any:
		var out []string
		for _, e := range t {
			out = append(out, asStrings(e)...)
		}
		return out
	case map[string]any:
		return asStrings(pickLangValue(t))
	default:
		return nil
	}
}

// FieldStrings returns all string values for a notice field.
func FieldStrings(n Notice, key string) []string {
	if n == nil {
		return nil
	}
	return asStrings(n[key])
}

// FieldString returns a notice field flattened to a single string.
func FieldString(n Notice, key string) string {
	return strings.Join(FieldStrings(n, key), ", ")
}

// Link returns the document URL for a given format ("xml", "pdf", "html") and
// language code, falling back to preferred languages then any available one.
func Link(n Notice, format, lang string) string {
	links, ok := n["links"].(map[string]any)
	if !ok {
		return ""
	}
	byLang, ok := links[format].(map[string]any)
	if !ok {
		return ""
	}
	candidates := append([]string{lang}, preferredLangs...)
	for _, l := range candidates {
		if l == "" {
			continue
		}
		if u, ok := byLang[l].(string); ok && u != "" {
			return u
		}
	}
	for _, k := range sortedKeys(byLang) {
		if u, ok := byLang[k].(string); ok && u != "" {
			return u
		}
	}
	return ""
}

// NoticeSummary is a flattened, model-friendly view of a TED notice.
type NoticeSummary struct {
	PublicationNumber  string   `json:"publication_number"`
	Title              string   `json:"title,omitempty"`
	NoticeType         string   `json:"notice_type,omitempty"`
	BuyerName          string   `json:"buyer_name,omitempty"`
	BuyerCountry       string   `json:"buyer_country,omitempty"`
	CPV                []string `json:"cpv,omitempty"`
	PublicationDate    string   `json:"publication_date,omitempty"`
	Deadline           string   `json:"deadline,omitempty"`
	PlaceOfPerformance []string `json:"place_of_performance,omitempty"`
	TotalValue         string   `json:"total_value,omitempty"`
	WinnerName         string   `json:"winner_name,omitempty"`
	TEDURL             string   `json:"ted_url,omitempty"`
	PDFURL             string   `json:"pdf_url,omitempty"`
	XMLURL             string   `json:"xml_url,omitempty"`
}

// Summary builds a NoticeSummary from a raw notice.
func (n Notice) Summary() NoticeSummary {
	pn := FieldString(n, "publication-number")
	s := NoticeSummary{
		PublicationNumber:  pn,
		Title:              FieldString(n, "notice-title"),
		NoticeType:         FieldString(n, "notice-type"),
		BuyerName:          FieldString(n, "buyer-name"),
		BuyerCountry:       FieldString(n, "buyer-country"),
		CPV:                dedupe(FieldStrings(n, "classification-cpv")),
		PublicationDate:    trimDate(FieldString(n, "publication-date")),
		Deadline:           FieldString(n, "deadline-receipt-tender-date-lot"),
		PlaceOfPerformance: dedupe(FieldStrings(n, "place-of-performance")),
		TotalValue:         FieldString(n, "total-value"),
		WinnerName:         FieldString(n, "winner-name"),
		XMLURL:             Link(n, "xml", "MUL"),
		PDFURL:             Link(n, "pdf", "ENG"),
		TEDURL:             Link(n, "html", "ENG"),
	}
	if s.TEDURL == "" && pn != "" {
		s.TEDURL = "https://ted.europa.eu/en/notice/-/detail/" + pn
	}
	return s
}

// --- small helpers ---

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func dedupe(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// trimDate drops the timezone/time portion of a TED date like
// "2026-07-28+02:00", keeping the ISO calendar date.
func trimDate(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}
