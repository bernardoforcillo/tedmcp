package ted

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"regexp"
	"strings"
)

// AwardCriterion is one scoring criterion of the tender offer, as published
// inside a notice's eForms XML. Buyers are not obliged to publish the scoring
// grid in the notice — many point to the disciplinare di gara instead — but
// when they do, this is the only machine-readable place it appears.
type AwardCriterion struct {
	Type        string `json:"type,omitempty" jsonschema:"quality, cost, or price"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Weight      string `json:"weight,omitempty" jsonschema:"points or percentage assigned to this criterion, when published"`
	Lang        string `json:"lang,omitempty" jsonschema:"language the criterion is written in, as declared by the notice (ISO 639-2/T, e.g. ITA)"`
}

// Text reports the criterion's name and description as one searchable string.
func (a AwardCriterion) Text() string {
	return strings.TrimSpace(a.Name + " " + a.Description)
}

// ContentText is a single text node of the eForms XML together with the
// element it came from and the language it is written in. It is what free-text
// term matching runs over.
type ContentText struct {
	Element string
	Text    string
	Lang    string
}

// NoticeContent is the substantive procurement content parsed out of a notice's
// eForms XML: award criteria, descriptions, strategic-procurement flags and the
// links to the buyer's own tender documents.
type NoticeContent struct {
	AwardCriteria []AwardCriterion `json:"award_criteria,omitempty"`
	Descriptions  []string         `json:"descriptions,omitempty"`
	Strategic     []string         `json:"strategic_procurement,omitempty" jsonschema:"eForms strategic-procurement codes, e.g. env-imp for environmental impact"`
	DocumentURLs  []string         `json:"document_urls,omitempty" jsonschema:"buyer platform URLs where the capitolato and disciplinare are published"`
	Language      string           `json:"language,omitempty" jsonschema:"the notice's own language (ISO 639-2/T), used when a text node declares none"`

	// Texts holds every text node of the document, for term matching.
	Texts []ContentText `json:"-"`
}

// minTextLen skips the short codes and identifiers that make up most of an
// eForms document, keeping the text nodes that can carry meaning.
const minTextLen = 4

// ParseEForms extracts the procurement content from a notice's eForms XML.
//
// The parser is deliberately forgiving: it walks the token stream and picks out
// the elements that matter by local name, ignoring the namespace layering and
// the many optional blocks eForms allows.
func ParseEForms(data []byte) (*NoticeContent, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity

	out := &NoticeContent{}
	var (
		depth     int
		cur       xml.StartElement
		haveCur   bool
		crit      *AwardCriterion
		critDepth int
		paramList string // listName of the most recent ParameterCode element
	)

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse eForms XML: %w", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			cur, haveCur = t, true
			if t.Name.Local == "SubordinateAwardingCriterion" && crit == nil {
				crit, critDepth = &AwardCriterion{}, depth
			}

		case xml.EndElement:
			if crit != nil && depth == critDepth && t.Name.Local == "SubordinateAwardingCriterion" {
				if crit.Text() != "" {
					out.AwardCriteria = append(out.AwardCriteria, *crit)
				}
				crit, paramList = nil, ""
			}
			depth--
			haveCur = false

		case xml.CharData:
			if !haveCur {
				continue
			}
			text := cleanText(string(t))
			if text == "" {
				continue
			}
			out.absorb(cur, text, crit, &paramList)
		}
	}

	// A text node that declares no language belongs to the notice's own.
	for i := range out.Texts {
		if out.Texts[i].Lang == "" {
			out.Texts[i].Lang = out.Language
		}
	}
	for i := range out.AwardCriteria {
		if out.AwardCriteria[i].Lang == "" {
			out.AwardCriteria[i].Lang = out.Language
		}
	}

	out.AwardCriteria = dedupeCriteria(out.AwardCriteria)
	out.Descriptions = dedupe(out.Descriptions)
	out.Strategic = dedupe(out.Strategic)
	out.DocumentURLs = dedupe(out.DocumentURLs)
	return out, nil
}

// absorb files one text node under the right part of the content, based on the
// element it belongs to.
func (c *NoticeContent) absorb(elem xml.StartElement, text string, crit *AwardCriterion, paramList *string) {
	name := elem.Name.Local
	lang := normalizeLang(attr(elem, "languageID"))

	switch name {
	case "NoticeLanguageCode":
		c.Language = normalizeLang(text)
	case "Description":
		if crit != nil {
			crit.Description = joinText(crit.Description, text)
			crit.Lang = firstLang(crit.Lang, lang)
		} else {
			c.Descriptions = append(c.Descriptions, text)
		}
	case "Name":
		if crit != nil {
			crit.Name = joinText(crit.Name, text)
			crit.Lang = firstLang(crit.Lang, lang)
		}
	case "AwardingCriterionTypeCode":
		if crit != nil {
			crit.Type = text
		}
	case "ParameterCode":
		*paramList = attr(elem, "listName")
	case "ParameterNumeric":
		// Criteria carry several numbers (weight, minimum score, fixed value);
		// only the one flagged as a weight is the criterion's score.
		if crit != nil && *paramList == "number-weight" {
			crit.Weight = text
		}
	case "ProcurementTypeCode":
		if attr(elem, "listName") == "strategic-procurement" {
			c.Strategic = append(c.Strategic, text)
		}
	case "URI", "EndpointID", "WebsiteURI":
		if strings.HasPrefix(text, "http") {
			c.DocumentURLs = append(c.DocumentURLs, text)
		}
	}

	if len([]rune(text)) >= minTextLen {
		c.Texts = append(c.Texts, ContentText{Element: name, Text: text, Lang: lang})
	}
}

// normalizeLang puts a language code in the upper-case ISO 639-2/T form eForms
// uses ("ITA", "BUL"), so codes from attributes and elements compare equal.
func normalizeLang(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

func firstLang(existing, add string) string {
	if existing != "" {
		return existing
	}
	return add
}

// lineHyphen matches a hyphen used to break a word across lines, which buyers
// paste in from word processors: "An-\nsprechpartner".
var lineHyphen = regexp.MustCompile(`(\p{L})-[ \t]*[\r\n]+[ \t]*(\p{L})`)

// cleanText normalises a text node so that matching sees words, not layout.
//
// TED double-escapes entities, so a decoded node still holds things like
// "dell&#8217;infanzia"; whitespace wraps arbitrarily; and a word broken across
// lines keeps its hyphen. That last one is not cosmetic: leaving it in place
// turns "Ansprechpartner" into a token that starts with "sprech", which is how
// a search for the Italian "spreco" ends up matching German boilerplate.
func cleanText(s string) string {
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "­", "") // soft hyphen
	s = lineHyphen.ReplaceAllString(s, "$1$2")
	return strings.Join(strings.Fields(s), " ")
}

func joinText(existing, add string) string {
	if existing == "" {
		return add
	}
	return existing + " " + add
}

func attr(e xml.StartElement, name string) string {
	for _, a := range e.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// dedupeCriteria drops repeats, which are common because a multi-lot notice
// restates the same scoring grid for every lot.
func dedupeCriteria(in []AwardCriterion) []AwardCriterion {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[AwardCriterion]struct{}, len(in))
	out := make([]AwardCriterion, 0, len(in))
	for _, c := range in {
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	return out
}
