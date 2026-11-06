# tedmcp

An [MCP](https://modelcontextprotocol.io) server that lets an AI assistant search
**TED — Tenders Electronic Daily**, the European Union's public procurement journal
(<https://ted.europa.eu>). It finds tenders/procurement notices, and then goes after
what TED does not carry: the buyer's own capitolato, disciplinare and allegati, read
out of the PDFs, archives and signature envelopes they are published as, so their full
content can be searched.

Built with the official [Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk).
It talks to the public **TED search API v3** (`https://api.ted.europa.eu`), which
needs **no API key**.

## Tools

| Tool | What it does |
| --- | --- |
| `search_tenders` | Search notices by free-text `keywords`, `cpv` codes, buyer `country`, `notice_type`, publication date, and submission deadline. Returns matches with buyer, CPV, deadline, value, and document links. |
| `scan_tenders` | Search **inside** many notices at once: downloads each notice's eForms XML in parallel and reports where your terms appear, with the award criteria and their weights. Matches per declared language, so terms don't collide across borders. |
| `get_tender_dossier` | One procurement in full: buyer with contacts and codice fiscale, the exact deadline **including the time of day**, lots, award criteria, and every link labelled by what it leads to. |
| `fetch_tender_documents` | Download **all** of a tender's documents from the buyer's portal — following the procedure page to the capitolato, the disciplinare and the allegati — and return their text. Reads PDF, DOCX, XLSX, ODF, ZIP and `.p7m`. Obeys that site's `robots.txt` and reports honestly when it cannot. |
| `scan_tender_documents` | Search **inside** those documents, with the same per-language terms as `scan_tenders`. This is where a requirement lives when the notice omits it. |
| `lookup_anac` | Join a notice to the Italian national contracts database (ANAC) to get its **CIG**, official amounts and outcome. |
| `get_tender` | Fetch one notice by its `publication_number` (e.g. `519405-2026`) with all raw fields and PDF/XML/HTML links. |
| `get_tender_document` | Download a notice's file. `format=xml` (default) returns the machine-readable eForms content as text; `pdf`/`html` return the URL and metadata. This is how you read the full procurement text. |

For qualifying tenders and knowing the market:

| Tool | What it does |
| --- | --- |
| `extract_requirements` | The **go/no-go sheet**: reads a tender's documents and returns, per requirement, the value stated, the passage and the file — turnover, references, certifications, SOA, guarantees, compulsory site visit, questions deadline, subcontracting, avvalimento, lot limits, duration, social clause, CAM, penalties, ANAC fee. |
| `get_procedure_history` | Every notice of one procurement, in order — linked by the eForms procedure identifier — with deadline moves, change notices and the outcome. |
| `award_history` | **Competitive intelligence** from award notices: who wins, against how many bids, at what discount; ranks competitors and profiles buyers. |
| `forecast_retenders` | Contracts **ending soon**, read from award notices' durations, with the incumbent and the value — leads months before the contract notice. |

For working as a team — stored on the server, shared by everyone using it:

| Tool | What it does |
| --- | --- |
| `save_search` · `run_saved_search` · `list_saved_searches` · `delete_saved_search` | Searches saved once and run every morning; a run reports **only what is new**. |
| `shortlist_add` · `shortlist_update` · `shortlist_list` · `shortlist_remove` | One shortlist with a status, an owner and dated notes per tender. |
| `export_calendar` | Submission and question deadlines as an **.ics** file, at the right instant in every time zone. |
| `save_term_pack` · `list_term_packs` · `delete_term_pack` | Curated per-language vocabularies, usable by name (`term_pack`) in both scans. |

Plus five **prompts** — `go_no_go`, `competitor_brief`, `retender_pipeline`,
`cross_border_sweep`, `morning_digest` — and **resources** for the shortlist (JSON and
iCalendar), the saved searches and the term packs. `search_tenders`,
`award_history`, `forecast_retenders` and `shortlist_list` take `format=csv` to attach
a spreadsheet-ready export.

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
  which is not on TED at all — follow `document_urls`, or hand the same terms to
  [`scan_tender_documents`](#scan_tender_documents--searching-inside-the-capitolato).
- Notices whose document could not be downloaded are listed separately as **not
  searched**, so a throttled download never masquerades as "no match".

### Getting at the tender documents

A tender has three layers, and only the first is on TED:

| Layer | Where it lives | Retrievable automatically |
| --- | --- | --- |
| The notice — buyer, deadlines, lots, often the scoring grid | TED eForms XML | **yes** — `get_tender_dossier` |
| The capitolato and disciplinare — the actual requirements | the buyer's own portal | **when the portal allows it** — `fetch_tender_documents`, see below |
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

**`fetch_tender_documents`** goes and gets them — all of them.

The step that matters is the one that is easy to miss: **a notice never links to the
capitolato.** It links to a *procedure page* on the buyer's portal, and the documents
hang off that page one hop away. Fetching the link and stopping returns a page of
navigation, which is what this tool used to do. It now follows that page to the files
it lists, downloads them, and extracts their text:

```
[tender-documents] https://appalti.comune.…/procedure/codice/G00749
  status: fetched  (procedure page listing the documents)

[tender-document] Disciplinare di gara
  https://appalti.comune.…/download/1204
  status: fetched  file: disciplinare.pdf.p7m  bytes: 812345
  disciplinare.pdf — pdf, 34 page(s), 58210 character(s)
  --- disciplinare.pdf ---
  Art. 12 — Criteri di valutazione …
```

It follows **one** hop, and only within the site it started on. Walking further would
turn a document fetch into a crawl of the portal, which is the thing these sites'
`robots.txt` files exist to refuse. Set `discover: false` to fetch only the URL itself.

#### What it can read

| Arrives as | Read as |
| --- | --- |
| PDF | text, laid out line by line, with wide column gaps preserved so a scoring grid still reads as a grid |
| DOCX, XLSX, PPTX, ODT/ODS/ODP | text, including Word headers and footers, where the procedure reference often hides |
| ZIP | expanded into its members, each read in its own right; a PDF inside an archive is read as a PDF |
| `.p7m` | opened. Italian documents are published signed — `capitolato.pdf.p7m` — and the PDF is intact inside the CAdES envelope |
| HTML, plain text, XML, CSV | text |

Formats are decided by looking at the bytes, not the extension, because portals serve
everything as `application/octet-stream` under names like `download.php?id=42`.

Opening a `.p7m` does **not** verify the signature, and saying so matters: whether a
seal is valid is a legal question about a party's identity, needing a trust store,
revocation data and a timestamp. Extracting the document in order to read it makes no
claim either way.

Two caps keep one call from returning a library: `max_documents` (default 25) and
`max_total_chars` (default 150 000). Both are reported when they bite, and files are
ranked by name before the cut, so what gets dropped is the ESPD form rather than the
disciplinare.

#### When it cannot

Expect refusals. The Maggioli *Portale Appalti* software behind most Italian municipal
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

There is a second kind of refusal that looks nothing like the first and matters just as
much:

```
capitolato.pdf — pdf, 48 page(s)  [no-text-layer]
    48 page(s) carrying no text layer — this is a scanned document, readable by a
    person but not by this program without OCR
```

A capitolato that was printed, signed and scanned back in is a real document saying
real things. It is reported as `no-text-layer`, never as an empty one, because a count
of zero matches over a stack of scans is not a finding.

### `scan_tender_documents` — searching inside the capitolato

`scan_tenders` has to end with a caveat: a requirement absent from the notice may still
be in the tender documents. This tool follows that pointer, so "not found" can finally
mean something. It takes the same `terms`, `terms_by_language`, `near` and `regex`,
downloads each tender's documents, and searches inside them.

```json
{
  "publication_numbers": ["442511-2026", "517698-2026"],
  "terms_by_language": {"ITA": ["eccedenz", "spreco", "avanzi"]}
}
```

> Searched the documents of 2 tender(s); 1 contains a match.
>
> `442511-2026 — Servizio di ristorazione scolastica`
> `  language: ITA`
> `  14 file(s) retrieved, 11 readable, 2 match(es)`
>
> `  match in disciplinare.pdf [eccedenz]`
> `    …5 punti per il recupero delle eccedenze alimentari non somministrate…`

The per-language discipline is kept, and the language comes from a better place than a
guess: the eForms notice declares the language its procurement is conducted in, and its
capitolato is written in that language. Documents reached by `urls` rather than through
a notice carry no such declaration, so pass `language` for them — without it every term
is tried against every document, and the collisions described in
[Searching across languages](#searching-across-languages) come back.

Two lists sit beside the matches, and they are the reason to read the output at all:

- **NOT SEARCHED** — documents retrieved but unreadable: the scans, the encrypted
  files, the formats with no reader.
- **NOT RETRIEVED** — URLs the portal declined to serve, each with its reason.

A tender with nothing in either list and no matches genuinely does not mention your
terms. A tender with eleven entries under NOT RETRIEVED has not been searched at all.
Reporting those two the same way is the failure this output exists to prevent.

Downloading one tender's documents is a dozen requests to a small municipal server, so
this searches **a few tenders per call** (`limit`, default 5, max 20) rather than a page
of them. Narrow the field with `scan_tenders`, which is cheap and cached, then confirm
the survivors here.

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

### `extract_requirements` — the qualification sheet

The questions a bid team asks of every tender are the same, and the answers are in
the disciplinare rather than the notice: what turnover is demanded, which references,
which certifications, how large the guarantee is, whether the site visit is
compulsory, when questions close. Finding them by reading forty pages per tender is
the slowest part of qualifying a pipeline.

`extract_requirements` reads the tender's documents once — the same retrieval as
`fetch_tender_documents`, so the same robots.txt rules and the same honesty about
refusals — and answers each question with the value stated, the passage and the file:

```json
{"publication_number": "442511-2026"}
```

> **■ Minimum turnover**: € 1.500.000,00
> `    disciplinare.pdf: …un fatturato globale minimo annuo … non inferiore a € 1.500.000,00 IVA esclusa…`
> **■ Site visit**: 10/08/2026  [mandatory]
> **■ Deadline for questions / clarifications**: 14/08/2026

The notice's own facts come first — submission deadline with its time of day, the
deadline for questions, the opening session, the duration and renewal options — and
need no document at all.

The built-in checks are phrased the way Italian, English, French, German and Spanish
tender documents phrase them, as whole words and set phrases rather than stems. Teams
add their own with `custom_checks` (`terms_by_language` or `regex`, plus what to
`extract`: amount, percent, date, duration, codes). Three things are kept apart in the
output, for the same reason as in the scans: requirements found, requirements **not
found in the documents read**, and the documents that were not read at all.

### `get_procedure_history` — is this still the current notice?

A notice is a snapshot. The deadline may have been extended last week, a lot
cancelled, the procedure awarded. eForms gives every notice of one procurement the
same procedure identifier, and TED can search by it, so the whole sequence comes back
in one call, with what changed:

> Procedure ccc10029-… — 3 notice(s), status: awarded
>   ! 561293-2024 is not the latest notice of this procedure: 2 later notice(s) follow it
>   ! Deadline moved from 2024-10-21 12:00 (561293-2024) to 2024-11-04 12:00 (600000-2024)

Notices from before eForms (November 2023) carry no procedure identifier; the tool says
so rather than returning a history of one.

### `award_history` — who wins, at what price

Award notices are the most under-read part of TED. Their results section says who won
each lot, against how many rival bids, and for how much — read in bulk, that is the
competitive picture of a market:

```json
{"cpv": ["555*"], "country": ["ITA"], "published_from": "2024-01-01"}
```

The output ranks **competitors** (wins, value awarded, the buyers they win repeatedly,
typical number of rivals, sole-bidder wins, median discount), profiles **buyers** (how
many bids they attract, whether they keep the same supplier) and gives the **market**
view (median bids per lot, single-bid share, median discount). `winner` follows one
competitor; `buyer` narrows to one contracting authority. Operators are identified by
their registration number when the notice gives one, so "CIRFOOD S.C." and "Cirfood
Soc. Coop." are one competitor.

Discounts compare the award with the buyer's estimate, which often includes options and
renewals, so they are an upper bound; values that obviously measure different things
are left out rather than reported as a negative discount.

### `forecast_retenders` — leads before the notice exists

The same award notices say how long each contract runs, which is when it will be
tendered again. eForms has a field for it, and half of all notices leave it empty and
write the period into the title — `PERIODO dal 01/01/2027 al 31/12/2028`,
`GENNAIO 2026-AGOSTO 2029`. Both are read, and each end date says where it came from:
`notice` (the planned period), `text` (dates in the title or description) or `derived`
(a stated span from the contract date). Renewal options written as prose ("Eventuale
rinnovo di 12 mesi", "ulteriori 4 anni scolastici") give a second end date.

```json
{"cpv": ["555*"], "country": ["ITA"], "ends_to": "2028-06-30", "include_pins": true}
```

Each contract comes with its incumbent, its value and a `watch_from` date nine months
before the end. Lots whose duration cannot be read are **counted** as undated rather
than dropped, and `include_pins` adds the prior information notices published in the
last year — buyers announcing upcoming tenders.

### Team tools

Everything above answers a question in one conversation. The team tools keep the
answers between conversations, on the server, shared by everyone who uses it:

- **Saved searches.** `save_search` stores `search_tenders` filters under a name;
  `run_saved_search` runs it and reports only the notices it has not shown before.
  `name: "*"` runs every saved search — the morning digest.
- **Shortlist.** `shortlist_add` records a tender with a status (`watching`,
  `qualifying`, `bidding`, `submitted`, `won`, `lost`, `dropped`), an owner and a note,
  and reads the notice once for its deadline with time of day and offset and its
  questions deadline. `shortlist_list` sorts by deadline and flags anything closing
  within a week.
- **Calendar.** `export_calendar` writes those deadlines to an `.ics` file with
  reminders. An 08:00 deadline at `+02:00` becomes 06:00 UTC, so it lands at the right
  moment in every team member's calendar; a deadline without a stated time becomes an
  all-day event rather than an invented hour.
- **Term packs.** A cross-border search that took an afternoon to get right —
  `terms_by_language` for eight languages plus a `near` pair — is saved with
  `save_term_pack` and reused by anyone with `term_pack: "food-waste"` in
  `scan_tenders` or `scan_tender_documents`.

The data is one JSON file, `team.json`, written atomically on every change:

```sh
tedmcp -data-dir /path/to/data   # default .tenders/data; "off" keeps it in memory
```

The directory is chosen by the flag, then `TEDMCP_DATA_DIR`. Unlike the cache it is
not regenerable — back it up. A file that cannot be read stops the server instead of
being silently replaced by an empty shortlist. One process writes it, so run one
replica.

### Prompts

The prompts package the standard workflows, so a junior analyst runs the same sequence
a senior one would, with the caveats stated up front:

| Prompt | Arguments | What it runs |
| --- | --- | --- |
| `go_no_go` | `publication_number`, `company_profile` | history → dossier → requirements → award history for the buyer and market → requirement-by-requirement fit → recommendation and questions for the buyer |
| `competitor_brief` | `competitor`, `cpv`, `country` | award history filtered to the competitor, its incumbencies, and the contracts it will be defending |
| `retender_pipeline` | `cpv`, `country`, `months` | forecast grouped by quarter, undated contracts, prior information notices, saved searches for the buyers ending soonest |
| `cross_border_sweep` | `requirement`, `cpv`, `countries`, `term_pack` | per-language terms → scan_tenders → scan_tender_documents → three lists kept apart → save the terms as a pack |
| `morning_digest` | — | all saved searches, shortlist deadlines this week, changes to tenders being qualified |

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
  `-p 127.0.0.1:8080:8080`, or set `TEDMCP_TOKENS` (see
  [Authentication](#authentication)) — without tokens, anything that can reach the
  port can run the tools.
- **Team data lives in `/var/lib/tedmcp`.** Mount a second volume there (`make up`
  does); without it the shortlist dies with the container.

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
interface; binding `:8080` exposes it to anything that can reach the host. Before it
leaves the machine, configure tokens.

### Authentication

With the team tools, an open endpoint means anyone who finds it can rewrite the
shortlist. Give each member of the team a bearer token:

```sh
TEDMCP_TOKENS="giulia:$(openssl rand -hex 24),marco:$(openssl rand -hex 24)" tedmcp -http :8080
# or one "name token" pair per line in a file:
tedmcp -http :8080 -tokens-file /etc/tedmcp/tokens
```

When any token is configured, every request to `/mcp` must carry
`Authorization: Bearer <token>`; `/healthz` and `/` stay open. Tokens must be at least
16 characters, are compared by digest in constant time, and the name each one belongs
to is recorded on saved searches, shortlist notes and term packs. Clients pass the
header:

```sh
claude mcp add --transport http tedmcp https://tedmcp.example.org/mcp \
  --header "Authorization: Bearer <token>"
```

This is deliberately not OAuth: a team of analysts behind one ingress needs the open
endpoint shut and a name on each request, not an authorization server.

## Example

> "Find open IT tenders in Germany published this month mentioning cloud, then show me the full text of the most relevant one."

The assistant calls `search_tenders` (`cpv: ["72000000"]`, `country: ["DEU"]`,
`keywords: "cloud"`, `published_from: …`), picks a `publication_number`, then calls
`get_tender_document` with `format=xml` to read the procurement details.

> "Which open Italian catering tenders award points for recovering unserved food?"

Only the first half of that question can be answered from TED. The assistant calls
`scan_tenders` (`cpv: ["555*"]`, `country: ["ITA"]`, `terms_by_language: {"ITA":
["eccedenz","spreco"]}`) to find the notices that publish such a criterion, and gets a
handful — plus a longer list whose notices publish no scoring grid at all and defer to
the disciplinare. Those are not answers; they are the question restated. It then hands
the same terms to `scan_tender_documents` for that list, which downloads each tender's
disciplinare from its buyer's portal and searches inside. What comes back is three
things kept apart: the tenders that award points and where it says so, the tenders that
demonstrably do not, and the tenders whose portal would not serve a program — which
remain unanswered, and are reported as such rather than as an absence.

## Layout

```
Makefile                 development targets (make dev, check, image)
.air.toml                live-reload configuration
Dockerfile               two-stage build onto a distroless base
main.go                  server setup, stdio and HTTP transports
tools.go                 search/scan tool definitions and handlers
dossier_tools.go         dossier, document-fetch and ANAC tool handlers
docscan_tools.go         scan_tender_documents: searching inside the tender documents
requirements_tools.go    extract_requirements: the go/no-go qualification sheet
history_tools.go         get_procedure_history: every notice of one procurement
awards_tools.go          award_history and forecast_retenders
team_tools.go            saved searches, shortlist, calendar, term packs, resources
prompts.go               the workflow prompts
auth.go                  bearer tokens for the HTTP transport
common.go                notice selection, procedure reading, CSV export
documents.go             retrieving a tender's documents, shared by both tools
format.go                human-readable result rendering
dossier_format.go        rendering for the dossier/fetch/ANAC results
document_format.go       rendering for retrieved documents and document scans
internal/ted/client.go   TED search API v3 client + paced document downloads
internal/ted/query.go    expert-query builder
internal/ted/notice.go   notice field flattening + summaries
internal/ted/eforms.go   eForms parsing for award criteria, languages, term matching
internal/ted/dossier.go  eForms parsing for parties, lots, deadlines, link roles
internal/ted/procedure.go eForms results: winners, bids, values, changes, lot terms
internal/ted/duration.go contract periods from the notice or from its wording
internal/ted/cache.go    on-disk cache of notice documents
internal/match/          per-language and paired-root text matching
internal/webdoc/         robots.txt-respecting document retrieval and link discovery
internal/doctext/        text extraction from PDF, ZIP, DOCX/XLSX/ODF and .p7m
internal/anac/           ANAC open-data snapshot lookup by codice fiscale
internal/requirements/   requirement checks and value extraction for tender documents
internal/store/          the team's saved searches, shortlist and term packs
```

## Notes

- The TED API is public and unauthenticated; be considerate with request volume.
- Notice documents are served by `ted.europa.eu`, which rate-limits bursts with
  HTTP 429. Downloads are paced client-side (one every 250 ms, shared across
  goroutines) and retried with backoff, so `scan_tenders` covers a full page of
  notices without dropping any.
- Buyer portals are not `ted.europa.eu`. A municipal procurement site is a small
  server, so document downloads run four at a time by default (`concurrency`, max 8)
  and `scan_tender_documents` takes one tender at a time rather than a page of them.
- Notice fields are multilingual; English (`ENG`) is preferred where available, with
  a deterministic fallback to other languages.
- For deep pagination beyond what `page`/`limit` allow, refine the query with more
  filters rather than paging very far.
- Text extraction adds one dependency,
  [`github.com/ledongthuc/pdf`](https://github.com/ledongthuc/pdf) (BSD, pure Go, no
  cgo — the image stays a static binary on distroless). Everything else — ZIP, the
  OOXML and OpenDocument formats, and the CAdES `.p7m` envelope — is read with the
  standard library.

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
