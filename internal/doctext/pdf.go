package doctext

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/ledongthuc/pdf"
)

// extractPDF reads a PDF's text layer.
//
// Two failure modes are worth telling apart and are reported separately. A PDF
// that cannot be parsed is damaged or encrypted. A PDF that parses perfectly
// and yields nothing is a scan: the capitolato was printed, signed, and put
// back through a scanner, so its pages are images and there is no text to find.
// The second is common in Italian procurement and must never be reported as an
// empty or irrelevant document.
func extractPDF(doc Document, data []byte, opt Options) Document {
	doc.Kind = KindPDF

	reader, err := openPDF(data)
	if err != nil {
		switch {
		case isEncrypted(err):
			doc.Status = StatusEncrypted
			doc.Reason = "the PDF is password-protected"
		default:
			doc.Status = StatusDamaged
			doc.Reason = "could not read the PDF: " + err.Error()
		}
		return doc
	}

	pages := reader.NumPage()
	doc.Pages = pages

	var b strings.Builder
	unreadable := 0
	for i := 1; i <= pages; i++ {
		text, ok := pageText(reader, i)
		if !ok {
			unreadable++
			continue
		}
		if text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(text)

		// Stop early once the cap is met: a 400-page capitolato costs real time
		// to walk, and nothing past the cap would be returned anyway.
		if opt.MaxChars > 0 && b.Len() > opt.MaxChars*4 {
			break
		}
	}

	doc = withText(doc, normalizeText(b.String()), opt)
	switch {
	case doc.Chars > 0 && unreadable > 0:
		doc.Reason = fmt.Sprintf("%d of %d page(s) could not be parsed; the rest are below", unreadable, pages)
	case doc.Chars == 0 && unreadable == pages && pages > 0:
		doc.Status = StatusDamaged
		doc.Reason = "none of the pages could be parsed"
	case doc.Chars == 0:
		doc.Status = StatusNoTextLayer
		doc.Reason = fmt.Sprintf("%d page(s) carrying no text layer — this is a scanned document, "+
			"readable by a person but not by this program without OCR", pages)
	}
	return doc
}

// openPDF parses a PDF, converting the library's panics into errors. The
// library reports malformed input by panicking, which is fine for a command
// reading one file and unacceptable inside a server handling many.
func openPDF(data []byte) (r *pdf.Reader, err error) {
	defer func() {
		if v := recover(); v != nil {
			r, err = nil, fmt.Errorf("%v", v)
		}
	}()
	return pdf.NewReader(bytes.NewReader(data), int64(len(data)))
}

// pageText renders one page, reporting false if that page could not be parsed.
// Recovery is per page on purpose: one malformed page in a long capitolato
// should cost that page, not the document.
func pageText(r *pdf.Reader, num int) (text string, ok bool) {
	defer func() {
		if v := recover(); v != nil {
			text, ok = "", false
		}
	}()

	page := r.Page(num)
	if page.V.IsNull() {
		return "", false
	}
	return layoutText(page.Content().Text), true
}

func isEncrypted(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "encrypt") || strings.Contains(msg, "password")
}

// layoutText reassembles a page's text runs into lines.
//
// A PDF does not store lines. It stores glyphs at coordinates, and a single
// visual line commonly arrives as dozens of runs in arbitrary order. Reading
// them in storage order yields shuffled words; concatenating them without
// regard to position runs them together. So runs are grouped by their vertical
// position and ordered along each line, and the horizontal gaps between them
// are turned back into spaces.
//
// Keeping the wide gaps as wide gaps matters for what these documents are: the
// scoring grid of a disciplinare is a table, and a criterion separated from its
// weight by half a page reads very differently from "criterion 5".
func layoutText(runs []pdf.Text) string {
	// Group into lines first, then order each line. Doing it in one sort with a
	// tolerance would compare runs inconsistently — a is level with b, b with
	// c, but a not with c — which is not an ordering and would shuffle text.
	lines := groupLines(runs)

	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			// A drop much larger than the type size is a paragraph break, not
			// just the next line.
			if line.gapAbove > line.size*1.8 {
				b.WriteString("\n\n")
			} else {
				b.WriteString("\n")
			}
		}
		var prev pdf.Text
		for j, t := range line.runs {
			if j > 0 {
				b.WriteString(gapSpacing(prev, t))
			}
			b.WriteString(t.S)
			prev = t
		}
	}
	return strings.TrimSpace(b.String())
}

// line is one visual row of a page.
type line struct {
	runs     []pdf.Text
	y        float64
	size     float64 // the largest type size on the row
	gapAbove float64 // distance from the row above
}

// groupLines buckets runs into visual rows, top of the page first and left to
// right within each row.
func groupLines(runs []pdf.Text) []line {
	sorted := make([]pdf.Text, 0, len(runs))
	for _, t := range runs {
		if t.S != "" {
			sorted = append(sorted, t)
		}
	}
	if len(sorted) == 0 {
		return nil
	}
	// Y grows upwards in PDF space, so descending Y is top-down.
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Y > sorted[j].Y })

	var lines []line
	cur := line{runs: []pdf.Text{sorted[0]}, y: sorted[0].Y, size: sorted[0].FontSize}
	for _, t := range sorted[1:] {
		// Compare against the row's own position rather than the previous run,
		// so a row cannot drift downwards one small step at a time.
		if abs(t.Y-cur.y) <= tolerance(max(cur.size, t.FontSize)) {
			cur.runs = append(cur.runs, t)
			cur.size = max(cur.size, t.FontSize)
			continue
		}
		lines = append(lines, cur)
		cur = line{runs: []pdf.Text{t}, y: t.Y, size: t.FontSize, gapAbove: abs(t.Y - lines[len(lines)-1].y)}
	}
	lines = append(lines, cur)

	for i := range lines {
		l := lines[i].runs
		sort.SliceStable(l, func(a, b int) bool { return l[a].X < l[b].X })
	}
	return lines
}

// gapSpacing turns the horizontal distance between two runs back into
// whitespace: nothing when they touch, a space at word distance, and a run of
// spaces when they sit in different table columns.
func gapSpacing(prev, next pdf.Text) string {
	gap := next.X - (prev.X + prev.W)
	size := max(prev.FontSize, next.FontSize)
	if size <= 0 {
		size = 10
	}

	// Text that already carries its own spacing needs none added.
	if strings.HasSuffix(prev.S, " ") || strings.HasPrefix(next.S, " ") {
		return ""
	}
	switch {
	case gap < size*0.18:
		return "" // touching, or slightly overlapping as bold text often does
	case gap < size*1.5:
		return " "
	default:
		return "   " // different table columns, not adjacent words
	}
}

// tolerance is how far apart two runs may sit vertically and still count as the
// same line. It scales with the type size, because a heading's baseline wobble
// is larger than body text's.
func tolerance(size float64) float64 {
	if size <= 0 {
		return 2
	}
	return size * 0.35
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
