package webdoc

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newPortal stands in for a buyer's procurement portal: a procedure page that
// lists the tender documents, and the files themselves.
func newPortal(robotsBody string) *httptest.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		if robotsBody == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(robotsBody))
	})

	mux.HandleFunc("/procedure/G00749", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<html><body>
		  <h1>Procedura G00749</h1>
		  <ul>
		    <li><a href="/files/disciplinare.pdf">Disciplinare di gara</a></li>
		    <li><a href='files/capitolato.pdf.p7m'>Capitolato speciale <b>firmato</b></a></li>
		    <li><a href="/download.php?idAllegato=42">Allegato 3 &ndash; DGUE</a></li>
		    <li><a href="/files/disciplinare.pdf">Disciplinare (copia)</a></li>
		  </ul>
		  <a href="/procedure/G00749">questa pagina</a>
		  <a href="#top">torna su</a>
		  <a href="mailto:gare@comune.example">scrivi</a>
		  <a href="javascript:apri()">apri</a>
		  <a href="https://www.giustizia-amministrativa.it/ricorso.pdf">TAR</a>
		  <a href="/chi-siamo">Chi siamo</a>
		</body></html>`)
	})

	mux.HandleFunc("/files/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.7 contenuto"))
	})
	mux.HandleFunc("/direct.pdf", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.7 contenuto"))
	})

	return httptest.NewServer(mux)
}

func TestDiscoverFindsTheTenderDocuments(t *testing.T) {
	srv := newPortal("")
	defer srv.Close()

	c := NewClient("tedmcp-test/0.1")
	got, page, err := c.Discover(context.Background(), srv.URL+"/procedure/G00749", 0)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if !page.Retrieved() {
		t.Fatalf("page status = %q (%s)", page.Status, page.Reason)
	}

	want := []Candidate{
		{URL: srv.URL + "/files/disciplinare.pdf", Label: "Disciplinare di gara"},
		{URL: srv.URL + "/procedure/files/capitolato.pdf.p7m", Label: "Capitolato speciale firmato"},
		{URL: srv.URL + "/download.php?idAllegato=42", Label: "Allegato 3 – DGUE"},
	}
	if len(got) != len(want) {
		t.Fatalf("found %d candidates, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].URL != w.URL {
			t.Errorf("candidate %d url = %q, want %q", i, got[i].URL, w.URL)
		}
		if got[i].Label != w.Label {
			t.Errorf("candidate %d label = %q, want %q", i, got[i].Label, w.Label)
		}
	}
}

func TestDiscoverHonoursLimit(t *testing.T) {
	srv := newPortal("")
	defer srv.Close()

	got, _, err := NewClient("tedmcp-test/0.1").Discover(context.Background(), srv.URL+"/procedure/G00749", 2)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("found %d candidates, want 2", len(got))
	}
}

func TestDiscoverObeysRobots(t *testing.T) {
	srv := newPortal("User-agent: *\nDisallow: /\n")
	defer srv.Close()

	got, page, err := NewClient("tedmcp-test/0.1").Discover(context.Background(), srv.URL+"/procedure/G00749", 0)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a disallowed page must yield no candidates, got %+v", got)
	}
	if page.Status != StatusRobotsDenied {
		t.Errorf("status = %q, want %q", page.Status, StatusRobotsDenied)
	}
	// The refusal has to survive as a reason someone can act on.
	if !strings.Contains(page.Reason, "robots.txt") {
		t.Errorf("reason = %q, should name robots.txt", page.Reason)
	}
}

func TestDiscoverOnADirectFileReturnsIt(t *testing.T) {
	srv := newPortal("")
	defer srv.Close()

	// Some notices link straight to the capitolato rather than to a page.
	got, page, err := NewClient("tedmcp-test/0.1").Discover(context.Background(), srv.URL+"/direct.pdf", 0)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a file is not a page to search for links, got %+v", got)
	}
	if !page.Retrieved() || len(page.Data) == 0 {
		t.Fatalf("the file itself should come back: status %q, %d bytes", page.Status, len(page.Data))
	}
}

func TestLooksLikeDocument(t *testing.T) {
	cases := map[string]bool{
		"https://p.example/files/capitolato.pdf":   true,
		"https://p.example/files/allegati.zip":     true,
		"https://p.example/f/disciplinare.pdf.p7m": true,
		"https://p.example/download.php?id=9":      true,
		"https://p.example/it/scarica/12":          true,
		"https://p.example/GetFile?idDoc=3":        true,
		"https://p.example/chi-siamo":              false,
		"https://p.example/procedure/G00749":       false,
		"https://p.example/immagini/logo.png":      false,
		"https://p.example/style.css":              false,
	}
	for raw, want := range cases {
		if got := looksLikeDocument(raw); got != want {
			t.Errorf("looksLikeDocument(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestSameSite(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"appalti.comune.example.it", "appalti.comune.example.it", true},
		{"www.comune.example.it", "files.comune.example.it", true},
		{"appalti.comune.example.it:8443", "appalti.comune.example.it", true},
		{"appalti.comune.example.it", "www.giustizia-amministrativa.it", false},
		{"portale.example.com", "cdn.other.com", false},
	}
	for _, c := range cases {
		if got := sameSite(c.a, c.b); got != c.want {
			t.Errorf("sameSite(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
