package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The tools say what this server can fetch. The prompts below say how the work
// is actually done, which is a different kind of knowledge and belongs in a
// different place.
//
// They live in the server rather than in one client's own format because a
// bid office does not use one client. Prompts travel with the server: whatever
// speaks MCP gets them, and a workflow written once does not have to be
// rewritten when someone opens a different application.
//
// None of them names a sector. What a company does, what it holds and where it
// bids arrives as an argument, so one server serves an office with forty
// clients across forty industries. The moment a CPV family or a certification
// is written into this file, it stops being reusable and starts being one
// customer's configuration — the same rule internal/match already keeps for
// search terms, applied to procedure.

func registerPrompts(s *mcp.Server) {
	s.AddPrompt(&mcp.Prompt{
		Name:        "scouting",
		Title:       "Find tenders matching a company profile",
		Description: "Turn a description of what a company does into a searched, filtered shortlist of open tenders. Use at the start of a scouting round, once per client.",
		Arguments: []*mcp.PromptArgument{
			{Name: "profile", Description: "What the company does, in plain words: activities, products, services, the kind of contract it wants. Certifications and size can go in too.", Required: true},
			{Name: "countries", Description: "Buyer countries as ISO 3166-1 alpha-3, comma separated (default ITA)."},
			{Name: "days", Description: "Only tenders whose deadline is at least this many days away, so there is time to prepare a bid (default 15)."},
		},
	}, promptScouting)

	s.AddPrompt(&mcp.Prompt{
		Name:        "qualifica",
		Title:       "Decide whether a company can bid at all",
		Description: "Go/no-go on one tender: pull the participation requirements out of the tender documents and test them against what the company holds. Use before spending any effort on a bid.",
		Arguments: []*mcp.PromptArgument{
			{Name: "publication_number", Description: "TED publication number, e.g. 442511-2026.", Required: true},
			{Name: "holds", Description: "What the company can prove: turnover, comparable contracts, certifications, licences, staff, qualification categories. Anything missing here will be reported as unknown rather than assumed."},
		},
	}, promptQualifica)

	s.AddPrompt(&mcp.Prompt{
		Name:        "analisi",
		Title:       "Work out what winning a tender requires",
		Description: "Read one tender's documents and set out the scoring grid, what earns each point, and where the bid is won or lost. Use after qualifica returns a go.",
		Arguments: []*mcp.PromptArgument{
			{Name: "publication_number", Description: "TED publication number, e.g. 442511-2026.", Required: true},
			{Name: "focus", Description: "Optional: a theme to pay particular attention to, e.g. an environmental criterion or a staffing clause."},
		},
	}, promptAnalisi)
}

// honesty is repeated into every prompt because it is the one instruction that
// must survive being read in a hurry. Each of these failures produces an answer
// that looks like a finding and is not one.
const honesty = `Three results mean "not known", never "not there", and must be reported as gaps:

- robots-denied or captcha: the portal serves people, not programs. The documents
  exist and are public. Give the URL to open by hand; never write that a tender has
  no documents.
- no-text-layer: the file is a scan of paper. It says something; this server cannot
  read it without OCR. Never fold it into a count of zero matches.
- a notice or document that could not be downloaded at all: it was not searched.

Both scan tools list these separately from what they searched. Carry that separation
into your answer — a shortlist that hides it is worse than no shortlist, because the
reader cannot tell which entries were actually checked.`

func promptScouting(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	args := promptArgs(req)
	profile := strings.TrimSpace(args["profile"])
	if profile == "" {
		return nil, fmt.Errorf("profile is required: describe what the company does")
	}
	countries := firstNonEmpty(strings.TrimSpace(args["countries"]), "ITA")
	days := firstNonEmpty(strings.TrimSpace(args["days"]), "15")

	text := fmt.Sprintf(`Find the open tenders this company could bid for, and end with a shortlist short enough to act on.

COMPANY
%s

Buyer countries: %s
Only tenders whose deadline is at least %s days away.

Work in this order. Each step is cheaper than the one after it, so do not skip ahead.

1. CPV FAMILIES. Turn the profile into CPV families and write down which ones you
   chose and why, before searching. Choose at 3 digits, not 8: TED matches child
   codes automatically, so a full code is almost always too narrow, and a wrong or
   over-narrow family returns nothing — which reads exactly like "there are no
   tenders". This is the single most likely way this whole exercise fails silently.
   List the families in your answer so a person who knows the sector can correct you.

2. SEARCH. search_tenders with those families, the countries, notice_type
   cn-standard, and a deadline_from that respects the window above. This is the
   cheap, broad pass; do not put the company's requirements in "keywords" — TED's
   full-text index covers only the title and a short description, so a requirement
   searched that way is invisible.

3. SEARCH INSIDE THE NOTICES. scan_tenders over that selection, with
   terms_by_language rather than one multilingual pattern, and "near" for
   requirements written compositionally rather than as a set phrase. Terms are then
   only ever tested against the language a notice declares. Read uncovered_languages
   in the result: those notices were downloaded but not really searched.

4. SEARCH INSIDE THE DOCUMENTS, for survivors only. scan_tender_documents on the
   handful worth the cost. It downloads each buyer's capitolato and disciplinare —
   many requests to a small municipal server — so it takes a few tenders per call,
   not a page of them. This is the step that turns "the notice does not mention it"
   into an answer.

SHORTLIST. One row per tender, ordered by how well it fits:

  buyer · deadline with the time of day · value · CPV · lots
  why it matched — quote the criterion or passage, with the file it came from
  what is still unknown

The time of day is not decoration: submission deadlines are often 08:00 or 12:00,
and a bid planned for "that Friday" is a bid delivered late.

%s

Say plainly how many tenders you looked at, how many you could not check, and what
would widen the search: another CPV family, another language's terms, a longer
window. If nothing matched, before reporting that, check the family choice in step 1
again, then widen to fewer digits, drop keywords, and confirm the date window and
scope are not doing the excluding.

Answer in the language the request was written in.`, profile, countries, days, honesty)

	return &mcp.GetPromptResult{
		Description: "Scouting round for a company profile",
		Messages:    []*mcp.PromptMessage{userMessage(text)},
	}, nil
}

func promptQualifica(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	args := promptArgs(req)
	pn := strings.TrimSpace(args["publication_number"])
	if pn == "" {
		return nil, fmt.Errorf("publication_number is required, e.g. 442511-2026")
	}
	holds := strings.TrimSpace(args["holds"])
	if holds == "" {
		holds = "(not stated — treat every requirement as unverified and list what the company would have to confirm)"
	}

	text := fmt.Sprintf(`Decide whether this company can take part in tender %s at all, before anyone writes a word of the bid.

WHAT THE COMPANY CAN PROVE
%s

1. get_tender_dossier for %s: buyer, the deadline with its time of day, lots with
   their values and CPV, and any award criteria the notice publishes.

2. fetch_tender_documents for the same number. The conditions for taking part are
   almost never in the notice — they are in the disciplinare di gara, and the notice
   only points at the page that holds it.

3. Pull out the conditions. Nearly every procurement states the same kinds, whatever
   the sector, so look for each and say plainly when one is absent:

   - grounds for exclusion, and any the company would trip
   - professional standing: registrations, licences, authorisations, register entries
   - economic standing: turnover overall and in the field, insurance, bank references
   - technical standing: comparable contracts in a stated period, certifications,
     staff, equipment, qualification categories and classes
   - guarantees and fees: bid bond, performance bond, any authority fee
   - dates other than the deadline: site visit, deadline for questions, validity of
     the offer
   - how a shortfall may be covered: consortium or joint bid, reliance on another
     firm's capacity, subcontracting, and any cap on them

4. VERDICT, in one line first: can bid / cannot bid / can bid only jointly or by
   relying on another firm's capacity.

   Then the reasoning as a table: requirement, what the tender demands, what the
   company holds, and met / not met / unknown. Quote each demand in the tender's own
   words and name the file it came from — a paraphrased requirement is one nobody can
   check against the source.

   Never infer a requirement from what is usual. If the disciplinare does not say it,
   it is not a requirement; if a document could not be read, the verdict is
   provisional and must say which file would settle it.

%s

Finish with the earliest date something has to happen — the site visit or the
question deadline usually falls well before the submission date, and missing it can
end the matter regardless of the verdict above.

Answer in the language the request was written in.`, pn, holds, pn, honesty)

	return &mcp.GetPromptResult{
		Description: "Go/no-go on tender " + pn,
		Messages:    []*mcp.PromptMessage{userMessage(text)},
	}, nil
}

func promptAnalisi(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	args := promptArgs(req)
	pn := strings.TrimSpace(args["publication_number"])
	if pn == "" {
		return nil, fmt.Errorf("publication_number is required, e.g. 442511-2026")
	}

	focus := ""
	if f := strings.TrimSpace(args["focus"]); f != "" {
		focus = fmt.Sprintf("\n\nPay particular attention to: %s. Report what the documents say about it verbatim, including saying that they say nothing.\n", f)
	}

	text := fmt.Sprintf(`Work out what winning tender %s actually requires.
%s
1. get_tender_dossier for %s, then fetch_tender_documents. A notice is not obliged to
   publish its scoring grid and many defer to the disciplinare, so the notice alone
   will usually give you an incomplete picture — and an incomplete grid presented as
   the grid is the most expensive mistake available here.

2. THE SCORING GRID. Every criterion, as a table: name, points, and whether the
   points are awarded on a stated scale or left to the panel's judgement. Add up the
   technical points and the price points and state the split. If the total does not
   match what the documents claim, say so rather than reconciling it quietly.

3. WHAT EARNS THE POINTS. For each criterion that carries real weight, quote what the
   document says the bidder must offer or demonstrate, and name the file. This is the
   part that turns a grid into a plan.

4. THE CONTRACT. Base amount and how the price is scored, duration, renewals and
   options, penalties, staffing obligations including any duty to take on the
   outgoing contractor's staff, and anything that constrains how the work is done.

5. WHERE IT IS WON. The three criteria with the best ratio of points to effort, and
   for each, what would have to be true of the bid. Then the opposite: any criterion
   or clause that looks like a disqualifier.

%s

If the notice publishes no grid and the disciplinare could not be retrieved, that is
the finding: say the grid is not available to this program, give the URL to open, and
do not reconstruct it from what similar tenders usually do.

Answer in the language the request was written in.`, pn, focus, pn, honesty)

	return &mcp.GetPromptResult{
		Description: "What winning tender " + pn + " requires",
		Messages:    []*mcp.PromptMessage{userMessage(text)},
	}, nil
}

func userMessage(text string) *mcp.PromptMessage {
	return &mcp.PromptMessage{Role: "user", Content: &mcp.TextContent{Text: text}}
}

// promptArgs returns the arguments a client supplied, tolerating none at all.
func promptArgs(req *mcp.GetPromptRequest) map[string]string {
	if req == nil || req.Params == nil {
		return nil
	}
	return req.Params.Arguments
}
