package main

import (
	"fmt"
	"strings"
)

// maxRowsShown bounds the award and forecast tables in the text rendering; the
// structured output and the CSV export always carry every row.
const maxRowsShown = 40

func formatAwardHistory(out AwardHistoryOutput) string {
	var b strings.Builder
	m := out.Market
	fmt.Fprintf(&b, "Read %d award notice(s) of %d matching (page %d): %d lot(s), %d awarded, %d closed without a winner.\n",
		out.Read, out.Total, out.Page, m.Lots, m.Awarded, m.NoWinner)
	fmt.Fprintf(&b, "Query: %s\n", out.Query)

	var market []string
	if m.MedianTenders != nil {
		market = append(market, fmt.Sprintf("median bids per lot %.1f", *m.MedianTenders))
	}
	if m.SingleBidShare != nil {
		market = append(market, fmt.Sprintf("single-bid lots %.1f%%", *m.SingleBidShare))
	}
	if m.MedianDiscount != nil {
		market = append(market, fmt.Sprintf("median discount on estimate %.1f%%", *m.MedianDiscount))
	}
	if len(market) > 0 {
		fmt.Fprintf(&b, "Market: %s\n", strings.Join(market, ", "))
	}

	if len(out.Competitors) > 0 {
		b.WriteString("\nCompetitors (by wins):\n")
		for i, c := range out.Competitors {
			fmt.Fprintf(&b, "%2d. %s", i+1, c.Name)
			if c.CompanyID != "" {
				fmt.Fprintf(&b, " [%s]", c.CompanyID)
			}
			fmt.Fprintf(&b, " — %d win(s)", c.Wins)
			if c.TotalAwarded != "" {
				fmt.Fprintf(&b, ", %s awarded", c.TotalAwarded)
			}
			if c.MedianTenders != nil {
				fmt.Fprintf(&b, ", median %.1f bids", *c.MedianTenders)
			}
			if c.SingleBidWins > 0 {
				fmt.Fprintf(&b, ", %d sole-bidder win(s)", c.SingleBidWins)
			}
			if c.MedianDiscount != nil {
				fmt.Fprintf(&b, ", median discount %.1f%%", *c.MedianDiscount)
			}
			if len(c.Buyers) > 0 {
				fmt.Fprintf(&b, "\n    buyers: %s", strings.Join(c.Buyers, "; "))
			}
			b.WriteString("\n")
		}
	}

	if len(out.Buyers) > 0 {
		b.WriteString("\nBuyers:\n")
		for _, by := range out.Buyers {
			fmt.Fprintf(&b, "  - %s — %d award(s), %d distinct supplier(s)", by.Name, by.Awards, by.DistinctWinners)
			if by.MedianTenders != nil {
				fmt.Fprintf(&b, ", median %.1f bids", *by.MedianTenders)
			}
			if len(by.Suppliers) > 0 {
				fmt.Fprintf(&b, "\n      suppliers: %s", strings.Join(by.Suppliers, "; "))
			}
			b.WriteString("\n")
		}
	}

	if len(out.Awards) > 0 {
		b.WriteString("\nAwards:\n")
		for i, r := range out.Awards {
			if i == maxRowsShown {
				fmt.Fprintf(&b, "  … %d more in the structured output (or format=csv)\n", len(out.Awards)-maxRowsShown)
				break
			}
			fmt.Fprintf(&b, "  %s %s  %s", r.PublicationNumber, r.Published, truncate(r.Title, 90))
			if r.Lot != "" {
				fmt.Fprintf(&b, " [%s]", r.Lot)
			}
			fmt.Fprintf(&b, "\n      %s", firstNonEmpty(r.Buyer, "(buyer not named)"))
			switch {
			case len(r.Winners) > 0:
				fmt.Fprintf(&b, " → %s", strings.Join(r.Winners, " + "))
			case r.Status != "":
				fmt.Fprintf(&b, " → %s", r.Status)
			}
			if r.Tenders >= 0 {
				fmt.Fprintf(&b, ", %d bid(s)", r.Tenders)
			}
			if r.Awarded != "" {
				fmt.Fprintf(&b, ", %s %s", r.Awarded, r.Currency)
			}
			if r.DiscountPct != nil {
				fmt.Fprintf(&b, " (−%.1f%% on estimate)", *r.DiscountPct)
			}
			b.WriteString("\n")
		}
	}

	writeFailures(&b, out.Failures)
	if out.Note != "" {
		fmt.Fprintf(&b, "\n%s", out.Note)
	}
	return b.String()
}

func formatForecast(out ForecastOutput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Contracts ending %s → %s: %d found among %d award notice(s) read (of %d matching, page %d).\n",
		out.Window[0], out.Window[1], len(out.Contracts), out.Read, out.Total, out.Page)
	fmt.Fprintf(&b, "Undated: %d lot(s); ending outside the window: %d.\n", out.Unknown, out.OutOfWindow)

	for i, r := range out.Contracts {
		if i == maxRowsShown {
			fmt.Fprintf(&b, "\n… %d more in the structured output (or format=csv)\n", len(out.Contracts)-maxRowsShown)
			break
		}
		end := r.End
		if r.InWindow == "renewal" {
			end = r.EndWithRenewals + " (after renewals; base term ends " + r.End + ")"
		} else if r.EndWithRenewals != "" {
			end += " (" + r.EndWithRenewals + " with renewals)"
		}
		fmt.Fprintf(&b, "\n%d. ends %s [%s]\n   %s — %s", i+1, end, r.EndSource, firstNonEmpty(r.Buyer, "(buyer)"), truncate(r.Title, 100))
		if len(r.Incumbent) > 0 {
			fmt.Fprintf(&b, "\n   incumbent: %s", strings.Join(r.Incumbent, " + "))
		}
		if r.Awarded != "" {
			fmt.Fprintf(&b, "  value: %s %s", r.Awarded, r.Currency)
		}
		if r.WatchFrom != "" {
			fmt.Fprintf(&b, "\n   watch for the new notice from: %s", r.WatchFrom)
		}
		if r.Renewal != "" {
			fmt.Fprintf(&b, "\n   renewal: %s", truncate(r.Renewal, 160))
		}
		fmt.Fprintf(&b, "\n   award notice: %s", r.PublicationNumber)
	}

	if len(out.UnknownRows) > 0 {
		b.WriteString("\n\nAwarded, duration not stated in the notice:\n")
		for _, r := range out.UnknownRows {
			fmt.Fprintf(&b, "  - %s %s — %s", r.PublicationNumber, firstNonEmpty(r.Buyer, ""), truncate(r.Title, 90))
			if len(r.Incumbent) > 0 {
				fmt.Fprintf(&b, " (incumbent %s)", strings.Join(r.Incumbent, " + "))
			}
			b.WriteString("\n")
		}
	}

	if len(out.PINs) > 0 {
		b.WriteString("\n\nPrior information notices (last 12 months):\n")
		for _, n := range out.PINs {
			fmt.Fprintf(&b, "  - %s %s %s — %s\n", n.PublicationNumber, n.PublicationDate, n.BuyerName, truncate(n.Title, 100))
		}
	}

	writeFailures(&b, out.Failures)
	if out.Note != "" {
		fmt.Fprintf(&b, "\n\n%s", out.Note)
	}
	return b.String()
}

func writeFailures(b *strings.Builder, failures []ScanError) {
	if len(failures) == 0 {
		return
	}
	fmt.Fprintf(b, "\nNOT READ (%d notice(s) could not be downloaded or parsed):\n", len(failures))
	for _, f := range failures {
		fmt.Fprintf(b, "  - %s: %s\n", f.PublicationNumber, f.Error)
	}
}
