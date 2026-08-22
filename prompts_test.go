package main

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func promptRequest(args map[string]string) *mcp.GetPromptRequest {
	return &mcp.GetPromptRequest{Params: &mcp.GetPromptParams{Arguments: args}}
}

// promptText renders a prompt and returns its single message.
func promptText(t *testing.T, h mcp.PromptHandler, args map[string]string) string {
	t.Helper()
	res, err := h(context.Background(), promptRequest(args))
	if err != nil {
		t.Fatalf("prompt returned an error: %v", err)
	}
	if len(res.Messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(res.Messages))
	}
	content, ok := res.Messages[0].Content.(*mcp.TextContent)
	if !ok {
		t.Fatalf("message content is %T, want text", res.Messages[0].Content)
	}
	if res.Messages[0].Role != "user" {
		t.Errorf("role = %q, want user", res.Messages[0].Role)
	}
	return content.Text
}

func TestScoutingPromptCarriesTheProfileAndDefaults(t *testing.T) {
	text := promptText(t, promptScouting, map[string]string{
		"profile": "raccolta e trasporto di rifiuti urbani, con sensoristica sui contenitori",
	})

	if !strings.Contains(text, "sensoristica sui contenitori") {
		t.Error("the company profile must reach the prompt verbatim")
	}
	if !strings.Contains(text, "ITA") {
		t.Error("countries should default to ITA")
	}
	if !strings.Contains(text, "15 days") {
		t.Error("the deadline window should default to 15 days")
	}
	// The CPV step is the one that fails silently, so it has to be first and
	// has to ask for the choice to be shown.
	if !strings.Contains(text, "3 digits") {
		t.Error("the prompt must tell the model to choose CPV families broadly")
	}
}

func TestScoutingPromptRespectsOverrides(t *testing.T) {
	text := promptText(t, promptScouting, map[string]string{
		"profile":   "servizi di pulizia industriale",
		"countries": "ITA,FRA,DEU",
		"days":      "30",
	})

	if !strings.Contains(text, "ITA,FRA,DEU") {
		t.Error("countries were not carried through")
	}
	if !strings.Contains(text, "30 days") {
		t.Error("the window was not carried through")
	}
}

func TestScoutingPromptNeedsAProfile(t *testing.T) {
	if _, err := promptScouting(context.Background(), promptRequest(nil)); err == nil {
		t.Fatal("a scouting round without a profile must be an error, not an empty search")
	}
}

func TestQualificaPromptNamesTheTender(t *testing.T) {
	text := promptText(t, promptQualifica, map[string]string{
		"publication_number": "442511-2026",
		"holds":              "ISO 9001, fatturato 4M, tre servizi analoghi",
	})

	if strings.Count(text, "442511-2026") < 2 {
		t.Error("the publication number must appear where the tools are called")
	}
	if !strings.Contains(text, "ISO 9001") {
		t.Error("what the company holds must reach the prompt verbatim")
	}
	// A requirement nobody can trace to the source is a requirement nobody can
	// check, and inventing one is the expensive failure here.
	if !strings.Contains(text, "name the file") {
		t.Error("the prompt must require each requirement to be sourced")
	}
	if !strings.Contains(text, "Never infer a requirement") {
		t.Error("the prompt must forbid inventing requirements from what is usual")
	}
}

func TestQualificaPromptWithoutStatedCapabilities(t *testing.T) {
	text := promptText(t, promptQualifica, map[string]string{"publication_number": "442511-2026"})

	// Silence about what the company holds must become "unknown", never "met".
	if !strings.Contains(text, "unverified") {
		t.Errorf("an unstated capability must be treated as unverified:\n%s", text)
	}
}

func TestQualificaAndAnalisiNeedAPublicationNumber(t *testing.T) {
	for name, h := range map[string]mcp.PromptHandler{
		"qualifica": promptQualifica,
		"analisi":   promptAnalisi,
	} {
		if _, err := h(context.Background(), promptRequest(nil)); err == nil {
			t.Errorf("%s must require a publication number", name)
		}
	}
}

func TestAnalisiPromptFocusIsOptional(t *testing.T) {
	plain := promptText(t, promptAnalisi, map[string]string{"publication_number": "517698-2026"})
	if strings.Contains(plain, "Pay particular attention") {
		t.Error("no focus was given, so none should be asked for")
	}

	focused := promptText(t, promptAnalisi, map[string]string{
		"publication_number": "517698-2026",
		"focus":              "clausola sociale",
	})
	if !strings.Contains(focused, "clausola sociale") {
		t.Error("the focus was not carried through")
	}
}

func TestEveryPromptCarriesTheHonestyRules(t *testing.T) {
	cases := []struct {
		name string
		h    mcp.PromptHandler
		args map[string]string
	}{
		{"scouting", promptScouting, map[string]string{"profile": "x"}},
		{"qualifica", promptQualifica, map[string]string{"publication_number": "1-2026"}},
		{"analisi", promptAnalisi, map[string]string{"publication_number": "1-2026"}},
	}
	for _, c := range cases {
		text := promptText(t, c.h, c.args)
		for _, rule := range []string{"robots-denied", "no-text-layer", "not searched"} {
			if !strings.Contains(text, rule) {
				t.Errorf("%s: the prompt does not carry the %q rule", c.name, rule)
			}
		}
	}
}

// The prompts exist to be reusable across an office's whole client list. A
// sector written into one of them quietly turns it into one client's setup.
func TestPromptsNameNoSector(t *testing.T) {
	texts := []string{
		promptText(t, promptScouting, map[string]string{"profile": "PROFILE"}),
		promptText(t, promptQualifica, map[string]string{"publication_number": "1-2026"}),
		promptText(t, promptAnalisi, map[string]string{"publication_number": "1-2026"}),
	}
	// Terms from the sectors this server has been used on. None belongs here:
	// what a company does arrives as an argument.
	forbidden := []string{"catering", "ristorazione", "rifiuti", "waste", "cloud", "55500000", "90500000"}

	for i, text := range texts {
		lower := strings.ToLower(text)
		for _, w := range forbidden {
			if strings.Contains(lower, w) {
				t.Errorf("prompt %d names a sector (%q); the profile is an argument, not content", i, w)
			}
		}
	}
}

func TestRegisterPromptsAddsThemToTheServer(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "tedmcp", Version: "test"}, nil)
	registerPrompts(server)

	// Registration is what makes them reachable; a prompt defined and never
	// added is invisible to every client.
	for _, name := range []string{"scouting", "qualifica", "analisi"} {
		if !strings.Contains(serverPromptNames(t, server), name) {
			t.Errorf("prompt %q was not registered", name)
		}
	}
}

// serverPromptNames lists the prompts a server exposes, over the protocol so
// the test sees what a client would.
func serverPromptNames(t *testing.T, server *mcp.Server) string {
	t.Helper()

	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("connect server: %v", err)
	}
	defer serverSession.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	defer clientSession.Close()

	res, err := clientSession.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	var names []string
	for _, p := range res.Prompts {
		names = append(names, p.Name)
	}
	return strings.Join(names, ",")
}
