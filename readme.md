# tedmcp

An [MCP](https://modelcontextprotocol.io) server that lets an AI assistant search
**TED — Tenders Electronic Daily**, the European Union's public procurement journal
(<https://ted.europa.eu>). It finds tenders/procurement notices and downloads the
underlying tender files so their full content can be read and searched.

Built with the official [Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk).
It talks to the public **TED search API v3** (`https://api.ted.europa.eu`), which
needs **no API key**.

## Tools

| Tool | What it does |
| --- | --- |
| `search_tenders` | Search notices by free-text `keywords`, `cpv` codes, buyer `country`, `notice_type`, publication date, and submission deadline. Returns matches with buyer, CPV, deadline, value, and document links. |
| `scan_tenders` | Search **inside** many notices at once: downloads each notice's eForms XML in parallel and reports where your terms appear, with the award criteria and their weights. Matches per declared language, so terms don't collide across borders. |
| `get_tender_dossier` | One procurement in full: buyer with contacts and codice fiscale, the exact deadline **including the time of day**, lots, award criteria, and every link labelled by what it leads to. |
| `fetch_tender_documents` | Try to download the capitolato and disciplinare from the buyer's portal, obeying that site's `robots.txt` and reporting honestly when it cannot. |
| `lookup_anac` | Join a notice to the Italian national contracts database (ANAC) to get its **CIG**, official amounts and outcome. |
| `get_tender` | Fetch one notice by its `publication_number` (e.g. `519405-2026`) with all raw fields and PDF/XML/HTML links. |
| `get_tender_document` | Download a notice's file. `format=xml` (default) returns the machine-readable eForms content as text; `pdf`/`html` return the URL and metadata. This is how you read the full procurement text. |

### `search_tenders` inputs

All optional, but you must supply at least one filter (or a raw `query`):

- `query` — a raw [TED expert query](https://docs.ted.europa.eu/) (combined with `AND` with the filters below)
- `keywords` — full-text terms, e.g. `cloud computing`
- `cpv` — one or more CPV codes (see [Advanced CPV matching](#advanced-cpv-matching))
- `country` — buyer country ISO alpha-3, e.g. `["DEU","FRA"]`
- `notice_type` — e.g. `cn-standard` (contract notice), `can-standard` (award)
- `published_from` / `published_to` — `YYYY-MM-DD`
- `deadline_from` / `deadline_to` — `YYYY-MM-DD`
- `page` (default 1), `limit` (1–250, default 20)
- `sort_by` (default `publication-date`), `sort_order` (`ASC`/`DESC`, default `DESC`)
- `scope` — `ALL` / `LATEST` / `ACTIVE`

The compiled expert query is returned in the result so you can see exactly what ran, e.g.:

```
classification-cpv=72000000 AND FT ~ ("cloud") SORT BY publication-date DESC
```

### `scan_tenders` — searching inside notices

TED's full-text index only covers a notice's **title and short description**. That is
enough to find the *subject* of a tender, and useless for a *requirement* inside it:
scoring criteria such as food-waste recovery, CO2 reporting or staff training are in
the eForms XML or in the tender documents, where `keywords` never looks. Searching
TED for `"spreco alimentare"` across Italian catering tenders returns almost nothing,
even though several tenders award points for exactly that.

`scan_tenders` closes that gap. It selects notices (by `publication_numbers`, or with
the same filters as `search_tenders`), downloads each one's XML in parallel, and
reports:

- **where each term matched** — award criteria first, with the **points** attached
- the **full scoring grid** of every matching notice, so a hit can be read in context
- `criteria_published: false` when the notice publishes no grid at all and defers to
  the *disciplinare di gara* — a real finding, not an empty result
- `strategic_procurement` flags (e.g. `env-imp`) and the **buyer-platform URLs** where
  the capitolato lives

```json
{
  "cpv": ["555*", "553*"],
  "country": ["ITA"],
  "notice_type": "cn-standard",
  "deadline_from": "2026-07-28",
  "terms": ["eccedenz", "spreco", "avanzi", "non somministrat"]
}
```

> Scanned 70 notice document(s); 3 contain a match.
> …
> `match [award-criterion, weight 5]: D_ Prevenzione, monitoraggio e recupero delle eccedenze alimentari`

Inputs: `terms_by_language` and `near` (preferred, see below), or `terms` / `regex`
for a quick single-language search; `publication_numbers` **or** the `search_tenders`
filters; `limit` (1–120, default 40) with `page` for larger result sets;
`include_unmatched`; `include_criteria`; `concurrency` (1–8, default 4).

### Searching across languages

Writing one multilingual regular expression does not work, and the failure is not
subtle. Searching 338 open catering tenders across Europe for food-waste requirements
with a hand-written 57-term pattern returned **29 hits of which 8 were real**: the
Italian stem for waste, `sprec`, sits inside the perfectly ordinary German word
*ent**sprec**henden*. Tightening it with word boundaries still matched
*An-**sprec**hpartner*, because the source hyphenates across line breaks. And the
tightened pattern then **missed** the Hungarian *ételhulladék* and the Maltese notice
that said "Minimizing Waste Generation" with "Food" ten words earlier.

Two inputs fix all three, and neither commits the server to a vocabulary of its own:

**`terms_by_language`** — terms keyed by the language they belong to. Every eForms
notice declares its language, on the document and on each text node, so Italian terms
are only ever offered Italian text and cross-language collisions cannot happen. No
word-boundary tricks needed.

```json
{
  "cpv": ["555*", "553*"], "notice_type": "cn-standard", "deadline_from": "2026-07-28",
  "terms_by_language": {
    "ITA": ["eccedenz", "spreco", "avanzi"],
    "DEU": ["lebensmittelverschwendung", "speisereste"],
    "BUL": ["хранителни отпадъци"],
    "*":   ["food waste"]
  },
  "near": {
    "window": 60,
    "a": {"ENG": ["food", "meal"], "DEU": ["lebensmittel", "speise"]},
    "b": {"ENG": ["waste", "minimizing"], "DEU": ["reduzierung", "vermeidung"]}
  }
}
```

**`near`** pairs two groups of roots within a window, catching the phrasings no term
list can enumerate: *lebensmittel* + *reduzierung*, *Food* … *Minimizing Waste*. On
the 338 notices above, the per-language form returns **12 hits, all genuine**, and a
deliberately broad control search finds nothing further that mentions food.

Two rules keep the result honest:

- A text that declares **no** language is searched with every term — there the
  alternative to a possible collision is a silent gap.
- A text that declares a language your terms **do not cover** is searched only with
  the `*` terms, never another language's. Those notices are counted and returned as
  `uncovered_languages`, so a blind spot is visible rather than silent:

  > NOT SEARCHED: DEU (15), FRA (30), POL (25), … — these notices are written in
  > languages your terms do not cover; add terms for them before concluding anything
  > about those countries.

### Caching

Notice documents are cached on disk, because TED notices are immutable — a corrected
notice is republished under a new number, so a cached document never goes stale.

This is what makes refining a search affordable. Scanning those 338 notices cold takes
**2m 19s**; with a warm cache the same scan takes **3.8s**, so trying another
set of terms is nearly free.

```sh
tedmcp -cache-dir /path/to/cache   # default: .tenders/cache, "off" disables it
```

The directory is chosen by the flag, then `TEDMCP_CACHE_DIR`, then `.tenders/cache`
beside the working directory. It is bounded (512 MB) and evicts the least recently
written entries; `scan_tenders` reports how many documents it read locally.

Two things to keep in mind when reading a result:

- **No match is not proof of absence.** The requirement may be in the capitolato,
  which is not on TED at all — follow `document_urls`.
- Notices whose document could not be downloaded are listed separately as **not
  searched**, so a throttled download never masquerades as "no match".

### Getting at the tender documents

A tender has three layers, and only the first is on TED:

| Layer | Where it lives | Retrievable automatically |
| --- | --- | --- |
| The notice — buyer, deadlines, lots, often the scoring grid | TED eForms XML | **yes** — `get_tender_dossier` |
| The capitolato and disciplinare — the actual requirements | the buyer's own portal | **usually not**, see below |
| CIG, official amounts, award outcome | ANAC national database | **yes**, from a downloaded snapshot — `lookup_anac` |

**`get_tender_dossier`** costs no extra request beyond the XML that is fetched anyway,
and surfaces what the notice already contains: the buyer's PEC and phone, their codice
fiscale, the deadline *with its time of day* (often 08:00, and easy to miss), the lots
with values and CPV, and the links **labelled by role** — because a notice's URLs are
not interchangeable:

```
[tender-documents]    https://appalti.comune.…/procedure/codice/G00749
[buyer-profile]       https://appalti.comune.…/PortaleAppalti/
[buyer-website]       https://www.comune.….it/
[appeal-body]         https://www.giustizia-amministrativa.it/   ← the TAR, not a document source
```

**`fetch_tender_documents`** attempts the download and obeys `robots.txt`. Expect it to
be refused: the Maggioli *Portale Appalti* software behind most Italian municipal
portals answers

```
User-agent: *
Disallow: /
```

with a narrow exception for Googlebot and Bingbot on the listing pages, and puts the
procedure page behind a captcha. Consip's `acquistinretepa.it` ends its `robots.txt`
the same way. So the tool reports:

```
status: robots-denied
reason: appalti.comune.grugliasco.to.it robots.txt disallows /PortaleAppalti/it/procedure/codice/G00749
        for automated clients — open it in a browser
```

That is the intended outcome, not a failure to work around. The documents are public;
the buyer has chosen to serve them to people rather than to programs. A `robots-denied`
or `captcha` result must never be reported as "no documents exist" — it means *go and
open this URL*. The server sends a user agent that names itself (`tedmcp/0.1`)
precisely so those rules can apply to it.

### `lookup_anac` — getting the CIG

TED notices do not carry the CIG, but every Italian eForms notice carries the buyer's
**codice fiscale**, and ANAC repeats it as `cf_amministrazione_appaltante`. Buyer plus
contract value pins down the single gara:

```json
{"publication_number": "442511-2026", "snapshot_path": "~/Downloads/20260701-cig_json.zip"}
```

> 234643 records scanned, 318 name this buyer, 1 matched.
> **CIG BC31BD63BC** — 7 249 359,11 — GARA 22/2026 … FABBRICA DEL VAPORE

The snapshot is the monthly JSON archive of the `cig` dataset from
[dati.anticorruzione.it](https://dati.anticorruzione.it/opendata/dataset/cig). This
server does not download it for you: that site's firewall rejects any client that does
not present itself as a web browser, and this server will not pretend to be one. Fetch
the file in a browser and pass its path. The archives are incremental, so a gara
published after the snapshot date will not be in it yet.

### Advanced CPV matching

The `cpv` filter accepts exact codes and trailing-wildcard prefixes, mixed freely
in one list. Entries can also be comma-separated inside a single string.

| Form | Meaning | Compiles to |
| --- | --- | --- |
| `55500000` | exact 8-digit code (TED includes its child codes automatically) | `classification-cpv=55500000` |
| `55500000, 55524000` | several exact codes | `classification-cpv IN (55500000 55524000)` |
| `555*` / `555XXXXX` | whole `555…` CPV family (trailing wildcard) | `classification-cpv=555*` |
| `5552X` | narrower `5552…` family | `classification-cpv=5552*` |
| `55524000-5` | exact code with check digit (digit is dropped) | `classification-cpv=55524000` |

Mixed lists are OR'd together, e.g. `["55500000","55524000","555XXXXX"]` →
`(classification-cpv IN (55500000 55524000) OR classification-cpv=555*)`.

Notes:

- Wildcards are **prefix only** (`555*`), matching the TED expert-search engine.
  A wildcard in the middle (`5X5*`) is rejected with a clear error.
- A prefix needs at least 2 digits (`55*` is the broadest sensible match).
- `X`, `x`, and `*` are all accepted as trailing placeholders.

## Build

Requires Go 1.25+.

```sh
make build     # or: go build -o tedmcp .
```

### Docker

```sh
make image    # docker build -t tedmcp .
make up       # docker run --rm -p 127.0.0.1:8080:8080 -v tedmcp-cache:/var/cache/tedmcp tedmcp
```

The image is a two-stage build: `golang:1.26-alpine` compiles a static binary with
`CGO_ENABLED=0` and runs the tests, and the result ships on
`gcr.io/distroless/static-debian12:nonroot` — CA certificates for reaching TED over
HTTPS, a non-root user, and nothing else. No shell, no package manager, ~9 MB of
binary.

It serves **HTTP** by default, since stdio only makes sense when a client owns the
process. For stdio in a container, override the arguments:

```sh
docker run -i --rm -v tedmcp-cache:/var/cache/tedmcp tedmcp -cache-dir /var/cache/tedmcp
```

Two things worth knowing:

- **The volume is the point.** TED notices never change once published, so a mounted
  `/var/cache/tedmcp` turns a repeated scan from minutes into seconds. Without it the
  cache dies with the container.
- **`0.0.0.0` inside, loopback outside.** The process must bind `0.0.0.0` for a
  published port to reach it; keep the exposure on the host side with
  `-p 127.0.0.1:8080:8080`. The server has no authentication — anything that can
  reach the port can run the tools.

## Development

```sh
make          # list the targets
make dev      # live reload (air), MCP served over HTTP on 127.0.0.1:8080
make check    # fmt + vet + test
make tools    # install air, if it isn't already
```

`make dev` runs [air](https://github.com/air-verse/air): it rebuilds and restarts the
server on every save. It serves **HTTP** rather than stdio, and that is the whole
point — a stdio server is owned by the client that launched it, so it cannot be
restarted underneath one. Over HTTP the client simply reconnects to the same URL
after each rebuild.

Two details in [`.air.toml`](.air.toml) matter:

- **`.tenders` is excluded from watching.** Every scan writes cached notices there;
  watching it would make the server rebuild itself in a loop while it works.
- **`send_interrupt = true`.** The server shuts down gracefully on SIGINT, so open
  MCP sessions get a moment to close instead of being cut mid-response.

The checked-in [`.mcp.json`](.mcp.json) points at that URL, so it carries no absolute
path and works wherever the repository lives:

```json
{ "mcpServers": { "tedmcp": { "type": "http", "url": "http://127.0.0.1:8080/mcp" } } }
```

The trade-off is worth stating: **the tools exist only while the server is running.**
Start `make dev` (or `make run`) before your client, or it will find nothing. For a
client that should own the process instead, use the stdio form below.

## Use with an MCP client

The server speaks MCP over **stdio** (default) or **streamable HTTP** (`-http`).
One process serves one transport: stdio when a client launches it as a subprocess,
HTTP when it needs to be reachable over the network.

### stdio

```sh
claude mcp add tedmcp -- /absolute/path/to/tedmcp
```

**Claude Desktop / generic** (`claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "tedmcp": {
      "command": "/absolute/path/to/tedmcp"
    }
  }
}
```

### HTTP

```sh
tedmcp -http 127.0.0.1:8080     # endpoint: http://127.0.0.1:8080/mcp
```

```sh
claude mcp add --transport http tedmcp http://127.0.0.1:8080/mcp
```

`GET /healthz` answers `ok`, and `GET /` says where the endpoint is rather than 404.
The process shuts down gracefully on SIGINT/SIGTERM, giving open sessions 10 seconds.

Two protections apply, and both are worth knowing before you change the address:

- **Cross-origin requests are rejected**, so a page a user happens to have open in a
  browser cannot drive the server on their behalf.
- **DNS rebinding is blocked** by the SDK: a request arriving on a loopback address
  with a non-loopback `Host` header gets 403.

Neither is authentication. `-http 127.0.0.1:8080` keeps the listener on the loopback
interface; binding `:8080` exposes it to anything that can reach the host, and the
tools will happily run for whoever connects. Put it behind a reverse proxy that
authenticates if it needs to leave the machine.

## Example

> "Find open IT tenders in Germany published this month mentioning cloud, then show me the full text of the most relevant one."

The assistant calls `search_tenders` (`cpv: ["72000000"]`, `country: ["DEU"]`,
`keywords: "cloud"`, `published_from: …`), picks a `publication_number`, then calls
`get_tender_document` with `format=xml` to read the procurement details.

## Layout

```
Makefile                 development targets (make dev, check, image)
.air.toml                live-reload configuration
Dockerfile               two-stage build onto a distroless base
main.go                  server setup, stdio and HTTP transports
tools.go                 search/scan tool definitions and handlers
dossier_tools.go         dossier, document-fetch and ANAC tool handlers
format.go                human-readable result rendering
dossier_format.go        rendering for the dossier/fetch/ANAC results
internal/ted/client.go   TED search API v3 client + paced document downloads
internal/ted/query.go    expert-query builder
internal/ted/notice.go   notice field flattening + summaries
internal/ted/eforms.go   eForms parsing for award criteria, languages, term matching
internal/ted/dossier.go  eForms parsing for parties, lots, deadlines, link roles
internal/ted/cache.go    on-disk cache of notice documents
internal/match/          per-language and paired-root text matching
internal/webdoc/         robots.txt-respecting document retrieval
internal/anac/           ANAC open-data snapshot lookup by codice fiscale
```

## Notes

- The TED API is public and unauthenticated; be considerate with request volume.
- Notice documents are served by `ted.europa.eu`, which rate-limits bursts with
  HTTP 429. Downloads are paced client-side (one every 250 ms, shared across
  goroutines) and retried with backoff, so `scan_tenders` covers a full page of
  notices without dropping any.
- Notice fields are multilingual; English (`ENG`) is preferred where available, with
  a deterministic fallback to other languages.
- For deep pagination beyond what `page`/`limit` allow, refine the query with more
  filters rather than paging very far.

## Licence

Copyright (C) 2026 Bernardo Forcillo.

This program is free software: you can redistribute it and modify it under the terms
of the **GNU Affero General Public License, version 3** or later. It comes with no
warranty. See [LICENSE](LICENSE) for the full text.

The Affero clause is the one that matters for a server like this. Section 13 means
that **anyone who uses this program over a network is entitled to its source**, not
only whoever received a copy of the binary. If you run a modified version and let
others reach it, you have to offer them your modified source.

That is why the HTTP root endpoint answers with the source URL alongside the usual
pointer to `/mcp`: the offer travels with the running server rather than living only
in a repository its users may never see. If you fork this, update `sourceURL` in
[main.go](main.go) to point at your own source — a test enforces that the offer stays
on the page, but only you can make it point somewhere true.
