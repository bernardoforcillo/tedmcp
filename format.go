package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bernardoforcillo/tedmcp/internal/ted"
)

// formatSearch renders search results as a compact, human-readable summary.
// The full structured data is also returned as the tool's structured content.
func formatSearch(out SearchOutput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d matching notice(s) in TED — showing page %d (%d shown, limit %d).\n",
		out.Total, out.Page, out.Count, out.Limit)
	fmt.Fprintf(&b, "Query: %s\n", out.Query)

	if out.Count == 0 {
		b.WriteString("\nNo notices on this page.")
		return b.String()
	}

	for i, n := range out.Notices {
		fmt.Fprintf(&b, "\n%d. %s", i+1, firstNonEmpty(n.Title, "(untitled)"))
		fmt.Fprintf(&b, "\n   publication: %s", n.PublicationNumber)
		if n.NoticeType != "" {
			fmt.Fprintf(&b, "  type: %s", n.NoticeType)
		}
		if buyer := joinNonEmpty(" / ", n.BuyerName, n.BuyerCountry); buyer != "" {
			fmt.Fprintf(&b, "\n   buyer: %s", buyer)
		}
		if len(n.CPV) > 0 {
			fmt.Fprintf(&b, "\n   cpv: %s", strings.Join(n.CPV, ", "))
		}
		if n.PublicationDate != "" {
			fmt.Fprintf(&b, "\n   published: %s", n.PublicationDate)
		}
		if n.Deadline != "" {
			fmt.Fprintf(&b, "  deadline: %s", n.Deadline)
		}
		if n.TotalValue != "" {
			fmt.Fprintf(&b, "\n   value: %s", n.TotalValue)
		}
		if n.TEDURL != "" {
			fmt.Fprintf(&b, "\n   link: %s", n.TEDURL)
		}
	}
	return b.String()
}

func formatSummaryDetail(n ted.NoticeSummary) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", firstNonEmpty(n.Title, "(untitled)"))
	fmt.Fprintf(&b, "publication number: %s\n", n.PublicationNumber)
	writeLine(&b, "notice type", n.NoticeType)
	writeLine(&b, "buyer", joinNonEmpty(" / ", n.BuyerName, n.BuyerCountry))
	if len(n.CPV) > 0 {
		writeLine(&b, "cpv", strings.Join(n.CPV, ", "))
	}
	writeLine(&b, "published", n.PublicationDate)
	writeLine(&b, "deadline", n.Deadline)
	if len(n.PlaceOfPerformance) > 0 {
		writeLine(&b, "place of performance", strings.Join(n.PlaceOfPerformance, ", "))
	}
	writeLine(&b, "total value", n.TotalValue)
	writeLine(&b, "winner", n.WinnerName)
	writeLine(&b, "web page", n.TEDURL)
	writeLine(&b, "pdf", n.PDFURL)
	writeLine(&b, "xml", n.XMLURL)
	return strings.TrimRight(b.String(), "\n")
}

func formatDoc(out GetDocOutput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Document for notice %s [%s / %s]\n", out.PublicationNumber, out.Format, out.Language)
	fmt.Fprintf(&b, "url: %s\n", out.URL)
	if out.ContentType != "" {
		fmt.Fprintf(&b, "content-type: %s\n", out.ContentType)
	}
	if out.Bytes > 0 {
		fmt.Fprintf(&b, "bytes: %d%s\n", out.Bytes, ternary(out.Truncated, " (text truncated)", ""))
	}
	if out.Note != "" {
		fmt.Fprintf(&b, "note: %s\n", out.Note)
	}
	if out.Text != "" {
		b.WriteString("\n--- content ---\n")
		b.WriteString(out.Text)
	}
	return b.String()
}

// formatScan renders the result of scanning notice documents, leading with
// where each term was found and what it is worth in the scoring grid.
func formatScan(out ScanOutput) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Scanned %d notice document(s)", out.Scanned)
	if out.Total > out.Scanned {
		fmt.Fprintf(&b, " out of %d matching the query", out.Total)
		if out.Page > 0 && out.PageSize > 0 {
			pages := (out.Total + out.PageSize - 1) / out.PageSize
			fmt.Fprintf(&b, " — page %d of %d, so the rest is NOT covered by this call", out.Page, pages)
		}
	}
	if out.Pattern != "" {
		fmt.Fprintf(&b, "; %d contain a match.\n", out.Matched)
		fmt.Fprintf(&b, "Pattern: %s\n", out.Pattern)
	} else {
		b.WriteString(" (no search pattern given — content extracted only).\n")
	}
	if out.Query != "" {
		fmt.Fprintf(&b, "Query: %s\n", out.Query)
	}
	if len(out.Uncovered) > 0 {
		langs := make([]string, 0, len(out.Uncovered))
		for lang, n := range out.Uncovered {
			langs = append(langs, fmt.Sprintf("%s (%d)", lang, n))
		}
		sort.Strings(langs)
		fmt.Fprintf(&b, "NOT SEARCHED: %s — these notices are written in languages your terms do not cover; add terms for them before concluding anything about those countries.\n",
			strings.Join(langs, ", "))
	}
	if n := out.Cache.Hits + out.Cache.Misses; n > 0 {
		fmt.Fprintf(&b, "Cache: %d of %d document(s) read locally", out.Cache.Hits, n)
		if out.Cache.Hits < n {
			b.WriteString("; re-running this scan with different terms will be much faster")
		}
		b.WriteString(".\n")
	}

	if len(out.Notices) == 0 {
		b.WriteString("\nNo notice to report. The requirement may still be in the tender documents on the buyer's platform: TED notices often carry only a pointer to the disciplinare di gara.")
		return writeScanFailures(&b, out)
	}

	for i, n := range out.Notices {
		fmt.Fprintf(&b, "\n%d. %s", i+1, firstNonEmpty(n.Notice.Title, "(untitled)"))
		fmt.Fprintf(&b, "\n   publication: %s", n.Notice.PublicationNumber)
		if n.Notice.Deadline != "" {
			fmt.Fprintf(&b, "  deadline: %s", n.Notice.Deadline)
		}
		if n.Notice.TotalValue != "" {
			fmt.Fprintf(&b, "  value: %s", n.Notice.TotalValue)
		}
		if buyer := joinNonEmpty(" / ", n.Notice.BuyerName, n.Notice.BuyerCountry); buyer != "" {
			fmt.Fprintf(&b, "\n   buyer: %s", buyer)
		}
		if n.Notice.TEDURL != "" {
			fmt.Fprintf(&b, "\n   link: %s", n.Notice.TEDURL)
		}
		if len(n.Strategic) > 0 {
			fmt.Fprintf(&b, "\n   strategic procurement: %s", strings.Join(n.Strategic, ", "))
		}

		for _, m := range n.Matches {
			where := m.Where
			if m.Weight != "" {
				where += ", weight " + m.Weight
			}
			fmt.Fprintf(&b, "\n   match [%s]: %s", where, m.Text)
		}
		if len(n.Matches) == 0 {
			b.WriteString("\n   no match in this notice")
		}

		if !n.CriteriaPublished {
			b.WriteString("\n   award criteria: not published in the notice (see the tender documents)")
		} else if len(n.AwardCriteria) > 0 {
			fmt.Fprintf(&b, "\n   award criteria (%d):", len(n.AwardCriteria))
			for _, c := range n.AwardCriteria {
				weight := c.Weight
				if weight == "" {
					weight = "-"
				}
				fmt.Fprintf(&b, "\n     [%s] %s", weight, truncate(c.Text(), 160))
			}
		}
		for _, u := range n.DocumentURLs {
			fmt.Fprintf(&b, "\n   documents: %s", u)
		}
	}

	return writeScanFailures(&b, out)
}

func writeScanFailures(b *strings.Builder, out ScanOutput) string {
	if len(out.Failures) == 0 {
		return b.String()
	}
	fmt.Fprintf(b, "\n\n%d notice document(s) could not be read, so they were NOT searched and may hide matches", len(out.Failures))
	b.WriteString("\n(re-run scan_tenders with these publication_numbers, or lower concurrency, to cover them):")
	for _, f := range out.Failures {
		fmt.Fprintf(b, "\n   %s: %s", f.PublicationNumber, f.Error)
	}
	return b.String()
}

// --- tiny string helpers ---

func writeLine(b *strings.Builder, label, value string) {
	if value != "" {
		fmt.Fprintf(b, "%s: %s\n", label, value)
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func joinNonEmpty(sep string, values ...string) string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return strings.Join(out, sep)
}

func ternary(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
