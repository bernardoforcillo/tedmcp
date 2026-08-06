package doctext

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// buildZip assembles an archive from a name→content map, in the order given.
func buildZip(t *testing.T, entries ...[2]string) []byte {
	t.Helper()

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		f, err := w.Create(e[0])
		if err != nil {
			t.Fatalf("create %s: %v", e[0], err)
		}
		if _, err := f.Write([]byte(e[1])); err != nil {
			t.Fatalf("write %s: %v", e[0], err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func TestExtractDocx(t *testing.T) {
	document := `<?xml version="1.0"?>
<w:document xmlns:w="x"><w:body>
<w:p><w:r><w:t>Capitolato speciale d'appalto</w:t></w:r></w:p>
<w:p><w:r><w:t>Art. 1 </w:t></w:r><w:r><w:t>Oggetto dell'appalto</w:t></w:r></w:p>
</w:body></w:document>`

	data := buildZip(t,
		[2]string{"[Content_Types].xml", "<Types/>"},
		[2]string{"word/document.xml", document},
		[2]string{"word/header1.xml", `<w:hdr xmlns:w="x"><w:p><w:r><w:t>Gara 22/2026</w:t></w:r></w:p></w:hdr>`},
	)

	docs := Extract("capitolato.docx", "", data, Options{})
	if len(docs) != 1 {
		t.Fatalf("got %d documents, want 1", len(docs))
	}
	d := docs[0]
	if d.Kind != KindDocx {
		t.Errorf("kind = %q, want %q", d.Kind, KindDocx)
	}
	if d.Status != StatusExtracted {
		t.Fatalf("status = %q (%s)", d.Status, d.Reason)
	}
	for _, want := range []string{"Capitolato speciale d'appalto", "Oggetto dell'appalto", "Gara 22/2026"} {
		if !strings.Contains(d.Text, want) {
			t.Errorf("missing %q in:\n%s", want, d.Text)
		}
	}
	// Paragraphs are separate requirements and must not merge.
	if strings.Contains(d.Text, "appaltoArt") {
		t.Errorf("paragraphs ran together:\n%s", d.Text)
	}
	// Runs inside one paragraph are one sentence and must not be split.
	if !strings.Contains(d.Text, "Art. 1 Oggetto") {
		t.Errorf("runs within a paragraph were split:\n%s", d.Text)
	}
}

func TestExtractXlsxReadsSharedStrings(t *testing.T) {
	shared := `<?xml version="1.0"?><sst xmlns="x">` +
		`<si><t>Criterio</t></si><si><t>Punteggio</t></si><si><t>Recupero eccedenze</t></si></sst>`
	sheet := `<?xml version="1.0"?><worksheet xmlns="x"><sheetData>` +
		`<row><c t="n"><v>5</v></c></row></sheetData></worksheet>`

	data := buildZip(t,
		[2]string{"xl/workbook.xml", "<workbook/>"},
		[2]string{"xl/sharedStrings.xml", shared},
		[2]string{"xl/worksheets/sheet1.xml", sheet},
	)

	d := Extract("criteri.xlsx", "", data, Options{})[0]
	if d.Kind != KindXlsx {
		t.Fatalf("kind = %q, want %q", d.Kind, KindXlsx)
	}
	for _, want := range []string{"Criterio", "Recupero eccedenze", "5"} {
		if !strings.Contains(d.Text, want) {
			t.Errorf("missing %q in:\n%s", want, d.Text)
		}
	}
	if strings.Contains(d.Text, "CriterioPunteggio") {
		t.Errorf("cells ran together:\n%s", d.Text)
	}
}

func TestExtractODF(t *testing.T) {
	content := `<?xml version="1.0"?><office:document-content xmlns:office="x" xmlns:text="y">` +
		`<office:body><text:h>Disciplinare</text:h><text:p>Termine: 12 settembre</text:p></office:body>` +
		`</office:document-content>`

	data := buildZip(t,
		[2]string{"mimetype", "application/vnd.oasis.opendocument.text"},
		[2]string{"content.xml", content},
	)

	d := Extract("disciplinare.odt", "", data, Options{})[0]
	if d.Kind != KindODF {
		t.Fatalf("kind = %q, want %q", d.Kind, KindODF)
	}
	if !strings.Contains(d.Text, "Termine: 12 settembre") {
		t.Errorf("missing text in:\n%s", d.Text)
	}
	if strings.Contains(d.Text, "DisciplinareTermine") {
		t.Errorf("heading merged into the paragraph:\n%s", d.Text)
	}
}

func TestExtractArchiveReturnsItsMembers(t *testing.T) {
	inner := buildPDF(t, []string{textStream([]struct {
		X, Y float64
		S    string
	}{{72, 720, "Capitolato tecnico"}})})

	data := buildZip(t,
		[2]string{"lettera.txt", "Si invita codesta impresa"},
		[2]string{"capitolato.pdf", string(inner)},
		[2]string{"__MACOSX/._capitolato.pdf", "resource fork"},
	)

	docs := Extract("allegati.zip", "application/zip", data, Options{})
	if len(docs) != 2 {
		t.Fatalf("got %d documents, want 2 (the macOS fork must be dropped): %+v", len(docs), docs)
	}

	// The capitolato sorts ahead of the covering letter.
	if docs[0].Name != "capitolato.pdf" {
		t.Errorf("first document = %q, want capitolato.pdf", docs[0].Name)
	}
	if docs[0].Kind != KindPDF || !strings.Contains(docs[0].Text, "Capitolato tecnico") {
		t.Errorf("the PDF inside the archive was not read as a PDF: %+v", docs[0])
	}
	for _, d := range docs {
		if d.Container != "allegati.zip" {
			t.Errorf("%s: container = %q, want allegati.zip", d.Name, d.Container)
		}
	}
}

func TestExtractArchiveCapsMembers(t *testing.T) {
	var entries [][2]string
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		entries = append(entries, [2]string{n + ".txt", "contenuto " + n})
	}

	docs := Extract("allegati.zip", "", buildZip(t, entries...), Options{MaxMembers: 2})

	var read, notice int
	for _, d := range docs {
		if d.Status == StatusUnsupported && strings.Contains(d.Reason, "not read") {
			notice++
			continue
		}
		read++
	}
	if read != 2 {
		t.Errorf("read %d members, want 2", read)
	}
	// Silently dropping the rest would read as "the archive held two files".
	if notice != 1 {
		t.Fatalf("expected one notice about the files that were skipped, got %d", notice)
	}
}

func TestExtractNestedArchiveStopsAtDepth(t *testing.T) {
	deepest := buildZip(t, [2]string{"nota.txt", "testo profondo"})
	middle := buildZip(t, [2]string{"livello3.zip", string(deepest)})
	outer := buildZip(t, [2]string{"livello2.zip", string(middle)})

	docs := Extract("livello1.zip", "", outer, Options{MaxDepth: 2})
	for _, d := range docs {
		if d.Status == StatusUnsupported && strings.Contains(d.Reason, "nested") {
			return
		}
	}
	t.Errorf("expected a notice that the nesting was too deep, got %+v", docs)
}
