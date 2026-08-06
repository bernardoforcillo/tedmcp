package doctext

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
)

var (
	zipMagic      = []byte("PK\x03\x04")
	zipEmptyMagic = []byte("PK\x05\x06") // an archive with no members
)

// zipKind decides which of the zip-based formats an archive is by looking for
// the member each one is required to contain. The extension is consulted only
// when the archive cannot be opened, since a document renamed .zip is still a
// DOCX and a portal's "allegati.docx" is sometimes really an archive.
func zipKind(name string, data []byte) string {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		if k, ok := kindByExt[strings.ToLower(path.Ext(name))]; ok {
			return k
		}
		return KindZip
	}
	for _, f := range r.File {
		switch f.Name {
		case "word/document.xml":
			return KindDocx
		case "xl/workbook.xml":
			return KindXlsx
		case "ppt/presentation.xml":
			return KindPptx
		case "mimetype":
			if mime, err := readMember(f, 128); err == nil &&
				bytes.HasPrefix(mime, []byte("application/vnd.oasis.opendocument")) {
				return KindODF
			}
		}
	}
	return KindZip
}

// extractZipFamily reads an office document, or expands an archive of them.
func extractZipFamily(doc Document, data []byte, opt Options, depth int) []Document {
	doc.Kind = sniff(doc.Name, "", data)

	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		doc.Status, doc.Reason = StatusDamaged, "could not open the archive: "+err.Error()
		return []Document{doc}
	}

	switch doc.Kind {
	case KindDocx, KindXlsx, KindPptx, KindODF:
		return []Document{extractOffice(doc, r, opt)}
	}

	// A plain archive is not a document; its members are. Returning it as one
	// blob would bury the disciplinare among the modules and the ESPD form.
	return expandArchive(doc, r, opt, depth)
}

// expandArchive turns an archive into its members, each extracted in its own
// right so a nested PDF is read as a PDF.
func expandArchive(doc Document, r *zip.Reader, opt Options, depth int) []Document {
	doc.Kind = KindZip

	if depth >= opt.MaxDepth {
		doc.Status = StatusUnsupported
		doc.Reason = fmt.Sprintf("archives nested more than %d deep are not expanded", opt.MaxDepth)
		return []Document{doc}
	}

	members := readableMembers(r)
	if len(members) == 0 {
		doc.Status, doc.Reason = StatusExtracted, "the archive holds no files"
		return []Document{doc}
	}

	// Read the interesting files first, so a cap that bites drops the ESPD form
	// rather than the capitolato.
	sort.SliceStable(members, func(i, j int) bool {
		return Rank(members[i].Name) < Rank(members[j].Name)
	})

	var (
		out     []Document
		budget  = opt.MaxBytes
		skipped int
	)
	for i, f := range members {
		if i >= opt.MaxMembers || budget <= 0 {
			skipped = len(members) - i
			break
		}
		data, err := readMember(f, budget)
		if err != nil {
			out = append(out, Document{
				Name: f.Name, Container: doc.Name, Kind: KindUnknown,
				Status: StatusDamaged, Reason: "could not read from the archive: " + err.Error(),
			})
			continue
		}
		budget -= len(data)

		for _, d := range extract(f.Name, "", data, opt, depth+1) {
			// Keep the outermost archive as the container, so a file two levels
			// down still says where a person would go to find it.
			d.Container = firstNonEmpty(doc.Name, d.Container)
			out = append(out, d)
		}
	}

	if skipped > 0 {
		out = append(out, Document{
			Name: doc.Name, Kind: KindZip, Status: StatusUnsupported,
			Reason: fmt.Sprintf("%d further file(s) in this archive were not read (limit %d files / %d MB) — "+
				"raise max_documents or open the archive directly", skipped, opt.MaxMembers, opt.MaxBytes>>20),
		})
	}
	return out
}

// readableMembers drops the entries that are not files a reader wants: the
// directory entries, the resource forks macOS adds to every archive it makes,
// and the signature blocks that travel beside signed documents.
func readableMembers(r *zip.Reader) []*zip.File {
	var out []*zip.File
	for _, f := range r.File {
		name := f.Name
		if strings.HasSuffix(name, "/") || f.FileInfo().IsDir() {
			continue
		}
		if strings.HasPrefix(name, "__MACOSX/") || path.Base(name) == ".DS_Store" {
			continue
		}
		out = append(out, f)
	}
	return out
}

// Rank orders files by how likely they are to be the document someone came for,
// lowest first. The names are the ones Italian buyers actually use.
//
// It exists because every limit has to fall somewhere: an archive of forty
// allegati, or a procedure page listing thirty files, will be read only in
// part, and which part is not a detail. Something unrecognised ranks last but
// is still ahead of nothing — a file named by a database key may well be the
// capitolato.
func Rank(name string) int {
	lower := strings.ToLower(path.Base(name))
	for i, prefix := range documentNameOrder {
		if strings.Contains(lower, prefix) {
			return i
		}
	}
	return len(documentNameOrder)
}

var documentNameOrder = []string{
	"disciplinare", "capitolato", "csa", "bando", "lettera", "invito",
	"criteri", "offerta", "tecnic", "economic", "relazione", "progetto",
	"schema", "contratto", "allegato",
}

// readMember decompresses one archive member, refusing to exceed the caller's
// remaining budget. The limit is what keeps a small archive of highly
// compressible XML from expanding into gigabytes.
func readMember(f *zip.File, limit int) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	if limit <= 0 {
		limit = defaultMaxBytes
	}
	data, err := io.ReadAll(io.LimitReader(rc, int64(limit)))
	if err != nil {
		return nil, err
	}
	return data, nil
}

// extractOffice reads the text out of an office document by pulling the parts
// that hold it. Each format keeps its words somewhere different, and the rest
// of the archive is styling, relationships and images.
func extractOffice(doc Document, r *zip.Reader, opt Options) Document {
	var parts []string
	budget := opt.MaxBytes

	for _, f := range officeParts(doc.Kind, r) {
		data, err := readMember(f, budget)
		if err != nil {
			continue
		}
		budget -= len(data)
		if text := officeText(doc.Kind, data); text != "" {
			parts = append(parts, text)
		}
		if budget <= 0 {
			break
		}
	}

	doc = withText(doc, normalizeText(strings.Join(parts, "\n\n")), opt)
	if doc.Chars == 0 && doc.Status == StatusExtracted {
		doc.Reason = "the document holds no text — its content may be images"
	}
	return doc
}

// officeParts lists the members holding a format's text, in reading order.
func officeParts(kind string, r *zip.Reader) []*zip.File {
	var out []*zip.File
	for _, f := range r.File {
		name := f.Name
		keep := false
		switch kind {
		case KindDocx:
			// Headers and footers carry the procedure reference and the lot
			// number, which is often the only place they appear.
			keep = name == "word/document.xml" ||
				strings.HasPrefix(name, "word/header") || strings.HasPrefix(name, "word/footer") ||
				name == "word/footnotes.xml" || name == "word/endnotes.xml"
		case KindXlsx:
			keep = name == "xl/sharedStrings.xml" || strings.HasPrefix(name, "xl/worksheets/sheet")
		case KindPptx:
			keep = strings.HasPrefix(name, "ppt/slides/slide") ||
				strings.HasPrefix(name, "ppt/notesSlides/notesSlide")
		case KindODF:
			keep = name == "content.xml"
		}
		if keep {
			out = append(out, f)
		}
	}
	// slide2 must not sort before slide10 by length, but plain name order is
	// close enough and stable, which matters more than perfect slide sequence.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// officeText renders one office XML part, keeping the breaks that carry
// meaning: a paragraph in a disciplinare is usually one requirement.
func officeText(kind string, data []byte) string {
	s := string(data)
	switch kind {
	case KindDocx:
		s = replaceAllOf(s, "\n", "</w:p>", "<w:br/>", "<w:br />")
		s = replaceAllOf(s, "\t", "<w:tab/>", "<w:tab />")
	case KindXlsx:
		// Every shared string and every cell value is its own item.
		s = replaceAllOf(s, "\n", "</si>", "</c>")
	case KindPptx:
		s = replaceAllOf(s, "\n", "</a:p>")
	case KindODF:
		s = replaceAllOf(s, "\n", "</text:p>", "</text:h>")
		s = replaceAllOf(s, "\t", "<text:tab/>", "<text:tab />")
	}
	return XMLToText([]byte(s))
}

func replaceAllOf(s, with string, olds ...string) string {
	for _, old := range olds {
		s = strings.ReplaceAll(s, old, with+old)
	}
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
