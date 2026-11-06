package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Prompts are the team's standard ways of working, packaged so that a junior
// analyst runs the same sequence a senior one would: the tools in the right
// order, with the caveats that keep a result honest stated up front rather than
// remembered afterwards. Clients show them as slash commands or templates.

type promptSpec struct {
	name, title, description string
	args                     []*mcp.PromptArgument
	render                   func(args map[string]string) string
}

var prompts = []promptSpec{
	{
		name:        "go_no_go",
		title:       "Go / no-go assessment",
		description: "Qualify one tender: status, requirements, competition and deadlines, ending in a recommendation.",
		args: []*mcp.PromptArgument{
			{Name: "publication_number", Description: "TED publication number, e.g. 442511-2026", Required: true},
			{Name: "company_profile", Description: "what the bidder has: turnover, certifications, references, regions served"},
		},
		render: func(a map[string]string) string {
			profile := a["company_profile"]
			if profile == "" {
				profile = "(not given — list the requirements as questions for the team instead of judging fit)"
			}
			return fmt.Sprintf(`Assess whether we should bid for TED notice %[1]s.

Our profile: %[2]s

Work through these steps and report each one:
1. get_procedure_history for %[1]s — is this the latest notice? Has the deadline moved, a lot been cancelled, or the procedure already been awarded? Stop and say so if it is closed.
2. get_tender_dossier — buyer, lots, values, award criteria with weights, submission deadline WITH time of day, deadline for questions.
3. extract_requirements — the qualification sheet. For every requirement, quote the value and the file. Keep "not found in the documents read" distinct from "not required", and list the documents that were refused or unreadable.
4. award_history with the notice's CPV family, country and this buyer (buyer=<buyer name>) — who won this buyer's previous tenders, how many bids they drew, typical discount. Then the same without the buyer filter for the market view.
5. Compare the requirements with our profile, requirement by requirement: met / not met / unknown.

Finish with: a recommendation (go / no-go / need information), the three deciding reasons, the open questions to send the buyer before the questions deadline, and the key dates. Offer to add the tender to the shortlist (shortlist_add) with the right status.`, a["publication_number"], profile)
		},
	},
	{
		name:        "competitor_brief",
		title:       "Competitor brief",
		description: "What a competitor wins, where, against whom and at what discount.",
		args: []*mcp.PromptArgument{
			{Name: "competitor", Description: "company name or VAT/company ID as it appears in award notices", Required: true},
			{Name: "cpv", Description: "CPV family, e.g. 555*"},
			{Name: "country", Description: "ISO alpha-3 country code, e.g. ITA"},
		},
		render: func(a map[string]string) string {
			scope := scopeLine(a)
			return fmt.Sprintf(`Brief me on competitor %q%s.

1. award_history with winner=%q%s, published in the last three years. Page through until the total is covered or you have at least 200 notices.
2. Summarise: number of wins and total value, the buyers it wins repeatedly (incumbencies), typical number of rival bids, share of sole-bidder wins, median discount on estimate, regions.
3. forecast_retenders for the same scope and list the contracts where %q is the incumbent and the end falls in the next 18 months — those are where it is defending.
4. Note the limits: discounts are against estimates that may include options; award notices published before eForms (Nov 2023) may lack results.`, a["competitor"], scope, a["competitor"], scopeArgs(a), a["competitor"])
		},
	},
	{
		name:        "retender_pipeline",
		title:       "Re-tender pipeline",
		description: "Contracts in a market expiring soon, with incumbents and when to start watching.",
		args: []*mcp.PromptArgument{
			{Name: "cpv", Description: "CPV family, e.g. 555*", Required: true},
			{Name: "country", Description: "ISO alpha-3 country code, e.g. ITA"},
			{Name: "months", Description: "how far ahead to look, in months (default 18)"},
		},
		render: func(a map[string]string) string {
			months := firstNonEmpty(a["months"], "18")
			return fmt.Sprintf(`Build the re-tender pipeline for CPV %s%s over the next %s months.

1. forecast_retenders with cpv=[%q]%s, ends_to set %s months from today, include_pins=true. Page through all award notices (page=2, 3…) — they are cached.
2. Group the contracts by quarter of expected end. For each: buyer, incumbent, value, end date and where it came from (notice / text / derived), renewal options.
3. Report the undated count and say plainly that those contracts are not absent, only undated.
4. List the prior information notices separately: those buyers have announced an upcoming tender.
5. Offer to save a search for each buyer whose contract ends soonest (save_search), so the new contract notice is caught on the day it appears.`,
				a["cpv"], countryLine(a), months, a["cpv"], scopeArgs(map[string]string{"country": a["country"]}), months)
		},
	},
	{
		name:        "cross_border_sweep",
		title:       "Cross-border requirement sweep",
		description: "Find open tenders that ask for a given requirement, across several countries and languages.",
		args: []*mcp.PromptArgument{
			{Name: "requirement", Description: "what to look for, in plain words, e.g. food waste reduction", Required: true},
			{Name: "cpv", Description: "CPV family, e.g. 555*", Required: true},
			{Name: "countries", Description: "comma-separated ISO alpha-3 codes, e.g. ITA,FRA,DEU,ESP"},
			{Name: "term_pack", Description: "an existing term pack to start from"},
		},
		render: func(a map[string]string) string {
			pack := ""
			if a["term_pack"] != "" {
				pack = fmt.Sprintf(" Start from term_pack=%q and extend it.", a["term_pack"])
			}
			return fmt.Sprintf(`Find open tenders (cn-standard, deadline from today) for CPV %s in %s that ask for: %s.

1. Write terms_by_language for every language of those countries — stems and set phrases in each language, never one multilingual regex — and a near pair for compositional phrasings.%s
2. scan_tenders with those terms. Report matches with the award-criterion weight, and treat uncovered_languages as a gap to fix, not a result: add terms and rescan (it is cached).
3. For notices with criteria_published=false, and for the strongest matches, run scan_tender_documents on a few at a time.
4. Report three lists kept apart: tenders that ask for it (with the quote and points), tenders that demonstrably do not, and tenders that could not be checked (refused, scanned PDFs, uncovered languages).
5. If the terms worked, save them with save_term_pack so the team can rerun this sweep.`,
				a["cpv"], firstNonEmpty(a["countries"], "the EU"), a["requirement"], pack)
		},
	},
	{
		name:        "morning_digest",
		title:       "Morning digest",
		description: "What is new across all saved searches, and what on the shortlist needs attention today.",
		render: func(map[string]string) string {
			return `Prepare today's tender digest.

1. run_saved_search with name="*": list what is new under each saved search. For each new notice give buyer, value, deadline and one line on fit.
2. shortlist_list: flag every tender whose submission or questions deadline is within 7 days, and any whose deadline has passed but whose status is still watching, qualifying or bidding.
3. For each shortlisted tender in qualifying or bidding, run get_procedure_history and report any change notice or deadline move since it was added.
4. End with a short action list: who needs to do what today.`
		},
	},
}

func registerPrompts(s *mcp.Server) {
	for _, p := range prompts {
		p := p
		s.AddPrompt(&mcp.Prompt{Name: p.name, Title: p.title, Description: p.description, Arguments: p.args},
			func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
				args := map[string]string{}
				if req != nil && req.Params != nil {
					for k, v := range req.Params.Arguments {
						args[k] = strings.TrimSpace(v)
					}
				}
				for _, a := range p.args {
					if a.Required && args[a.Name] == "" {
						return nil, fmt.Errorf("prompt %s needs the %s argument", p.name, a.Name)
					}
				}
				return &mcp.GetPromptResult{
					Description: p.description,
					Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: p.render(args)}}},
				}, nil
			})
	}
}

func scopeLine(a map[string]string) string {
	var parts []string
	if a["cpv"] != "" {
		parts = append(parts, "CPV "+a["cpv"])
	}
	if a["country"] != "" {
		parts = append(parts, a["country"])
	}
	if len(parts) == 0 {
		return ""
	}
	return " in " + strings.Join(parts, ", ")
}

func countryLine(a map[string]string) string {
	if a["country"] == "" {
		return ""
	}
	return " in " + a["country"]
}

func scopeArgs(a map[string]string) string {
	var parts []string
	if a["cpv"] != "" {
		parts = append(parts, fmt.Sprintf(", cpv=[%q]", a["cpv"]))
	}
	if a["country"] != "" {
		parts = append(parts, fmt.Sprintf(", country=[%q]", a["country"]))
	}
	return strings.Join(parts, "")
}
