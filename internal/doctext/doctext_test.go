package doctext

import (
	"strings"
	"testing"
)

func TestHTMLToText(t *testing.T) {
	page := `<!DOCTYPE html><html><head><title>Portale</title>
<style>.x{color:red}</style></head>
<body>
<script>var a = 1 < 2;</script>
<h1>Documenti di gara</h1>
<ul><li>Disciplinare &amp; allegati</li><li>Capitolato speciale</li></ul>
<p>Scadenza: 12/09/2026 alle 08:00</p>
</body></html>`

	got := HTMLToText([]byte(page))

	for _, want := range []string{"Documenti di gara", "Disciplinare & allegati", "Capitolato speciale", "Scadenza: 12/09/2026"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"var a", "color:red", "Portale"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("script/style/head content leaked through (%q):\n%s", unwanted, got)
		}
	}
	// List items are separate documents and must not run together.
	if strings.Contains(got, "allegatiCapitolato") {
		t.Errorf("list items merged:\n%s", got)
	}
}

func TestHTMLToTextHandlesUnclosedAndCommented(t *testing.T) {
	got := HTMLToText([]byte(`<p>Valore 5 < 10</p><!-- <p>nascosto</p> --><p>Fine</p>`))

	if !strings.Contains(got, "Valore 5") {
		t.Errorf("lost text around a bare '<': %q", got)
	}
	if strings.Contains(got, "nascosto") {
		t.Errorf("comment content leaked: %q", got)
	}
	if !strings.Contains(got, "Fine") {
		t.Errorf("lost text after a comment: %q", got)
	}
}

func TestXMLToTextKeepsCDATA(t *testing.T) {
	got := XMLToText([]byte(`<doc><t><![CDATA[Criterio: recupero eccedenze]]></t></doc>`))
	if !strings.Contains(got, "Criterio: recupero eccedenze") {
		t.Errorf("CDATA lost: %q", got)
	}
}

func TestSniffPrefersBytesOverDeclaredType(t *testing.T) {
	// Portals serve documents through download scripts that label everything
	// application/octet-stream, and name them from a database column.
	pdf := buildPDF(t, []string{textStream([]struct {
		X, Y float64
		S    string
	}{{72, 720, "Capitolato"}})})

	cases := []struct {
		name, contentType string
		data              []byte
		want              string
	}{
		{"download.php", "application/octet-stream", pdf, KindPDF},
		{"", "", pdf, KindPDF},
		{"allegati", "application/octet-stream", buildZip(t, [2]string{"a.txt", "x"}), KindZip},
		{"page", "text/html; charset=utf-8", []byte("<p>ciao</p>"), KindHTML},
		{"", "", []byte("<!DOCTYPE html><html><body>ciao</body></html>"), KindHTML},
		{"nota.txt", "", []byte("testo semplice"), KindText},
		{"", "", []byte("<?xml version=\"1.0\"?><a>b</a>"), KindXML},
	}
	for _, c := range cases {
		if got := sniff(c.name, c.contentType, c.data); got != c.want {
			t.Errorf("sniff(%q, %q) = %q, want %q", c.name, c.contentType, got, c.want)
		}
	}
}

func TestExtractUnknownBinaryIsReportedNotGuessed(t *testing.T) {
	d := Extract("disegno.dwg", "application/octet-stream", []byte{0x41, 0x43, 0x31, 0x30, 0x33, 0x32, 0x00, 0x01, 0x02, 0x03}, Options{})[0]

	if d.Status != StatusUnsupported {
		t.Fatalf("status = %q, want %q", d.Status, StatusUnsupported)
	}
	if !strings.Contains(d.Reason, "AC10") {
		t.Errorf("reason should describe the bytes seen, got %q", d.Reason)
	}
}

func TestExtractEmptyFile(t *testing.T) {
	d := Extract("vuoto.pdf", "application/pdf", nil, Options{})[0]
	if d.Status != StatusDamaged {
		t.Errorf("status = %q, want %q", d.Status, StatusDamaged)
	}
}

func TestExtractTextTruncates(t *testing.T) {
	long := strings.Repeat("clausola contrattuale ", 500)

	d := Extract("nota.txt", "text/plain", []byte(long), Options{MaxChars: 50})[0]
	if !d.Truncated {
		t.Error("expected truncation")
	}
	if got := len([]rune(d.Text)); got != 50 {
		t.Errorf("kept %d characters, want 50", got)
	}
	if d.Chars != len([]rune(strings.TrimSpace(long))) {
		t.Errorf("chars = %d, should be the untruncated length", d.Chars)
	}
	if !d.Readable() {
		t.Error("a truncated document is still readable")
	}
}

func TestReadableIsFalseWithoutText(t *testing.T) {
	for _, d := range []Document{
		{Status: StatusNoTextLayer},
		{Status: StatusEncrypted},
		{Status: StatusExtracted, Chars: 0},
	} {
		if d.Readable() {
			t.Errorf("%+v should not be readable", d)
		}
	}
}
