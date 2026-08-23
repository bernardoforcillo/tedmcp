package main

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func lookupCPV(t *testing.T, in LookupCPVInput) (LookupCPVOutput, string) {
	t.Helper()
	res, out, err := handleLookupCPV()(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool reported an error: %v", res.Content)
	}
	return out, renderedText(t, res)
}

func TestLookupCPVReturnsTheFamilyToSearch(t *testing.T) {
	out, text := lookupCPV(t, LookupCPVInput{Text: "raccolta rifiuti"})

	if out.Total == 0 {
		t.Fatal("no codes matched")
	}
	var filter string
	for _, f := range out.Families {
		if f.Prefix == "905" {
			filter = f.Filter
		}
	}
	if filter != "905*" {
		t.Errorf("expected the 905* family, got %+v", out.Families)
	}
	// The rendered answer has to hand over the value to paste into a search;
	// a list of codes the reader must roll up by hand is half a tool.
	if !strings.Contains(text, "905*") {
		t.Errorf("the filter value is not in the rendered result:\n%s", text)
	}
	if !strings.Contains(text, "families to search") {
		t.Errorf("families should lead the output:\n%s", text)
	}
}

func TestLookupCPVExplainsOneCode(t *testing.T) {
	out, text := lookupCPV(t, LookupCPVInput{Code: "90511000-2"})

	if out.Entry == nil || out.Entry.Code != "90511000" {
		t.Fatalf("entry = %+v, want 90511000 (the check digit is not part of the code)", out.Entry)
	}
	if len(out.Ancestors) == 0 {
		t.Error("a code should come back with what it sits under")
	}
	if !strings.Contains(text, "sits under") || !strings.Contains(text, "90000000") {
		t.Errorf("the ancestry is not rendered:\n%s", text)
	}
}

func TestLookupCPVZeroHitsBlamesTheWordsNotTheTenders(t *testing.T) {
	out, _ := lookupCPV(t, LookupCPVInput{Text: "zzzzqqqq"})

	if out.Total != 0 {
		t.Fatalf("expected no matches, got %d", out.Total)
	}
	// The dangerous reading of an empty CPV lookup is "there is no such work".
	// The note has to close that door and suggest what to do instead.
	for _, want := range []string{"try", "language"} {
		if !strings.Contains(strings.ToLower(out.Note), want) {
			t.Errorf("note should suggest rephrasing (%q missing): %q", want, out.Note)
		}
	}
	if !strings.Contains(out.Note, "Do not conclude anything about what tenders exist") {
		t.Errorf("note must not let an empty lookup read as an absence of tenders: %q", out.Note)
	}
}

func TestLookupCPVZeroHitsSaysWhichWordIsTheProblem(t *testing.T) {
	// The phrase reads naturally and is in no code: CPV files this work under
	// the maintenance of parks. What unblocks the reader is seeing that one of
	// their words is everywhere and the other nowhere.
	out, text := lookupCPV(t, LookupCPVInput{Text: "manutenzione verde", Languages: []string{"it"}})

	if out.Total != 0 {
		t.Fatalf("expected no match, got %d", out.Total)
	}
	if len(out.Words) != 2 {
		t.Fatalf("expected a count for each word, got %+v", out.Words)
	}
	// Rarest first: that is the one worth searching on its own.
	if out.Words[0].Codes > out.Words[1].Codes {
		t.Errorf("word counts should be rarest first, got %+v", out.Words)
	}
	if out.Words[1].Codes < 50 {
		t.Errorf("%q should be a common word, found in %d codes", out.Words[1].Word, out.Words[1].Codes)
	}
	if !strings.Contains(text, "each word on its own") {
		t.Errorf("the per-word counts are not rendered:\n%s", text)
	}
}

// A search that finds nothing must not be widened into one that finds the wrong
// thing. Over 9,454 short official phrases, an either-or search returns noise
// however it is ranked, and noise presented as an answer is the failure this
// whole server is built to avoid.
func TestLookupCPVDoesNotGuessWhenItCannotMatch(t *testing.T) {
	out, _ := lookupCPV(t, LookupCPVInput{Text: "manutenzione verde", Languages: []string{"it"}})

	if len(out.Matches) != 0 || len(out.Families) != 0 {
		t.Errorf("a failed search must return nothing, not a widened guess: %d matches, %d families",
			len(out.Matches), len(out.Families))
	}
}

func TestLookupCPVNeedsSomething(t *testing.T) {
	res, _, err := handleLookupCPV()(context.Background(), nil, LookupCPVInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Error("a call with neither text nor code must be an error")
	}
}

func TestLookupCPVUnknownCodeIsAToolError(t *testing.T) {
	res, _, err := handleLookupCPV()(context.Background(), nil, LookupCPVInput{Code: "99999999"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Error("an unknown code must be reported, not silently empty")
	}
}

func TestLookupCPVRespectsTheLimit(t *testing.T) {
	out, _ := lookupCPV(t, LookupCPVInput{Text: "software", Limit: 5})

	if len(out.Matches) != 5 {
		t.Errorf("listed %d codes, want 5", len(out.Matches))
	}
	if out.Total <= 5 {
		t.Errorf("total = %d, should report every match, not just the listed ones", out.Total)
	}
	// Truncating the roll-up would hide families entirely, which is the one
	// part of the answer that must stay complete.
	if len(out.Families) < 5 {
		t.Errorf("the family roll-up was truncated: %d families", len(out.Families))
	}
}

// renderedText returns the human-readable text a tool result carries, which is
// what a client actually shows and therefore what these tests check.
func renderedText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) != 1 {
		t.Fatalf("got %d content blocks, want 1", len(res.Content))
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content is %T, want text", res.Content[0])
	}
	return text.Text
}
