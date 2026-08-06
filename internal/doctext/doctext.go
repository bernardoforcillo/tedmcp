// Package doctext turns a tender document into text that can be read and
// searched.
//
// What a buyer publishes is not one format but a small zoo: the capitolato as a
// PDF, the allegati inside a ZIP, a modulo as DOCX, and — in Italy, routinely —
// any of them wrapped in a .p7m digital-signature envelope. A client that reads
// only what arrives as text/html sees none of it, which is the difference
// between having the tender documents and merely having downloaded them.
//
// Extraction never returns an error. A document that cannot be read comes back
// with a status saying why, because "this file contains no such requirement"
// and "this file is a photograph of paper" are opposite findings and must not
// look alike. The statuses are deliberately narrow: something was extracted, or
// there was no text to extract, or the format is not one this package reads.
package doctext

import (
	"bytes"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"
)

// Kind names the format a document turned out to be. It is decided by looking
// at the bytes, not by trusting the extension: procurement portals serve PDFs
// as application/octet-stream, and name ZIP archives .zip.p7m.
const (
	KindPDF     = "pdf"
	KindZip     = "zip"
	KindDocx    = "docx"
	KindXlsx    = "xlsx"
	KindPptx    = "pptx"
	KindODF     = "odf" // OpenDocument: .odt, .ods, .odp
	KindHTML    = "html"
	KindText    = "text"
	KindXML     = "xml"
	KindP7M     = "p7m" // CAdES signature envelope, unwrapped to its payload
	KindUnknown = "unknown"
)

// Outcomes of trying to read a document.
const (
	// StatusExtracted means the text below is the document's own words.
	StatusExtracted = "extracted"

	// StatusNoTextLayer means the file is a PDF whose pages carry no text —
	// almost always a scan of paper. The document exists and says something;
	// this program simply cannot read it without OCR. Reporting it as an empty
	// document would be a lie of omission.
	StatusNoTextLayer = "no-text-layer"

	// StatusEncrypted means the file is password-protected.
	StatusEncrypted = "encrypted"

	// StatusUnsupported means the format is not one this package reads.
	StatusUnsupported = "unsupported"

	// StatusDamaged means the file claims a format it then fails to honour.
	StatusDamaged = "damaged"
)

// Document is one file's text, or the reason there is none.
type Document struct {
	Name      string `json:"name,omitempty"`
	Container string `json:"container,omitempty" jsonschema:"the archive this file was found in, when it did not arrive on its own"`
	Kind      string `json:"kind,omitempty"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty" jsonschema:"why no text could be read"`
	Bytes     int    `json:"bytes,omitempty"`
	Chars     int    `json:"chars,omitempty" jsonschema:"characters of text extracted"`
	Pages     int    `json:"pages,omitempty" jsonschema:"pages, for a PDF"`
	Text      string `json:"text,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// Readable reports whether the document yielded text.
func (d Document) Readable() bool { return d.Status == StatusExtracted && d.Chars > 0 }

// Options bound what an extraction may cost. Archives are the reason these
// exist: a 2 MB ZIP can hold a gigabyte of expanded XML, and a tender's
// "allegati" can be a hundred files when only a few are worth reading.
type Options struct {
	MaxChars   int // characters of text kept per document (0 = unbounded)
	MaxMembers int // archive members expanded (0 = defaultMaxMembers)
	MaxDepth   int // nested-archive depth (0 = defaultMaxDepth)
	MaxBytes   int // total uncompressed bytes read from one archive
}

const (
	defaultMaxChars   = 200_000
	defaultMaxMembers = 60
	defaultMaxDepth   = 3
	defaultMaxBytes   = 64 << 20
)

func (o Options) withDefaults() Options {
	if o.MaxChars <= 0 {
		o.MaxChars = defaultMaxChars
	}
	if o.MaxMembers <= 0 {
		o.MaxMembers = defaultMaxMembers
	}
	if o.MaxDepth <= 0 {
		o.MaxDepth = defaultMaxDepth
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = defaultMaxBytes
	}
	return o
}

// Extract reads a downloaded file and returns its text.
//
// The result is a slice because one download is not always one document: an
// archive of allegati is returned as its members, each with its own status, so
// a single unreadable scan among twenty readable files does not sink the lot.
// A .p7m envelope is unwrapped and its payload extracted in its place.
func Extract(name, contentType string, data []byte, opt Options) []Document {
	return extract(name, contentType, data, opt.withDefaults(), 0)
}

func extract(name, contentType string, data []byte, opt Options, depth int) []Document {
	doc := Document{Name: name, Bytes: len(data)}

	if len(data) == 0 {
		doc.Kind, doc.Status, doc.Reason = KindUnknown, StatusDamaged, "the file is empty"
		return []Document{doc}
	}

	switch sniff(name, contentType, data) {
	case KindPDF:
		return []Document{extractPDF(doc, data, opt)}

	case KindP7M:
		if depth >= opt.MaxDepth {
			doc.Kind, doc.Status = KindP7M, StatusUnsupported
			doc.Reason = "signature envelopes nested deeper than this program unwraps"
			return []Document{doc}
		}
		payload, err := unwrapPKCS7(data)
		if err != nil {
			doc.Kind, doc.Status, doc.Reason = KindP7M, StatusDamaged, err.Error()
			return []Document{doc}
		}
		// The payload keeps the name it had before signing: capitolato.pdf.p7m
		// was capitolato.pdf, and that is the name a person would quote.
		inner := strings.TrimSuffix(name, path.Ext(name))
		if inner == "" {
			inner = name
		}
		return extract(inner, "", payload, opt, depth+1)

	case KindZip, KindDocx, KindXlsx, KindPptx, KindODF:
		return extractZipFamily(doc, data, opt, depth)

	case KindHTML:
		doc.Kind = KindHTML
		return []Document{withText(doc, HTMLToText(data), opt)}

	case KindXML:
		doc.Kind = KindXML
		return []Document{withText(doc, XMLToText(data), opt)}

	case KindText:
		doc.Kind = KindText
		return []Document{withText(doc, normalizeText(string(data)), opt)}

	default:
		doc.Kind, doc.Status = KindUnknown, StatusUnsupported
		doc.Reason = fmt.Sprintf("not a format this program reads (%s)", describeBytes(contentType, data))
		return []Document{doc}
	}
}

// withText attaches extracted text to a document, applying the character cap.
func withText(doc Document, text string, opt Options) Document {
	text = strings.TrimSpace(text)
	if text == "" {
		doc.Status = StatusExtracted
		doc.Reason = "the file holds no text"
		return doc
	}

	runes := []rune(text)
	doc.Chars = len(runes)
	if opt.MaxChars > 0 && len(runes) > opt.MaxChars {
		text = string(runes[:opt.MaxChars])
		doc.Truncated = true
	}
	doc.Status, doc.Text = StatusExtracted, text
	return doc
}

// sniff decides a file's format from its leading bytes, falling back to the
// extension and then the declared content type.
//
// The bytes come first on purpose. Buyer portals serve documents through
// download scripts that label everything application/octet-stream, and name
// them from a database column that may carry no extension at all.
func sniff(name, contentType string, data []byte) string {
	ext := strings.ToLower(path.Ext(name))

	switch {
	case bytes.HasPrefix(data, []byte("%PDF-")):
		return KindPDF
	case bytes.HasPrefix(data, zipMagic), bytes.HasPrefix(data, zipEmptyMagic):
		return zipKind(name, data)
	}

	// A signature envelope is DER (a SEQUENCE tag) or the same DER in base64.
	// Neither has a magic number, so the extension is the honest signal, and
	// unwrapPKCS7 verifies the guess before anything is done with it.
	if ext == ".p7m" || ext == ".p7s" || strings.Contains(contentType, "pkcs7") {
		return KindP7M
	}

	if k, ok := kindByExt[ext]; ok {
		return k
	}

	ct := strings.ToLower(contentType)
	switch {
	case strings.Contains(ct, "pdf"):
		// A truncated download can still be labelled a PDF; extractPDF says so.
		return KindPDF
	case strings.Contains(ct, "html"):
		return KindHTML
	case strings.Contains(ct, "xml"):
		return KindXML
	case strings.HasPrefix(ct, "text/"), strings.Contains(ct, "json"), strings.Contains(ct, "csv"):
		return KindText
	}

	// Nothing declared it, so read it: text that is valid UTF-8 and mostly
	// printable is text, whatever the server chose to call it.
	if looksTextual(data) {
		if isMarkup(data, "<html", "<!doctype html") {
			return KindHTML
		}
		if isMarkup(data, "<?xml") {
			return KindXML
		}
		return KindText
	}
	return KindUnknown
}

var kindByExt = map[string]string{
	".pdf":  KindPDF,
	".zip":  KindZip,
	".docx": KindDocx,
	".xlsx": KindXlsx,
	".pptx": KindPptx,
	".odt":  KindODF,
	".ods":  KindODF,
	".odp":  KindODF,
	".htm":  KindHTML,
	".html": KindHTML,
	".xml":  KindXML,
	".txt":  KindText,
	".csv":  KindText,
	".md":   KindText,
	".json": KindText,
}

// looksTextual reports whether bytes read as text rather than as a binary
// format this package has no reader for.
func looksTextual(data []byte) bool {
	head := data[:min(len(data), 8<<10)]
	if !utf8.Valid(head) {
		return false
	}
	control := 0
	for _, b := range head {
		if b < 0x09 || (b > 0x0d && b < 0x20) {
			control++
		}
	}
	return control*100 < len(head) // under 1% control characters
}

func isMarkup(data []byte, prefixes ...string) bool {
	head := strings.ToLower(strings.TrimSpace(string(data[:min(len(data), 1024)])))
	for _, p := range prefixes {
		if strings.HasPrefix(head, p) {
			return true
		}
	}
	return false
}

// describeBytes names an unreadable file as precisely as it can, so a caller
// can tell a proprietary CAD format from a corrupted download.
func describeBytes(contentType string, data []byte) string {
	if ct := strings.TrimSpace(contentType); ct != "" && !strings.Contains(ct, "octet-stream") {
		return ct
	}
	head := data[:min(len(data), 8)]
	var printable []byte
	for _, b := range head {
		if b >= 0x20 && b < 0x7f {
			printable = append(printable, b)
		}
	}
	if len(printable) >= 3 {
		return "leading bytes " + string(printable)
	}
	return "unrecognised binary"
}

// normalizeText makes extracted text safe to embed in a tool result: one
// newline convention, no lone carriage returns, no runs of blank lines.
func normalizeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return s
}
