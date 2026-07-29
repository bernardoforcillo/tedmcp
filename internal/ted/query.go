package ted

import (
	"fmt"
	"regexp"
	"strings"
)

// SearchFilters is a high-level, user-friendly description of a search that is
// compiled into a TED expert query by BuildQuery.
type SearchFilters struct {
	// Query is a raw TED expert query. When set it is combined (AND) with the
	// structured filters below, so callers can express anything TED supports.
	Query string

	Keywords   string   // full-text terms matched across the whole notice
	CPV        []string // Common Procurement Vocabulary codes
	Country    []string // buyer country ISO 3166-1 alpha-3 codes (e.g. DEU)
	NoticeType string   // e.g. cn-standard, can-standard

	PublishedFrom string // inclusive, YYYY-MM-DD or YYYYMMDD
	PublishedTo   string
	DeadlineFrom  string
	DeadlineTo    string

	SortBy    string // default: publication-date
	SortOrder string // ASC or DESC (default DESC)
}

// BuildQuery compiles filters into a TED expert query string.
func BuildQuery(f SearchFilters) (string, error) {
	var clauses []string

	if q := strings.TrimSpace(f.Query); q != "" {
		clauses = append(clauses, "("+q+")")
	}
	if len(f.CPV) > 0 {
		clause, err := cpvClause(f.CPV)
		if err != nil {
			return "", err
		}
		if clause != "" {
			clauses = append(clauses, clause)
		}
	}
	if len(f.Country) > 0 {
		clauses = append(clauses, inClause("buyer-country", upperAll(f.Country)))
	}
	if nt := strings.TrimSpace(f.NoticeType); nt != "" {
		clauses = append(clauses, "notice-type="+nt)
	}

	dateClauses := []struct {
		field, op, value string
	}{
		{"publication-date", ">=", f.PublishedFrom},
		{"publication-date", "<=", f.PublishedTo},
		{"deadline-receipt-tender-date-lot", ">=", f.DeadlineFrom},
		{"deadline-receipt-tender-date-lot", "<=", f.DeadlineTo},
	}
	for _, dc := range dateClauses {
		v, err := normalizeQueryDate(dc.value)
		if err != nil {
			return "", err
		}
		if v != "" {
			clauses = append(clauses, dc.field+dc.op+v)
		}
	}

	if kw := strings.TrimSpace(f.Keywords); kw != "" {
		clauses = append(clauses, fmt.Sprintf("FT ~ (%s)", quotePhrase(kw)))
	}

	if len(clauses) == 0 {
		return "", fmt.Errorf("empty search: provide `query` or at least one filter (keywords, cpv, country, notice_type, dates)")
	}

	query := strings.Join(clauses, " AND ")

	sortBy := strings.TrimSpace(f.SortBy)
	if sortBy == "" {
		sortBy = "publication-date"
	}
	order := strings.ToUpper(strings.TrimSpace(f.SortOrder))
	if order == "" {
		order = "DESC"
	}
	if order != "ASC" && order != "DESC" {
		return "", fmt.Errorf("invalid sort_order %q: use ASC or DESC", f.SortOrder)
	}
	query += " SORT BY " + sortBy + " " + order

	return query, nil
}

// cpvSplit separates CPV tokens supplied in a single string, so both
// ["55500000","55524000"] and ["55500000, 55524000"] are accepted.
var cpvSplit = regexp.MustCompile(`[,;\s]+`)

// cpvCheckDigit matches a full CPV code written with its trailing check digit,
// e.g. "55524000-5", so the check digit can be dropped.
var cpvCheckDigit = regexp.MustCompile(`^(\d{8})-\d$`)

// cpvClause builds the classification-cpv predicate, supporting exact 8-digit
// codes and trailing-wildcard prefixes (e.g. 555*, 555XXXXX, 5552X). Because
// TED's CPV search is hierarchical, an exact parent code already matches its
// children; wildcards add explicit prefix matching. Multiple entries are OR'd.
func cpvClause(entries []string) (string, error) {
	var exact, wildcards []string
	seen := map[string]bool{}

	for _, raw := range entries {
		for _, tok := range cpvSplit.Split(strings.TrimSpace(raw), -1) {
			if tok == "" {
				continue
			}
			val, isWild, err := normalizeCPV(tok)
			if err != nil {
				return "", err
			}
			if seen[val] {
				continue
			}
			seen[val] = true
			if isWild {
				wildcards = append(wildcards, val)
			} else {
				exact = append(exact, val)
			}
		}
	}

	var preds []string
	if len(exact) > 0 {
		preds = append(preds, inClause("classification-cpv", exact))
	}
	for _, w := range wildcards {
		preds = append(preds, "classification-cpv="+w)
	}

	switch len(preds) {
	case 0:
		return "", nil
	case 1:
		return preds[0], nil
	default:
		return "(" + strings.Join(preds, " OR ") + ")", nil
	}
}

// normalizeCPV parses one CPV token into a TED query value. It returns the
// value ("55500000" or "555*") and whether it is a wildcard prefix.
//
// Accepted forms:
//   - exact 8-digit code, optionally with a check digit: 55500000, 55524000-5
//   - trailing wildcard: 555*, 555XXXXX, 5552X (X/x/* are placeholders)
//   - a bare partial code (1-7 digits): 555 -> treated as the prefix 555*
func normalizeCPV(tok string) (value string, isWildcard bool, err error) {
	s := strings.ToUpper(strings.TrimSpace(tok))
	if m := cpvCheckDigit.FindStringSubmatch(s); m != nil {
		s = m[1]
	}

	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	prefix, rest := s[:i], s[i:]
	if prefix == "" {
		return "", false, fmt.Errorf("invalid CPV code %q: must start with digits", tok)
	}
	for _, r := range rest {
		if r != 'X' && r != '*' {
			return "", false, fmt.Errorf("invalid CPV code %q: wildcards must be trailing (e.g. 555* or 555XXXXX)", tok)
		}
	}

	if len(prefix) > 8 {
		return "", false, fmt.Errorf("invalid CPV code %q: more than 8 digits", tok)
	}
	// A full 8-digit code is an exact match (any trailing placeholder is redundant).
	if len(prefix) == 8 {
		return prefix, false, nil
	}
	if len(prefix) < 2 {
		return "", false, fmt.Errorf("CPV prefix %q is too short: use at least 2 digits (e.g. 55*)", tok)
	}
	return prefix + "*", true, nil
}

// inClause renders "field=v" for a single value or "field IN (v1 v2 ...)" for many.
func inClause(field string, values []string) string {
	values = nonEmpty(values)
	if len(values) == 1 {
		return field + "=" + values[0]
	}
	return field + " IN (" + strings.Join(values, " ") + ")"
}

// quotePhrase wraps text in double quotes for a full-text (FT) clause,
// dropping any embedded double quotes that would break the syntax.
func quotePhrase(s string) string {
	return `"` + strings.ReplaceAll(strings.TrimSpace(s), `"`, "") + `"`
}

// normalizeQueryDate accepts YYYY-MM-DD or YYYYMMDD (or empty) and returns the
// 8-digit YYYYMMDD form TED expects in expert queries.
func normalizeQueryDate(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	var digits strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
		case r == '-' || r == '/' || r == '.':
			// separators are allowed and ignored
		default:
			return "", fmt.Errorf("invalid date %q: expected YYYY-MM-DD or YYYYMMDD", s)
		}
	}
	d := digits.String()
	if len(d) != 8 {
		return "", fmt.Errorf("invalid date %q: expected YYYY-MM-DD or YYYYMMDD", s)
	}
	return d, nil
}

func upperAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		out = append(out, strings.ToUpper(strings.TrimSpace(v)))
	}
	return out
}

func nonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
