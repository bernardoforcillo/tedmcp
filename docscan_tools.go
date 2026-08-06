package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/bernardoforcillo/tedmcp/internal/doctext"
	"github.com/bernardoforcillo/tedmcp/internal/match"
	"github.com/bernardoforcillo/tedmcp/internal/ted"
	"github.com/bernardoforcillo/tedmcp/internal/webdoc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// scan_tender_documents finishes the sentence scan_tenders starts.
//
// scan_tenders searches what TED publishes, which is the notice. The readme has
// always had to end that search with a caveat: a requirement absent from the
// notice may still be in the capitolato, and the capitolato is not on TED. This
// tool follows that pointer — it downloads the tender's own documents and
// searches inside them, so "not found" can finally mean something.
//
// It keeps the per-language discipline that makes scan_tenders trustworthy, and
// gets the language from a better place than a guess: the eForms notice
// declares the language its procurement is conducted in, and its capitolato is
// written in that language.

const (
	defaultDocScanLimit = 5
	maxDocScanLimit     = 20
	docScanChars        = 500_000 // how deep into each document the search reads
	maxDocMatches       = 12
)

type ScanDocsInput struct {
	Terms           []string            `json:"terms,omitempty" jsonschema:"case-insensitive substrings to look for inside the tender documents. Matching is on substrings, so a stem like 'eccedenz' also matches 'eccedenze'"`
	TermsByLanguage map[string][]string `json:"terms_by_language,omitempty" jsonschema:"terms keyed by the language they belong to (ISO 639-2/T, e.g. ITA, DEU; * means any language). Each tender's documents are matched only against the terms of the language its notice declares, which is what stops one language's word stem from colliding with another's ordinary vocabulary"`
	Near            *match.Near         `json:"near,omitempty" jsonschema:"match two groups of roots appearing close together, for requirements written compositionally rather than as a set phrase. Keyed by language like terms_by_language"`
	Regex           string              `json:"regex,omitempty" jsonschema:"regular expression to match instead of terms (RE2 syntax, case-insensitive unless it sets its own flags)"`

	PublicationNumbers []string `json:"publication_numbers,omitempty" jsonschema:"tenders whose documents should be searched, e.g. [\"442511-2026\"]. Their notices supply both the document links and the language to match in"`
	URLs               []string `json:"urls,omitempty" jsonschema:"search documents at these URLs instead, when they are not reached from a notice. A procedure page works as well as a direct file link"`
	Language           string   `json:"language,omitempty" jsonschema:"language of the documents given as urls (ISO 639-2/T, e.g. ITA). Without it every term is tried against every document, which can collide across languages"`

	Limit            int   `json:"limit,omitempty" jsonschema:"how many tenders to search, 1-20 (default 5). Each one means downloading its documents from the buyer's portal, so this is deliberately small"`
	Discover         *bool `json:"discover,omitempty" jsonschema:"follow each procedure page to the files it lists (default true). With false, only the notice's own links are searched"`
	MaxDocuments     int   `json:"max_documents,omitempty" jsonschema:"how many files to download per tender, 1-60 (default 25)"`
	MaxChars         int   `json:"max_chars,omitempty" jsonschema:"how far into each document to read when searching (default 500000 characters). This bounds the search, not the output: only matching passages are returned"`
	Concurrency      int   `json:"concurrency,omitempty" jsonschema:"parallel downloads within one tender, 1-8 (default 4)"`
	IncludeUnmatched bool  `json:"include_unmatched,omitempty" jsonschema:"also return tenders where nothing matched (default false). Useful to see which documents were readable at all"`
	IncludeInventory bool  `json:"include_inventory,omitempty" jsonschema:"list every document retrieved, matching or not, with its format and size"`
}

// DocMatch is one passage found inside a tender document.
type DocMatch struct {
	Document  string `json:"document" jsonschema:"the file the passage was found in"`
	Container string `json:"container,omitempty" jsonschema:"the archive that file came in, when it was not downloaded on its own"`
	Term      string `json:"term" jsonschema:"the term or pair of roots that matched"`
	Text      string `json:"text" jsonschema:"the matching passage, with surrounding context"`
}

// RefusedURL is a document the portal would not serve to an automated client.
type RefusedURL struct {
	URL    string `json:"url"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type ScanDocsResult struct {
	PublicationNumber string             `json:"publication_number,omitempty"`
	Title             string             `json:"title,omitempty"`
	Language          string             `json:"language,omitempty" jsonschema:"the language the notice declares, which is what its documents were matched against"`
	Retrieved         int                `json:"retrieved" jsonschema:"files downloaded"`
	Readable          int                `json:"readable" jsonschema:"files whose text could be read and therefore searched"`
	Matched           bool               `json:"matched"`
	Matches           []DocMatch         `json:"matches,omitempty"`
	Documents         []doctext.Document `json:"documents,omitempty" jsonschema:"every document retrieved, without its text"`
	Unreadable        []doctext.Document `json:"unreadable,omitempty" jsonschema:"documents downloaded but not searchable — scans, encrypted files, formats not supported"`
	Refused           []RefusedURL       `json:"refused,omitempty" jsonschema:"URLs the portal declined to serve; the documents exist and need a browser"`
	Note              string             `json:"note,omitempty"`
}

type ScanDocsOutput struct {
	Pattern string           `json:"pattern,omitempty" jsonschema:"what was searched for"`
	Scanned int              `json:"scanned" jsonschema:"tenders whose documents were searched"`
	Matched int              `json:"matched" jsonschema:"tenders with at least one match"`
	Results []ScanDocsResult `json:"results,omitempty"`
	Note    string           `json:"note,omitempty"`
}

func handleScanDocuments(tc *ted.Client, wc *webdoc.Client) func(context.Context, *mcp.CallToolRequest, ScanDocsInput) (*mcp.CallToolResult, ScanDocsOutput, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ScanDocsInput) (*mcp.CallToolResult, ScanDocsOutput, error) {
		matcher, err := buildMatcher(in.Terms, in.TermsByLanguage, in.Near, in.Regex)
		if err != nil {
			return errorResult(err), ScanDocsOutput{}, nil
		}
		if len(in.PublicationNumbers) == 0 && len(in.URLs) == 0 {
			return errorResult(fmt.Errorf("give either publication_numbers or urls")), ScanDocsOutput{}, nil
		}

		opt := gatherOptions{
			Discover:     in.Discover == nil || *in.Discover,
			MaxDocuments: in.MaxDocuments,
			MaxChars:     in.MaxChars,
			Workers:      in.Concurrency,
		}
		if opt.MaxChars <= 0 {
			opt.MaxChars = docScanChars
		}

		out := ScanDocsOutput{}
		if matcher != nil {
			out.Pattern = matcher.describe()
		}

		// One tender at a time. Each one is a different buyer's portal being
		// asked for a dozen files, and stacking those requests up in parallel
		// is exactly the behaviour these sites publish rules against.
		if len(in.URLs) > 0 {
			targets, _, _ := documentTargets(ctx, tc, "", in.URLs, false)
			r := scanOneTender(ctx, wc, targets, strings.ToUpper(strings.TrimSpace(in.Language)), matcher, opt, in.IncludeInventory)
			collect(&out, r, in.IncludeUnmatched)
		}

		numbers := in.PublicationNumbers
		if limit := clamp(in.Limit, defaultDocScanLimit, 1, maxDocScanLimit); len(numbers) > limit {
			out.Note = fmt.Sprintf("%d publication numbers were given and %d searched (limit %d); "+
				"call again for the rest.", len(numbers), limit, limit)
			numbers = numbers[:limit]
		}

		for _, pn := range numbers {
			pn = strings.TrimSpace(pn)
			if pn == "" {
				continue
			}
			summary, links, language, err := noticeDocumentContext(ctx, tc, pn)
			if err != nil {
				collect(&out, ScanDocsResult{
					PublicationNumber: pn,
					Note:              "the notice could not be read: " + err.Error(),
				}, true)
				continue
			}

			var targets []ted.DocumentLink
			for _, l := range links {
				if hasRole(documentRoles, l.Role) {
					targets = append(targets, l)
				}
			}

			r := scanOneTender(ctx, wc, targets, language, matcher, opt, in.IncludeInventory)
			r.PublicationNumber = pn
			r.Title = summary.Title
			if len(targets) == 0 {
				r.Note = "This notice names no link that could hold tender documents."
			}
			collect(&out, r, in.IncludeUnmatched)
		}

		if out.Note == "" {
			out.Note = docScanNote(out, matcher)
		}
		return textResult(formatScanDocuments(out)), out, nil
	}
}

// collect adds a result to the output, keeping the counts honest whether or not
// the result is returned in full.
func collect(out *ScanDocsOutput, r ScanDocsResult, includeUnmatched bool) {
	out.Scanned++
	if r.Matched {
		out.Matched++
	}
	if r.Matched || includeUnmatched || len(r.Refused) > 0 || len(r.Unreadable) > 0 || r.Note != "" {
		out.Results = append(out.Results, r)
	}
}

// scanOneTender downloads one procurement's documents and searches them.
func scanOneTender(ctx context.Context, wc *webdoc.Client, targets []ted.DocumentLink, language string,
	matcher textMatcher, opt gatherOptions, inventory bool) ScanDocsResult {

	r := ScanDocsResult{Language: language}
	if len(targets) == 0 {
		r.Note = "There was no link to follow, so no document was searched."
		return r
	}

	downloads := gatherDocuments(ctx, wc, targets, opt)
	r.Retrieved = retrievedCount(downloads)
	r.Readable = readableFiles(downloads)

	// Documents the caps left behind were not searched either, and belong with
	// the rest of what this scan could not see.
	for _, reason := range skippedNotices(downloads) {
		r.Refused = append(r.Refused, RefusedURL{Status: "not-downloaded", Reason: reason})
	}

	seen := map[string]struct{}{}
	for _, d := range downloads {
		if !d.Result.Retrieved() && d.Result.URL != "" {
			r.Refused = append(r.Refused, RefusedURL{
				URL: d.Result.URL, Status: d.Result.Status, Reason: d.Result.Reason,
			})
		}
		for _, f := range d.Files {
			// The procedure page's own text is navigation, not a document; it
			// would match on the link labels and say nothing.
			if d.Index && f.Kind == doctext.KindHTML {
				continue
			}
			if !f.Readable() {
				if f.Status != doctext.StatusExtracted || f.Reason != "" {
					r.Unreadable = append(r.Unreadable, stripText(f))
				}
				continue
			}
			if inventory {
				r.Documents = append(r.Documents, stripText(f))
			}
			if matcher == nil || len(r.Matches) >= maxDocMatches {
				continue
			}
			if m, ok := matcher.find(language, f.Text); ok {
				text := snippet(f.Text, []int{m.Start, m.End})
				key := matchKey(text)
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = struct{}{}
				r.Matches = append(r.Matches, DocMatch{
					Document:  firstNonEmpty(f.Name, "(unnamed)"),
					Container: f.Container,
					Term:      m.Term,
					Text:      text,
				})
			}
		}
	}
	r.Matched = len(r.Matches) > 0

	if r.Retrieved == 0 {
		r.Note = "Nothing could be downloaded from the buyer's portal, so nothing was searched. " +
			"The documents exist and are public — open the URL in a browser."
	} else if r.Readable == 0 {
		r.Note = "The documents came back but none could be read, so this tender was not really searched."
	}
	return r
}

// stripText returns a document's metadata without its body, for the inventory
// and the not-searched lists where the text is not the point.
func stripText(f doctext.Document) doctext.Document {
	f.Text = ""
	return f
}

// docScanNote states the limit that matters most when reading these results.
func docScanNote(out ScanDocsOutput, matcher textMatcher) string {
	if matcher == nil {
		return "No terms were given, so nothing was searched for; the documents were only retrieved."
	}
	if out.Scanned > 0 && out.Matched == 0 {
		return "No match is not proof of absence: a document that could not be downloaded or read was not " +
			"searched, and is listed as such above. Check those before concluding the requirement is missing."
	}
	return ""
}

// noticeDocumentContext reads a notice once and returns what is needed to go
// after its documents: how to describe it, where the documents are, and which
// language they will be written in.
func noticeDocumentContext(ctx context.Context, tc *ted.Client, pn string) (ted.NoticeSummary, []ted.DocumentLink, string, error) {
	notice, err := tc.GetNotice(ctx, pn, ted.SummaryFields)
	if err != nil {
		return ted.NoticeSummary{}, nil, "", err
	}
	summary := notice.Summary()
	if summary.XMLURL == "" {
		return summary, nil, "", fmt.Errorf("notice %s has no XML document", pn)
	}

	_, data, err := tc.FetchDocumentRetrying(ctx, summary.XMLURL, 16<<20, 4)
	if err != nil {
		return summary, nil, "", err
	}
	dossier, err := ted.ParseDossier(data)
	if err != nil {
		return summary, nil, "", err
	}
	// The language is worth the second parse: it is what keeps an Italian term
	// from ever being tried against a German capitolato.
	var language string
	if content, err := ted.ParseEForms(data); err == nil {
		language = content.Language
	}
	return summary, dossier.Links, language, nil
}
