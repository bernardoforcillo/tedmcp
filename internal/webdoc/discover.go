package webdoc

import (
	"context"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/bernardoforcillo/tedmcp/internal/doctext"
)

// Candidate is a file linked from a procedure page.
type Candidate struct {
	URL   string `json:"url"`
	Label string `json:"label,omitempty" jsonschema:"the link's own words, which is how the buyer names the document"`
}

// Discover lists the documents a procedure page links to.
//
// A notice never points at the capitolato. It points at a page on the buyer's
// portal that lists the capitolato, the disciplinare, the DGUE and a dozen
// allegati, each behind its own link. Retrieving the page and stopping there
// gives navigation furniture; following its links gives the tender.
//
// It follows exactly one hop, and only within the site it started on. Walking
// further would turn a document fetch into a crawl of the portal, which is the
// thing these sites' robots.txt files exist to refuse — and this package's
// whole position is that those refusals are binding.
func (c *Client) Discover(ctx context.Context, pageURL string, limit int) ([]Candidate, Result, error) {
	page, err := c.FetchRaw(ctx, pageURL)
	if err != nil {
		return nil, Result{}, err
	}
	if !page.Retrieved() {
		return nil, page, nil
	}
	// The link sometimes goes straight to the file — a notice whose
	// "tender documents" URL is the capitolato itself. There is nothing to
	// discover; the caller already holds the document.
	if !isHTML(page.ContentType, page.Data) {
		return nil, page, nil
	}

	base, err := url.Parse(pageURL)
	if err != nil {
		return nil, page, nil
	}

	seen := map[string]bool{pageURL: true}
	var out []Candidate

	for _, a := range anchors(string(page.Data)) {
		target, ok := resolve(base, a.href)
		if !ok || seen[target] || !looksLikeDocument(target) {
			continue
		}
		seen[target] = true
		out = append(out, Candidate{URL: target, Label: linkLabel(a.text)})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, page, nil
}

// anchorPattern finds links and the words they are written with. A tolerant
// pattern is the right tool here: portal pages are generated HTML of varying
// quality, and the alternative — a full parser — buys correctness on markup
// that no browser would render differently either way.
var anchorPattern = regexp.MustCompile(`(?is)<a\b[^>]*?href\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>"']+))[^>]*>(.*?)</a\s*>`)

type anchor struct{ href, text string }

func anchors(html string) []anchor {
	matches := anchorPattern.FindAllStringSubmatch(html, -1)
	out := make([]anchor, 0, len(matches))
	for _, m := range matches {
		href := firstNonEmpty(m[1], m[2], m[3])
		if href != "" {
			out = append(out, anchor{href: href, text: m[4]})
		}
	}
	return out
}

// resolve turns a link into an absolute URL, and reports whether it is one
// worth following: same site, over HTTP, not a mail or script link.
func resolve(base *url.URL, href string) (string, bool) {
	href = strings.TrimSpace(href)
	if href == "" || strings.HasPrefix(href, "#") {
		return "", false
	}
	switch {
	case strings.HasPrefix(strings.ToLower(href), "javascript:"),
		strings.HasPrefix(strings.ToLower(href), "mailto:"),
		strings.HasPrefix(strings.ToLower(href), "tel:"),
		strings.HasPrefix(strings.ToLower(href), "data:"):
		return "", false
	}

	u, err := base.Parse(href)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	// Staying on the site keeps a document fetch from becoming a walk of the
	// whole web: a procedure page links to the ministry, the CIG register and
	// the software vendor, none of which hold this tender's documents.
	if !sameSite(base.Host, u.Host) {
		return "", false
	}
	u.Fragment = ""
	return u.String(), true
}

// sameSite compares hosts down to the registrable part, so a portal that serves
// its files from files.comune.example beside www.comune.example still counts as
// one site.
func sameSite(a, b string) bool {
	a, b = strings.ToLower(hostOnly(a)), strings.ToLower(hostOnly(b))
	if a == b {
		return true
	}
	return lastLabels(a, 3) == lastLabels(b, 3)
}

func hostOnly(host string) string {
	if i := strings.LastIndexByte(host, ':'); i > strings.LastIndexByte(host, ']') {
		return host[:i]
	}
	return host
}

func lastLabels(host string, n int) string {
	parts := strings.Split(host, ".")
	if len(parts) <= n {
		return host
	}
	return strings.Join(parts[len(parts)-n:], ".")
}

// documentExtensions are the files a tender is published as.
var documentExtensions = map[string]bool{
	".pdf": true, ".p7m": true, ".zip": true, ".7z": true, ".rar": true,
	".doc": true, ".docx": true, ".rtf": true, ".odt": true,
	".xls": true, ".xlsx": true, ".ods": true, ".csv": true,
	".ppt": true, ".pptx": true, ".odp": true,
	".txt": true, ".xml": true,
}

// downloadHints are the shapes a portal's download link takes when it carries
// no extension at all, which is the common case: the file is served by a script
// from a database, and its name lives in a query parameter or nowhere.
var downloadHints = []string{
	"download", "scarica", "allegat", "documenti", "documento", "getfile",
	"getdocument", "downloadfile", "attachment", "/file/", "idallegato", "iddoc",
}

// looksLikeDocument reports whether a URL is worth trying as a file.
func looksLikeDocument(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if documentExtensions[strings.ToLower(path.Ext(u.Path))] {
		return true
	}

	probe := strings.ToLower(u.Path + "?" + u.RawQuery)
	for _, hint := range downloadHints {
		if strings.Contains(probe, hint) {
			return true
		}
	}
	return false
}

// linkLabel reduces a link's markup to the words a person reads, which is how
// the buyer names the document: "Disciplinare di gara", "Allegato 3 - DGUE".
func linkLabel(inner string) string {
	label := strings.Join(strings.Fields(strings.ReplaceAll(doctext.HTMLToText([]byte(inner)), "\n", " ")), " ")
	const maxLabel = 160
	if runes := []rune(label); len(runes) > maxLabel {
		return strings.TrimSpace(string(runes[:maxLabel])) + "…"
	}
	return label
}

func isHTML(contentType string, data []byte) bool {
	if strings.Contains(strings.ToLower(contentType), "html") {
		return true
	}
	head := strings.ToLower(string(data[:min(len(data), 1024)]))
	return strings.Contains(head, "<html") || strings.Contains(head, "<!doctype html") ||
		strings.Contains(head, "<a href")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
