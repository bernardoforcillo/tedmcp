package doctext

import (
	"html"
	"strings"
)

// HTMLToText renders a web page as the text a reader would see.
//
// Procurement portals publish a good deal of what matters as ordinary pages —
// the procedure summary, the list of allegati, the clarifications — and the
// markup around it is navigation, not content. Passing raw HTML on would spend
// most of a caller's attention on script tags.
func HTMLToText(data []byte) string {
	s := string(data)
	s = dropElements(s, "script", "style", "head", "noscript", "svg")
	s = breakAt(s, htmlBlockTags...)
	return collapse(html.UnescapeString(stripTags(s)))
}

// XMLToText returns the character data of an XML document, tags removed.
func XMLToText(data []byte) string {
	return collapse(html.UnescapeString(stripTags(string(data))))
}

// htmlBlockTags are the elements that end a line when a page is read as text.
var htmlBlockTags = []string{
	"p", "div", "br", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6",
	"table", "section", "article", "header", "footer", "blockquote", "pre", "option",
}

// dropElements removes whole elements, content and all.
func dropElements(s string, names ...string) string {
	for _, name := range names {
		for {
			start := indexTag(s, name)
			if start < 0 {
				break
			}
			end := indexFold(s[start:], "</"+name)
			if end < 0 {
				s = s[:start] // unclosed: the rest of the document is inside it
				break
			}
			rest := s[start+end:]
			if gt := strings.IndexByte(rest, '>'); gt >= 0 {
				s = s[:start] + rest[gt+1:]
			} else {
				s = s[:start]
				break
			}
		}
	}
	return s
}

// breakAt marks the boundaries of block elements with a newline, so a list of
// allegati does not come back as one run-on line.
func breakAt(s string, names ...string) string {
	var b strings.Builder
	b.Grow(len(s))

	for i := 0; i < len(s); {
		c := s[i]
		if c != '<' {
			b.WriteByte(c)
			i++
			continue
		}
		end := strings.IndexByte(s[i:], '>')
		if end < 0 {
			b.WriteString(s[i:])
			break
		}
		tag := s[i : i+end+1]
		if isOneOf(tagName(tag), names) {
			b.WriteString("\n")
		}
		b.WriteString(tag)
		i += end + 1
	}
	return b.String()
}

// stripTags removes markup, leaving character data. Comments and CDATA
// sections are handled explicitly: a naive scan for '>' would end a comment at
// the first one inside it.
func stripTags(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	for i := 0; i < len(s); {
		c := s[i]
		if c != '<' {
			b.WriteByte(c)
			i++
			continue
		}
		switch {
		case strings.HasPrefix(s[i:], "<!--"):
			if end := strings.Index(s[i:], "-->"); end >= 0 {
				i += end + 3
			} else {
				i = len(s)
			}
		case strings.HasPrefix(s[i:], "<![CDATA["):
			rest := s[i+len("<![CDATA["):]
			if end := strings.Index(rest, "]]>"); end >= 0 {
				b.WriteString(rest[:end])
				i += len("<![CDATA[") + end + 3
			} else {
				b.WriteString(rest)
				i = len(s)
			}
		default:
			if end := strings.IndexByte(s[i:], '>'); end >= 0 {
				i += end + 1
			} else {
				// An unclosed '<' in running text is a less-than sign.
				b.WriteByte(c)
				i++
			}
		}
	}
	return b.String()
}

// collapse reduces runs of whitespace to single spaces within a line and drops
// blank lines, while keeping the line breaks that structure carries.
func collapse(s string) string {
	lines := strings.Split(normalizeText(s), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if f := strings.Join(strings.Fields(line), " "); f != "" {
			out = append(out, f)
		}
	}
	return strings.Join(out, "\n")
}

// tagName returns the element name of a tag, without its attributes or closing
// slash, lower-cased.
func tagName(tag string) string {
	t := strings.TrimPrefix(strings.TrimSuffix(strings.TrimPrefix(tag, "<"), ">"), "/")
	t = strings.TrimSuffix(t, "/")
	if i := strings.IndexAny(t, " \t\r\n"); i >= 0 {
		t = t[:i]
	}
	return strings.ToLower(t)
}

// indexTag finds where an element with this name opens.
func indexTag(s, name string) int {
	from := 0
	for {
		i := indexFold(s[from:], "<"+name)
		if i < 0 {
			return -1
		}
		at := from + i
		// "<script" must not match "<scriptish": the next character has to end
		// the name.
		if next := at + 1 + len(name); next >= len(s) || s[next] == '>' || s[next] == ' ' ||
			s[next] == '\t' || s[next] == '\n' || s[next] == '\r' || s[next] == '/' {
			return at
		}
		from = at + 1
	}
}

func indexFold(s, substr string) int {
	return strings.Index(strings.ToLower(s), strings.ToLower(substr))
}

func isOneOf(v string, list []string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
