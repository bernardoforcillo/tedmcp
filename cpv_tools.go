package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/bernardoforcillo/tedmcp/internal/cpv"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- lookup_cpv ---

type LookupCPVInput struct {
	Text      string   `json:"text,omitempty" jsonschema:"words describing the work, in any EU language — \"raccolta rifiuti\", \"waste collection\", \"Straßenreinigung\". Every word must appear in a code's name, so two words narrow and one word explores"`
	Code      string   `json:"code,omitempty" jsonschema:"look one code up instead, e.g. 90511000 or 90511000-2, to see what it means and which families it sits under"`
	Languages []string `json:"languages,omitempty" jsonschema:"restrict the search to these languages (ISO 639-1, e.g. [\"it\",\"en\"]). With none, all 24 official languages are searched and every hit says which one it matched in"`
	Limit     int      `json:"limit,omitempty" jsonschema:"how many individual codes to list (default 25). The family roll-up is never truncated"`
}

type LookupCPVOutput struct {
	Query     string          `json:"query,omitempty"`
	Entry     *cpv.Entry      `json:"entry,omitempty" jsonschema:"the code that was looked up, when a code was given"`
	Ancestors []cpv.Entry     `json:"ancestors,omitempty" jsonschema:"the broader codes it sits under, narrowest first"`
	Matches   []cpv.Match     `json:"matches,omitempty"`
	Families  []cpv.Family    `json:"families,omitempty" jsonschema:"the families the matches fall into, with the value to pass as a cpv filter. Prefer these over the individual codes"`
	Total     int             `json:"total,omitempty" jsonschema:"codes that matched, before the limit"`
	Words     []cpv.WordCount `json:"words,omitempty" jsonschema:"when nothing matched, how many codes carry each word alone"`
	Note      string          `json:"note,omitempty"`
}

const defaultCPVLimit = 25

func handleLookupCPV() func(context.Context, *mcp.CallToolRequest, LookupCPVInput) (*mcp.CallToolResult, LookupCPVOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, in LookupCPVInput) (*mcp.CallToolResult, LookupCPVOutput, error) {
		text, code := strings.TrimSpace(in.Text), strings.TrimSpace(in.Code)

		if code != "" {
			entry, ancestors, err := cpv.Get(code)
			if err != nil {
				return errorResult(err), LookupCPVOutput{}, nil
			}
			out := LookupCPVOutput{Query: code, Entry: &entry, Ancestors: ancestors}
			// The family is what a search should usually be expressed as, so
			// hand it over rather than leaving it to be worked out.
			out.Families = []cpv.Family{{
				Prefix: entry.Code[:3],
				Filter: entry.Code[:3] + "*",
				Label:  ancestorLabel(ancestors, in.Languages),
			}}
			return textResult(formatLookupCPV(out)), out, nil
		}

		if text == "" {
			return errorResult(fmt.Errorf("give either text to search for, or a code to look up")), LookupCPVOutput{}, nil
		}

		limit := clamp(in.Limit, defaultCPVLimit, 1, 200)
		res, err := cpv.Search(text, in.Languages, limit)
		if err != nil {
			return errorResult(err), LookupCPVOutput{}, nil
		}

		out := LookupCPVOutput{
			Query:    text,
			Matches:  res.Matches,
			Families: res.Families,
			Total:    res.Total,
			Words:    res.Words,
		}
		switch {
		case res.Total == 0:
			out.Note = fmt.Sprintf("No code's name carries all of those words, across %d codes. That is about the "+
				"words, not about tenders: CPV names work in the language of a contract, not of a trade. %s"+
				"Search the rarest word on its own, or try the term a contract would use. "+
				"Do not conclude anything about what tenders exist from this.",
				res.Searched, wordAdvice(res.Words))
		case len(res.Families) == 1:
			out.Note = "One family covers every match: search it with cpv=" + res.Families[0].Filter + "."
		default:
			out.Note = "Search the families, not the individual codes: TED matches child codes automatically, so a " +
				"3-digit family is both broader and more likely to be right than the leaves that happened to match a word."
		}
		return textResult(formatLookupCPV(out)), out, nil
	}
}

// wordAdvice turns the per-word counts into the sentence that actually unblocks
// someone: which of their words the vocabulary knows, and how narrowly.
func wordAdvice(words []cpv.WordCount) string {
	if len(words) == 0 {
		return ""
	}
	var parts []string
	for _, w := range words {
		switch w.Codes {
		case 0:
			parts = append(parts, fmt.Sprintf("%q is in no code at all", w.Word))
		default:
			parts = append(parts, fmt.Sprintf("%q is in %d", w.Word, w.Codes))
		}
	}
	return "Taken separately: " + strings.Join(parts, ", ") + ". "
}

func ancestorLabel(ancestors []cpv.Entry, langs []string) string {
	for _, a := range ancestors {
		if a.Level <= 2 {
			label, _ := a.Label(langs...)
			return label
		}
	}
	return ""
}

// formatLookupCPV puts the families first, because they are the answer: the
// individual codes are evidence for them.
func formatLookupCPV(out LookupCPVOutput) string {
	var b strings.Builder

	if out.Entry != nil {
		label, lang := out.Entry.Label()
		fmt.Fprintf(&b, "CPV %s — %s (%s)\n", out.Entry.Code, label, lang)
		fmt.Fprintf(&b, "level: %s\n", cpvLevel(out.Entry.Level))
		if len(out.Ancestors) > 0 {
			b.WriteString("\nsits under:\n")
			for _, a := range out.Ancestors {
				l, _ := a.Label()
				fmt.Fprintf(&b, "  %s  %s\n", a.Code, l)
			}
		}
		if len(out.Families) > 0 && out.Families[0].Filter != "" {
			fmt.Fprintf(&b, "\nto search the whole family: cpv=%s\n", out.Families[0].Filter)
		}
		return strings.TrimRight(b.String(), "\n")
	}

	fmt.Fprintf(&b, "%q — %d code(s) matched.\n", out.Query, out.Total)

	if len(out.Families) > 0 {
		b.WriteString("\nfamilies to search (pass as cpv):\n")
		for _, f := range out.Families {
			fmt.Fprintf(&b, "  %-6s %3d match(es)  %s\n", f.Filter, f.Matches, f.Label)
		}
	}

	if len(out.Matches) > 0 {
		fmt.Fprintf(&b, "\ncodes (%d shown):\n", len(out.Matches))
		for _, m := range out.Matches {
			fmt.Fprintf(&b, "  %s  [%s]  %s (%s)\n", m.Code, cpvLevel(m.Level), truncate(m.Label, 90), m.Language)
		}
	}

	if len(out.Words) > 0 {
		b.WriteString("\neach word on its own:\n")
		for _, w := range out.Words {
			fmt.Fprintf(&b, "  %-24s %d code(s)\n", w.Word, w.Codes)
		}
	}

	if out.Note != "" {
		fmt.Fprintf(&b, "\nnote: %s\n", out.Note)
	}
	return strings.TrimRight(b.String(), "\n")
}

func cpvLevel(level int) string {
	switch level {
	case 1:
		return "division"
	case 2:
		return "group"
	case 3:
		return "class"
	case 4:
		return "category"
	}
	return "code"
}
