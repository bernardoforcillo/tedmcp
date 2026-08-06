package main

import (
	"fmt"
	"strings"

	"github.com/bernardoforcillo/tedmcp/internal/doctext"
)

// writeDownload renders one retrieved URL: what it was, whether it answered,
// and what could be read out of it.
//
// The unreadable outcomes get as much room as the readable ones. A capitolato
// that is a scan, a portal that answers with a captcha and an archive whose
// members were capped are three different situations, and each one tells a
// reader to do something different next.
func writeDownload(b *strings.Builder, d download) {
	r := d.Result

	// The skipped-documents notice carries no URL of its own.
	if r.URL == "" && r.Reason != "" {
		fmt.Fprintf(b, "\nnot downloaded: %s\n", r.Reason)
		return
	}

	fmt.Fprintf(b, "\n[%s] %s\n", firstNonEmpty(d.Role, "requested"), r.URL)
	if d.Label != "" {
		fmt.Fprintf(b, "  named: %s\n", d.Label)
	}
	fmt.Fprintf(b, "  status: %s", r.Status)
	if d.Index {
		b.WriteString("  (procedure page listing the documents)")
	}
	if r.Filename != "" {
		fmt.Fprintf(b, "  file: %s", r.Filename)
	}
	if r.Bytes > 0 {
		fmt.Fprintf(b, "  bytes: %d", r.Bytes)
	}
	b.WriteString("\n")
	if r.Reason != "" {
		fmt.Fprintf(b, "  reason: %s\n", r.Reason)
	}

	for _, f := range d.Files {
		writeFile(b, f)
	}
}

// writeFile renders one file's text, or the reason there is none.
func writeFile(b *strings.Builder, f doctext.Document) {
	name := firstNonEmpty(f.Name, "(unnamed)")
	if f.Container != "" {
		name = f.Container + " → " + f.Name
	}

	fmt.Fprintf(b, "  %s — %s", name, firstNonEmpty(f.Kind, "unknown"))
	if f.Pages > 0 {
		fmt.Fprintf(b, ", %d page(s)", f.Pages)
	}
	if f.Chars > 0 {
		fmt.Fprintf(b, ", %d character(s)", f.Chars)
	}
	if f.Status != doctext.StatusExtracted {
		fmt.Fprintf(b, "  [%s]", f.Status)
	}
	b.WriteString("\n")

	if f.Reason != "" {
		fmt.Fprintf(b, "      %s\n", f.Reason)
	}
	switch {
	case f.Text != "":
		fmt.Fprintf(b, "  --- %s%s ---\n%s\n", name, ternary(f.Truncated, " (truncated)", ""), f.Text)
	case f.Truncated && f.Chars > 0:
		// Text withheld by the overall budget rather than absent.
		fmt.Fprintf(b, "      text not shown here — fetch this file on its own to read it\n")
	}
}

// formatScanDocuments renders a search across a tender's own documents.
func formatScanDocuments(out ScanDocsOutput) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Searched the documents of %d tender(s); %d contain a match.\n", out.Scanned, out.Matched)
	if out.Pattern != "" {
		fmt.Fprintf(&b, "searching for: %s\n", out.Pattern)
	}

	for _, r := range out.Results {
		b.WriteString("\n")
		if r.PublicationNumber != "" {
			fmt.Fprintf(&b, "%s", r.PublicationNumber)
			if r.Title != "" {
				fmt.Fprintf(&b, " — %s", truncate(r.Title, 90))
			}
			b.WriteString("\n")
		}
		if r.Language != "" {
			fmt.Fprintf(&b, "  language: %s\n", r.Language)
		}
		fmt.Fprintf(&b, "  %d file(s) retrieved, %d readable", r.Retrieved, r.Readable)
		if r.Matched {
			fmt.Fprintf(&b, ", %d match(es)", len(r.Matches))
		}
		b.WriteString("\n")

		for _, m := range r.Matches {
			where := m.Document
			if m.Container != "" {
				where = m.Container + " → " + m.Document
			}
			fmt.Fprintf(&b, "\n  match in %s [%s]\n    %s\n", where, m.Term, m.Text)
		}

		if len(r.Unreadable) > 0 {
			b.WriteString("\n  NOT SEARCHED — these documents were retrieved but could not be read:\n")
			for _, f := range r.Unreadable {
				fmt.Fprintf(&b, "    %s [%s] %s\n", firstNonEmpty(f.Name, "(unnamed)"), f.Status, f.Reason)
			}
		}
		if len(r.Refused) > 0 {
			b.WriteString("\n  NOT RETRIEVED — the portal did not serve these to an automated client:\n")
			for _, u := range r.Refused {
				if u.URL == "" {
					fmt.Fprintf(&b, "    [%s] %s\n", u.Status, u.Reason)
					continue
				}
				fmt.Fprintf(&b, "    [%s] %s\n", u.Status, u.URL)
				if u.Reason != "" {
					fmt.Fprintf(&b, "      %s\n", u.Reason)
				}
			}
		}
		if r.Note != "" {
			fmt.Fprintf(&b, "\n  note: %s\n", r.Note)
		}
	}

	if out.Note != "" {
		fmt.Fprintf(&b, "\nnote: %s\n", out.Note)
	}
	return strings.TrimRight(b.String(), "\n")
}
