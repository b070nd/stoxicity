# Fetchers blueprint

How news gets into the shared information base, and how the SEC EDGAR reader
fits in. This is a design sketch for code that is not written yet. Facts come
from [docs/research/bleeding-edge-design.md](research/bleeding-edge-design.md)
(section 3.1 and 3.8, read on 2026-11-03); anything marked **(unverified)** was
not confirmed there and must be checked before it is relied on.

## 1. Principles

- A fetcher only reads one source and returns articles. It does not schedule,
  rate-limit, store, score or know about tenants.
- Every byte comes in through package `httpx`: one User-Agent, per-host limits,
  retries, a body cap, no private addresses.
- Every text field is cleaned with `sanitize.Text` before it leaves the fetcher.
- A poll is safe to repeat. Articles are keyed by a stable ID, and saving the
  same one twice changes nothing.
- A source that fails or blocks us degrades its own health and nothing else.

## 2. Data flow

```
 scheduler ──(due source, previous State)──▶ fetcher ──▶ httpx ──▶ the internet
     ▲                                          │
     │                                          ▼
     │                                  feeds.Result{Articles, State, NotModified}
     │                                          │
     └──── saves State, health ◀────────────────┤
                                                ▼
                       pipeline: dedupe/cluster → entities → base score → store
```

| Part | Does | Does not |
|---|---|---|
| `scheduler` | Decides when each source is due, backs off after failures, keeps `State` and health | Parse anything |
| `feeds/*` | Builds requests, reads responses, produces `Article`s and the next `State` | Retry, sleep, write to the database |
| `httpx` | Limits, retries, `Retry-After`, 429 cooldown, SSRF denial, size cap, robots.txt | Know any source format |
| `pipeline` | Everything after a fetch, once per article | Fetch |

## 3. The contract

```go
type Fetcher interface {
    Fetch(ctx, src model.Source, prev State) (Result, error)
}
```

(`app/src/feeds/feeds.go` has the types.) `State` holds the ETag and
Last-Modified where a source sends them, the newest IDs already seen, and a
source-specific cursor. Rules:

1. State moves forward only after the returned articles are stored, so a crash
   repeats a poll instead of losing articles.
2. A partial read returns what it got and a State to resume from, with no error.
3. An error means nothing useful was read. It carries a class the scheduler can
   act on: *retryable* (network, 5xx), *throttled* (429, `Retry-After`),
   *blocked* (see 6), *permanent* (404, parse failure, bad config).

## 4. Fetcher kinds

| Package | Source `kind` | Reads | Uses State for |
|---|---|---|---|
| `feeds/rss` | `rss` | RSS 2.0 and Atom: central banks, regulators, company IR | ETag, Last-Modified; `Seen` as a backstop |
| `feeds/edgar` | `filings` | SEC EDGAR (section 7) | `Seen` (accession numbers), `Cursor` (back-fill date) |
| `feeds/api` | `api` | Licensed JSON APIs, optional | Cursor or `since` token; daily request budget |

Source definitions live in `app/config/sources.yaml`. A file in a user's
`sources/` folder that reuses a shipped `id` overrides only the fields it names
(cvewatch does the same), so turning a source on is a two-line file. A token is
never written in a source: `auth_env: NAME` names a variable in `.env.local`.

## 5. Article rules

- `ID`: the accession number for filings; otherwise the feed GUID, or a hash of
  the canonical URL with tracking parameters removed.
- `Published`: the source's time, stored as UTC. A missing or future time is
  replaced by the fetch time and noted in `Score.Reasons`.
- `Domain`: the publisher's domain; subdomains count as the same publisher.
- Store the headline, link and a short extract. The source's licence says
  whether more may be stored or shown, and to whom.

## 6. Failure model

| Condition | Class | Scheduler response |
|---|---|---|
| Timeout, connection reset, 5xx | retryable | Exponential backoff with jitter; health YELLOW, then RED |
| 429, or `Retry-After` | throttled | Honour the header; the host cools down for every source on it |
| 200 with a body that is not the expected feed | blocked | Treat as a block, back off hard, health RED. The SEC answered one burst of requests with an HTML "Request Rate Threshold Exceeded" page, so an HTML body where a feed was expected is not an empty result |
| 304 | normal | Reschedule; counts as healthy |
| 404, 410, parse error | permanent | Health RED with a hint; stop polling until config or code changes |

## 7. SEC EDGAR

### 7.1 What is used

| Need | Endpoint | Notes |
|---|---|---|
| New filings as they appear | `https://www.sec.gov/cgi-bin/browse-edgar?action=getcurrent&type=<form>&output=atom` | Rolling window; no ETag or Last-Modified; ISO-8859-1; verified for `10-K`, `8-K` and `4` |
| One company's filing history | `https://data.sec.gov/submissions/CIK<10 digits>.json` | Filings with form and date; whether each carries 8-K item numbers needs checking **(unverified)** |
| Fundamentals | XBRL `companyfacts` and `companyconcept` on `data.sec.gov` | Updated typically within a minute of a filing |
| First load | `submissions.zip`, `companyfacts.zip` bulk files | Rebuilt nightly around 03:00 ET; download once |
| Back-fill since 2026-11-01 | daily index files | The RSS window cannot reach back |
| Ownership | Form 4 XML from the feed; 13F data sets (quarterly) | Form 4 structured data set not confirmed **(unverified)** |

Not used: EDGAR full-text search. It documents no API, no rate limit and no
statement on programmatic use.

### 7.2 Request rules

- User-Agent from `httpx.UserAgent`, built from `STOXICITY_CONTACT`. Without it
  the program does not start fetching.
- One limiter for all of EDGAR, at most 10 requests a second in total,
  regardless of how many sources or forms are configured.
- Ask for gzip. Download bulk files once, never per request.
- A shared SEC-wide cooldown when any request is throttled or blocked.

### 7.3 Live path (the current-filings feed)

For each form in the source's `forms`:

1. Request `type=<form>&count=100&start=0`.
2. Read the entries, newest first. Take the accession number from each.
3. If a page contains no already-seen accession, request the next page
   (`start=100`, ...). Stop at the first already-seen accession, or after a
   configured maximum number of pages, whichever comes first. A page that
   hits the maximum raises a health warning instead of silently losing entries.
4. Return the new entries; put the newest accessions in `State.Seen`.

A busy form (8-K, Form 4) returned a full 100-entry page, so a 30-minute poll
can lose entries if it reads only the first page. The largest `count` and the
deepest usable `start` are not documented in what was read **(unverified)**;
find them by testing, politely.

### 7.4 Enrichment path

New accession → one of:

| Form | Next step | Result |
|---|---|---|
| 8-K | Read the item numbers (from the submissions JSON or the filing index) | `Article.Items`, `Score.Kind` prior from `items.go`, a starting materiality |
| 10-K, 10-Q | Queue a facts refresh for the company | Fundamentals on its stock page |
| 4 | Fetch and parse the Form 4 XML | `insider_buy` or `insider_sell`, with amounts |

Company name and CIK come from the entry title; `ciks.go` maps CIK to ticker
for `entity`. The SEC states that the accuracy and scope of its ticker files
are not guaranteed, so a match is cross-checked, not trusted.

### 7.5 Back-fill and reconciliation

- First start: load history for the followed companies from bulk files, then
  list every filing since `since` (2026-11-01) from the daily index.
- Daily: compare the index with what the live path stored, and fetch each
  accession that is missing. The count of misses is a health metric; it is how
  we learn whether the live path is losing entries.

### 7.6 Time

EDGAR times are US Eastern. Store UTC, show `Europe/Berlin`. Which offset the
feed's timestamps carry has not been checked **(unverified)**; the fixture
test (section 9) must pin it down. US filing hours fall in the Berlin
afternoon and evening, and a filing after 6 pm New York time lands on the next
day in Berlin. The US and Europe change clocks on different dates, so the gap
between the two is not constant all year.

### 7.7 Polling cadence

No source states a recommended interval. Start with a short interval for 8-K
and Form 4 during US business hours and a long one otherwise, as `interval`
on the source or a per-form override, and measure with the test in section 10.

## 8. Packages and files

```
app/src/feeds/
  feeds.go            Fetcher, State, Result
  rss/rss.go          RSS and Atom
  api/api.go          licensed JSON APIs (optional)
  edgar/
    edgar.go          package overview
    current.go        live feed, paged
    submissions.go    per-company history
    facts.go          XBRL facts
    bulk.go           nightly bulk files
    index.go          daily index, back-fill
    accession.go      accession numbers
    items.go          8-K item → EventKind
    form4.go          insider transactions
    ciks.go           ticker and CIK lists
app/src/httpx/        shared client (UserAgent exists; the rest is planned)
app/src/envfile/      loads .env and .env.local
```

## 9. Tests to write first

- Recorded fixtures in `testdata/`: a current-filings page for each form, a
  submissions file, a Form 4, an 8-K index. Parsing is tested against them, so
  tests never touch the network.
- Paging: stops at a known accession; stops at the page maximum and reports it;
  a page of only known entries ends the read.
- The "Request Rate Threshold Exceeded" HTML page, served with a feed content
  type or a 200 status, is classified as blocked, not as an empty feed.
- ISO-8859-1 decoding, including characters outside ASCII in company names.
- Hostile feed text passes through `sanitize` before it is stored.
- Every 8-K item number is mapped or listed as ignored.
- Fuzz the XML and Atom parsers.

## 10. First real experiment

Enable `sec-edgar` with `STOXICITY_CONTACT` set, run for 24 hours, and compare
with the daily index: duplicates, missed accessions, and the share of polls that
returned only known entries. Then set the intervals and the page maximum from
the numbers.

## 11. Out of scope

Windows (see [configuration](configuration.md)); scraping sites whose
`robots.txt` or terms forbid it; EDGAR full-text search.
