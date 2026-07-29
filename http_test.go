package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "tedmcp", Version: "test"}, nil)
	return mcpHandler(server)
}

const initializeRequest = `{"jsonrpc":"2.0","id":1,"method":"initialize",` +
	`"params":{"protocolVersion":"2025-11-25","capabilities":{},` +
	`"clientInfo":{"name":"probe","version":"0"}}}`

func postMCP(t *testing.T, h http.Handler, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, mcpPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHTTPServesMCPInitialize(t *testing.T) {
	rec := postMCP(t, newTestHandler(t), initializeRequest, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	// The streamable transport may answer as JSON or as an event stream; the
	// result is what matters.
	body := rec.Body.String()
	if !strings.Contains(body, `"serverInfo"`) || !strings.Contains(body, "tedmcp") {
		t.Fatalf("initialize did not return server info: %s", body)
	}
}

func TestHTTPRejectsCrossOriginRequest(t *testing.T) {
	// A page the user happens to have open must not be able to drive the
	// server on their behalf.
	rec := postMCP(t, newTestHandler(t), initializeRequest, map[string]string{
		"Origin": "https://evil.example",
	})
	if rec.Code == http.StatusOK {
		t.Fatalf("a cross-origin request was accepted (status %d)", rec.Code)
	}
}

func TestHTTPHealthAndRoot(t *testing.T) {
	h := newTestHandler(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ok") {
		t.Errorf("health check = %d %q", rec.Code, rec.Body.String())
	}

	// A stray browser visit should say where the endpoint is, not 404.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, mcpPath) {
		t.Errorf("root = %d %q, want a pointer to %s", rec.Code, body, mcpPath)
	}
	// The AGPL requires the source to be offered to network users, so the
	// offer has to survive here even if the page is reworded.
	if !strings.Contains(body, sourceURL) || !strings.Contains(body, "AGPL") {
		t.Errorf("root does not offer the source: %q", body)
	}
}

func TestHTTPToolsAreRegistered(t *testing.T) {
	// The tools must be reachable over HTTP exactly as over stdio.
	server := mcp.NewServer(&mcp.Implementation{Name: "tedmcp", Version: "test"}, nil)
	registerTools(server, nil, nil)
	h := mcpHandler(server)

	rec := postMCP(t, h, initializeRequest, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("initialize failed: %d %s", rec.Code, rec.Body.String())
	}
	session := rec.Header().Get("Mcp-Session-Id")
	if session == "" {
		t.Fatal("no session id returned")
	}

	headers := map[string]string{"Mcp-Session-Id": session}
	postMCP(t, h, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, headers)
	rec = postMCP(t, h, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`, headers)

	if rec.Code != http.StatusOK {
		t.Fatalf("tools/list failed: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, name := range []string{"search_tenders", "scan_tenders", "get_tender_dossier"} {
		if !strings.Contains(body, name) {
			t.Errorf("tool %s missing from the HTTP transport", name)
		}
	}
}

// The JSON body of an event-stream response is embedded in a data: line; this
// keeps the assertions above readable if the format is ever tightened.
func TestInitializeRequestIsValidJSON(t *testing.T) {
	var v map[string]any
	if err := json.Unmarshal([]byte(initializeRequest), &v); err != nil {
		t.Fatal(err)
	}
}
