package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bernardoforcillo/tedmcp/internal/ted"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Shared plumbing for the tools that read many award notices at once
// (award_history, forecast_retenders) and for the team tools: one way of
// selecting notices, one way of reading their procedures, one CSV export, and
// one answer to "who is calling".

// MarketFilters selects the notices a market analysis reads. It mirrors the
// search_tenders filters a research team actually uses, plus the buyer by name.
type MarketFilters struct {
	Query         string   `json:"query,omitempty" jsonschema:"raw TED expert query, combined with AND with the filters below"`
	Keywords      string   `json:"keywords,omitempty" jsonschema:"full-text terms matched against the notice title and short description"`
	Cpv           []string `json:"cpv,omitempty" jsonschema:"CPV codes, exact or trailing-wildcard prefixes (555*), as in search_tenders"`
	Country       []string `json:"country,omitempty" jsonschema:"buyer country as ISO 3166-1 alpha-3 code(s), e.g. ITA"`
	Buyer         string   `json:"buyer,omitempty" jsonschema:"buyer name as TED records it, e.g. COMUNE DI VIGEVANO (matched as a phrase)"`
	PublishedFrom string   `json:"published_from,omitempty" jsonschema:"award notices published on/after this date (YYYY-MM-DD)"`
	PublishedTo   string   `json:"published_to,omitempty" jsonschema:"award notices published on/before this date (YYYY-MM-DD)"`
	Limit         int      `json:"limit,omitempty" jsonschema:"how many award notices to read per call, 1-120 (default 60). Notices are cached, so a second pass is fast"`
	Page          int      `json:"page,omitempty" jsonschema:"1-based page of the selection, for result sets larger than limit (default 1)"`
	Concurrency   int      `json:"concurrency,omitempty" jsonschema:"parallel downloads, 1-8 (default 4)"`
}

const (
	defaultMarketLimit = 60
	maxMarketLimit     = 120
)

// awardNoticeTypes are the notice types that carry a results section.
const awardNoticeTypes = "notice-type IN (can-standard can-social can-desg can-tran)"

// query compiles the filters into a TED expert query.
func (f MarketFilters) query(noticeClause string) (string, error) {
	var parts []string
	if q := strings.TrimSpace(f.Query); q != "" {
		parts = append(parts, "("+q+")")
	}
	if b := strings.TrimSpace(f.Buyer); b != "" {
		parts = append(parts, `buyer-name="`+strings.ReplaceAll(b, `"`, ``)+`"`)
	}
	if noticeClause != "" {
		parts = append(parts, "("+noticeClause+")")
	}
	return ted.BuildQuery(ted.SearchFilters{
		Query:         strings.Join(parts, " AND "),
		Keywords:      f.Keywords,
		CPV:           f.Cpv,
		Country:       f.Country,
		PublishedFrom: f.PublishedFrom,
		PublishedTo:   f.PublishedTo,
	})
}

func (f MarketFilters) empty() bool {
	return strings.TrimSpace(f.Query) == "" && strings.TrimSpace(f.Keywords) == "" && len(f.Cpv) == 0 &&
		len(f.Country) == 0 && strings.TrimSpace(f.Buyer) == ""
}

// procNotice is one notice read for its procedure.
type procNotice struct {
	Summary   ted.NoticeSummary
	Procedure *ted.Procedure
	Dossier   *ted.Dossier
	Err       error
}

// selection is what a market query matched, before anything is downloaded.
type selection struct {
	Query string
	Total int
	Page  int
}

// searchNotices runs a market query and returns one page of matching notices.
func searchNotices(ctx context.Context, tc *ted.Client, query string, page, limit int) ([]ted.Notice, selection, error) {
	if page <= 0 {
		page = 1
	}
	resp, err := tc.Search(ctx, ted.SearchRequest{
		Query:          query,
		Fields:         ted.SummaryFields,
		Page:           page,
		Limit:          limit,
		Scope:          "ALL",
		PaginationMode: "PAGE_NUMBER",
	})
	if err != nil {
		return nil, selection{}, err
	}
	return resp.Notices, selection{Query: query, Total: resp.TotalNoticeCount, Page: page}, nil
}

// readProcedures downloads and parses notices in parallel. A notice that cannot
// be read is returned with its error rather than dropped, so the caller can
// report it as not read instead of as empty.
func readProcedures(ctx context.Context, tc *ted.Client, notices []ted.Notice, workers int) []procNotice {
	out := make([]procNotice, len(notices))
	runParallel(len(notices), clamp(workers, defaultScanConcurrency, 1, maxScanConcurrency), func(i int) {
		out[i] = readProcedure(ctx, tc, notices[i].Summary())
	})
	return out
}

func readProcedure(ctx context.Context, tc *ted.Client, summary ted.NoticeSummary) procNotice {
	pn := procNotice{Summary: summary}
	if summary.XMLURL == "" {
		pn.Err = fmt.Errorf("notice has no XML document")
		return pn
	}
	_, data, err := tc.FetchDocumentRetrying(ctx, summary.XMLURL, 16<<20, 4)
	if err != nil {
		pn.Err = err
		return pn
	}
	if pn.Procedure, err = ted.ParseProcedure(data); err != nil {
		pn.Err = err
		return pn
	}
	pn.Dossier, _ = ted.ParseDossier(data)
	return pn
}

// readNotice fetches one notice by publication number and parses it.
func readNotice(ctx context.Context, tc *ted.Client, publicationNumber string) procNotice {
	notice, err := tc.GetNotice(ctx, publicationNumber, ted.SummaryFields)
	if err != nil {
		return procNotice{Summary: ted.NoticeSummary{PublicationNumber: publicationNumber}, Err: err}
	}
	return readProcedure(ctx, tc, notice.Summary())
}

// --- numbers ---

func parseAmount(s string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v, err == nil && v > 0
}

func formatAmount(v float64) string {
	if v == 0 {
		return ""
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}

func median(values []float64) (float64, bool) {
	if len(values) == 0 {
		return 0, false
	}
	s := append([]float64(nil), values...)
	sort.Float64s(s)
	mid := len(s) / 2
	if len(s)%2 == 1 {
		return s[mid], true
	}
	return (s[mid-1] + s[mid]) / 2, true
}

func today() time.Time { return time.Now().UTC().Truncate(24 * time.Hour) }

func dateOr(s string, fallback time.Time) string {
	if strings.TrimSpace(s) != "" {
		return strings.TrimSpace(s)
	}
	return fallback.Format("2006-01-02")
}

// --- CSV ---

// wantsCSV reports whether a tool's format input asked for a CSV export.
func wantsCSV(format string) bool {
	return strings.EqualFold(strings.TrimSpace(format), "csv")
}

// csvResult returns a summary for the reader plus the CSV itself as an
// embedded file, which clients can save or open in a spreadsheet directly.
func csvResult(summary, name string, rows [][]string) *mcp.CallToolResult {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.WriteAll(rows)
	data := buf.String()
	return &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.TextContent{Text: summary + fmt.Sprintf("\n\nCSV export attached (%d row(s)): tedmcp://export/%s.csv", max(len(rows)-1, 0), name)},
		&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{
			URI:      "tedmcp://export/" + name + ".csv",
			MIMEType: "text/csv",
			Text:     data,
		}},
	}}
}

func searchCSV(notices []ted.NoticeSummary) [][]string {
	rows := [][]string{{"publication_number", "title", "notice_type", "buyer", "country", "cpv", "published", "deadline", "value", "url"}}
	for _, n := range notices {
		rows = append(rows, []string{n.PublicationNumber, n.Title, n.NoticeType, n.BuyerName, n.BuyerCountry,
			strings.Join(n.CPV, " "), n.PublicationDate, n.Deadline, n.TotalValue, n.TEDURL})
	}
	return rows
}

// --- identity ---

// caller names whoever made the request, when the server knows: the user a
// bearer token was issued to. Over stdio, and over HTTP without tokens, it is
// empty and team records simply carry no author.
func caller(req *mcp.CallToolRequest) string {
	if req == nil || req.Extra == nil || req.Extra.TokenInfo == nil {
		return ""
	}
	return req.Extra.TokenInfo.UserID
}
