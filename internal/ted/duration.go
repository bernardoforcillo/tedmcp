package ted

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Knowing how long a contract runs is what turns an award notice from history
// into a lead: a school-catering contract awarded in 2024 for three years will
// be tendered again in 2027. eForms has a field for it (the planned period),
// and a good half of notices leave it empty and write the period into the
// title instead — "PERIODO dal 01/01/2027 al 31/12/2028", "GENNAIO 2026-AGOSTO
// 2029". Reading both, and saying which one an answer came from, is the
// difference between a forecast and a guess presented as one.

// Where a duration was read from.
const (
	DurationFromNotice = "notice"  // the eForms planned period
	DurationFromText   = "text"    // dates or a stated span in the title or description
	DurationEstimated  = "derived" // computed from a start date plus a span
)

// Duration is how long a contract runs.
type Duration struct {
	Start         string  `json:"start,omitempty"`
	End           string  `json:"end,omitempty"`
	Months        float64 `json:"months,omitempty"`
	RenewalMonths int     `json:"renewal_months,omitempty" jsonschema:"length of the renewal option, when the buyer states one in months or years"`
	Source        string  `json:"source,omitempty" jsonschema:"notice (eForms planned period) or text (read from the title/description)"`
	Text          string  `json:"text,omitempty"`
}

// Empty reports whether nothing at all is known about the duration.
func (d Duration) Empty() bool {
	return d.Start == "" && d.End == "" && d.Months == 0
}

// EndDate works out when the contract ends, given a fallback start date for
// when the notice gives a span but no start. It returns "" when there is not
// enough to go on.
func (d Duration) EndDate(fallbackStart string) (end, how string) {
	if d.End != "" {
		return d.End, d.Source
	}
	start := firstNonEmptyString(d.Start, fallbackStart)
	if d.Months <= 0 || start == "" {
		return "", ""
	}
	t, err := time.Parse("2006-01-02", start)
	if err != nil {
		return "", ""
	}
	return addMonths(t, d.Months).Format("2006-01-02"), DurationEstimated
}

// AddMonths shifts a YYYY-MM-DD date by a (possibly fractional) number of
// months, returning "" for a date it cannot read.
func AddMonths(date string, months float64) string {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return ""
	}
	return addMonths(t, months).Format("2006-01-02")
}

func addMonths(t time.Time, months float64) time.Time {
	whole := int(math.Floor(months))
	days := int(math.Round((months - float64(whole)) * 30.4375))
	return t.AddDate(0, whole, days)
}

var (
	dmyDate   = regexp.MustCompile(`\b(\d{1,2})[/.\-](\d{1,2})[/.\-](\d{4})\b`)
	isoDate   = regexp.MustCompile(`\b(20\d{2})-(\d{2})-(\d{2})\b`)
	monthYear = regexp.MustCompile(`(?i)\b(` + monthAlternation() + `)\.?\s+(\d{4})\b`)

	// A stated span is only trusted next to a word meaning "duration": "24
	// mesi" on its own is as likely to be a warranty or a payment term.
	statedSpan = regexp.MustCompile(`(?i)\b(?:durata|duration|durée|dauer|laufzeit|duración|duração|looptijd)\b[^.;\n]{0,60}?\b(\d{1,3}|` +
		numberWordAlternation() + `)\s*(` + unitAlternation() + `)\b`)
	anySpan = regexp.MustCompile(`(?i)\b(\d{1,3}|` + numberWordAlternation() + `)\s*(?:\(\w+\)\s*)?(` + unitAlternation() + `)\b`)
)

// InferDuration reads a contract period out of free text: two or more dates
// give a start and an end; failing that, a span stated next to a word meaning
// "duration" gives a length. The texts are tried together, because the start
// is often in the title and the end in the description.
func InferDuration(texts ...string) Duration {
	joined := strings.Join(texts, "\n")
	if strings.TrimSpace(joined) == "" {
		return Duration{}
	}

	dates := datesIn(joined)
	if len(dates) >= 2 {
		return Duration{Start: dates[0], End: dates[len(dates)-1], Source: DurationFromText}
	}

	if m := statedSpan.FindStringSubmatch(joined); m != nil {
		if months := spanMonths(m[1], m[2]); months > 0 {
			return Duration{Months: months, Source: DurationFromText, Text: strings.TrimSpace(m[0])}
		}
	}
	return Duration{}
}

// MonthsIn returns the first span stated in a text, in months — used for
// renewal options, which are phrased as "Eventuale rinnovo di 12 mesi" or
// "ulteriori 4 anni scolastici". It returns 0 when no span is stated.
func MonthsIn(text string) int {
	if m := anySpan.FindStringSubmatch(text); m != nil {
		return int(math.Round(spanMonths(m[1], m[2])))
	}
	return 0
}

// datesIn returns the distinct dates in a text, sorted. A month named with its
// year ("agosto 2029") counts as the last day of that month when it could be
// an end, and as the first when it is the earliest of the set.
func datesIn(text string) []string {
	set := map[string]bool{}
	for _, m := range dmyDate.FindAllStringSubmatch(text, -1) {
		if d := makeDate(m[3], m[2], m[1]); d != "" {
			set[d] = true
		}
	}
	for _, m := range isoDate.FindAllStringSubmatch(text, -1) {
		if d := makeDate(m[1], m[2], m[3]); d != "" {
			set[d] = true
		}
	}

	type monthRef struct{ first, last string }
	var months []monthRef
	for _, m := range monthYear.FindAllStringSubmatch(text, -1) {
		month := monthNumber(m[1])
		year, _ := strconv.Atoi(m[2])
		if month == 0 || year < 2000 || year > 2100 {
			continue
		}
		first := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
		months = append(months, monthRef{
			first: first.Format("2006-01-02"),
			last:  first.AddDate(0, 1, -1).Format("2006-01-02"),
		})
	}

	out := make([]string, 0, len(set)+len(months))
	for d := range set {
		out = append(out, d)
	}
	if len(months) > 0 {
		sort.Slice(months, func(i, j int) bool { return months[i].first < months[j].first })
		out = append(out, months[0].first)
		if len(months) > 1 {
			out = append(out, months[len(months)-1].last)
		}
	}
	sort.Strings(out)
	return dedupe(out)
}

func makeDate(y, m, d string) string {
	year, _ := strconv.Atoi(y)
	month, _ := strconv.Atoi(m)
	day, _ := strconv.Atoi(d)
	if year < 2000 || year > 2100 || month < 1 || month > 12 || day < 1 || day > 31 {
		return ""
	}
	t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if t.Day() != day { // 31/02 does not exist
		return ""
	}
	return t.Format("2006-01-02")
}

// --- vocabularies ---
//
// These are calendar words, not search terms: month names and time units in
// the languages most TED notices are written in.

var monthNames = map[string]int{
	// Italian
	"gennaio": 1, "febbraio": 2, "marzo": 3, "aprile": 4, "maggio": 5, "giugno": 6,
	"luglio": 7, "agosto": 8, "settembre": 9, "ottobre": 10, "novembre": 11, "dicembre": 12,
	// English
	"january": 1, "february": 2, "march": 3, "april": 4, "may": 5, "june": 6,
	"july": 7, "august": 8, "september": 9, "october": 10, "november": 11, "december": 12,
	// French
	"janvier": 1, "février": 2, "fevrier": 2, "mars": 3, "avril": 4, "mai": 5, "juin": 6,
	"juillet": 7, "août": 8, "aout": 8, "septembre": 9, "octobre": 10, "décembre": 12,
	// German
	"januar": 1, "februar": 2, "märz": 3, "juni": 6, "juli": 7, "oktober": 10, "dezember": 12,
	// Spanish
	"enero": 1, "febrero": 2, "abril": 4, "mayo": 5, "junio": 6, "julio": 7,
	"septiembre": 9, "octubre": 10, "noviembre": 11, "diciembre": 12,
}

func monthNumber(s string) int { return monthNames[strings.ToLower(s)] }

func monthAlternation() string {
	names := make([]string, 0, len(monthNames))
	for n := range monthNames {
		names = append(names, regexp.QuoteMeta(n))
	}
	// Longest first, so "settembre" is not cut short by a shorter prefix.
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
	return strings.Join(names, "|")
}

// unitMonths maps a time unit to its length in months.
var unitMonths = map[string]float64{
	"mesi": 1, "mese": 1, "months": 1, "month": 1, "mois": 1, "monate": 1, "monaten": 1, "monat": 1, "meses": 1, "mes": 1,
	"anni": 12, "anno": 12, "years": 12, "year": 12, "ans": 12, "an": 12, "années": 12, "jahre": 12, "jahren": 12, "jahr": 12, "años": 12, "año": 12, "anos": 12,
	"giorni": 1 / 30.4375, "days": 1 / 30.4375, "jours": 1 / 30.4375, "tage": 1 / 30.4375, "días": 1 / 30.4375,
}

func unitAlternation() string {
	units := make([]string, 0, len(unitMonths))
	for u := range unitMonths {
		units = append(units, regexp.QuoteMeta(u))
	}
	sort.Slice(units, func(i, j int) bool { return len(units[i]) > len(units[j]) })
	return strings.Join(units, "|")
}

var numberWords = map[string]float64{
	"un": 1, "uno": 1, "una": 1, "one": 1, "due": 2, "two": 2, "tre": 3, "three": 3,
	"quattro": 4, "four": 4, "cinque": 5, "five": 5, "sei": 6, "six": 6,
	"dodici": 12, "twelve": 12, "ventiquattro": 24, "trentasei": 36,
	"deux": 2, "trois": 3, "quatre": 4, "zwei": 2, "drei": 3, "vier": 4, "dos": 2, "tres": 3, "cuatro": 4,
}

func numberWordAlternation() string {
	words := make([]string, 0, len(numberWords))
	for w := range numberWords {
		words = append(words, w)
	}
	sort.Slice(words, func(i, j int) bool { return len(words[i]) > len(words[j]) })
	return strings.Join(words, "|")
}

func spanMonths(number, unit string) float64 {
	n, err := strconv.ParseFloat(number, 64)
	if err != nil {
		n = numberWords[strings.ToLower(number)]
	}
	return n * unitMonths[strings.ToLower(unit)]
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
