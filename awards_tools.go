package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/bernardoforcillo/tedmcp/internal/ted"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Award notices are the most under-read part of TED. A contract notice says
// what a buyer wants; the award notice says who got it, against how many
// rivals, and for how much less than the estimate. Read in bulk, that is the
// competitive picture a bid team otherwise rebuilds from memory: who usually
// wins this kind of tender, at what discount, and whether this buyer always
// renews its incumbent.
//
// The same notices also say how long each contract runs, which is when it will
// be tendered again — the earliest a lead can be known.

func registerAwardTools(s *mcp.Server, tc *ted.Client) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "award_history",
		Description: "Competitive intelligence from TED award notices: who wins which tenders, against how many bidders, and at what " +
			"discount on the buyer's estimate. Select award notices with the same filters as search_tenders (CPV, country, buyer, " +
			"keywords, dates); each one's eForms results section is read and aggregated into a table of awards, a ranking of " +
			"competitors (wins, value, buyers, typical discount) and a profile of each buyer (how many bids it attracts, whether " +
			"it keeps the same supplier). Use `winner` to follow one competitor. Award notices are cached, so slicing the same " +
			"market several ways is cheap. Set format=csv to get the award table as a spreadsheet.",
	}, handleAwardHistory(tc))

	mcp.AddTool(s, &mcp.Tool{
		Name: "forecast_retenders",
		Description: "Find contracts that will be tendered again soon: reads past award notices, works out when each contract ends " +
			"(from the notice's planned period, or from the dates and spans the buyer wrote into the title), and lists the ones " +
			"ending in a window — by default the next 18 months — with the incumbent supplier, the value, and when renewal " +
			"options would push the end out to. This is the earliest a lead can be known: months before a contract notice " +
			"appears. Every end date says where it came from, and contracts whose duration the notice does not state are " +
			"counted rather than silently dropped. Optionally also lists recent prior information notices (pre-announcements).",
	}, handleForecast(tc))
}

// --- award_history ---

type AwardHistoryInput struct {
	MarketFilters
	Winner string `json:"winner,omitempty" jsonschema:"keep only awards won by an operator whose name or company ID contains this text, e.g. CIRFOOD"`
	Format string `json:"format,omitempty" jsonschema:"text (default) or csv to attach the award table"`
	Top    int    `json:"top,omitempty" jsonschema:"how many competitors and buyers to rank (default 15)"`
}

// AwardRow is one awarded (or failed) lot.
type AwardRow struct {
	PublicationNumber string   `json:"publication_number"`
	Published         string   `json:"published,omitempty"`
	Buyer             string   `json:"buyer,omitempty"`
	BuyerID           string   `json:"buyer_id,omitempty"`
	Country           string   `json:"country,omitempty"`
	Title             string   `json:"title,omitempty"`
	Lot               string   `json:"lot,omitempty"`
	CPV               []string `json:"cpv,omitempty"`
	Status            string   `json:"status,omitempty"`
	Winners           []string `json:"winners,omitempty"`
	WinnerIDs         []string `json:"winner_ids,omitempty"`
	Tenders           int      `json:"tenders_received" jsonschema:"-1 when the notice does not say"`
	Estimated         string   `json:"estimated_value,omitempty"`
	Awarded           string   `json:"awarded_value,omitempty"`
	Currency          string   `json:"currency,omitempty"`
	DiscountPct       *float64 `json:"discount_pct,omitempty" jsonschema:"percentage below the buyer's estimate; the estimate often includes options and renewals, so read it as an upper bound"`
	AwardDate         string   `json:"award_date,omitempty"`
	URL               string   `json:"url,omitempty"`
}

// CompetitorStat summarises one operator's wins.
type CompetitorStat struct {
	Name            string   `json:"name"`
	CompanyID       string   `json:"company_id,omitempty"`
	Wins            int      `json:"wins"`
	TotalAwarded    string   `json:"total_awarded,omitempty"`
	Buyers          []string `json:"buyers,omitempty"`
	MedianTenders   *float64 `json:"median_tenders,omitempty" jsonschema:"typical number of bids in the competitions it won"`
	MedianDiscount  *float64 `json:"median_discount_pct,omitempty"`
	SingleBidWins   int      `json:"single_bid_wins" jsonschema:"wins where it was the only bidder"`
	LatestPublished string   `json:"latest,omitempty"`
}

// BuyerStat summarises one buyer's awards.
type BuyerStat struct {
	Name            string   `json:"name"`
	CompanyID       string   `json:"company_id,omitempty"`
	Awards          int      `json:"awards"`
	DistinctWinners int      `json:"distinct_winners"`
	MedianTenders   *float64 `json:"median_tenders,omitempty"`
	Suppliers       []string `json:"suppliers,omitempty" jsonschema:"who it awarded to, most frequent first"`
}

// MarketStat summarises the whole selection.
type MarketStat struct {
	Lots           int      `json:"lots"`
	Awarded        int      `json:"awarded"`
	NoWinner       int      `json:"no_winner" jsonschema:"lots closed without a winner — deserted or cancelled competitions"`
	MedianTenders  *float64 `json:"median_tenders,omitempty"`
	SingleBidShare *float64 `json:"single_bid_share_pct,omitempty" jsonschema:"share of awarded lots with exactly one bid, among those that state the count"`
	MedianDiscount *float64 `json:"median_discount_pct,omitempty"`
}

type AwardHistoryOutput struct {
	Query       string           `json:"query"`
	Total       int              `json:"total" jsonschema:"award notices matching the selection in TED"`
	Page        int              `json:"page"`
	Read        int              `json:"read" jsonschema:"award notices downloaded and parsed in this call"`
	Market      MarketStat       `json:"market"`
	Competitors []CompetitorStat `json:"competitors,omitempty"`
	Buyers      []BuyerStat      `json:"buyers,omitempty"`
	Awards      []AwardRow       `json:"awards,omitempty"`
	Failures    []ScanError      `json:"failures,omitempty"`
	Note        string           `json:"note,omitempty"`
}

func handleAwardHistory(tc *ted.Client) func(context.Context, *mcp.CallToolRequest, AwardHistoryInput) (*mcp.CallToolResult, AwardHistoryOutput, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in AwardHistoryInput) (*mcp.CallToolResult, AwardHistoryOutput, error) {
		if in.empty() && strings.TrimSpace(in.Winner) == "" {
			return errorResult(fmt.Errorf("give at least one filter: cpv, country, buyer, keywords or query")), AwardHistoryOutput{}, nil
		}
		query, err := in.query(awardNoticeTypes)
		if err != nil {
			return errorResult(err), AwardHistoryOutput{}, nil
		}
		limit := clamp(in.Limit, defaultMarketLimit, 1, maxMarketLimit)
		notices, sel, err := searchNotices(ctx, tc, query, in.Page, limit)
		if err != nil {
			return errorResult(err), AwardHistoryOutput{}, nil
		}

		read := readProcedures(ctx, tc, notices, in.Concurrency)
		out := AwardHistoryOutput{Query: sel.Query, Total: sel.Total, Page: sel.Page}
		for _, r := range read {
			if r.Err != nil {
				out.Failures = append(out.Failures, ScanError{PublicationNumber: r.Summary.PublicationNumber, Error: r.Err.Error()})
				continue
			}
			out.Read++
			out.Awards = append(out.Awards, awardRows(r)...)
		}
		if w := strings.ToLower(strings.TrimSpace(in.Winner)); w != "" {
			out.Awards = filterByWinner(out.Awards, w)
		}

		top := in.Top
		if top <= 0 {
			top = 15
		}
		out.Market = marketStats(out.Awards)
		out.Competitors = competitorStats(out.Awards, top)
		out.Buyers = buyerStats(out.Awards, top)
		out.Note = awardNote(out, limit)

		if wantsCSV(in.Format) {
			return csvResult(formatAwardHistory(out), "awards", awardsCSV(out.Awards)), out, nil
		}
		return textResult(formatAwardHistory(out)), out, nil
	}
}

// awardRows turns one award notice into a row per lot that has an outcome.
func awardRows(r procNotice) []AwardRow {
	p := r.Procedure
	var rows []AwardRow
	for _, l := range p.Lots {
		if l.Outcome == nil {
			continue
		}
		row := AwardRow{
			PublicationNumber: r.Summary.PublicationNumber,
			Published:         r.Summary.PublicationDate,
			Buyer:             firstNonEmpty(r.Summary.BuyerName, buyerName(p)),
			Country:           r.Summary.BuyerCountry,
			Title:             firstNonEmpty(l.Title, p.Title, r.Summary.Title),
			Lot:               l.ID,
			CPV:               firstNonEmptySlice(l.CPV, r.Summary.CPV),
			Status:            l.Outcome.Status,
			Tenders:           l.Outcome.TendersCount,
			Estimated:         l.EstimatedValue,
			Awarded:           l.Outcome.AwardedValue,
			Currency:          firstNonEmpty(l.Outcome.Currency, l.Currency, p.Currency),
			AwardDate:         l.Outcome.AwardDate,
			URL:               r.Summary.TEDURL,
		}
		if p.Buyer != nil {
			row.BuyerID = p.Buyer.CompanyID
		}
		for _, w := range l.Outcome.Winners {
			row.Winners = append(row.Winners, w.Name)
			row.WinnerIDs = append(row.WinnerIDs, w.CompanyID)
		}
		// A single-lot notice states the awarded value once, for the whole
		// procedure, when the lot tender carries none.
		if row.Awarded == "" && len(p.Lots) == 1 {
			row.Awarded = p.TotalAwarded
		}
		row.DiscountPct = discount(row.Estimated, row.Awarded)
		rows = append(rows, row)
	}
	return rows
}

// discount is how far below the estimate the award came in. It is withheld
// when the two numbers clearly measure different things — an award far above
// the estimate means the estimate excluded something, not that the winner bid
// a negative discount.
func discount(estimated, awarded string) *float64 {
	e, ok1 := parseAmount(estimated)
	a, ok2 := parseAmount(awarded)
	if !ok1 || !ok2 || a > e*1.05 || a < e*0.2 {
		return nil
	}
	d := (e - a) / e * 100
	d = float64(int(d*10+0.5)) / 10
	return &d
}

func filterByWinner(rows []AwardRow, needle string) []AwardRow {
	var out []AwardRow
	for _, r := range rows {
		hay := strings.ToLower(strings.Join(r.Winners, " ") + " " + strings.Join(r.WinnerIDs, " "))
		if strings.Contains(hay, needle) {
			out = append(out, r)
		}
	}
	return out
}

func marketStats(rows []AwardRow) MarketStat {
	m := MarketStat{Lots: len(rows)}
	var tenders, discounts []float64
	single, counted := 0, 0
	for _, r := range rows {
		switch r.Status {
		case ted.OutcomeWinner:
			m.Awarded++
			if r.Tenders >= 0 {
				tenders = append(tenders, float64(r.Tenders))
				counted++
				if r.Tenders == 1 {
					single++
				}
			}
			if r.DiscountPct != nil {
				discounts = append(discounts, *r.DiscountPct)
			}
		case ted.OutcomeNoWinner:
			m.NoWinner++
		}
	}
	m.MedianTenders = ptrMedian(tenders)
	m.MedianDiscount = ptrMedian(discounts)
	if counted > 0 {
		share := float64(int(float64(single)/float64(counted)*1000+0.5)) / 10
		m.SingleBidShare = &share
	}
	return m
}

func competitorStats(rows []AwardRow, top int) []CompetitorStat {
	type acc struct {
		stat      CompetitorStat
		total     float64
		buyers    map[string]int
		tenders   []float64
		discounts []float64
	}
	byKey := map[string]*acc{}
	for _, r := range rows {
		if r.Status != ted.OutcomeWinner {
			continue
		}
		for i, name := range r.Winners {
			id := ""
			if i < len(r.WinnerIDs) {
				id = r.WinnerIDs[i]
			}
			key := operatorKey(name, id)
			a := byKey[key]
			if a == nil {
				a = &acc{stat: CompetitorStat{Name: name, CompanyID: id}, buyers: map[string]int{}}
				byKey[key] = a
			}
			a.stat.Wins++
			// A shared award is split evenly among a consortium's members.
			if v, ok := parseAmount(r.Awarded); ok {
				a.total += v / float64(len(r.Winners))
			}
			if r.Buyer != "" {
				a.buyers[r.Buyer]++
			}
			if r.Tenders >= 0 {
				a.tenders = append(a.tenders, float64(r.Tenders))
				if r.Tenders == 1 {
					a.stat.SingleBidWins++
				}
			}
			if r.DiscountPct != nil {
				a.discounts = append(a.discounts, *r.DiscountPct)
			}
			if r.Published > a.stat.LatestPublished {
				a.stat.LatestPublished = r.Published
			}
		}
	}
	out := make([]CompetitorStat, 0, len(byKey))
	for _, a := range byKey {
		a.stat.TotalAwarded = formatAmount(a.total)
		a.stat.Buyers = topKeys(a.buyers, 5)
		a.stat.MedianTenders = ptrMedian(a.tenders)
		a.stat.MedianDiscount = ptrMedian(a.discounts)
		out = append(out, a.stat)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Wins != out[j].Wins {
			return out[i].Wins > out[j].Wins
		}
		ti, _ := strconv.ParseFloat(out[i].TotalAwarded, 64)
		tj, _ := strconv.ParseFloat(out[j].TotalAwarded, 64)
		return ti > tj
	})
	if len(out) > top {
		out = out[:top]
	}
	return out
}

func buyerStats(rows []AwardRow, top int) []BuyerStat {
	type acc struct {
		stat      BuyerStat
		suppliers map[string]int
		tenders   []float64
	}
	byKey := map[string]*acc{}
	for _, r := range rows {
		if r.Buyer == "" {
			continue
		}
		key := operatorKey(r.Buyer, r.BuyerID)
		a := byKey[key]
		if a == nil {
			a = &acc{stat: BuyerStat{Name: r.Buyer, CompanyID: r.BuyerID}, suppliers: map[string]int{}}
			byKey[key] = a
		}
		a.stat.Awards++
		for _, w := range r.Winners {
			a.suppliers[w]++
		}
		if r.Tenders >= 0 && r.Status == ted.OutcomeWinner {
			a.tenders = append(a.tenders, float64(r.Tenders))
		}
	}
	out := make([]BuyerStat, 0, len(byKey))
	for _, a := range byKey {
		a.stat.DistinctWinners = len(a.suppliers)
		a.stat.Suppliers = topKeys(a.suppliers, 5)
		a.stat.MedianTenders = ptrMedian(a.tenders)
		out = append(out, a.stat)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Awards != out[j].Awards {
			return out[i].Awards > out[j].Awards
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > top {
		out = out[:top]
	}
	return out
}

func awardNote(out AwardHistoryOutput, limit int) string {
	var notes []string
	if out.Total > out.Page*limit {
		notes = append(notes, fmt.Sprintf("This call read page %d of %d award notices (%d per page); "+
			"call again with page=%d for more, or narrow the filters. Read notices are cached.",
			out.Page, out.Total, limit, out.Page+1))
	}
	notes = append(notes, "Discounts compare the award with the buyer's estimate, which often includes options and "+
		"renewals: read them as an upper bound, and compare like with like.")
	return strings.Join(notes, " ")
}

func awardsCSV(rows []AwardRow) [][]string {
	out := [][]string{{"publication_number", "published", "buyer", "buyer_id", "country", "title", "lot", "cpv", "status",
		"winners", "winner_ids", "tenders_received", "estimated_value", "awarded_value", "currency", "discount_pct", "award_date", "url"}}
	for _, r := range rows {
		disc := ""
		if r.DiscountPct != nil {
			disc = strconv.FormatFloat(*r.DiscountPct, 'f', 1, 64)
		}
		tenders := ""
		if r.Tenders >= 0 {
			tenders = strconv.Itoa(r.Tenders)
		}
		out = append(out, []string{r.PublicationNumber, r.Published, r.Buyer, r.BuyerID, r.Country, r.Title, r.Lot,
			strings.Join(r.CPV, " "), r.Status, strings.Join(r.Winners, "; "), strings.Join(r.WinnerIDs, "; "),
			tenders, r.Estimated, r.Awarded, r.Currency, disc, r.AwardDate, r.URL})
	}
	return out
}

// --- forecast_retenders ---

type ForecastInput struct {
	MarketFilters
	EndsFrom        string `json:"ends_from,omitempty" jsonschema:"list contracts ending on/after this date (YYYY-MM-DD, default today)"`
	EndsTo          string `json:"ends_to,omitempty" jsonschema:"list contracts ending on/before this date (YYYY-MM-DD, default 18 months from today)"`
	IncludeUnknown  bool   `json:"include_unknown,omitempty" jsonschema:"also list awarded contracts whose duration could not be determined (default false; they are always counted)"`
	IncludePINs     bool   `json:"include_pins,omitempty" jsonschema:"also list prior information notices published in the last 12 months for the same filters — buyers announcing upcoming tenders"`
	RenewalsInScope *bool  `json:"renewals_in_scope,omitempty" jsonschema:"also list contracts whose end falls in the window only once renewal options are exercised (default true)"`
	Format          string `json:"format,omitempty" jsonschema:"text (default) or csv to attach the forecast as a spreadsheet"`
}

// RetenderRow is one contract expected to end.
type RetenderRow struct {
	PublicationNumber string   `json:"publication_number"`
	Buyer             string   `json:"buyer,omitempty"`
	BuyerID           string   `json:"buyer_id,omitempty"`
	Country           string   `json:"country,omitempty"`
	Title             string   `json:"title,omitempty"`
	Lot               string   `json:"lot,omitempty"`
	CPV               []string `json:"cpv,omitempty"`
	Incumbent         []string `json:"incumbent,omitempty"`
	Awarded           string   `json:"awarded_value,omitempty"`
	Currency          string   `json:"currency,omitempty"`
	Start             string   `json:"start,omitempty"`
	End               string   `json:"end,omitempty"`
	EndSource         string   `json:"end_source,omitempty" jsonschema:"notice (planned period), text (read from the title/description) or derived (start + stated span)"`
	EndWithRenewals   string   `json:"end_with_renewals,omitempty"`
	Renewal           string   `json:"renewal,omitempty"`
	WatchFrom         string   `json:"watch_from,omitempty" jsonschema:"when to start watching for the new contract notice: nine months before the end"`
	InWindow          string   `json:"in_window" jsonschema:"base (the contract's own end is in the window) or renewal (only its end after renewals is)"`
	URL               string   `json:"url,omitempty"`
}

type ForecastOutput struct {
	Query       string              `json:"query"`
	Total       int                 `json:"total" jsonschema:"award notices matching the selection"`
	Page        int                 `json:"page"`
	Read        int                 `json:"read"`
	Window      [2]string           `json:"window"`
	Contracts   []RetenderRow       `json:"contracts,omitempty"`
	Unknown     int                 `json:"unknown_duration" jsonschema:"awarded lots whose end could not be determined"`
	UnknownRows []RetenderRow       `json:"unknown,omitempty"`
	OutOfWindow int                 `json:"out_of_window"`
	PINs        []ted.NoticeSummary `json:"prior_information_notices,omitempty"`
	Failures    []ScanError         `json:"failures,omitempty"`
	Note        string              `json:"note,omitempty"`
}

func handleForecast(tc *ted.Client) func(context.Context, *mcp.CallToolRequest, ForecastInput) (*mcp.CallToolResult, ForecastOutput, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ForecastInput) (*mcp.CallToolResult, ForecastOutput, error) {
		if in.empty() {
			return errorResult(fmt.Errorf("give at least one filter: cpv, country, buyer, keywords or query")), ForecastOutput{}, nil
		}
		now := today()
		from := dateOr(in.EndsFrom, now)
		to := dateOr(in.EndsTo, now.AddDate(0, 18, 0))
		// Contracts ending in the window were mostly awarded years ago; look
		// back far enough to find them unless told otherwise.
		filters := in.MarketFilters
		if filters.PublishedFrom == "" {
			filters.PublishedFrom = now.AddDate(-6, 0, 0).Format("2006-01-02")
		}

		query, err := filters.query(awardNoticeTypes)
		if err != nil {
			return errorResult(err), ForecastOutput{}, nil
		}
		limit := clamp(in.Limit, defaultMarketLimit, 1, maxMarketLimit)
		notices, sel, err := searchNotices(ctx, tc, query, in.Page, limit)
		if err != nil {
			return errorResult(err), ForecastOutput{}, nil
		}

		out := ForecastOutput{Query: sel.Query, Total: sel.Total, Page: sel.Page, Window: [2]string{from, to}}
		renewals := in.RenewalsInScope == nil || *in.RenewalsInScope
		for _, r := range readProcedures(ctx, tc, notices, in.Concurrency) {
			if r.Err != nil {
				out.Failures = append(out.Failures, ScanError{PublicationNumber: r.Summary.PublicationNumber, Error: r.Err.Error()})
				continue
			}
			out.Read++
			for _, row := range retenderRows(r) {
				switch {
				case row.End == "":
					out.Unknown++
					if in.IncludeUnknown {
						out.UnknownRows = append(out.UnknownRows, row)
					}
				case row.End >= from && row.End <= to:
					row.InWindow = "base"
					out.Contracts = append(out.Contracts, row)
				case renewals && row.EndWithRenewals != "" && row.EndWithRenewals >= from && row.EndWithRenewals <= to:
					row.InWindow = "renewal"
					out.Contracts = append(out.Contracts, row)
				default:
					out.OutOfWindow++
				}
			}
		}
		sort.SliceStable(out.Contracts, func(i, j int) bool {
			return effectiveEnd(out.Contracts[i]) < effectiveEnd(out.Contracts[j])
		})

		if in.IncludePINs {
			pinQuery, err := in.MarketFilters.withPublished(now.AddDate(-1, 0, 0).Format("2006-01-02"), "").
				query("notice-type IN (pin-only pin-buyer pin-tran pin-rtl pin-cfc-standard pin-cfc-social)")
			if err == nil {
				if pins, _, err := searchNotices(ctx, tc, pinQuery, 1, 50); err == nil {
					for _, n := range pins {
						out.PINs = append(out.PINs, n.Summary())
					}
				}
			}
		}

		out.Note = forecastNote(out, limit)
		if wantsCSV(in.Format) {
			return csvResult(formatForecast(out), "retenders", forecastCSV(out.Contracts)), out, nil
		}
		return textResult(formatForecast(out)), out, nil
	}
}

func (f MarketFilters) withPublished(from, to string) MarketFilters {
	f.PublishedFrom, f.PublishedTo = from, to
	return f
}

func effectiveEnd(r RetenderRow) string {
	if r.InWindow == "renewal" {
		return r.EndWithRenewals
	}
	return r.End
}

// retenderRows works out when each awarded lot of a notice ends.
func retenderRows(r procNotice) []RetenderRow {
	p := r.Procedure
	var rows []RetenderRow
	for _, l := range p.Lots {
		if l.Outcome == nil || l.Outcome.Status != ted.OutcomeWinner {
			continue
		}
		// The contract starts when it is signed, or failing that when it was
		// awarded; the notice date is the last resort.
		start := firstNonEmpty(l.Duration.Start, l.Outcome.ContractDate, l.Outcome.AwardDate, p.IssueDate)
		end, how := l.Duration.EndDate(start)
		row := RetenderRow{
			PublicationNumber: r.Summary.PublicationNumber,
			Buyer:             firstNonEmpty(r.Summary.BuyerName, buyerName(p)),
			Country:           r.Summary.BuyerCountry,
			Title:             firstNonEmpty(l.Title, p.Title, r.Summary.Title),
			Lot:               l.ID,
			CPV:               firstNonEmptySlice(l.CPV, r.Summary.CPV),
			Awarded:           firstNonEmpty(l.Outcome.AwardedValue, p.TotalAwarded),
			Currency:          firstNonEmpty(l.Outcome.Currency, l.Currency, p.Currency),
			Start:             start,
			End:               end,
			EndSource:         how,
			Renewal:           l.RenewalDescription,
			URL:               r.Summary.TEDURL,
		}
		if p.Buyer != nil {
			row.BuyerID = p.Buyer.CompanyID
		}
		for _, w := range l.Outcome.Winners {
			row.Incumbent = append(row.Incumbent, w.Name)
		}
		if end != "" {
			row.WatchFrom = ted.AddMonths(end, -9)
			if months := l.Duration.RenewalMonths; months > 0 {
				times := max(l.MaxRenewals, 1)
				row.EndWithRenewals = ted.AddMonths(end, float64(months*times))
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func forecastNote(out ForecastOutput, limit int) string {
	var notes []string
	if out.Total > out.Page*limit {
		notes = append(notes, fmt.Sprintf("Read page %d of %d award notices (%d per page); call again with page=%d "+
			"to cover the rest — read notices are cached.", out.Page, out.Total, limit, out.Page+1))
	}
	if out.Unknown > 0 {
		notes = append(notes, fmt.Sprintf("%d awarded lot(s) state no duration the server could read: they are not "+
			"absent from the pipeline, only undated. Set include_unknown to list them; the capitolato states the term.", out.Unknown))
	}
	notes = append(notes, "End dates from text are read from what the buyer wrote in the title or description; "+
		"buyers also extend contracts with proroghe that no award notice records, so treat each date as when to start "+
		"watching, not as when the new tender will appear.")
	return strings.Join(notes, " ")
}

func forecastCSV(rows []RetenderRow) [][]string {
	out := [][]string{{"publication_number", "buyer", "buyer_id", "country", "title", "lot", "cpv", "incumbent", "awarded_value",
		"currency", "start", "end", "end_source", "end_with_renewals", "renewal", "watch_from", "in_window", "url"}}
	for _, r := range rows {
		out = append(out, []string{r.PublicationNumber, r.Buyer, r.BuyerID, r.Country, r.Title, r.Lot, strings.Join(r.CPV, " "),
			strings.Join(r.Incumbent, "; "), r.Awarded, r.Currency, r.Start, r.End, r.EndSource, r.EndWithRenewals,
			r.Renewal, r.WatchFrom, r.InWindow, r.URL})
	}
	return out
}

// --- helpers ---

func buyerName(p *ted.Procedure) string {
	if p.Buyer != nil {
		return p.Buyer.Name
	}
	return ""
}

// operatorKey identifies an organisation across notices: by its registration
// number when it has one, since names are spelt a dozen ways ("CIRFOOD S.C.",
// "Cirfood Soc. Coop."), and by its normalised name otherwise.
func operatorKey(name, id string) string {
	if id = strings.ToUpper(strings.TrimSpace(id)); id != "" {
		return "id:" + strings.TrimPrefix(id, "IT")
	}
	return "name:" + strings.Join(strings.Fields(strings.ToUpper(name)), " ")
}

func topKeys(m map[string]int, n int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	if len(keys) > n {
		keys = keys[:n]
	}
	for i, k := range keys {
		if m[k] > 1 {
			keys[i] = fmt.Sprintf("%s (%d)", k, m[k])
		}
	}
	return keys
}

func ptrMedian(values []float64) *float64 {
	m, ok := median(values)
	if !ok {
		return nil
	}
	m = float64(int(m*10+0.5)) / 10
	return &m
}

func firstNonEmptySlice(values ...[]string) []string {
	for _, v := range values {
		if len(v) > 0 {
			return v
		}
	}
	return nil
}
