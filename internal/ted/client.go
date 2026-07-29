// Package ted is a small client for the official TED (Tenders Electronic Daily)
// search API — the European Union's public procurement journal.
//
// It talks to the public v3 REST API at https://api.ted.europa.eu, which
// requires no API key for read-only search. The main entry point is the
// /v3/notices/search endpoint, driven by TED's "expert" query language.
package ted

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the public TED API root.
const DefaultBaseURL = "https://api.ted.europa.eu"

// DocumentInterval is the minimum spacing between document downloads. TED
// serves notice documents from ted.europa.eu, which rate-limits bursts, so
// downloads are paced client-side rather than left to collide and fail.
const DocumentInterval = 250 * time.Millisecond

// Client is a TED search API client. The zero value is not usable; use NewClient.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
	UserAgent  string

	// Cache holds notice documents between runs. A nil cache disables caching.
	Cache *Cache

	// docPace serialises the start of retrying document downloads across all
	// goroutines sharing this client. A nil value disables pacing.
	docPace *pacer
}

// NewClient returns a Client with sensible defaults.
func NewClient() *Client {
	return &Client{
		BaseURL:    DefaultBaseURL,
		HTTPClient: &http.Client{Timeout: 45 * time.Second},
		UserAgent:  "tedmcp/0.1 (+https://github.com/bernardoforcillo/tedmcp)",
		docPace:    &pacer{gap: DocumentInterval},
	}
}

// pacer hands out request slots no closer together than gap.
type pacer struct {
	mu   sync.Mutex
	next time.Time
	gap  time.Duration
}

// reserve claims the next slot and reports how long the caller must wait for it.
func (p *pacer) reserve() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	if p.next.Before(now) {
		p.next = now
	}
	at := p.next
	p.next = at.Add(p.gap)
	return time.Until(at)
}

// hold pushes every pending slot back, so one throttled request slows all of them.
func (p *pacer) hold(d time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if until := time.Now().Add(d); until.After(p.next) {
		p.next = until
	}
}

// pace blocks until this client's next download slot comes up.
func (c *Client) pace(ctx context.Context) error {
	if c.docPace == nil {
		return nil
	}
	wait := c.docPace.reserve()
	if wait <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait):
		return nil
	}
}

// delay backs off every pending download on this client.
func (c *Client) delay(d time.Duration) {
	if c.docPace != nil {
		c.docPace.hold(d)
	}
}

// Notice is a single TED notice. Field values are heterogeneous: a plain
// string, an array of strings, or a multilingual map (language code -> value),
// so it is kept as a generic map and read through the helpers in notice.go.
type Notice map[string]any

// SearchRequest is the JSON body for POST /v3/notices/search.
type SearchRequest struct {
	Query              string   `json:"query"`
	Fields             []string `json:"fields,omitempty"`
	Page               int      `json:"page,omitempty"`
	Limit              int      `json:"limit,omitempty"`
	Scope              string   `json:"scope,omitempty"`
	PaginationMode     string   `json:"paginationMode,omitempty"`
	IterationNextToken string   `json:"iterationNextToken,omitempty"`
	CheckQuerySyntax   bool     `json:"checkQuerySyntax,omitempty"`
	OnlyLatestVersions bool     `json:"onlyLatestVersions,omitempty"`
}

// SearchResponse is the JSON returned by POST /v3/notices/search.
type SearchResponse struct {
	Notices            []Notice `json:"notices"`
	TotalNoticeCount   int      `json:"totalNoticeCount"`
	IterationNextToken string   `json:"iterationNextToken"`
	TimedOut           bool     `json:"timedOut"`
}

// apiError models the two error shapes TED returns: a plain {"message": "..."}
// and a validation error whose "error" is an array of field problems.
type apiError struct {
	Message string          `json:"message"`
	Error   json.RawMessage `json:"error"`
}

// Search runs an expert-query search against TED.
func (c *Client) Search(ctx context.Context, req SearchRequest) (*SearchResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v3/notices/search", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if c.UserAgent != "" {
		httpReq.Header.Set("User-Agent", c.UserAgent)
	}

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call TED search: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20)) // 32 MiB safety cap
	if err != nil {
		return nil, fmt.Errorf("read TED response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("TED API returned %s: %s", resp.Status, apiErrorText(data))
	}

	var out SearchResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode TED response: %w", err)
	}
	return &out, nil
}

// GetNotice fetches a single notice by its publication number (e.g. "521055-2026").
func (c *Client) GetNotice(ctx context.Context, publicationNumber string, fields []string) (Notice, error) {
	resp, err := c.Search(ctx, SearchRequest{
		Query:          "publication-number=" + publicationNumber,
		Fields:         fields,
		Limit:          1,
		Page:           1,
		Scope:          "ALL",
		PaginationMode: "PAGE_NUMBER",
	})
	if err != nil {
		return nil, err
	}
	if len(resp.Notices) == 0 {
		return nil, fmt.Errorf("no notice found with publication number %q", publicationNumber)
	}
	return resp.Notices[0], nil
}

// MaxSearchLimit is the largest page size the TED search API accepts.
const MaxSearchLimit = 250

// GetNotices fetches many notices in a single search call. Notices TED does not
// know about are simply absent from the result, so the caller should match on
// publication number rather than on position.
func (c *Client) GetNotices(ctx context.Context, publicationNumbers []string, fields []string) ([]Notice, error) {
	nums := nonEmpty(upperAll(publicationNumbers))
	if len(nums) == 0 {
		return nil, fmt.Errorf("no publication numbers given")
	}
	if len(nums) > MaxSearchLimit {
		return nil, fmt.Errorf("too many publication numbers (%d): TED returns at most %d notices per call", len(nums), MaxSearchLimit)
	}

	resp, err := c.Search(ctx, SearchRequest{
		Query:          inClause("publication-number", nums),
		Fields:         fields,
		Limit:          len(nums),
		Page:           1,
		Scope:          "ALL",
		PaginationMode: "PAGE_NUMBER",
	})
	if err != nil {
		return nil, err
	}
	return resp.Notices, nil
}

// FetchDocument downloads a notice document (XML/PDF/HTML) from a TED URL,
// reading at most maxBytes. It returns the content type and raw bytes.
func (c *Client) FetchDocument(ctx context.Context, url string, maxBytes int64) (contentType string, data []byte, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", nil, err
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("fetch document: %w", err)
	}
	defer resp.Body.Close()

	if maxBytes <= 0 {
		maxBytes = 16 << 20
	}
	data, err = io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return "", nil, fmt.Errorf("read document: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", nil, &StatusError{
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			URL:        url,
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		}
	}
	return resp.Header.Get("Content-Type"), data, nil
}

// StatusError reports a non-200 response from a document URL.
type StatusError struct {
	StatusCode int
	Status     string
	URL        string
	RetryAfter time.Duration // from the Retry-After header, when the server sends one
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("document URL returned %s", e.Status)
}

// Retryable reports whether the request is worth repeating: TED answers 429
// when documents are pulled too quickly, which clears on its own.
func (e *StatusError) Retryable() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500
}

const (
	// documentRetryBackoff is the wait before the first retry; it doubles each time.
	documentRetryBackoff = time.Second
	// documentRetryAttempts is how many times a throttled download is tried.
	documentRetryAttempts = 5
)

// FetchDocumentRetrying downloads a document, retrying rate-limited and
// server-side failures with exponential backoff. Use it when fetching many
// documents at once: ted.europa.eu throttles concurrent downloads hard, and a
// dropped document is a silently missing result rather than a visible error.
func (c *Client) FetchDocumentRetrying(ctx context.Context, url string, maxBytes int64, attempts int) (contentType string, data []byte, err error) {
	key, cacheable := "", false
	if c.Cache != nil {
		key, cacheable = CacheKey(url)
		if cacheable {
			if data, ok := c.Cache.Get(key); ok {
				return "application/xml", data, nil
			}
		}
	}

	if attempts <= 0 {
		attempts = documentRetryAttempts
	}
	backoff := documentRetryBackoff

	for i := 0; ; i++ {
		if err := c.pace(ctx); err != nil {
			return "", nil, err
		}

		contentType, data, err = c.FetchDocument(ctx, url, maxBytes)
		if err == nil {
			if cacheable {
				// A cache that cannot be written is not a reason to fail the
				// request; the document is already in hand.
				_ = c.Cache.Put(key, data)
			}
			return contentType, data, nil
		}
		if ctx.Err() != nil {
			return "", nil, ctx.Err()
		}

		// A definitive HTTP answer other than throttling will not change.
		var se *StatusError
		isStatus := errors.As(err, &se)
		if isStatus && !se.Retryable() {
			return "", nil, err
		}
		if i >= attempts-1 {
			return "", nil, err
		}

		wait := backoff + jitter(backoff)
		if isStatus && se.RetryAfter > 0 {
			wait = se.RetryAfter
		}
		// Being throttled is a property of the whole client, not of this one
		// request, so hold every other in-flight download back too.
		c.delay(wait)

		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		case <-time.After(wait):
		}
		backoff *= 2
	}
}

// jitter spreads retries so parallel workers do not all come back at once.
func jitter(d time.Duration) time.Duration {
	return time.Duration(rand.Int64N(int64(d) / 2))
}

// parseRetryAfter reads the Retry-After header in its delay-seconds form.
func parseRetryAfter(h string) time.Duration {
	secs, err := strconv.Atoi(strings.TrimSpace(h))
	if err != nil || secs <= 0 {
		return 0
	}
	if secs > 60 {
		secs = 60 // do not stall a tool call on an absurd value
	}
	return time.Duration(secs) * time.Second
}

// apiErrorText renders a TED error body into a readable one-line message.
func apiErrorText(data []byte) string {
	var e apiError
	if err := json.Unmarshal(data, &e); err == nil && e.Message != "" {
		if len(e.Error) > 0 && string(e.Error) != "null" {
			return fmt.Sprintf("%s (%s)", e.Message, string(e.Error))
		}
		return e.Message
	}
	if len(data) > 500 {
		data = data[:500]
	}
	return string(data)
}
