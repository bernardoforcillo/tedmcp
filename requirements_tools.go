package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/bernardoforcillo/tedmcp/internal/doctext"
	"github.com/bernardoforcillo/tedmcp/internal/requirements"
	"github.com/bernardoforcillo/tedmcp/internal/ted"
	"github.com/bernardoforcillo/tedmcp/internal/webdoc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// extract_requirements is the go/no-go sheet. It reads a tender's documents
// once and answers the questions every qualification meeting asks — turnover,
// references, certifications, guarantees, site visit, deadlines — each with
// the value stated, the passage it came from and the file. It is built on the
// same retrieval as fetch_tender_documents, so it is honest in the same way:
// a check that found nothing is "not found in the documents read", and the
// documents that were not read are listed beside it.

func registerRequirementTools(s *mcp.Server, tc *ted.Client, wc *webdoc.Client) {
	ids := requirements.BuiltinIDs()
	names := make([]string, 0, len(ids))
	for id := range ids {
		names = append(names, id)
	}
	sort.Strings(names)

	mcp.AddTool(s, &mcp.Tool{
		Name: "extract_requirements",
		Description: "Build a go/no-go qualification sheet for one tender: downloads its documents from the buyer's portal (as " +
			"fetch_tender_documents does) and extracts, for each requirement, the value stated, the passage it came from and the " +
			"file — minimum turnover, similar contracts, certifications (ISO codes), SOA categories, bid and performance " +
			"guarantees, compulsory site visit, deadline for questions, opening session, subcontracting and avvalimento rules, " +
			"lot limits, base value, duration and renewals, staff-transfer clause, green criteria (CAM), penalties and the ANAC fee. " +
			"Also reports the facts the notice itself states (deadline with time of day, clarification deadline, renewal options). " +
			"Checks are written for Italian, English, French, German and Spanish documents. Add team-specific checks with " +
			"custom_checks. A requirement marked not found was not found in the documents that could be read — always report the " +
			"not-read and refused lists alongside. Built-in check ids: " + strings.Join(names, ", ") + ".",
	}, handleExtractRequirements(tc, wc))
}

type CustomCheckInput struct {
	ID              string              `json:"id" jsonschema:"short identifier, e.g. food_waste"`
	Label           string              `json:"label,omitempty"`
	TermsByLanguage map[string][]string `json:"terms_by_language,omitempty" jsonschema:"literal phrases keyed by language (ISO 639-2/T, * for any)"`
	Regex           string              `json:"regex,omitempty" jsonschema:"RE2 pattern, applied to every document"`
	Extract         []string            `json:"extract,omitempty" jsonschema:"what to pull from each passage: amount, percent, date, duration, codes"`
}

type RequirementsInput struct {
	PublicationNumber string             `json:"publication_number,omitempty" jsonschema:"TED notice whose tender documents should be read"`
	URLs              []string           `json:"urls,omitempty" jsonschema:"read documents at these URLs instead (a procedure page or direct file links)"`
	Language          string             `json:"language,omitempty" jsonschema:"language of documents given as urls (ISO 639-2/T, e.g. ITA); taken from the notice otherwise"`
	Checks            []string           `json:"checks,omitempty" jsonschema:"run only these built-in checks (default: all)"`
	CustomChecks      []CustomCheckInput `json:"custom_checks,omitempty" jsonschema:"additional checks for this call"`
	PerCheck          int                `json:"per_check,omitempty" jsonschema:"passages kept per check, 1-6 (default 3)"`
	MaxDocuments      int                `json:"max_documents,omitempty" jsonschema:"files to download, 1-60 (default 25)"`
	Concurrency       int                `json:"concurrency,omitempty" jsonschema:"parallel downloads, 1-8 (default 4)"`
}

// NoticeFacts are what the TED notice itself says, which needs no document.
type NoticeFacts struct {
	Title             string   `json:"title,omitempty"`
	Buyer             string   `json:"buyer,omitempty"`
	ProcedureType     string   `json:"procedure_type,omitempty"`
	Deadline          string   `json:"deadline,omitempty"`
	QuestionsDeadline string   `json:"questions_deadline,omitempty"`
	Opening           string   `json:"opening,omitempty"`
	EstimatedValue    string   `json:"estimated_value,omitempty"`
	Duration          []string `json:"duration,omitempty" jsonschema:"per lot, with where it was read from"`
	Renewals          []string `json:"renewals,omitempty"`
	DocumentsURL      string   `json:"documents_url,omitempty"`
}

type RequirementsOutput struct {
	PublicationNumber string                `json:"publication_number,omitempty"`
	Language          string                `json:"language,omitempty"`
	Notice            *NoticeFacts          `json:"notice,omitempty"`
	Found             int                   `json:"found" jsonschema:"checks with at least one passage"`
	Checked           int                   `json:"checked"`
	Results           []requirements.Result `json:"results"`
	DocumentsRead     []string              `json:"documents_read,omitempty"`
	Unreadable        []doctext.Document    `json:"unreadable,omitempty" jsonschema:"retrieved but not searchable — scans, encrypted files"`
	Refused           []RefusedURL          `json:"refused,omitempty" jsonschema:"not served to an automated client; open them in a browser"`
	Note              string                `json:"note,omitempty"`
}

func handleExtractRequirements(tc *ted.Client, wc *webdoc.Client) func(context.Context, *mcp.CallToolRequest, RequirementsInput) (*mcp.CallToolResult, RequirementsOutput, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in RequirementsInput) (*mcp.CallToolResult, RequirementsOutput, error) {
		pn := strings.TrimSpace(in.PublicationNumber)
		if pn == "" && len(in.URLs) == 0 {
			return errorResult(fmt.Errorf("give either publication_number or urls")), RequirementsOutput{}, nil
		}
		checks, err := buildChecks(in.Checks, in.CustomChecks)
		if err != nil {
			return errorResult(err), RequirementsOutput{}, nil
		}

		out := RequirementsOutput{PublicationNumber: pn, Language: strings.ToUpper(strings.TrimSpace(in.Language))}
		var targets []ted.DocumentLink
		if len(in.URLs) > 0 {
			targets, _, _ = documentTargets(ctx, tc, "", in.URLs, false)
		} else {
			r := readNotice(ctx, tc, pn)
			if r.Err != nil {
				return errorResult(r.Err), RequirementsOutput{}, nil
			}
			out.Notice = noticeFacts(r)
			if out.Language == "" {
				out.Language = r.Procedure.Language
			}
			if r.Dossier != nil {
				for _, l := range r.Dossier.Links {
					if hasRole(documentRoles, l.Role) {
						targets = append(targets, l)
					}
				}
			}
		}

		if len(targets) == 0 {
			out.Results = requirements.Run(checks, out.Language, nil, 1)
			out.Checked = len(out.Results)
			out.Note = "The notice names no link to the tender documents, so only the notice's own facts are reported."
			return textResult(formatRequirements(out)), out, nil
		}

		downloads := gatherDocuments(ctx, wc, targets, gatherOptions{
			Discover:     true,
			MaxDocuments: in.MaxDocuments,
			MaxChars:     docScanChars,
			Workers:      in.Concurrency,
		})

		var docs []requirements.Document
		ranks := map[string]int{}
		for _, reason := range skippedNotices(downloads) {
			out.Refused = append(out.Refused, RefusedURL{Status: "not-downloaded", Reason: reason})
		}
		for _, d := range downloads {
			if !d.Result.Retrieved() && d.Result.URL != "" {
				out.Refused = append(out.Refused, RefusedURL{URL: d.Result.URL, Status: d.Result.Status, Reason: d.Result.Reason})
			}
			for _, f := range d.Files {
				if d.Index && f.Kind == doctext.KindHTML {
					continue // the procedure page: navigation, not requirements
				}
				if !f.Readable() {
					if f.Status != doctext.StatusExtracted || f.Reason != "" {
						out.Unreadable = append(out.Unreadable, stripText(f))
					}
					continue
				}
				name := firstNonEmpty(f.Name, d.Label, d.URL)
				docs = append(docs, requirements.Document{Name: name, Container: f.Container, Text: f.Text})
				ranks[name] = min(doctext.Rank(name), doctext.Rank(d.Label))
				out.DocumentsRead = append(out.DocumentsRead, name)
			}
		}
		// The disciplinare states the requirements; the allegati repeat
		// fragments of them. Reading the core documents first means the
		// passages kept are the authoritative ones.
		sort.SliceStable(docs, func(i, j int) bool { return ranks[docs[i].Name] < ranks[docs[j].Name] })

		perCheck := clamp(in.PerCheck, 3, 1, 6)
		out.Results = requirements.Run(checks, out.Language, docs, perCheck)
		out.Checked = len(out.Results)
		for _, r := range out.Results {
			if r.Found {
				out.Found++
			}
		}
		out.Note = requirementsNote(out, checks)
		return textResult(formatRequirements(out)), out, nil
	}
}

func buildChecks(ids []string, custom []CustomCheckInput) ([]requirements.Check, error) {
	checks := requirements.Builtin(ids...)
	if len(ids) > 0 && len(checks) < len(ids) {
		known := requirements.BuiltinIDs()
		var unknown []string
		for _, id := range ids {
			if _, ok := known[strings.TrimSpace(id)]; !ok {
				unknown = append(unknown, id)
			}
		}
		return nil, fmt.Errorf("unknown check id(s): %s", strings.Join(unknown, ", "))
	}
	for _, c := range custom {
		check, err := requirements.CustomCheck(c.ID, c.Label, c.TermsByLanguage, c.Regex, c.Extract)
		if err != nil {
			return nil, err
		}
		checks = append(checks, check)
	}
	if len(checks) == 0 {
		return nil, fmt.Errorf("no checks to run")
	}
	return checks, nil
}

func noticeFacts(r procNotice) *NoticeFacts {
	f := &NoticeFacts{Title: r.Summary.Title, Buyer: r.Summary.BuyerName, EstimatedValue: r.Summary.TotalValue}
	if d := r.Dossier; d != nil {
		f.ProcedureType = d.ProcedureType
		f.Deadline = joinNonEmpty(" ", d.DeadlineDate, d.DeadlineTime)
		f.QuestionsDeadline = joinNonEmpty(" ", d.QuestionsDate, d.QuestionsTime)
		f.Opening = joinNonEmpty(" ", d.OpeningDate, d.OpeningTime)
		f.DocumentsURL = d.DocumentsURL()
	}
	for _, l := range r.Procedure.Lots {
		label := firstNonEmpty(l.ID, "lot")
		if s := describeDuration(l.Duration); s != "" {
			f.Duration = append(f.Duration, label+": "+s)
		}
		if l.RenewalDescription != "" {
			f.Renewals = append(f.Renewals, label+": "+l.RenewalDescription)
		}
	}
	return f
}

func describeDuration(d ted.Duration) string {
	var parts []string
	if d.Start != "" || d.End != "" {
		parts = append(parts, joinNonEmpty(" → ", d.Start, d.End))
	}
	if d.Months > 0 {
		parts = append(parts, fmt.Sprintf("%.0f months", d.Months))
	}
	if d.Text != "" && d.Source == ted.DurationFromText && d.Months > 0 {
		parts = append(parts, fmt.Sprintf("%q", d.Text))
	}
	if len(parts) == 0 {
		return ""
	}
	s := strings.Join(parts, ", ")
	if d.Source != "" {
		s += " (from " + d.Source + ")"
	}
	return s
}

func requirementsNote(out RequirementsOutput, checks []requirements.Check) string {
	var notes []string
	switch {
	case len(out.DocumentsRead) == 0:
		notes = append(notes, "No document could be read, so every check below is unanswered rather than absent. "+
			"Open the tender-documents URL in a browser.")
	case len(out.Refused) > 0 || len(out.Unreadable) > 0:
		notes = append(notes, "Some documents were not read (listed below): a requirement marked not found may be in them.")
	}
	if out.Language != "" && !requirements.Covers(checks, out.Language) {
		notes = append(notes, fmt.Sprintf("The built-in checks have no %s phrasing (they cover %s); only language-neutral patterns "+
			"such as ISO codes were tried. Add custom_checks in %s.", out.Language,
			strings.Join(requirements.Languages(checks), ", "), out.Language))
	}
	return strings.Join(notes, " ")
}

func formatRequirements(out RequirementsOutput) string {
	var b strings.Builder
	title := out.PublicationNumber
	if out.Notice != nil && out.Notice.Title != "" {
		title = joinNonEmpty(" — ", out.PublicationNumber, out.Notice.Title)
	}
	fmt.Fprintf(&b, "Qualification sheet: %s\n", firstNonEmpty(title, "(documents by URL)"))
	fmt.Fprintf(&b, "%d of %d checks answered from %d document(s) read", out.Found, out.Checked, len(out.DocumentsRead))
	if out.Language != "" {
		fmt.Fprintf(&b, " (language %s)", out.Language)
	}
	b.WriteString(".\n")

	if n := out.Notice; n != nil {
		b.WriteString("\nFrom the notice:\n")
		writeLine(&b, "  buyer", n.Buyer)
		writeLine(&b, "  procedure", n.ProcedureType)
		writeLine(&b, "  submission deadline", n.Deadline)
		writeLine(&b, "  questions deadline", n.QuestionsDeadline)
		writeLine(&b, "  opening", n.Opening)
		writeLine(&b, "  estimated value", n.EstimatedValue)
		for _, d := range n.Duration {
			writeLine(&b, "  duration", d)
		}
		for _, r := range n.Renewals {
			writeLine(&b, "  renewal", r)
		}
		writeLine(&b, "  documents", n.DocumentsURL)
	}

	b.WriteString("\nFrom the documents:\n")
	for _, r := range out.Results {
		if !r.Found {
			continue
		}
		fmt.Fprintf(&b, "\n■ %s", r.Label)
		if len(r.Values) > 0 {
			fmt.Fprintf(&b, ": %s", strings.Join(r.Values, " | "))
		}
		if len(r.Flags) > 0 {
			fmt.Fprintf(&b, "  [%s]", strings.Join(r.Flags, ", "))
		}
		for _, f := range r.Findings {
			fmt.Fprintf(&b, "\n    %s: %s", f.Document, f.Text)
		}
		if r.Hits > len(r.Findings) {
			fmt.Fprintf(&b, "\n    (%d passage(s) in all)", r.Hits)
		}
	}

	var missing []string
	for _, r := range out.Results {
		if !r.Found {
			missing = append(missing, r.Label)
		}
	}
	if len(missing) > 0 {
		b.WriteString("\n\nNot found in the documents read:\n")
		for _, m := range missing {
			fmt.Fprintf(&b, "  - %s\n", m)
		}
	}
	if len(out.Unreadable) > 0 {
		b.WriteString("\nNOT SEARCHED (retrieved, unreadable):\n")
		for _, d := range out.Unreadable {
			fmt.Fprintf(&b, "  - %s [%s] %s\n", firstNonEmpty(d.Name, "(unnamed)"), d.Status, d.Reason)
		}
	}
	if len(out.Refused) > 0 {
		b.WriteString("\nNOT RETRIEVED:\n")
		for _, r := range out.Refused {
			fmt.Fprintf(&b, "  - %s [%s] %s\n", firstNonEmpty(r.URL, "(further documents)"), r.Status, r.Reason)
		}
	}
	if out.Note != "" {
		fmt.Fprintf(&b, "\n%s", out.Note)
	}
	return b.String()
}
