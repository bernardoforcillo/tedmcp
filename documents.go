package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/bernardoforcillo/tedmcp/internal/doctext"
	"github.com/bernardoforcillo/tedmcp/internal/ted"
	"github.com/bernardoforcillo/tedmcp/internal/webdoc"
)

// Retrieving a tender's documents is a two-step job, and the first step is the
// one that is easy to miss. A notice never links to the capitolato: it links to
// a page on the buyer's portal that lists the capitolato, the disciplinare, the
// DGUE and however many allegati. Fetching that link and stopping gives a page
// of navigation. Following it, once, gives the tender.
//
// Both tools that need documents share the code below, so "all the documents"
// means the same thing whether they are being read or searched.

const (
	defaultMaxDocuments = 25
	maxMaxDocuments     = 60
	defaultDocChars     = 20000
	defaultTotalChars   = 150000
	defaultDocWorkers   = 4
	maxDocWorkers       = 8

	// indexPageChars caps the text kept from a procedure page, as opposed to
	// from the documents it lists.
	indexPageChars = 4000

	// maxDiscoveredLinks bounds how many document links are considered on one
	// page. It is far above what a procedure page carries, and exists only so a
	// pathological page cannot produce an unbounded list.
	maxDiscoveredLinks = 500
)

// discoverRoles are the links that name one procedure and can therefore be
// followed to its documents. The buyer's profile is deliberately not among
// them: it is the portal's front door, and its links lead to every other tender
// the buyer has ever run.
var discoverRoles = []string{ted.LinkTenderDocuments, ted.LinkSubmission, "requested"}

// download is one URL tried, with whatever came back and whatever could be read
// out of it. One download can yield several files: an archive of allegati is a
// single request and a dozen documents.
type download struct {
	URL    string             `json:"url"`
	Role   string             `json:"role,omitempty"`
	Label  string             `json:"label,omitempty" jsonschema:"how the buyer's own page names this document"`
	Index  bool               `json:"index,omitempty" jsonschema:"true when this URL was the procedure page listing the documents rather than a document itself"`
	Result webdoc.Result      `json:"result"`
	Files  []doctext.Document `json:"files,omitempty" jsonschema:"the text read out of this download"`
}

type gatherOptions struct {
	Discover     bool // follow a procedure page to the files it lists
	MaxDocuments int
	MaxChars     int // per file
	Workers      int
}

func (o gatherOptions) withDefaults() gatherOptions {
	o.MaxDocuments = clamp(o.MaxDocuments, defaultMaxDocuments, 1, maxMaxDocuments)
	if o.MaxChars <= 0 {
		o.MaxChars = defaultDocChars
	}
	o.Workers = clamp(o.Workers, defaultDocWorkers, 1, maxDocWorkers)
	return o
}

// gatherDocuments retrieves every document reachable from the given links.
func gatherDocuments(ctx context.Context, wc *webdoc.Client, targets []ted.DocumentLink, opt gatherOptions) []download {
	opt = opt.withDefaults()
	if len(targets) == 0 {
		return nil
	}

	// Step one: fetch what the notice pointed at. When discovery is on, each of
	// these may turn out to be an index page rather than a document.
	first := make([]download, len(targets))
	var found [][]webdoc.Candidate
	if opt.Discover {
		found = make([][]webdoc.Candidate, len(targets))
	}

	runParallel(len(targets), opt.Workers, func(i int) {
		t := targets[i]
		d := download{URL: t.URL, Role: t.Role, Label: t.Note}

		if !opt.Discover || !hasRole(discoverRoles, t.Role) {
			d.Result = fetchOne(ctx, wc, t.URL)
			d.Files = extractFiles(d.Result, opt.MaxChars)
			first[i] = d
			return
		}

		// Everything the page lists, not just the first max_documents of them:
		// the cap is applied after ranking, and ranking a truncated list would
		// pick by page order rather than by what the files are.
		candidates, page, err := wc.Discover(ctx, t.URL, maxDiscoveredLinks)
		if err != nil {
			page = webdoc.Result{URL: t.URL, Status: webdoc.StatusError, Reason: err.Error()}
		}
		d.Result = page
		d.Index = len(candidates) > 0

		// An index page is context, not content: it says when the procedure
		// closes and what the documents are called. Given the full budget it
		// would crowd out the capitolato it points to, so it gets a corner of it.
		chars := opt.MaxChars
		if d.Index {
			chars = min(chars, indexPageChars)
		}
		d.Files = extractFiles(page, chars)
		first[i], found[i] = d, candidates
	})

	out := make([]download, 0, len(first))
	out = append(out, first...)
	if !opt.Discover {
		return out
	}

	// Step two: the files those pages list. They are ranked before the cap is
	// applied, so a limit that bites drops the ESPD form and not the capitolato.
	seen := map[string]bool{}
	for _, t := range targets {
		seen[t.URL] = true
	}
	var wanted []webdoc.Candidate
	for _, list := range found {
		for _, c := range list {
			if !seen[c.URL] {
				seen[c.URL] = true
				wanted = append(wanted, c)
			}
		}
	}
	if len(wanted) == 0 {
		return out
	}

	sort.SliceStable(wanted, func(i, j int) bool {
		return candidateRank(wanted[i]) < candidateRank(wanted[j])
	})
	skipped := 0
	if len(wanted) > opt.MaxDocuments {
		skipped = len(wanted) - opt.MaxDocuments
		wanted = wanted[:opt.MaxDocuments]
	}

	files := make([]download, len(wanted))
	runParallel(len(wanted), opt.Workers, func(i int) {
		c := wanted[i]
		res := fetchOne(ctx, wc, c.URL)
		files[i] = download{
			URL:    c.URL,
			Role:   "tender-document",
			Label:  c.Label,
			Result: res,
			Files:  extractFiles(res, opt.MaxChars),
		}
	})
	out = append(out, files...)

	// A cap that silently drops documents would read as "this is everything".
	if skipped > 0 {
		out = append(out, download{
			Role: "tender-document",
			Result: webdoc.Result{
				Status: webdoc.StatusError,
				Reason: fmt.Sprintf("%d further document(s) listed on the procedure page were not downloaded "+
					"(max_documents = %d) — raise max_documents to see them", skipped, opt.MaxDocuments),
			},
		})
	}
	return out
}

// fetchOne retrieves a document, turning a malformed URL into a result rather
// than an error: one bad link among thirty must not sink the other twenty-nine.
func fetchOne(ctx context.Context, wc *webdoc.Client, url string) webdoc.Result {
	res, err := wc.FetchRaw(ctx, url)
	if err != nil {
		return webdoc.Result{URL: url, Status: webdoc.StatusError, Reason: err.Error()}
	}
	return res
}

// extractFiles turns a retrieved body into text. Nothing is extracted from a
// refusal: there are no bytes, and inventing an empty document would blur the
// line between "could not be read" and "says nothing".
func extractFiles(res webdoc.Result, maxChars int) []doctext.Document {
	if !res.Retrieved() || len(res.Data) == 0 {
		return nil
	}
	name := res.Filename
	if name == "" {
		name = res.URL
	}
	return doctext.Extract(name, res.ContentType, res.Data, doctext.Options{MaxChars: maxChars})
}

// candidateRank orders discovered files by how likely they are to be what
// someone came for, judging by the file name and by the words the buyer's page
// uses for the link — which is often the more informative of the two, since the
// file itself may be called 4711.pdf.
func candidateRank(c webdoc.Candidate) int {
	return min(doctext.Rank(c.URL), doctext.Rank(c.Label))
}

// applyTextBudget keeps the total text returned within bounds, in the order the
// documents were ranked. Documents past the budget keep their metadata and lose
// their text, which is the honest trade: the caller can see that a document
// exists, and ask for it directly.
func applyTextBudget(downloads []download, total int) int {
	if total <= 0 {
		return 0
	}
	spent, dropped := 0, 0
	for i := range downloads {
		for j := range downloads[i].Files {
			f := &downloads[i].Files[j]
			if f.Text == "" {
				continue
			}
			runes := []rune(f.Text)
			switch {
			case spent >= total:
				f.Text, f.Truncated = "", true
				dropped++
			case spent+len(runes) > total:
				f.Text = string(runes[:total-spent])
				f.Truncated = true
				spent = total
			default:
				spent += len(runes)
			}
		}
	}
	return dropped
}

// readableFiles counts the documents that yielded text.
func readableFiles(downloads []download) int {
	n := 0
	for _, d := range downloads {
		for _, f := range d.Files {
			if f.Readable() {
				n++
			}
		}
	}
	return n
}

// retrievedCount reports how many of the URLs tried actually came back.
func retrievedCount(downloads []download) int {
	n := 0
	for _, d := range downloads {
		if d.Result.Retrieved() {
			n++
		}
	}
	return n
}

// attemptedCount counts the URLs actually requested, which is not the length of
// the slice: a notice about documents that were never downloaded rides along in
// it and would otherwise be counted as an attempt.
func attemptedCount(downloads []download) int {
	n := 0
	for _, d := range downloads {
		if d.URL != "" {
			n++
		}
	}
	return n
}

// skippedNotices returns the reasons documents were left undownloaded, which
// are carried as entries with no URL of their own.
func skippedNotices(downloads []download) []string {
	var out []string
	for _, d := range downloads {
		if d.URL == "" && d.Result.URL == "" && d.Result.Reason != "" {
			out = append(out, d.Result.Reason)
		}
	}
	return out
}

// documentTargets resolves what to download: the caller's own URLs, or the
// links a notice carries.
func documentTargets(ctx context.Context, tc *ted.Client, publicationNumber string, urls []string, allRoles bool) ([]ted.DocumentLink, []ted.DocumentLink, error) {
	if len(urls) > 0 {
		var targets []ted.DocumentLink
		for _, u := range urls {
			if u = strings.TrimSpace(u); u != "" {
				targets = append(targets, ted.DocumentLink{URL: u, Role: "requested"})
			}
		}
		return targets, nil, nil
	}

	links, err := noticeLinks(ctx, tc, publicationNumber)
	if err != nil {
		return nil, nil, err
	}
	var targets []ted.DocumentLink
	for _, l := range links {
		if allRoles || hasRole(documentRoles, l.Role) {
			targets = append(targets, l)
		}
	}
	return targets, links, nil
}

// runParallel applies fn to each index, at most workers at a time.
func runParallel(n, workers int, fn func(i int)) {
	if n == 0 {
		return
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			fn(i)
		}(i)
	}
	wg.Wait()
}
