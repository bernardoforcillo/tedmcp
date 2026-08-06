package main

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bernardoforcillo/tedmcp/internal/doctext"
	"github.com/bernardoforcillo/tedmcp/internal/ted"
	"github.com/bernardoforcillo/tedmcp/internal/webdoc"
)

// A buyer's portal, in the shape they actually take: the notice's link goes to
// a procedure page, and the documents hang off it one hop away.
func newBuyerPortal(robots string) *httptest.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		if robots == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(robots))
	})

	mux.HandleFunc("/procedure/G00749", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<html><body><h1>Procedura G00749</h1>
		  <a href="/files/disciplinare.txt">Disciplinare di gara</a>
		  <a href="/files/capitolato.docx">Capitolato speciale</a>
		  <a href="/files/allegati.zip">Allegati</a>
		  <a href="/chi-siamo">Chi siamo</a>
		</body></html>`)
	})

	mux.HandleFunc("/files/disciplinare.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = fmt.Fprint(w, "Art. 12 - Criteri di valutazione\n"+
			"Sono attribuiti 5 punti per il recupero delle eccedenze alimentari non somministrate.")
	})

	mux.HandleFunc("/files/capitolato.docx", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(testZip(
			[2]string{"word/document.xml",
				`<w:document xmlns:w="x"><w:body><w:p><w:r><w:t>Oggetto: servizio di ristorazione scolastica</w:t></w:r></w:p></w:body></w:document>`},
		))
	})

	mux.HandleFunc("/files/allegati.zip", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(testZip([2]string{"nota tecnica.txt", "La formazione del personale e obbligatoria."}))
	})

	return httptest.NewServer(mux)
}

func testZip(entries ...[2]string) []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		f, _ := w.Create(e[0])
		_, _ = f.Write([]byte(e[1]))
	}
	_ = w.Close()
	return buf.Bytes()
}

// findFile locates an extracted document by name across every download.
func findFile(out FetchDocsOutput, name string) (doctext.Document, bool) {
	for _, d := range out.Documents {
		for _, f := range d.Files {
			if strings.Contains(f.Name, name) {
				return f, true
			}
		}
	}
	return doctext.Document{}, false
}

func fetchDocuments(t *testing.T, in FetchDocsInput) FetchDocsOutput {
	t.Helper()
	handler := handleFetchDocuments(ted.NewClient(), webdoc.NewClient("tedmcp-test/0.1"))
	res, out, err := handler(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("handler returned an error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool reported an error: %v", res.Content)
	}
	return out
}

func TestFetchTenderDocumentsFollowsThePageToTheFiles(t *testing.T) {
	srv := newBuyerPortal("")
	defer srv.Close()

	out := fetchDocuments(t, FetchDocsInput{URLs: []string{srv.URL + "/procedure/G00749"}})

	// The procedure page plus the three documents it lists.
	if out.Attempted != 4 {
		t.Errorf("attempted %d URLs, want 4 (the page and its three documents)", out.Attempted)
	}
	if out.Retrieved != 4 {
		t.Errorf("retrieved %d, want 4", out.Retrieved)
	}

	disciplinare, ok := findFile(out, "disciplinare.txt")
	if !ok {
		t.Fatalf("the disciplinare was never downloaded: %+v", out.Documents)
	}
	if !strings.Contains(disciplinare.Text, "eccedenze alimentari") {
		t.Errorf("the disciplinare's text was not read: %q", disciplinare.Text)
	}

	capitolato, ok := findFile(out, "capitolato.docx")
	if !ok {
		t.Fatal("the capitolato was never downloaded")
	}
	if capitolato.Kind != doctext.KindDocx || !strings.Contains(capitolato.Text, "ristorazione scolastica") {
		t.Errorf("the DOCX was not read as a DOCX: %+v", capitolato)
	}

	// An archive is not one document; its members are.
	nota, ok := findFile(out, "nota tecnica.txt")
	if !ok {
		t.Fatal("the archive was not expanded into its members")
	}
	if nota.Container == "" {
		t.Error("a file from an archive must say which archive it came from")
	}
	if !strings.Contains(nota.Text, "formazione del personale") {
		t.Errorf("the archive member's text was not read: %q", nota.Text)
	}

	// The page that merely lists the documents is marked as such.
	var indexes int
	for _, d := range out.Documents {
		if d.Index {
			indexes++
		}
	}
	if indexes != 1 {
		t.Errorf("%d downloads marked as the index page, want 1", indexes)
	}
}

func TestFetchTenderDocumentsWithoutDiscoveryStopsAtTheLink(t *testing.T) {
	srv := newBuyerPortal("")
	defer srv.Close()

	no := false
	out := fetchDocuments(t, FetchDocsInput{
		URLs:     []string{srv.URL + "/procedure/G00749"},
		Discover: &no,
	})

	if out.Attempted != 1 {
		t.Errorf("attempted %d URLs, want 1 — discovery was off", out.Attempted)
	}
	if _, ok := findFile(out, "disciplinare.txt"); ok {
		t.Error("no document should have been followed with discover=false")
	}
}

func TestFetchTenderDocumentsReportsARefusalHonestly(t *testing.T) {
	srv := newBuyerPortal("User-agent: *\nDisallow: /\n")
	defer srv.Close()

	out := fetchDocuments(t, FetchDocsInput{URLs: []string{srv.URL + "/procedure/G00749"}})

	if out.Retrieved != 0 {
		t.Fatalf("retrieved %d, want 0 — the site disallows automated clients", out.Retrieved)
	}
	if out.Documents[0].Result.Status != webdoc.StatusRobotsDenied {
		t.Errorf("status = %q, want %q", out.Documents[0].Result.Status, webdoc.StatusRobotsDenied)
	}
	// The note must send a person to a browser, never suggest the documents
	// are missing.
	if !strings.Contains(out.Note, "browser") {
		t.Errorf("note = %q, should say to open the URL in a browser", out.Note)
	}
	for _, wrong := range []string{"no documents exist", "not available"} {
		if strings.Contains(strings.ToLower(out.Note), wrong) {
			t.Errorf("note must not claim the documents are absent: %q", out.Note)
		}
	}
}

func TestFetchTenderDocumentsHonoursTheTotalTextBudget(t *testing.T) {
	srv := newBuyerPortal("")
	defer srv.Close()

	out := fetchDocuments(t, FetchDocsInput{
		URLs:          []string{srv.URL + "/procedure/G00749"},
		MaxTotalChars: 40,
	})

	total := 0
	for _, d := range out.Documents {
		for _, f := range d.Files {
			total += len([]rune(f.Text))
		}
	}
	if total > 40 {
		t.Errorf("returned %d characters of text, want at most 40", total)
	}
	// Documents still have to be listed even when their text is withheld.
	if _, ok := findFile(out, "capitolato.docx"); !ok {
		t.Error("a document past the budget must still be listed")
	}
	if !strings.Contains(out.Note, "max_total_chars") {
		t.Errorf("note = %q, should explain how to see the rest", out.Note)
	}
}

func TestFetchTenderDocumentsRanksBeforeItCaps(t *testing.T) {
	srv := newBuyerPortal("")
	defer srv.Close()

	// Room for the page and one document only. The one taken must be the
	// disciplinare, which is listed first by name rather than by page order.
	out := fetchDocuments(t, FetchDocsInput{
		URLs:         []string{srv.URL + "/procedure/G00749"},
		MaxDocuments: 1,
	})

	if _, ok := findFile(out, "disciplinare.txt"); !ok {
		t.Error("the cap dropped the disciplinare and kept something else")
	}
	if out.Attempted != 2 {
		t.Errorf("attempted %d URLs, want 2 — the page and one document", out.Attempted)
	}

	// The two it did not take must be visible, not silently absent.
	var notice string
	for _, d := range out.Documents {
		if d.URL == "" && d.Result.Reason != "" {
			notice = d.Result.Reason
		}
	}
	if !strings.Contains(notice, "2 further document") {
		t.Errorf("skipped documents were not reported: %q", notice)
	}
}

func TestGatherDocumentsDoesNotCrawlTheBuyerProfile(t *testing.T) {
	srv := newBuyerPortal("")
	defer srv.Close()

	// The buyer's profile is the portal's front door. Following its links would
	// download every other tender the buyer has ever run.
	docs := gatherDocuments(context.Background(), webdoc.NewClient("tedmcp-test/0.1"),
		[]ted.DocumentLink{{URL: srv.URL + "/procedure/G00749", Role: ted.LinkBuyerProfile}},
		gatherOptions{Discover: true})

	if len(docs) != 1 {
		t.Fatalf("followed %d links from the buyer profile, want 1 (the page itself)", len(docs))
	}
	if docs[0].Index {
		t.Error("the buyer profile must not be treated as a procedure page")
	}
}

func TestFetchTenderDocumentsNeedsATarget(t *testing.T) {
	handler := handleFetchDocuments(ted.NewClient(), webdoc.NewClient("tedmcp-test/0.1"))
	res, _, err := handler(context.Background(), nil, FetchDocsInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Error("a call with neither publication_number nor urls must be an error")
	}
}

// --- scan_tender_documents ---

func scanDocuments(t *testing.T, in ScanDocsInput) ScanDocsOutput {
	t.Helper()
	handler := handleScanDocuments(ted.NewClient(), webdoc.NewClient("tedmcp-test/0.1"))
	res, out, err := handler(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("handler returned an error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool reported an error: %v", res.Content)
	}
	return out
}

func TestScanTenderDocumentsFindsTermsInsideTheCapitolato(t *testing.T) {
	srv := newBuyerPortal("")
	defer srv.Close()

	out := scanDocuments(t, ScanDocsInput{
		URLs:     []string{srv.URL + "/procedure/G00749"},
		Terms:    []string{"eccedenz"},
		Language: "ITA",
	})

	if out.Matched != 1 {
		t.Fatalf("matched %d tenders, want 1: %+v", out.Matched, out.Results)
	}
	r := out.Results[0]
	if len(r.Matches) == 0 {
		t.Fatal("no matching passage returned")
	}
	m := r.Matches[0]
	if !strings.Contains(m.Document, "disciplinare.txt") {
		t.Errorf("match came from %q, want the disciplinare", m.Document)
	}
	if !strings.Contains(m.Text, "eccedenze alimentari") {
		t.Errorf("the passage does not show the match in context: %q", m.Text)
	}
	if m.Term != "eccedenz" {
		t.Errorf("term = %q, want the term that matched", m.Term)
	}
}

func TestScanTenderDocumentsKeepsTermsToTheirLanguage(t *testing.T) {
	srv := newBuyerPortal("")
	defer srv.Close()

	// The German stem for "corresponding" contains the Italian stem for waste.
	// Told the documents are German, an Italian term must not be tried on them.
	out := scanDocuments(t, ScanDocsInput{
		URLs:            []string{srv.URL + "/procedure/G00749"},
		TermsByLanguage: map[string][]string{"ITA": {"eccedenz"}},
		Language:        "DEU",
	})

	if out.Matched != 0 {
		t.Errorf("an Italian term matched a document declared German: %+v", out.Results)
	}
}

func TestScanTenderDocumentsReportsWhatItCouldNotSearch(t *testing.T) {
	srv := newBuyerPortal("User-agent: *\nDisallow: /\n")
	defer srv.Close()

	out := scanDocuments(t, ScanDocsInput{
		URLs:     []string{srv.URL + "/procedure/G00749"},
		Terms:    []string{"eccedenz"},
		Language: "ITA",
	})

	if out.Matched != 0 {
		t.Fatalf("nothing was readable, so nothing can have matched: %+v", out.Results)
	}
	if len(out.Results) == 0 {
		t.Fatal("a tender whose documents could not be read must still be reported")
	}
	r := out.Results[0]
	if len(r.Refused) == 0 {
		t.Error("the refused URL must be listed, not silently dropped")
	}
	if !strings.Contains(r.Note, "browser") {
		t.Errorf("note = %q, should send a person to a browser", r.Note)
	}
	if !strings.Contains(out.Note, "not proof of absence") {
		t.Errorf("output note = %q, should warn against reading nil as absence", out.Note)
	}
}

func TestScanTenderDocumentsIgnoresTheIndexPageItself(t *testing.T) {
	srv := newBuyerPortal("")
	defer srv.Close()

	// "Disciplinare di gara" is a link label on the procedure page. A match
	// there would be a match on navigation, not on the tender's terms.
	out := scanDocuments(t, ScanDocsInput{
		URLs:     []string{srv.URL + "/procedure/G00749"},
		Terms:    []string{"chi siamo"},
		Language: "ITA",
	})

	if out.Matched != 0 {
		t.Errorf("matched the procedure page's own navigation: %+v", out.Results)
	}
}

func TestScanTenderDocumentsReportsDocumentsItNeverDownloaded(t *testing.T) {
	srv := newBuyerPortal("")
	defer srv.Close()

	out := scanDocuments(t, ScanDocsInput{
		URLs:         []string{srv.URL + "/procedure/G00749"},
		Terms:        []string{"eccedenz"},
		Language:     "ITA",
		MaxDocuments: 1,
	})

	if len(out.Results) == 0 {
		t.Fatal("no result returned")
	}
	r := out.Results[0]
	if !r.Matched {
		t.Error("the one document that was downloaded does match")
	}
	// A cap is a limit on the search, so it belongs with what was not searched.
	var found bool
	for _, u := range r.Refused {
		if u.Status == "not-downloaded" && strings.Contains(u.Reason, "further document") {
			found = true
		}
	}
	if !found {
		t.Errorf("documents left undownloaded were not reported: %+v", r.Refused)
	}
}

func TestScanTenderDocumentsNeedsATarget(t *testing.T) {
	handler := handleScanDocuments(ted.NewClient(), webdoc.NewClient("tedmcp-test/0.1"))
	res, _, err := handler(context.Background(), nil, ScanDocsInput{Terms: []string{"x"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Error("a call with neither publication_numbers nor urls must be an error")
	}
}
