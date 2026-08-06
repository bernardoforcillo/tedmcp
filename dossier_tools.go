package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/bernardoforcillo/tedmcp/internal/anac"
	"github.com/bernardoforcillo/tedmcp/internal/ted"
	"github.com/bernardoforcillo/tedmcp/internal/webdoc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- get_tender_dossier ---

type DossierInput struct {
	PublicationNumber string `json:"publication_number" jsonschema:"TED publication number, e.g. 517698-2026"`
}

type DossierOutput struct {
	Notice   ted.NoticeSummary    `json:"notice"`
	Dossier  ted.Dossier          `json:"dossier"`
	Criteria []ted.AwardCriterion `json:"award_criteria,omitempty" jsonschema:"notice-level scoring grid; empty when the notice defers to the disciplinare di gara"`
	Green    []string             `json:"strategic_procurement,omitempty"`
	Note     string               `json:"note,omitempty"`
}

func handleDossier(tc *ted.Client) func(context.Context, *mcp.CallToolRequest, DossierInput) (*mcp.CallToolResult, DossierOutput, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DossierInput) (*mcp.CallToolResult, DossierOutput, error) {
		pn := strings.TrimSpace(in.PublicationNumber)
		if pn == "" {
			return errorResult(fmt.Errorf("publication_number is required")), DossierOutput{}, nil
		}

		notice, err := tc.GetNotice(ctx, pn, ted.SummaryFields)
		if err != nil {
			return errorResult(err), DossierOutput{}, nil
		}
		summary := notice.Summary()
		if summary.XMLURL == "" {
			return errorResult(fmt.Errorf("notice %s has no XML document", pn)), DossierOutput{}, nil
		}

		_, data, err := tc.FetchDocumentRetrying(ctx, summary.XMLURL, 16<<20, 4)
		if err != nil {
			return errorResult(err), DossierOutput{}, nil
		}

		dossier, err := ted.ParseDossier(data)
		if err != nil {
			return errorResult(err), DossierOutput{}, nil
		}
		content, err := ted.ParseEForms(data)
		if err != nil {
			return errorResult(err), DossierOutput{}, nil
		}

		out := DossierOutput{
			Notice:   summary,
			Dossier:  *dossier,
			Criteria: content.AwardCriteria,
			Green:    content.Strategic,
		}
		if len(content.AwardCriteria) == 0 {
			out.Note = "This notice publishes no scoring grid; it is in the disciplinare di gara on the buyer's platform."
		}
		return textResult(formatDossier(out)), out, nil
	}
}

// --- fetch_tender_documents ---

type FetchDocsInput struct {
	PublicationNumber string   `json:"publication_number,omitempty" jsonschema:"TED publication number whose document links should be followed"`
	URLs              []string `json:"urls,omitempty" jsonschema:"specific URLs to retrieve, instead of taking them from a notice. A procedure page works as well as a direct file link"`
	Discover          *bool    `json:"discover,omitempty" jsonschema:"follow the procedure page to the files it lists — the capitolato, the disciplinare and the allegati — instead of stopping at the page itself (default true)"`
	MaxDocuments      int      `json:"max_documents,omitempty" jsonschema:"how many files to download, 1-60 (default 25). When a page lists more, the ones whose names look like the tender's core documents are taken first and the rest are reported as skipped"`
	MaxChars          int      `json:"max_chars,omitempty" jsonschema:"characters of text kept per document (default 20000)"`
	MaxTotalChars     int      `json:"max_total_chars,omitempty" jsonschema:"characters of text kept across all documents (default 150000). Documents past the budget are still listed, with their name and size, so they can be fetched one at a time"`
	IncludeAllRoles   bool     `json:"include_all_roles,omitempty" jsonschema:"also try the buyer's website and the appeals body, which do not hold tender documents (default false)"`
	Concurrency       int      `json:"concurrency,omitempty" jsonschema:"parallel downloads, 1-8 (default 4). Buyer portals are small servers; be considerate"`
}

type FetchDocsOutput struct {
	PublicationNumber string             `json:"publication_number,omitempty"`
	Attempted         int                `json:"attempted" jsonschema:"URLs tried"`
	Retrieved         int                `json:"retrieved" jsonschema:"URLs that answered with content"`
	Readable          int                `json:"readable" jsonschema:"files whose text could actually be read"`
	Documents         []download         `json:"documents,omitempty"`
	Links             []ted.DocumentLink `json:"links,omitempty" jsonschema:"every link in the notice, labelled by role"`
	Note              string             `json:"note,omitempty"`
}

// documentRoles are the links worth trying: the others lead to institutional
// sites and courts, not to procurement files.
var documentRoles = []string{ted.LinkTenderDocuments, ted.LinkSubmission, ted.LinkBuyerProfile}

func handleFetchDocuments(tc *ted.Client, wc *webdoc.Client) func(context.Context, *mcp.CallToolRequest, FetchDocsInput) (*mcp.CallToolResult, FetchDocsOutput, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in FetchDocsInput) (*mcp.CallToolResult, FetchDocsOutput, error) {
		out := FetchDocsOutput{PublicationNumber: strings.TrimSpace(in.PublicationNumber)}
		if out.PublicationNumber == "" && len(in.URLs) == 0 {
			return errorResult(fmt.Errorf("give either publication_number or urls")), FetchDocsOutput{}, nil
		}

		targets, links, err := documentTargets(ctx, tc, out.PublicationNumber, in.URLs, in.IncludeAllRoles)
		if err != nil {
			return errorResult(err), FetchDocsOutput{}, nil
		}
		out.Links = links

		if len(targets) == 0 {
			out.Note = "The notice names no link that could hold tender documents."
			return textResult(formatFetchDocs(out)), out, nil
		}

		docs := gatherDocuments(ctx, wc, targets, gatherOptions{
			Discover:     in.Discover == nil || *in.Discover,
			MaxDocuments: in.MaxDocuments,
			MaxChars:     in.MaxChars,
			Workers:      in.Concurrency,
		})

		totalChars := in.MaxTotalChars
		if totalChars <= 0 {
			totalChars = defaultTotalChars
		}
		withheld := applyTextBudget(docs, totalChars)

		out.Documents = docs
		out.Attempted = attemptedCount(docs)
		out.Retrieved = retrievedCount(docs)
		out.Readable = readableFiles(docs)
		out.Note = fetchNote(out, withheld, totalChars)

		return textResult(formatFetchDocs(out)), out, nil
	}
}

// fetchNote says what the numbers mean, because the interesting outcomes here
// are the partial ones and none of them is self-evident from a count.
func fetchNote(out FetchDocsOutput, withheld, totalChars int) string {
	switch {
	case out.Retrieved == 0:
		return "Nothing could be retrieved automatically. This is normal for Italian procurement portals: " +
			"the documents exist and are public, but the site reserves them for human visitors. " +
			"Open the tender-documents URL in a browser."
	case out.Readable == 0:
		return "The files came back but none of them yielded text — see each file's status. A PDF reported as " +
			"no-text-layer is a scan: it is readable by a person and needs OCR, not a different tool."
	case withheld > 0:
		return fmt.Sprintf("%d document(s) were retrieved but their text is not shown, because the total budget of "+
			"%d characters was reached. They are listed with their names: fetch them individually with urls, "+
			"or raise max_total_chars.", withheld, totalChars)
	}
	return ""
}

// --- lookup_anac ---

type LookupANACInput struct {
	SnapshotPath      string   `json:"snapshot_path" jsonschema:"path to a downloaded ANAC 'cig' dataset archive (the JSON .zip from dati.anticorruzione.it)"`
	PublicationNumber string   `json:"publication_number,omitempty" jsonschema:"TED notice to join: its buyer codice fiscale and contract value are taken from the notice itself"`
	BuyerCF           []string `json:"buyer_cf,omitempty" jsonschema:"codice fiscale of the contracting authority, when not taking it from a notice"`
	Value             string   `json:"value,omitempty" jsonschema:"contract value to match exactly, which usually pins down the single gara"`
	CPVPrefixes       []string `json:"cpv_prefixes,omitempty" jsonschema:"keep only these CPV families, e.g. [\"555\",\"553\"]"`
	Limit             int      `json:"limit,omitempty" jsonschema:"maximum records to return (default 50)"`
}

type LookupANACOutput struct {
	BuyerCF []string          `json:"buyer_cf"`
	Value   string            `json:"value,omitempty"`
	Result  anac.LookupResult `json:"result"`
	Note    string            `json:"note,omitempty"`
}

func handleLookupANAC(tc *ted.Client) func(context.Context, *mcp.CallToolRequest, LookupANACInput) (*mcp.CallToolResult, LookupANACOutput, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in LookupANACInput) (*mcp.CallToolResult, LookupANACOutput, error) {
		path := strings.TrimSpace(in.SnapshotPath)
		if path == "" {
			return errorResult(fmt.Errorf(
				"snapshot_path is required: download the JSON archive of the 'cig' dataset from " +
					"https://dati.anticorruzione.it/opendata/dataset/cig and pass its path")), LookupANACOutput{}, nil
		}

		cfs := in.BuyerCF
		value := strings.TrimSpace(in.Value)

		// Taking the join keys from the notice is the reliable path: every
		// Italian eForms notice carries the buyer's codice fiscale.
		if pn := strings.TrimSpace(in.PublicationNumber); pn != "" {
			notice, err := tc.GetNotice(ctx, pn, ted.SummaryFields)
			if err != nil {
				return errorResult(err), LookupANACOutput{}, nil
			}
			summary := notice.Summary()
			_, data, err := tc.FetchDocumentRetrying(ctx, summary.XMLURL, 16<<20, 4)
			if err != nil {
				return errorResult(err), LookupANACOutput{}, nil
			}
			dossier, err := ted.ParseDossier(data)
			if err != nil {
				return errorResult(err), LookupANACOutput{}, nil
			}
			if dossier.Buyer != nil && dossier.Buyer.CompanyID != "" {
				cfs = append(cfs, dossier.Buyer.CompanyID)
			}
			if value == "" {
				value = summary.TotalValue
			}
		}

		if len(cfs) == 0 {
			return errorResult(fmt.Errorf("no buyer codice fiscale: pass buyer_cf, or a publication_number whose notice carries one")), LookupANACOutput{}, nil
		}

		snapshot, err := anac.Open(path)
		if err != nil {
			return errorResult(err), LookupANACOutput{}, nil
		}
		res, err := snapshot.Lookup(anac.Query{
			BuyerCF:     cfs,
			CPVPrefixes: in.CPVPrefixes,
			Value:       value,
			Limit:       in.Limit,
		})
		if err != nil {
			return errorResult(err), LookupANACOutput{}, nil
		}

		out := LookupANACOutput{BuyerCF: cfs, Value: value, Result: *res}
		switch {
		case len(res.Records) == 0 && res.Candidate > 0:
			out.Note = "The buyer appears in the snapshot but no record matches the value or CPV filter. " +
				"The amounts TED and ANAC publish can differ when options or renewals are counted differently — retry without value."
		case len(res.Records) == 0:
			out.Note = "This buyer is absent from the snapshot. Monthly archives are incremental, so a gara published after the " +
				"snapshot date will not be there yet; try a more recent one."
		}
		return textResult(formatLookupANAC(out)), out, nil
	}
}

func hasRole(roles []string, role string) bool {
	for _, r := range roles {
		if r == role {
			return true
		}
	}
	return false
}

// noticeLinks fetches a notice's XML and returns its classified links.
func noticeLinks(ctx context.Context, tc *ted.Client, pn string) ([]ted.DocumentLink, error) {
	notice, err := tc.GetNotice(ctx, pn, ted.SummaryFields)
	if err != nil {
		return nil, err
	}
	summary := notice.Summary()
	if summary.XMLURL == "" {
		return nil, fmt.Errorf("notice %s has no XML document", pn)
	}
	_, data, err := tc.FetchDocumentRetrying(ctx, summary.XMLURL, 16<<20, 4)
	if err != nil {
		return nil, err
	}
	dossier, err := ted.ParseDossier(data)
	if err != nil {
		return nil, err
	}
	return dossier.Links, nil
}
