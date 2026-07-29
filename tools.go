package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/bernardoforcillo/tedmcp/internal/match"
	"github.com/bernardoforcillo/tedmcp/internal/ted"
	"github.com/bernardoforcillo/tedmcp/internal/webdoc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerTools wires all tedmcp tools onto the MCP server.
func registerTools(s *mcp.Server, tc *ted.Client, wc *webdoc.Client) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "search_tenders",
		Description: "Search the EU public procurement journal TED (Tenders Electronic Daily) for tenders/procurement notices. " +
			"Filter by free-text keywords, CPV codes, buyer country, notice type, publication date, and submission deadline. " +
			"Returns matching notices with buyer, CPV, deadline, and links to the full PDF/XML documents.",
	}, handleSearch(tc))

	mcp.AddTool(s, &mcp.Tool{
		Name: "get_tender",
		Description: "Fetch full details of a single TED notice by its publication number (e.g. 521055-2026), " +
			"including all raw fields and links to the PDF/XML/HTML documents.",
	}, handleGetTender(tc))

	mcp.AddTool(s, &mcp.Tool{
		Name: "get_tender_document",
		Description: "Download the actual document file for a TED notice so its procurement content can be read/searched. " +
			"Format 'xml' (default) returns the machine-readable eForms content as text; 'pdf'/'html' return the document URL and metadata. " +
			"Use this after search_tenders/get_tender to inspect the full text of a tender.",
	}, handleGetDocument(tc))

	mcp.AddTool(s, &mcp.Tool{
		Name: "get_tender_dossier",
		Description: "Everything a notice itself tells you about one procurement, in a single call: who the buyer is with their contact " +
			"details and codice fiscale, the exact submission deadline including the time of day, the lots with values and CPV codes, " +
			"the award criteria with their weights, and every URL in the notice labelled by what it actually leads to — the tender " +
			"documents, the submission endpoint, the buyer's site, or the court that hears appeals. " +
			"Prefer this over get_tender_document when you want to understand a tender rather than read raw XML.",
	}, handleDossier(tc))

	mcp.AddTool(s, &mcp.Tool{
		Name: "fetch_tender_documents",
		Description: "Try to download the tender documents (capitolato, disciplinare) from the buyer's own portal, which is where they live — " +
			"TED carries only the notice. Obeys each site's robots.txt and reports honestly when a document cannot be retrieved: most Italian " +
			"procurement portals disallow automated clients and put the procedure page behind a captcha, in which case this returns the URL to " +
			"open manually rather than pretending nothing is there. Never treat a robots-denied or captcha result as 'no documents exist'.",
	}, handleFetchDocuments(tc, wc))

	mcp.AddTool(s, &mcp.Tool{
		Name: "lookup_anac",
		Description: "Join a TED notice to the Italian national contracts database (ANAC) to get its CIG, the award outcome and the official amounts. " +
			"Matches on the buyer's codice fiscale, which every Italian eForms notice carries, optionally narrowed by contract value or CPV. " +
			"Requires a snapshot of the ANAC 'cig' dataset downloaded from https://dati.anticorruzione.it/opendata/dataset/cig — that site's " +
			"firewall refuses non-browser clients, so the file must be fetched with a browser rather than by this server. " +
			"ANAC carries structured data only: it never holds the tender documents.",
	}, handleLookupANAC(tc))

	mcp.AddTool(s, &mcp.Tool{
		Name: "scan_tenders",
		Description: "Search INSIDE many notices at once: downloads each notice's eForms XML in parallel and reports where your search terms appear, " +
			"together with the award criteria (and their weights/points), strategic-procurement flags, and the buyer-platform links to the tender documents. " +
			"Use this instead of `keywords` in search_tenders whenever the question is about a requirement rather than a subject: TED's full-text index only covers " +
			"the notice title and a short description, so requirements that live in the scoring grid (e.g. food waste, staff training, CO2 footprint) are invisible to it. " +
			"For a search spanning more than one country, give `terms_by_language` rather than `terms` or `regex`: each notice is then matched only against the terms of the " +
			"language it declares, which is what keeps one language's word stem from colliding with another's ordinary vocabulary. Use `near` when the requirement is written " +
			"compositionally rather than as a set phrase. " +
			"Give it either `publication_numbers` or the same search filters as search_tenders, and it does the search, the bulk download, and the matching in one call. " +
			"Documents are cached on disk, so re-running a scan with different terms costs almost nothing.",
	}, handleScan(tc))
}

// --- search_tenders ---

type SearchInput struct {
	Query         string   `json:"query,omitempty" jsonschema:"raw TED expert query; combined with AND with the structured filters below. Example: classification-cpv=72000000 AND buyer-country=DEU"`
	Keywords      string   `json:"keywords,omitempty" jsonschema:"free-text terms matched across the whole notice (full-text search), e.g. cloud computing"`
	Cpv           []string `json:"cpv,omitempty" jsonschema:"one or more CPV codes (Common Procurement Vocabulary). Exact 8-digit codes (55500000, 55524000) or trailing-wildcard prefixes (555*, 555XXXXX, 5552X) to match a whole CPV family. Mixed lists are OR'd together"`
	Country       []string `json:"country,omitempty" jsonschema:"buyer country as ISO 3166-1 alpha-3 code(s), e.g. DEU, FRA, ITA"`
	NoticeType    string   `json:"notice_type,omitempty" jsonschema:"TED notice type, e.g. cn-standard (contract notice) or can-standard (contract award)"`
	PublishedFrom string   `json:"published_from,omitempty" jsonschema:"only notices published on/after this date (YYYY-MM-DD)"`
	PublishedTo   string   `json:"published_to,omitempty" jsonschema:"only notices published on/before this date (YYYY-MM-DD)"`
	DeadlineFrom  string   `json:"deadline_from,omitempty" jsonschema:"tender submission deadline on/after this date (YYYY-MM-DD)"`
	DeadlineTo    string   `json:"deadline_to,omitempty" jsonschema:"tender submission deadline on/before this date (YYYY-MM-DD)"`
	Page          int      `json:"page,omitempty" jsonschema:"1-based page number (default 1)"`
	Limit         int      `json:"limit,omitempty" jsonschema:"results per page, 1-250 (default 20)"`
	SortBy        string   `json:"sort_by,omitempty" jsonschema:"field to sort by (default publication-date)"`
	SortOrder     string   `json:"sort_order,omitempty" jsonschema:"ASC or DESC (default DESC)"`
	Scope         string   `json:"scope,omitempty" jsonschema:"ALL, LATEST, or ACTIVE (default ALL)"`
}

type SearchOutput struct {
	Query   string              `json:"query" jsonschema:"the compiled TED expert query that was executed"`
	Total   int                 `json:"total" jsonschema:"total number of matching notices in TED"`
	Page    int                 `json:"page"`
	Limit   int                 `json:"limit"`
	Count   int                 `json:"count" jsonschema:"number of notices returned on this page"`
	Notices []ted.NoticeSummary `json:"notices"`
}

func handleSearch(tc *ted.Client) func(context.Context, *mcp.CallToolRequest, SearchInput) (*mcp.CallToolResult, SearchOutput, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in SearchInput) (*mcp.CallToolResult, SearchOutput, error) {
		limit := in.Limit
		if limit <= 0 {
			limit = 20
		}
		if limit > 250 {
			limit = 250
		}
		page := in.Page
		if page <= 0 {
			page = 1
		}
		scope := strings.ToUpper(strings.TrimSpace(in.Scope))
		if scope == "" {
			scope = "ALL"
		}

		query, err := ted.BuildQuery(ted.SearchFilters{
			Query:         in.Query,
			Keywords:      in.Keywords,
			CPV:           in.Cpv,
			Country:       in.Country,
			NoticeType:    in.NoticeType,
			PublishedFrom: in.PublishedFrom,
			PublishedTo:   in.PublishedTo,
			DeadlineFrom:  in.DeadlineFrom,
			DeadlineTo:    in.DeadlineTo,
			SortBy:        in.SortBy,
			SortOrder:     in.SortOrder,
		})
		if err != nil {
			return errorResult(err), SearchOutput{}, nil
		}

		resp, err := tc.Search(ctx, ted.SearchRequest{
			Query:          query,
			Fields:         ted.SummaryFields,
			Page:           page,
			Limit:          limit,
			Scope:          scope,
			PaginationMode: "PAGE_NUMBER",
		})
		if err != nil {
			return errorResult(err), SearchOutput{}, nil
		}

		out := SearchOutput{
			Query: query,
			Total: resp.TotalNoticeCount,
			Page:  page,
			Limit: limit,
		}
		for _, n := range resp.Notices {
			out.Notices = append(out.Notices, n.Summary())
		}
		out.Count = len(out.Notices)

		return textResult(formatSearch(out)), out, nil
	}
}

// --- get_tender ---

type GetTenderInput struct {
	PublicationNumber string `json:"publication_number" jsonschema:"TED publication number, e.g. 521055-2026"`
}

type GetTenderOutput struct {
	Summary ted.NoticeSummary `json:"summary"`
	Fields  ted.Notice        `json:"fields" jsonschema:"all raw fields returned by TED for this notice"`
}

func handleGetTender(tc *ted.Client) func(context.Context, *mcp.CallToolRequest, GetTenderInput) (*mcp.CallToolResult, GetTenderOutput, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in GetTenderInput) (*mcp.CallToolResult, GetTenderOutput, error) {
		pn := strings.TrimSpace(in.PublicationNumber)
		if pn == "" {
			return errorResult(fmt.Errorf("publication_number is required")), GetTenderOutput{}, nil
		}

		notice, err := tc.GetNotice(ctx, pn, ted.DetailFields)
		if err != nil {
			return errorResult(err), GetTenderOutput{}, nil
		}

		out := GetTenderOutput{Summary: notice.Summary(), Fields: notice}
		return textResult(formatSummaryDetail(out.Summary)), out, nil
	}
}

// --- get_tender_document ---

type GetDocInput struct {
	PublicationNumber string `json:"publication_number" jsonschema:"TED publication number, e.g. 521055-2026"`
	Format            string `json:"format,omitempty" jsonschema:"xml (default, full machine-readable content as text), pdf, or html"`
	Language          string `json:"language,omitempty" jsonschema:"3-letter TED language code for pdf/html, e.g. ENG (default), FRA, DEU; xml is always MUL"`
	MaxChars          int    `json:"max_chars,omitempty" jsonschema:"truncate returned text to this many characters (default 60000)"`
}

type GetDocOutput struct {
	PublicationNumber string `json:"publication_number"`
	Format            string `json:"format"`
	Language          string `json:"language"`
	URL               string `json:"url"`
	ContentType       string `json:"content_type,omitempty"`
	Bytes             int    `json:"bytes,omitempty"`
	Text              string `json:"text,omitempty" jsonschema:"document text (for xml/html formats)"`
	Truncated         bool   `json:"truncated,omitempty"`
	Note              string `json:"note,omitempty"`
}

func handleGetDocument(tc *ted.Client) func(context.Context, *mcp.CallToolRequest, GetDocInput) (*mcp.CallToolResult, GetDocOutput, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in GetDocInput) (*mcp.CallToolResult, GetDocOutput, error) {
		pn := strings.TrimSpace(in.PublicationNumber)
		if pn == "" {
			return errorResult(fmt.Errorf("publication_number is required")), GetDocOutput{}, nil
		}

		format := strings.ToLower(strings.TrimSpace(in.Format))
		if format == "" {
			format = "xml"
		}
		if format != "xml" && format != "pdf" && format != "html" {
			return errorResult(fmt.Errorf("invalid format %q: use xml, pdf, or html", in.Format)), GetDocOutput{}, nil
		}

		lang := strings.ToUpper(strings.TrimSpace(in.Language))
		if format == "xml" {
			lang = "MUL"
		} else if lang == "" {
			lang = "ENG"
		}

		maxChars := in.MaxChars
		if maxChars <= 0 {
			maxChars = 60000
		}

		notice, err := tc.GetNotice(ctx, pn, []string{"publication-number", "links"})
		if err != nil {
			return errorResult(err), GetDocOutput{}, nil
		}

		url := ted.Link(notice, format, lang)
		if url == "" {
			return errorResult(fmt.Errorf("no %s document available for notice %s", format, pn)), GetDocOutput{}, nil
		}

		out := GetDocOutput{PublicationNumber: pn, Format: format, Language: lang, URL: url}

		// PDF is binary; return the link and metadata rather than raw bytes.
		if format == "pdf" {
			out.Note = "PDF is binary; open the URL to read it. For machine-readable full text use format=xml."
			return textResult(formatDoc(out)), out, nil
		}

		contentType, data, err := tc.FetchDocument(ctx, url, int64(maxChars)*4+1<<20)
		if err != nil {
			return errorResult(err), GetDocOutput{}, nil
		}
		out.ContentType = contentType
		out.Bytes = len(data)

		text := string(data)
		if len(text) > maxChars {
			text = text[:maxChars]
			out.Truncated = true
		}
		out.Text = text
		if format == "html" {
			out.Note = "This is the raw TED web page markup. For structured procurement content prefer format=xml."
		}

		return textResult(formatDoc(out)), out, nil
	}
}

// --- scan_tenders ---

// Scanning downloads one document per notice, so the defaults keep a single
// call to a few seconds while still covering a whole search page.
const (
	defaultScanLimit       = 40
	maxScanLimit           = 120
	defaultScanConcurrency = 4
	maxScanConcurrency     = 8
	scanContextChars       = 180
	maxMatchesPerNotice    = 12
	maxCriterionChars      = 500
)

type ScanInput struct {
	Terms           []string            `json:"terms,omitempty" jsonschema:"case-insensitive substrings to look for in every notice whatever its language, e.g. [\"food waste\",\"gaspillage\"]. Matching is on substrings, so a stem like 'eccedenz' also matches 'eccedenze'"`
	TermsByLanguage map[string][]string `json:"terms_by_language,omitempty" jsonschema:"terms keyed by the language they belong to (ISO 639-2/T, e.g. ITA, DEU, BUL; * means any language). Each notice is matched only against the terms of the language it declares, which is what stops one language's word stem from colliding with another's ordinary vocabulary. Strongly preferred over terms or regex for any search spanning more than one country"`
	Near            *match.Near         `json:"near,omitempty" jsonschema:"match two groups of roots appearing close together, for requirements written compositionally rather than as a set phrase (e.g. a food root near a waste root catches both 'food waste' and 'Minimizing Waste Generation ... Food'). Keyed by language like terms_by_language"`
	Regex           string              `json:"regex,omitempty" jsonschema:"regular expression to match instead of terms (RE2 syntax, case-insensitive unless it sets its own flags). Applied to every notice regardless of language, so short stems will collide across languages"`

	PublicationNumbers []string `json:"publication_numbers,omitempty" jsonschema:"scan these notices directly, e.g. [\"478044-2026\",\"517698-2026\"]. When omitted, the search filters below select the notices to scan"`

	Query         string   `json:"query,omitempty" jsonschema:"raw TED expert query, as in search_tenders"`
	Keywords      string   `json:"keywords,omitempty" jsonschema:"full-text terms used to SELECT notices (matched against the notice summary only); use terms to search inside them"`
	Cpv           []string `json:"cpv,omitempty" jsonschema:"CPV codes, exact or trailing-wildcard prefixes, as in search_tenders"`
	Country       []string `json:"country,omitempty" jsonschema:"buyer country as ISO 3166-1 alpha-3 code(s), e.g. ITA"`
	NoticeType    string   `json:"notice_type,omitempty" jsonschema:"TED notice type, e.g. cn-standard (contract notice)"`
	PublishedFrom string   `json:"published_from,omitempty" jsonschema:"only notices published on/after this date (YYYY-MM-DD)"`
	PublishedTo   string   `json:"published_to,omitempty" jsonschema:"only notices published on/before this date (YYYY-MM-DD)"`
	DeadlineFrom  string   `json:"deadline_from,omitempty" jsonschema:"submission deadline on/after this date (YYYY-MM-DD)"`
	DeadlineTo    string   `json:"deadline_to,omitempty" jsonschema:"submission deadline on/before this date (YYYY-MM-DD)"`
	Scope         string   `json:"scope,omitempty" jsonschema:"ALL, LATEST, or ACTIVE (default ALL)"`

	Limit            int  `json:"limit,omitempty" jsonschema:"how many notices to scan per call, 1-120 (default 40). Notices already cached from an earlier scan cost nothing to re-read, so repeating a scan with different terms is fast"`
	Page             int  `json:"page,omitempty" jsonschema:"1-based page of the selection, so a result set larger than limit can be covered in several calls (default 1). The reported total tells you how many pages there are"`
	IncludeUnmatched bool `json:"include_unmatched,omitempty" jsonschema:"also return notices where no term matched (default false). Useful to see which notices publish no award criteria at all"`
	IncludeCriteria  bool `json:"include_criteria,omitempty" jsonschema:"return every award criterion of each notice, not just the matching ones"`
	Concurrency      int  `json:"concurrency,omitempty" jsonschema:"parallel downloads, 1-8 (default 4). TED rate-limits aggressively above this"`
}

type ScanMatch struct {
	Where  string `json:"where" jsonschema:"award-criterion, description, or the eForms element the text came from"`
	Weight string `json:"weight,omitempty" jsonschema:"points assigned to the criterion, when the match is an award criterion"`
	Text   string `json:"text" jsonschema:"the matching text, with surrounding context"`
}

type ScanNotice struct {
	Notice            ted.NoticeSummary    `json:"notice"`
	Language          string               `json:"language,omitempty" jsonschema:"the language the notice declares"`
	Matched           bool                 `json:"matched"`
	Matches           []ScanMatch          `json:"matches,omitempty"`
	CriteriaPublished bool                 `json:"criteria_published" jsonschema:"false when the notice publishes no scoring grid and refers to the disciplinare di gara instead"`
	AwardCriteria     []ted.AwardCriterion `json:"award_criteria,omitempty"`
	Strategic         []string             `json:"strategic_procurement,omitempty"`
	DocumentURLs      []string             `json:"document_urls,omitempty"`
}

type ScanError struct {
	PublicationNumber string `json:"publication_number"`
	Error             string `json:"error"`
}

type ScanOutput struct {
	Query     string         `json:"query,omitempty" jsonschema:"the compiled TED expert query used to select the notices"`
	Pattern   string         `json:"pattern,omitempty" jsonschema:"the regular expression the notices were searched with"`
	Total     int            `json:"total" jsonschema:"notices matching the selection query in TED (may exceed the number scanned)"`
	Page      int            `json:"page,omitempty" jsonschema:"which page of the selection was scanned"`
	PageSize  int            `json:"page_size,omitempty" jsonschema:"how many notices one page covers"`
	Scanned   int            `json:"scanned" jsonschema:"notices whose XML was downloaded and searched"`
	Matched   int            `json:"matched" jsonschema:"notices where at least one term was found"`
	Notices   []ScanNotice   `json:"notices,omitempty"`
	Failures  []ScanError    `json:"failures,omitempty" jsonschema:"notices whose document could not be read"`
	Uncovered map[string]int `json:"uncovered_languages,omitempty" jsonschema:"languages of scanned notices the search has no terms for, with how many notices each; those notices were read but could not really be searched"`
	Cache     ted.CacheStats `json:"cache,omitzero" jsonschema:"how many notice documents came from the local cache rather than the network"`
}

func handleScan(tc *ted.Client) func(context.Context, *mcp.CallToolRequest, ScanInput) (*mcp.CallToolResult, ScanOutput, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ScanInput) (*mcp.CallToolResult, ScanOutput, error) {
		matcher, err := buildMatcher(in)
		if err != nil {
			return errorResult(err), ScanOutput{}, nil
		}

		limit := clamp(in.Limit, defaultScanLimit, 1, maxScanLimit)
		workers := clamp(in.Concurrency, defaultScanConcurrency, 1, maxScanConcurrency)

		out := ScanOutput{PageSize: limit}
		if matcher != nil {
			out.Pattern = matcher.describe()
		}
		before := tc.Cache.Stats()

		notices, err := selectNotices(ctx, tc, in, limit, &out)
		if err != nil {
			return errorResult(err), ScanOutput{}, nil
		}

		results := make([]*ScanNotice, len(notices))
		failures := make([]ScanError, len(notices))
		sem := make(chan struct{}, workers)
		var wg sync.WaitGroup

		for i, n := range notices {
			wg.Add(1)
			go func(i int, n ted.Notice) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				summary := n.Summary()
				scanned, err := scanNotice(ctx, tc, summary, matcher, in.IncludeCriteria)
				if err != nil {
					failures[i] = ScanError{PublicationNumber: summary.PublicationNumber, Error: err.Error()}
					return
				}
				results[i] = scanned
			}(i, n)
		}
		wg.Wait()

		for i := range results {
			if f := failures[i]; f.PublicationNumber != "" {
				out.Failures = append(out.Failures, f)
				continue
			}
			r := results[i]
			if r == nil {
				continue
			}
			out.Scanned++
			if r.Matched {
				out.Matched++
			}
			// A notice in a language the search says nothing about was read
			// but not really searched; saying so turns a silent blind spot
			// into something the caller can fix.
			if matcher != nil && r.Language != "" && !matcher.covers(r.Language) {
				if out.Uncovered == nil {
					out.Uncovered = map[string]int{}
				}
				out.Uncovered[r.Language]++
			}
			if r.Matched || in.IncludeUnmatched || matcher == nil {
				out.Notices = append(out.Notices, *r)
			}
		}

		// Report what this call drew from the cache, not the running totals.
		after := tc.Cache.Stats()
		out.Cache = ted.CacheStats{
			Hits:   after.Hits - before.Hits,
			Misses: after.Misses - before.Misses,
			Dir:    after.Dir,
		}

		return textResult(formatScan(out)), out, nil
	}
}

// selectNotices resolves the notices to scan, either from an explicit list of
// publication numbers or by running a search.
func selectNotices(ctx context.Context, tc *ted.Client, in ScanInput, limit int, out *ScanOutput) ([]ted.Notice, error) {
	if len(in.PublicationNumbers) > 0 {
		nums := in.PublicationNumbers
		if len(nums) > limit {
			nums = nums[:limit]
		}
		notices, err := tc.GetNotices(ctx, nums, ted.SummaryFields)
		if err != nil {
			return nil, err
		}
		out.Total = len(notices)
		return notices, nil
	}

	query, err := ted.BuildQuery(ted.SearchFilters{
		Query:         in.Query,
		Keywords:      in.Keywords,
		CPV:           in.Cpv,
		Country:       in.Country,
		NoticeType:    in.NoticeType,
		PublishedFrom: in.PublishedFrom,
		PublishedTo:   in.PublishedTo,
		DeadlineFrom:  in.DeadlineFrom,
		DeadlineTo:    in.DeadlineTo,
	})
	if err != nil {
		return nil, err
	}

	scope := strings.ToUpper(strings.TrimSpace(in.Scope))
	if scope == "" {
		scope = "ALL"
	}
	page := in.Page
	if page <= 0 {
		page = 1
	}
	resp, err := tc.Search(ctx, ted.SearchRequest{
		Query:          query,
		Fields:         ted.SummaryFields,
		Page:           page,
		Limit:          limit,
		Scope:          scope,
		PaginationMode: "PAGE_NUMBER",
	})
	if err != nil {
		return nil, err
	}
	out.Query = query
	out.Total = resp.TotalNoticeCount
	out.Page = page
	return resp.Notices, nil
}

// scanNotice downloads one notice's eForms XML and searches it.
func scanNotice(ctx context.Context, tc *ted.Client, summary ted.NoticeSummary, matcher textMatcher, includeCriteria bool) (*ScanNotice, error) {
	url := summary.XMLURL
	if url == "" {
		return nil, fmt.Errorf("notice has no XML document")
	}

	_, data, err := tc.FetchDocumentRetrying(ctx, url, 16<<20, 4)
	if err != nil {
		return nil, err
	}
	content, err := ted.ParseEForms(data)
	if err != nil {
		return nil, err
	}

	out := &ScanNotice{
		Notice:            summary,
		Language:          content.Language,
		CriteriaPublished: len(content.AwardCriteria) > 0,
		Strategic:         content.Strategic,
		DocumentURLs:      content.DocumentURLs,
	}
	if includeCriteria {
		out.AwardCriteria = content.AwardCriteria
	}
	if matcher == nil {
		return out, nil
	}

	out.Matches = findMatches(content, matcher)
	out.Matched = len(out.Matches) > 0
	if out.Matched && !includeCriteria {
		// Matching criteria are already reported; showing the whole grid gives
		// the surrounding weights for free.
		out.AwardCriteria = content.AwardCriteria
	}
	return out, nil
}

// findMatches collects the places a notice's content matches, richest first:
// award criteria carry a weight, descriptions carry the procurement object,
// everything else is reported with the element it came from.
func findMatches(content *ted.NoticeContent, m textMatcher) []ScanMatch {
	var matches []ScanMatch
	seen := map[string]struct{}{}

	add := func(where, weight, text string) bool {
		// The same passage reaches us more than once — an award criterion is
		// also a Description text node, and multi-lot notices repeat both — so
		// dedupe on the opening words rather than on the exact rendering.
		key := matchKey(text)
		if _, ok := seen[key]; ok {
			return true
		}
		seen[key] = struct{}{}
		matches = append(matches, ScanMatch{Where: where, Weight: weight, Text: text})
		return len(matches) < maxMatchesPerNotice
	}

	// An award criterion is the most informative form of a finding, and its
	// text also shows up as a plain description node; report it once, here.
	var covered []string
	for _, c := range content.AwardCriteria {
		text := c.Text()
		if _, ok := m.find(c.Lang, text); !ok {
			continue
		}
		covered = append(covered, strings.ToLower(text))
		if !add("award-criterion", c.Weight, truncate(text, maxCriterionChars)) {
			return matches
		}
	}

	report := func(where, lang, text string) bool {
		hit, ok := m.find(lang, text)
		if !ok {
			return true
		}
		lower := strings.ToLower(text)
		for _, c := range covered {
			if strings.Contains(c, lower) {
				return true
			}
		}
		return add(where, "", snippet(text, []int{hit.Start, hit.End}))
	}

	for _, d := range content.Descriptions {
		if !report("description", content.Language, d) {
			return matches
		}
	}
	for _, t := range content.Texts {
		if !report(t.Element, t.Lang, t.Text) {
			return matches
		}
	}
	return matches
}

// textMatcher finds a search target inside one piece of notice text. The
// language is passed in because per-language terms are only ever tested
// against text written in the language they belong to.
type textMatcher interface {
	find(lang, text string) (found, bool)
	describe() string
	// covers reports whether the search has anything to look for in this
	// language, so a scan can say which notices it could not really search.
	covers(lang string) bool
}

type found struct {
	Term       string
	Start, End int
}

// regexMatcher applies one pattern to every notice, whatever its language.
type regexMatcher struct{ re *regexp.Regexp }

func (m regexMatcher) find(_, text string) (found, bool) {
	loc := m.re.FindStringIndex(text)
	if loc == nil {
		return found{}, false
	}
	return found{Term: text[loc[0]:loc[1]], Start: loc[0], End: loc[1]}, true
}

func (m regexMatcher) describe() string   { return m.re.String() }
func (m regexMatcher) covers(string) bool { return true }

// specMatcher applies caller-supplied terms, per language.
type specMatcher struct{ spec match.Spec }

func (m specMatcher) find(lang, text string) (found, bool) {
	hit, ok := m.spec.Find(lang, text)
	if !ok {
		return found{}, false
	}
	return found{Term: hit.Term, Start: hit.Start, End: hit.End}, true
}

func (m specMatcher) describe() string        { return m.spec.Describe() }
func (m specMatcher) covers(lang string) bool { return m.spec.Covers(lang) }

// buildMatcher chooses how to search. It returns nil when the caller asked for
// no search, in which case scanning only extracts content.
func buildMatcher(in ScanInput) (textMatcher, error) {
	// Language-aware matching wins whenever the caller supplied any, because a
	// term tested only against its own language cannot collide with another's.
	spec := match.Spec{Terms: map[string][]string{}, Near: in.Near}
	for lang, terms := range in.TermsByLanguage {
		spec.Terms[lang] = terms
	}
	if len(in.Terms) > 0 {
		spec.Terms[match.AnyLang] = append(spec.Terms[match.AnyLang], in.Terms...)
	}
	spec = spec.Normalize()
	if !spec.Empty() {
		return specMatcher{spec: spec}, nil
	}

	if r := strings.TrimSpace(in.Regex); r != "" {
		if !strings.HasPrefix(r, "(?") {
			r = "(?i)" + r
		}
		re, err := regexp.Compile(r)
		if err != nil {
			return nil, fmt.Errorf("invalid regex %q: %w", in.Regex, err)
		}
		return regexMatcher{re: re}, nil
	}

	return nil, nil
}

// matchKeyChars is how much of a passage identifies it for deduplication:
// enough to tell two findings apart, short enough that a truncated snippet and
// the full text collapse together.
const matchKeyChars = 80

func matchKey(s string) string {
	return string(clampRunes([]rune(strings.ToLower(strings.Trim(s, "… "))), matchKeyChars))
}

// snippet returns the matched text with some context on either side.
//
// Offsets are counted in characters, not bytes: notices are searched in every
// EU language, and slicing Greek or Cyrillic text on a byte boundary would cut
// a character in half and emit a replacement rune.
func snippet(text string, loc []int) string {
	runes := []rune(text)
	start := utf8.RuneCountInString(text[:loc[0]]) - scanContextChars
	end := utf8.RuneCountInString(text[:loc[1]]) + scanContextChars
	if start < 0 {
		start = 0
	}
	if end > len(runes) {
		end = len(runes)
	}

	out := strings.TrimSpace(string(runes[start:end]))
	if start > 0 {
		out = "…" + out
	}
	if end < len(runes) {
		out += "…"
	}
	return out
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return strings.TrimSpace(string(runes[:max])) + "…"
}

func clampRunes(r []rune, max int) []rune {
	if len(r) > max {
		return r[:max]
	}
	return r
}

func clamp(v, fallback, min, max int) int {
	if v <= 0 {
		return fallback
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// --- result helpers ---

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func errorResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
	}
}
