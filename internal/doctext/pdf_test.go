package doctext

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// buildPDF assembles a small but genuinely valid PDF: real objects, a real
// cross-reference table with real byte offsets, and a trailer. It has to be
// valid rather than approximate, because the point of the test is that a
// PDF parser reads it.
func buildPDF(t *testing.T, pageStreams []string) []byte {
	t.Helper()

	var (
		buf     bytes.Buffer
		offsets []int // offsets[i] is where object i+1 starts
	)
	buf.WriteString("%PDF-1.4\n")

	addObject := func(body string) int {
		offsets = append(offsets, buf.Len())
		num := len(offsets)
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", num, body)
		return num
	}

	// Object numbers are needed before the objects exist, so lay out the plan
	// first: 1 catalog, 2 pages, then a page and a content stream per page,
	// then the font.
	pageNums := make([]int, len(pageStreams))
	contentNums := make([]int, len(pageStreams))
	for i := range pageStreams {
		pageNums[i] = 3 + i*2
		contentNums[i] = 4 + i*2
	}
	fontNum := 3 + len(pageStreams)*2

	var kids []string
	for _, n := range pageNums {
		kids = append(kids, fmt.Sprintf("%d 0 R", n))
	}

	addObject("<< /Type /Catalog /Pages 2 0 R >>")
	addObject(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>",
		strings.Join(kids, " "), len(pageStreams)))

	for i, stream := range pageStreams {
		addObject(fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "+
				"/Resources << /Font << /F1 %d 0 R >> >> /Contents %d 0 R >>", fontNum, contentNums[i]))
		offsets = append(offsets, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n",
			len(offsets), len(stream), stream)
	}
	addObject("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")

	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", len(offsets)+1)
	buf.WriteString("0000000000 65535 f \n")
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len(offsets)+1, xref)

	return buf.Bytes()
}

// textStream draws one line of text at a position, the way a real PDF does.
func textStream(lines []struct {
	X, Y float64
	S    string
}) string {
	var b strings.Builder
	b.WriteString("BT\n/F1 12 Tf\n")
	for _, l := range lines {
		fmt.Fprintf(&b, "1 0 0 1 %.2f %.2f Tm\n(%s) Tj\n", l.X, l.Y, l.S)
	}
	b.WriteString("ET\n")
	return b.String()
}

func TestExtractPDFReadsTextLayer(t *testing.T) {
	stream := textStream([]struct {
		X, Y float64
		S    string
	}{
		{72, 720, "Disciplinare di gara"},
		{72, 700, "Criterio D: recupero delle eccedenze alimentari"},
	})

	docs := Extract("disciplinare.pdf", "application/pdf", buildPDF(t, []string{stream}), Options{})
	if len(docs) != 1 {
		t.Fatalf("got %d documents, want 1", len(docs))
	}
	d := docs[0]
	if d.Status != StatusExtracted {
		t.Fatalf("status = %q (%s), want %q", d.Status, d.Reason, StatusExtracted)
	}
	if d.Kind != KindPDF {
		t.Errorf("kind = %q, want %q", d.Kind, KindPDF)
	}
	if d.Pages != 1 {
		t.Errorf("pages = %d, want 1", d.Pages)
	}
	for _, want := range []string{"Disciplinare di gara", "eccedenze alimentari"} {
		if !strings.Contains(d.Text, want) {
			t.Errorf("text does not contain %q:\n%s", want, d.Text)
		}
	}
	// The two lines were drawn at different heights and must stay apart.
	if strings.Contains(d.Text, "garaCriterio") {
		t.Errorf("lines ran together:\n%s", d.Text)
	}
}

func TestExtractPDFSeparatesLinesAndColumns(t *testing.T) {
	// A scoring grid: the criterion on the left, its weight far to the right.
	stream := textStream([]struct {
		X, Y float64
		S    string
	}{
		{72, 700, "Prevenzione delle eccedenze"},
		{400, 700, "5"},
		{72, 680, "Formazione del personale"},
		{400, 680, "10"},
	})

	docs := Extract("criteri.pdf", "", buildPDF(t, []string{stream}), Options{})
	text := docs[0].Text

	lines := strings.Split(text, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines, got:\n%s", text)
	}
	if !strings.Contains(lines[0], "eccedenze") || !strings.Contains(lines[0], "5") {
		t.Errorf("first row lost its weight: %q", lines[0])
	}
	// A criterion and a distant number must not read as one word.
	if strings.Contains(text, "eccedenze5") {
		t.Errorf("column gap was dropped:\n%s", text)
	}
}

func TestExtractPDFWithoutTextLayerIsReportedAsScan(t *testing.T) {
	// A page that draws a filled rectangle and no text: what a scan looks like
	// once the image is stripped out.
	docs := Extract("capitolato.pdf", "", buildPDF(t, []string{"0 0 0 rg\n72 72 400 600 re f\n"}), Options{})
	d := docs[0]

	if d.Status != StatusNoTextLayer {
		t.Fatalf("status = %q (%s), want %q", d.Status, d.Reason, StatusNoTextLayer)
	}
	if !strings.Contains(d.Reason, "scanned") {
		t.Errorf("reason should say the document is scanned, got %q", d.Reason)
	}
	if d.Pages != 1 {
		t.Errorf("pages = %d, want 1", d.Pages)
	}
}

func TestExtractPDFMultiplePages(t *testing.T) {
	page := func(s string) string {
		return textStream([]struct {
			X, Y float64
			S    string
		}{{72, 720, s}})
	}
	data := buildPDF(t, []string{page("Parte prima"), page("Parte seconda")})

	d := Extract("capitolato.pdf", "", data, Options{})[0]
	if d.Pages != 2 {
		t.Errorf("pages = %d, want 2", d.Pages)
	}
	for _, want := range []string{"Parte prima", "Parte seconda"} {
		if !strings.Contains(d.Text, want) {
			t.Errorf("missing %q in:\n%s", want, d.Text)
		}
	}
}

func TestExtractPDFDamagedIsNotAnError(t *testing.T) {
	// Truncation is what a throttled or interrupted download leaves behind.
	full := buildPDF(t, []string{textStream([]struct {
		X, Y float64
		S    string
	}{{72, 720, "Capitolato"}})})

	d := Extract("capitolato.pdf", "application/pdf", full[:len(full)/2], Options{})[0]
	if d.Status != StatusDamaged {
		t.Fatalf("status = %q, want %q", d.Status, StatusDamaged)
	}
	if d.Reason == "" {
		t.Error("a damaged PDF must say what went wrong")
	}
}

func TestExtractPDFTruncatesToMaxChars(t *testing.T) {
	var lines []struct {
		X, Y float64
		S    string
	}
	for i := 0; i < 40; i++ {
		lines = append(lines, struct {
			X, Y float64
			S    string
		}{72, float64(720 - i*15), "Articolo con testo sufficientemente lungo da superare il limite"})
	}

	d := Extract("lungo.pdf", "", buildPDF(t, []string{textStream(lines)}), Options{MaxChars: 100})[0]
	if !d.Truncated {
		t.Error("expected the text to be marked truncated")
	}
	if got := len([]rune(d.Text)); got != 100 {
		t.Errorf("kept %d characters, want 100", got)
	}
	// Chars reports the document's real size, not the truncated one.
	if d.Chars <= 100 {
		t.Errorf("chars = %d, should report the full length", d.Chars)
	}
}
