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

	"github.com/bernardoforcillo/tedmcp/internal/ted"
	"github.com/bernardoforcillo/tedmcp/internal/webdoc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const serverInstructions = `tedmcp searches TED, the European Union's public procurement journal.

Tools:
- lookup_cpv: find the CPV codes for a kind of work, by name, in any EU language.
  Built in, no network. Use it before searching whenever the codes are not known.
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

Prompts carry the procedures these tools exist for, so a workflow does not have to be
rewritten in every client that connects:
- scouting: turn a description of what a company does into a shortlist of open tenders.
- qualifica: go/no-go on one tender, against what a company can prove it holds.
- analisi: what winning one tender requires — the scoring grid and where the points are.
None of them names a sector. What a company does is an argument, so the same three
serve every client of a bid office. Offer them when a request matches one.

Typical flow: search_tenders to find notices, then get_tender_dossier to understand
one, then fetch_tender_documents to read its capitolato.

CHOOSING CPV CODES is where a search fails most often, and it fails silently. Do not
guess them: lookup_cpv searches the vocabulary by name in any EU language and returns
the families to search, at no cost. TED matches child codes automatically, so a full
8-digit code is nearly always narrower than intended; start at 3 digits (e.g. 905*)
and narrow only if the result is unmanageable. A wrong family returns nothing, which
is indistinguishable from "there are no such tenders" — so state which families you
chose and why, where the person reading can correct you.

WHEN A SEARCH RETURNS NOTHING, that is a result to diagnose, not to report. In order:
widen the CPV to fewer digits; confirm the family means what you think it means; move
the requirement out of keywords and into scan_tenders terms, since keywords only sees
the title and a short description; widen the date window; check scope and notice_type.
Report "no tenders match" only once those have been tried, and say which of them you
tried.

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

Four limits are worth stating plainly whenever you report results:

- Notices are not obliged to publish their scoring grid; many just point to the
  disciplinare di gara, and scan_tenders flags that as criteria_published=false.
  Absence of a match is not proof the requirement is missing.
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
- ANAC holds structured data only (CIG, amounts, outcome), never the documents.`

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

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "tedmcp",
		Title:   "TED tenders search",
		Version: "0.1.0",
	}, &mcp.ServerOptions{
		Instructions: serverInstructions,
	})

	registerTools(server, client, documents)
	registerPrompts(server)

	// One process, one transport: stdio when launched as a client's subprocess,
	// HTTP when it needs to be reachable over the network.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var runErr error
	if *httpAddr != "" {
		runErr = serveHTTP(ctx, server, *httpAddr)
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
func serveHTTP(ctx context.Context, server *mcp.Server, addr string) error {
	httpServer := &http.Server{
		Addr:    addr,
		Handler: mcpHandler(server),
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
func mcpHandler(server *mcp.Server) http.Handler {
	streamable := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		nil,
	)

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
