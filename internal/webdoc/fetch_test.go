package webdoc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestServer stands in for a buyer's portal: a robots.txt, an open
// document, a captcha-gated page and a path robots closes off.
func newTestServer(robotsBody string) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		if robotsBody == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(robotsBody))
	})
	mux.HandleFunc("/open/capitolato.pdf", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `attachment; filename="capitolato speciale.pdf"`)
		_, _ = w.Write([]byte("%PDF-1.7 fake"))
	})
	mux.HandleFunc("/open/disciplinare.html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body>Disciplinare di gara: eccedenze alimentari</body></html>"))
	})
	mux.HandleFunc("/gated/procedura", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><style>.frc-captcha{}</style></head><body>verifica</body></html>`))
	})
	mux.HandleFunc("/closed/segreto.pdf", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	return httptest.NewServer(mux)
}

func TestFetchAllowed(t *testing.T) {
	srv := newTestServer("User-agent: *\nDisallow: /closed\n")
	defer srv.Close()

	c := NewClient("tedmcp/test")
	res, err := c.Fetch(context.Background(), srv.URL+"/open/capitolato.pdf", 1000)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !res.Retrieved() {
		t.Fatalf("status = %s (%s), want fetched", res.Status, res.Reason)
	}
	if res.Filename != "capitolato speciale.pdf" {
		t.Errorf("filename = %q, want the name from Content-Disposition", res.Filename)
	}
	// A PDF is binary: it must not be spilled into the text field.
	if res.Text != "" {
		t.Errorf("binary document should carry no text, got %q", res.Text)
	}
	if res.Bytes == 0 {
		t.Error("bytes not reported")
	}
}

func TestFetchTextIsReturnedAndTruncated(t *testing.T) {
	srv := newTestServer("")
	defer srv.Close()

	c := NewClient("tedmcp/test")
	res, err := c.Fetch(context.Background(), srv.URL+"/open/disciplinare.html", 20)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !res.Retrieved() {
		t.Fatalf("status = %s, want fetched (a missing robots.txt places no limit)", res.Status)
	}
	if !res.Truncated || len(res.Text) != 20 {
		t.Errorf("text = %q truncated=%v, want 20 chars and truncated", res.Text, res.Truncated)
	}
}

func TestFetchRespectsRobots(t *testing.T) {
	srv := newTestServer("User-agent: *\nDisallow: /closed\n")
	defer srv.Close()

	c := NewClient("tedmcp/test")
	res, err := c.Fetch(context.Background(), srv.URL+"/closed/segreto.pdf", 1000)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Status != StatusRobotsDenied {
		t.Fatalf("status = %s, want %s", res.Status, StatusRobotsDenied)
	}
	if !strings.Contains(res.Reason, "robots.txt") {
		t.Errorf("reason should name robots.txt, got %q", res.Reason)
	}
	// Refusing must mean not asking at all.
	if res.Bytes != 0 {
		t.Errorf("a denied path must not be requested, but %d bytes were read", res.Bytes)
	}
}

func TestFetchDetectsCaptcha(t *testing.T) {
	srv := newTestServer("")
	defer srv.Close()

	c := NewClient("tedmcp/test")
	res, err := c.Fetch(context.Background(), srv.URL+"/gated/procedura", 1000)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	// The server answered 200; reporting success here would turn a gated
	// document into an apparently empty one.
	if res.Status != StatusCaptcha {
		t.Fatalf("status = %s, want %s", res.Status, StatusCaptcha)
	}
	if res.Retrieved() {
		t.Error("a captcha page must not count as retrieved")
	}
}

func TestFetchReportsServerRefusal(t *testing.T) {
	srv := newTestServer("")
	defer srv.Close()

	c := NewClient("tedmcp/test")
	res, err := c.Fetch(context.Background(), srv.URL+"/closed/segreto.pdf", 1000)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Status != StatusBlocked || !strings.Contains(res.Reason, "403") {
		t.Fatalf("status = %s reason = %q, want blocked with the status", res.Status, res.Reason)
	}
}

func TestFetchRejectsNonHTTP(t *testing.T) {
	c := NewClient("tedmcp/test")
	if _, err := c.Fetch(context.Background(), "ftp://example.org/x", 100); err == nil {
		t.Fatal("expected an error for a non-HTTP URL")
	}
}
