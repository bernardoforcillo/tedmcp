package webdoc

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Outcomes of trying to retrieve a document. They are reported as-is so a
// caller can tell "the buyer does not publish this openly" from "there is
// nothing to publish" — a distinction that decides whether a human needs to go
// and look.
const (
	StatusFetched      = "fetched"       // retrieved
	StatusRobotsDenied = "robots-denied" // robots.txt forbids this path for us
	StatusCaptcha      = "captcha"       // the page is behind a human check
	StatusBlocked      = "blocked"       // refused by the server (401/403/429…)
	StatusError        = "error"         // network or protocol failure
)

// Result is the outcome of one document retrieval attempt.
type Result struct {
	URL         string `json:"url"`
	Status      string `json:"status"`
	Reason      string `json:"reason,omitempty" jsonschema:"why the document could not be retrieved"`
	ContentType string `json:"content_type,omitempty"`
	Bytes       int    `json:"bytes,omitempty"`
	Text        string `json:"text,omitempty" jsonschema:"text content, for HTML and plain-text documents"`
	Truncated   bool   `json:"truncated,omitempty"`
	Filename    string `json:"filename,omitempty"`

	// Data is the body as retrieved. It stays out of the JSON result — a
	// caller wants a capitolato's text, not eight megabytes of base64 — but it
	// is what makes reading a PDF or an archive possible at all.
	Data []byte `json:"-"`
}

// Retrieved reports whether the document was actually obtained.
func (r Result) Retrieved() bool { return r.Status == StatusFetched }

// Client fetches documents from buyer portals, subject to each site's
// robots.txt. Its zero value is not usable; use NewClient.
type Client struct {
	HTTPClient *http.Client
	UserAgent  string

	mu     sync.Mutex
	robots map[string]*robots // by scheme://host, fetched once per host
}

// NewClient returns a Client that identifies itself honestly. The user agent
// matters: it is the name a site's robots.txt rules are matched against, so it
// must not impersonate a browser or a search engine.
func NewClient(userAgent string) *Client {
	return &Client{
		HTTPClient: &http.Client{Timeout: 45 * time.Second},
		UserAgent:  userAgent,
		robots:     map[string]*robots{},
	}
}

// maxDocumentBytes caps a single download.
const maxDocumentBytes = 16 << 20

// Fetch retrieves one document if the site permits it. A refusal is a normal
// result, not an error: only a malformed request returns err.
//
// Textual bodies come back in Text, truncated to maxChars (0 for no limit).
func (c *Client) Fetch(ctx context.Context, rawURL string, maxChars int) (Result, error) {
	return c.fetch(ctx, rawURL, maxChars)
}

// FetchRaw retrieves a document without rendering its body as text, for a
// caller that will extract it themselves. A capitolato is a PDF, and copying
// its bytes into a string that nobody reads is pure waste.
func (c *Client) FetchRaw(ctx context.Context, rawURL string) (Result, error) {
	return c.fetch(ctx, rawURL, -1)
}

func (c *Client) fetch(ctx context.Context, rawURL string, maxChars int) (Result, error) {
	u, err := url.Parse(rawURL)
	if err != nil || !strings.HasPrefix(u.Scheme, "http") {
		return Result{}, fmt.Errorf("not a fetchable URL: %q", rawURL)
	}
	res := Result{URL: rawURL}

	allowed, err := c.allowed(ctx, u)
	if err != nil {
		res.Status, res.Reason = StatusError, "could not read robots.txt: "+err.Error()
		return res, nil
	}
	if !allowed {
		res.Status = StatusRobotsDenied
		res.Reason = fmt.Sprintf("%s robots.txt disallows %s for automated clients — open it in a browser", u.Host, requestPath(u))
		return res, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "*/*")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		res.Status, res.Reason = StatusError, err.Error()
		return res, nil
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDocumentBytes))
	if err != nil {
		res.Status, res.Reason = StatusError, "read body: "+err.Error()
		return res, nil
	}
	res.ContentType = resp.Header.Get("Content-Type")
	res.Bytes = len(data)
	res.Filename = filenameFrom(resp.Header.Get("Content-Disposition"), u)

	if resp.StatusCode != http.StatusOK {
		res.Status = StatusBlocked
		res.Reason = fmt.Sprintf("server answered %s", resp.Status)
		return res, nil
	}

	// A portal that answers 200 with a human check has not given us the
	// document; saying so is the difference between "no documents" and
	// "documents exist but are gated".
	if reason := detectHumanCheck(res.ContentType, data); reason != "" {
		res.Status, res.Reason = StatusCaptcha, reason
		return res, nil
	}

	res.Status = StatusFetched
	res.Data = data
	if maxChars >= 0 && isTextual(res.ContentType) {
		text := string(data)
		if maxChars > 0 && len(text) > maxChars {
			text, res.Truncated = text[:maxChars], true
		}
		res.Text = text
	}
	return res, nil
}

// allowed consults the host's robots.txt, fetching it at most once per host.
func (c *Client) allowed(ctx context.Context, u *url.URL) (bool, error) {
	host := u.Scheme + "://" + u.Host

	c.mu.Lock()
	r, cached := c.robots[host]
	c.mu.Unlock()

	if !cached {
		var err error
		r, err = c.loadRobots(ctx, host)
		if err != nil {
			return false, err
		}
		c.mu.Lock()
		c.robots[host] = r
		c.mu.Unlock()
	}

	return r.allows(c.UserAgent, requestPath(u)), nil
}

// requestPath is the path robots.txt rules are matched against: always rooted,
// and including the query string, which some rules key on.
func requestPath(u *url.URL) string {
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	return path
}

// loadRobots fetches and parses a host's robots.txt. A site that serves no
// robots.txt places no restriction, which is the standard reading.
func (c *Client) loadRobots(ctx context.Context, host string) (*robots, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, host+"/robots.txt", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	if err != nil {
		return nil, err
	}
	// 404 and other non-200 answers mean "no rules published".
	if resp.StatusCode != http.StatusOK {
		return parseRobots(""), nil
	}
	return parseRobots(string(body)), nil
}

// humanCheckMarkers are the fingerprints of the interstitials these portals
// serve instead of content.
var humanCheckMarkers = []struct {
	needle string
	reason string
}{
	{"frc-captcha", "the page is behind a friendly-captcha human check"},
	{"g-recaptcha", "the page is behind a reCAPTCHA human check"},
	{"h-captcha", "the page is behind an hCaptcha human check"},
	{"cf-challenge", "the page is behind a Cloudflare challenge"},
	{"request rejected", "the site's web application firewall rejected the request"},
	{"enable javascript and cookies to continue", "the page requires a browser to render"},
}

// detectHumanCheck reports why a 200 response is not the document, or "".
func detectHumanCheck(contentType string, data []byte) string {
	if !isTextual(contentType) {
		return ""
	}
	// The markers all appear in the head or early body of the interstitial.
	head := strings.ToLower(string(data[:min(len(data), 64<<10)]))
	for _, m := range humanCheckMarkers {
		if strings.Contains(head, m.needle) {
			return m.reason
		}
	}
	return ""
}

func isTextual(contentType string) bool {
	ct := strings.ToLower(contentType)
	return strings.HasPrefix(ct, "text/") ||
		strings.Contains(ct, "xml") ||
		strings.Contains(ct, "json") ||
		strings.Contains(ct, "html")
}

// filenameFrom picks a name for the document, preferring the one the server
// states and falling back to the last path segment.
func filenameFrom(disposition string, u *url.URL) string {
	if disposition != "" {
		if _, params, err := mime.ParseMediaType(disposition); err == nil {
			if name := params["filename"]; name != "" {
				return name
			}
		}
	}
	if i := strings.LastIndexByte(u.Path, '/'); i >= 0 && i+1 < len(u.Path) {
		return u.Path[i+1:]
	}
	return ""
}
