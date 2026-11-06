// tedmcp — an MCP server that searches TED, the EU public procurement journal.
// Copyright (C) 2026  Bernardo Forcillo
//
// This program is free software: you can redistribute it and/or modify it under
// the terms of the GNU Affero General Public License as published by the Free
// Software Foundation, either version 3 of the License, or (at your option) any
// later version.
//
// This program is distributed in the hope that it will be useful, but WITHOUT
// ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or FITNESS
// FOR A PARTICULAR PURPOSE. See the GNU Affero General Public License for more
// details. You should have received a copy of it along with this program; if
// not, see <https://www.gnu.org/licenses/>.

// Command tedmcp is a Model Context Protocol (MCP) server that exposes the EU
// public procurement journal TED (Tenders Electronic Daily) as tools an AI
// assistant can call: search tenders/procurements, fetch a notice, and download
// a notice's document files.
//
// It speaks MCP over stdio or streamable HTTP, so it can be launched by any MCP
// client (Claude Desktop, Claude Code, etc.) as a subprocess, or reached over
// the network.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bernardoforcillo/tedmcp/internal/store"
	"github.com/bernardoforcillo/tedmcp/internal/ted"
	"github.com/bernardoforcillo/tedmcp/internal/webdoc"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const serverInstructions = `tedmcp searches TED, the European Union's public procurement journal.

Tools:
- search_tenders: find procurement notices by keywords, CPV codes, buyer country,
  notice type, publication date, and submission deadline.
- scan_tenders: search inside many notices at once — downloads their eForms XML in
  parallel and reports matching award criteria (with their weights), plus links to
  the buyer's tender documents.
- get_tender_dossier: one procurement in full — buyer and contacts, exact deadline
  with the time of day, lots, award criteria, and every link labelled by role.
- fetch_tender_documents: download ALL of a tender's documents from the buyer's
  portal — following the procedure page to the capitolato, the disciplinare and the
  allegati — and return their text. Obeys that site's robots.txt.
- scan_tender_documents: search inside those documents, with the same per-language
  terms as scan_tenders. This is where a requirement lives when the notice omits it.
- lookup_anac: join a notice to the Italian national contracts database to get its
  CIG and award outcome, from a locally downloaded ANAC snapshot.
- get_tender: fetch one notice by its publication number (e.g. 521055-2026).
- get_tender_document: download a notice's file (xml full text, or pdf/html link).

For qualifying a tender and knowing the market:
- extract_requirements: the go/no-go sheet — turnover, references, certifications,
  guarantees, site visit, questions deadline and more, each with its value, the
  passage and the file, read from the tender's own documents.
- get_procedure_history: every notice of the same procurement, in order, with the
  deadline moves, change notices and outcome. Check it before investing in a bid.
- award_history: who wins which tenders, against how many bids, at what discount;
  ranks competitors and profiles buyers from award notices.
- forecast_retenders: contracts ending soon (from award notices' durations), with
  the incumbent — the earliest a lead can be known.

For working as a team (stored on the server, shared by everyone using it):
- save_search / run_saved_search / list_saved_searches / delete_saved_search:
  searches run every morning, reporting only what is new.
- shortlist_add / shortlist_update / shortlist_list / shortlist_remove: one
  shortlist with status, owner and notes per tender.
- export_calendar: submission and question deadlines as an .ics file.
- save_term_pack / list_term_packs / delete_term_pack: curated per-language terms,
  usable by name (term_pack) in scan_tenders and scan_tender_documents.
Prompts (go_no_go, competitor_brief, retender_pipeline, cross_border_sweep,
morning_digest) package the standard workflows. Several tools take format=csv to
attach a spreadsheet export.

Typical flow: search_tenders to find notices, then get_tender_dossier to understand
one, then fetch_tender_documents to read its capitolato — or extract_requirements to
go straight to the qualification sheet.

Award data has limits worth stating: discounts compare with the buyer's estimate,
which often includes options; award notices from before eForms (November 2023)
carry no structured results; and a contract end read from a title is the buyer's
own wording, often extended later by a proroga no notice records.

The two scans answer different questions and cost very different amounts.
scan_tenders reads notices from TED: cheap, cached, and good for sweeping a hundred
tenders down to a handful. scan_tender_documents reads the buyer's own documents:
many requests to a small server, a few tenders per call. Narrow with the first, then
confirm with the second. A requirement absent from a notice is not absent from the
tender — it is usually in the disciplinare, and only the second tool can see it.

Picking between keywords and scan_tenders matters. TED's full-text index only covers
a notice's title and short description, so keywords work for the SUBJECT of a tender
(school catering, cloud computing) and fail for a REQUIREMENT inside it (food waste
measures, CO2 reporting, staff training) — those live in the award criteria or in the
tender documents. Use scan_tenders for the second kind.

Searching across borders: give scan_tenders terms_by_language rather than one
multilingual regex. Terms are then matched only against the language each notice
declares, so an Italian word stem is never tested against German text. Written as a
single pattern, one such search returned 29 hits of which only 8 were real, because
the Italian stem for waste sits inside the ordinary German word "entsprechenden";
the same search expressed per language returns 12, all genuine. Use "near" for
requirements written compositionally rather than as a set phrase.

A scan reports uncovered_languages: notices written in a language your terms say
nothing about. Those notices were downloaded but not really searched, so report them
as a gap rather than as an absence of results.

Notice documents are cached on disk (see -cache-dir), and TED notices never change
once published, so refining a search is cheap: the first pass over a few hundred
notices takes minutes, every pass after that takes seconds. Say so rather than
narrowing a search to save time.

Three limits are worth stating plainly whenever you report results:

- Notices are not obliged to publish their scoring grid; many just point to the
  disciplinare di gara. Absence of a match is not proof the requirement is missing.
- TED never carries the capitolato itself. It lives on the buyer's own platform, and
  most Italian procurement portals disallow automated clients in robots.txt and put
  the procedure page behind a captcha. When fetch_tender_documents reports
  robots-denied or captcha, the documents exist and are public — they simply require
  a human with a browser. Never report that as "no documents available", and do not
  try to work around it with other tools.
- A document that was downloaded is not always a document that was read. A PDF
  reported as no-text-layer is a scan of paper: real, public, and unreadable without
  OCR. Both scans list what they could not read separately from what they searched;
  report those lists rather than folding them into a count of zero matches.
- ANAC holds structured data only (CIG, amounts, outcome), never the documents.

Picking between keywords and scan_tenders matters. TED's full-text index only covers
a notice's title and short description, so keywords work for the SUBJECT of a tender
(school catering, cloud computing) and fail for a REQUIREMENT inside it (food waste
measures, CO2 reporting, staff training) — those live in the award criteria or in the
tender documents. Use scan_tenders for the second kind: it takes the same filters as
search_tenders plus terms to look for inside each notice.

Notices are not obliged to publish their scoring grid; many just point to the
disciplinare di gara, and scan_tenders flags that as criteria_published=false. Absence
of a match is therefore not proof the requirement is missing — say so when reporting.`

// documentUserAgent identifies this server to the buyer portals it fetches
// documents from. It names the software honestly: sites match their robots.txt
// rules against this string, so disguising it as a browser would defeat the
// point of asking permission.
const documentUserAgent = "tedmcp/0.1 (+https://github.com/bernardoforcillo/tedmcp)"

// mcpPath is where the streamable HTTP transport is served, following the
// convention clients expect.
const mcpPath = "/mcp"

// sourceURL is where the Corresponding Source lives. The AGPL requires it to be
// offered to anyone who uses this server over a network, so a fork that moves
// the code has to update this line too.
const sourceURL = "https://github.com/bernardoforcillo/tedmcp"

func main() {
	cacheDir := flag.String("cache-dir", "",
		"where to keep downloaded notice documents (default "+ted.DefaultCacheDir+
			"; \"off\" disables caching; overrides TEDMCP_CACHE_DIR)")
	httpAddr := flag.String("http", "",
		"serve MCP over HTTP on this address (e.g. 127.0.0.1:8080) instead of stdio; "+
			"the endpoint is "+mcpPath)
	dataDir := flag.String("data-dir", "",
		"where to keep the team's saved searches, shortlist and term packs (default "+store.DefaultDir+
			"; \"off\" keeps them in memory; overrides TEDMCP_DATA_DIR)")
	tokensFile := flag.String("tokens-file", "",
		"file of \"name token\" lines; when it (or TEDMCP_TOKENS) names any token, HTTP requests must carry "+
			"one as a bearer token (overrides TEDMCP_TOKENS_FILE)")
	flag.Parse()

	client := ted.NewClient()
	documents := webdoc.NewClient(documentUserAgent)

	// Caching notice documents is what makes refining a search affordable: the
	// same few hundred notices get matched over and over. A cache that cannot
	// be opened is not fatal — the server just goes back to the network.
	cache, err := ted.NewCache(*cacheDir)
	if err != nil {
		log.Printf("tedmcp: caching disabled: %v", err)
	}
	client.Cache = cache

	// The team store, unlike the cache, is not disposable: a file that cannot
	// be read is a reason to stop, not to start over with an empty shortlist.
	team, err := store.Open(*dataDir)
	if err != nil {
		log.Fatalf("tedmcp: %v", err)
	}
	tokens, err := loadTokens(*tokensFile)
	if err != nil {
		log.Fatalf("tedmcp: %v", err)
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "tedmcp",
		Title:   "TED tenders search",
		Version: "0.1.0",
	}, &mcp.ServerOptions{
		Instructions: serverInstructions,
	})

	registerAll(server, client, documents, team)

	// One process, one transport: stdio when launched as a client's subprocess,
	// HTTP when it needs to be reachable over the network.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var runErr error
	if *httpAddr != "" {
		if len(tokens) == 0 {
			log.Printf("tedmcp: no tokens configured — the HTTP endpoint is open to anyone who can reach it")
		} else {
			log.Printf("tedmcp: bearer tokens required (%d configured)", len(tokens))
		}
		if p := team.Path(); p != "" {
			log.Printf("tedmcp: team data in %s", p)
		}
		runErr = serveHTTP(ctx, mcpHandler(server, tokens), *httpAddr)
	} else {
		runErr = server.Run(ctx, &mcp.StdioTransport{})
	}
	if runErr != nil {
		log.Printf("tedmcp: server stopped: %v", runErr)
		os.Exit(1)
	}
}

// serveHTTP serves the MCP server over the streamable HTTP transport until the
// context is cancelled.
func serveHTTP(ctx context.Context, handler http.Handler, addr string) error {
	httpServer := &http.Server{
		Addr:    addr,
		Handler: handler,
		// Streamable responses are long-lived, so only the header read is
		// bounded; a read or write deadline would cut sessions short.
		ReadHeaderTimeout: 20 * time.Second,
	}

	listening := make(chan error, 1)
	go func() {
		log.Printf("tedmcp: serving MCP on http://%s%s", addr, mcpPath)
		listening <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-listening:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}

// mcpHandler builds the HTTP surface: the MCP endpoint, plus a health check
// and a pointer at the root so a stray browser visit is not a silent 404.
//
// The handler is wrapped in cross-origin protection, so a page a user happens
// to have open cannot drive this server on their behalf. The SDK separately
// rejects requests that arrive on localhost carrying a non-localhost Host
// header, which is what stops DNS rebinding.
//
// With tokens configured, the MCP endpoint also requires a bearer token; the
// health check and the source offer stay open, since a probe and the AGPL
// notice must not need credentials.
func mcpHandler(server *mcp.Server, tokens tokenSet) http.Handler {
	var streamable http.Handler = mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		nil,
	)
	if len(tokens) > 0 {
		streamable = auth.RequireBearerToken(tokens.verifier(), nil)(streamable)
	}

	mux := http.NewServeMux()
	mux.Handle(mcpPath, http.NewCrossOriginProtection().Handler(streamable))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "tedmcp MCP server. Point your MCP client at %s\n", mcpPath)
		// AGPL §13: everyone interacting with this server over a network is
		// entitled to its source, so the offer travels with the server rather
		// than living only in a repository they may never see.
		fmt.Fprintf(w, "\nFree software under the GNU AGPL v3. Source: %s\n", sourceURL)
	})
	return mux
}

// registerAll wires every tool, prompt and resource onto the server.
func registerAll(s *mcp.Server, tc *ted.Client, wc *webdoc.Client, st *store.Store) {
	registerTools(s, tc, wc, st)
	registerRequirementTools(s, tc, wc)
	registerHistoryTools(s, tc)
	registerAwardTools(s, tc)
	registerTeamTools(s, tc, st)
	registerPrompts(s)
}
