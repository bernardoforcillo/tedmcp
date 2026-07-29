package main

import (
	"fmt"
	"strings"

	"github.com/bernardoforcillo/tedmcp/internal/ted"
	"github.com/bernardoforcillo/tedmcp/internal/webdoc"
)

// formatDossier renders one procurement as a briefing: what it is, who to
// contact, when it closes to the minute, and where each link leads.
func formatDossier(out DossierOutput) string {
	var b strings.Builder
	d := out.Dossier

	fmt.Fprintf(&b, "%s\n", firstNonEmpty(out.Notice.Title, "(untitled)"))
	fmt.Fprintf(&b, "publication: %s", out.Notice.PublicationNumber)
	if d.InternalID != "" {
		fmt.Fprintf(&b, "  buyer reference: %s", d.InternalID)
	}
	if d.ProcedureType != "" {
		fmt.Fprintf(&b, "  procedure: %s", d.ProcedureType)
	}
	b.WriteString("\n")

	if d.DeadlineDate != "" {
		fmt.Fprintf(&b, "deadline: %s", d.DeadlineDate)
		if d.DeadlineTime != "" {
			fmt.Fprintf(&b, " at %s", d.DeadlineTime)
		}
		b.WriteString("\n")
	}
	if out.Notice.TotalValue != "" {
		fmt.Fprintf(&b, "value: %s\n", out.Notice.TotalValue)
	}

	if d.Buyer != nil {
		fmt.Fprintf(&b, "\nbuyer: %s\n", d.Buyer.Name)
		writeLine(&b, "  codice fiscale", d.Buyer.CompanyID)
		writeLine(&b, "  email", d.Buyer.Email)
		writeLine(&b, "  phone", d.Buyer.Phone)
		writeLine(&b, "  city", d.Buyer.City)
	}

	if len(d.Links) > 0 {
		b.WriteString("\nlinks:\n")
		for _, l := range d.Links {
			fmt.Fprintf(&b, "  [%s] %s\n", l.Role, l.URL)
			if l.Note != "" && l.Role != ted.LinkBuyerWebsite {
				fmt.Fprintf(&b, "      %s\n", l.Note)
			}
		}
	}

	if len(d.Lots) > 0 {
		fmt.Fprintf(&b, "\nlots (%d):\n", len(d.Lots))
		for _, lot := range d.Lots {
			fmt.Fprintf(&b, "  %s %s", lot.ID, truncate(lot.Title, 90))
			if lot.Value != "" {
				fmt.Fprintf(&b, "\n      value: %s %s", lot.Value, lot.Currency)
			}
			if len(lot.CPV) > 0 {
				fmt.Fprintf(&b, "  cpv: %s", strings.Join(lot.CPV, ", "))
			}
			if lot.DeadlineDate != "" {
				fmt.Fprintf(&b, "\n      deadline: %s %s", lot.DeadlineDate, lot.DeadlineTime)
			}
			b.WriteString("\n")
		}
	}

	if len(out.Criteria) > 0 {
		fmt.Fprintf(&b, "\naward criteria (%d):\n", len(out.Criteria))
		for _, c := range out.Criteria {
			weight := c.Weight
			if weight == "" {
				weight = "-"
			}
			fmt.Fprintf(&b, "  [%s] %s\n", weight, truncate(c.Text(), 170))
		}
	}
	if len(out.Green) > 0 {
		fmt.Fprintf(&b, "\nstrategic procurement: %s\n", strings.Join(out.Green, ", "))
	}
	if out.Note != "" {
		fmt.Fprintf(&b, "\nnote: %s\n", out.Note)
	}
	return strings.TrimRight(b.String(), "\n")
}

// formatFetchDocs reports what came back and, just as importantly, what did
// not and why.
func formatFetchDocs(out FetchDocsOutput) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Tried %d document link(s); retrieved %d.\n", out.Attempted, out.Retrieved)
	if out.PublicationNumber != "" {
		fmt.Fprintf(&b, "notice: %s\n", out.PublicationNumber)
	}

	for _, d := range out.Documents {
		r := d.Result
		fmt.Fprintf(&b, "\n[%s] %s\n", firstNonEmpty(d.Role, "requested"), r.URL)
		fmt.Fprintf(&b, "  status: %s", r.Status)
		if r.Filename != "" {
			fmt.Fprintf(&b, "  file: %s", r.Filename)
		}
		if r.Bytes > 0 {
			fmt.Fprintf(&b, "  bytes: %d", r.Bytes)
		}
		b.WriteString("\n")
		if r.Reason != "" {
			fmt.Fprintf(&b, "  reason: %s\n", r.Reason)
		}
		if r.Text != "" {
			fmt.Fprintf(&b, "  --- content%s ---\n%s\n", ternary(r.Truncated, " (truncated)", ""), r.Text)
		}
	}

	// Links that were deliberately not tried still matter to a reader deciding
	// where to go next.
	var skipped []ted.DocumentLink
	for _, l := range out.Links {
		if !hasRole(documentRoles, l.Role) {
			skipped = append(skipped, l)
		}
	}
	if len(skipped) > 0 {
		b.WriteString("\nnot tried (these roles do not hold tender documents):\n")
		for _, l := range skipped {
			fmt.Fprintf(&b, "  [%s] %s\n", l.Role, l.URL)
		}
	}

	if out.Note != "" {
		fmt.Fprintf(&b, "\nnote: %s\n", out.Note)
	}
	return strings.TrimRight(b.String(), "\n")
}

// formatLookupANAC renders the national-database records joined to a notice.
func formatLookupANAC(out LookupANACOutput) string {
	var b strings.Builder
	r := out.Result

	fmt.Fprintf(&b, "ANAC snapshot: %d record(s) scanned, %d name this buyer, %d matched.\n",
		r.Scanned, r.Candidate, len(r.Records))
	fmt.Fprintf(&b, "buyer codice fiscale: %s\n", strings.Join(out.BuyerCF, ", "))
	if out.Value != "" {
		fmt.Fprintf(&b, "matching value: %s\n", out.Value)
	}
	if r.Malformed > 0 {
		fmt.Fprintf(&b, "warning: %d record(s) naming this buyer could not be decoded (%s) — results may be incomplete.\n",
			r.Malformed, r.Sample)
	}

	for i, rec := range r.Records {
		fmt.Fprintf(&b, "\n%d. CIG %s", i+1, rec.CIG)
		if rec.DataPubblicazione != "" {
			fmt.Fprintf(&b, "  published: %s", rec.DataPubblicazione)
		}
		if v := rec.ImportoLotto.Float(); v > 0 {
			fmt.Fprintf(&b, "\n   lot value: %.2f", v)
		}
		if v := rec.ImportoGara.Float(); v > 0 {
			fmt.Fprintf(&b, "  overall: %.2f", v)
		}
		if rec.CPV != "" {
			fmt.Fprintf(&b, "\n   cpv: %s %s", rec.CPV, truncate(rec.CPVDescription, 60))
		}
		if rec.OggettoLotto != "" {
			fmt.Fprintf(&b, "\n   object: %s", truncate(rec.OggettoLotto, 120))
		}
		if rec.Buyer != "" {
			fmt.Fprintf(&b, "\n   buyer: %s", rec.Buyer)
		}
		if rec.Stato != "" || rec.Esito != "" {
			fmt.Fprintf(&b, "\n   state: %s %s", rec.Stato, rec.Esito)
		}
		if rec.NumeroGara != "" {
			fmt.Fprintf(&b, "\n   gara: %s", rec.NumeroGara)
		}
		b.WriteString("\n")
	}

	if out.Note != "" {
		fmt.Fprintf(&b, "\nnote: %s\n", out.Note)
	}
	return strings.TrimRight(b.String(), "\n")
}

// ensure the webdoc status vocabulary stays referenced from the renderer side.
var _ = webdoc.StatusFetched
