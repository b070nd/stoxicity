# stoxicity: bleeding-edge design research

Research date: 2026-11-03. Every source was read on that date unless the source list says otherwise. Nothing in the project was modified except this file. Nothing was installed, no paid service was called, laya was not run, no model was downloaded.

**How to read this file**

- Inline tags `[S12]` point to the source list (section 7), which gives the URL and how it was read.
- Confidence labels: **V** = Verified (read in the primary source), **I** = Inferred (follows from verified facts; the reasoning is stated), **U** = Unverified (listed again in section 6).
- Method caveat: `WebFetch` returns a summary written by a small model, not the raw page. Where a number matters I re-read the raw bytes (`curl`, `gh api`, `pdftotext`) and say so. Pages that were JavaScript-only or that returned 403 (Interactive Brokers docs, EUR-Lex, Finnhub docs, GLEIF) are marked.
- No fetched page contained instructions aimed at me. The only "instruction-like" text was robots.txt/terms notices that forbid automated collection (reported in 3.1).
- One self-inflicted observation: after about ten quick requests to sec.gov from this machine (some with a browser-like User-Agent), a request for `robots.txt` was answered with an HTML page titled "Request Rate Threshold Exceeded" [S14]. That is the SEC's fair-access control working, and it is a reminder that the declared User-Agent and a hard rate cap are not optional.

---

## 1. Summary

### 1.1 Recommended architecture (one page)

```
 sources (primary first)                     shared information base (one SQLite WAL file)
 ─────────────────────────                   ──────────────────────────────────────────────
 EDGAR: current-filings Atom (paged),        raw_items → items → clusters → mentions
   submissions API per followed CIK,         entities (CIK/ticker/FIGI/LEI/MIC, aliases)
   daily-index, 8-K item parsing             item_scores(lens_version, ...)   <- versioned, never overwritten
 Central banks / regulators RSS              fts5 index (title, lede, entities)
 Company IR RSS (one-by-one, curated)        embeddings (int8/float32, MRL-truncated) brute-force
 Licensed news/market APIs (optional)        source_stats (per source/domain/day: total, relevant)
        │                                                       │
        ▼                                                       ▼
 polite HTTP gate (cvewatch httpx pattern)         one DB FILE PER TENANT (+ read-only ATTACH of base)
 per-host rate, ETag/Last-Modified,                labels, watch hits, alert state, read markers
 Retry-After, 429 cooldown, SSRF deny,             (isolation by construction, not by a WHERE clause)
 body cap, robots.txt, declared UA                              │
        │                                                       ▼
        ▼                                          watches (YAML) evaluated on read
 sanitize v2 (escapes + bidi + zero-width +        channels: tui · email · webhook · file · feed · MCP(read-only)
 tag chars) → canonical URL/accession key
        │
        ▼
 dedupe: exact → SimHash/MinHash → embedding window  →  entity resolution (layered, ambiguity-aware)
        │
        ▼
 LENS (three stages, each optional, all versioned):
   L0 structure priors (form type, 8-K item, source tier)   deterministic, highest precision
   L1 learned scorer on embeddings (+ BM25 features)         trained on the user's labels; temperature-scaled; conformal band
   L2 local LLM judge ONLY for the uncertain band and for label bootstrapping; never the sole gate
 summaries: extractive with sentence ids; optional abstractive that must cite ids and is checked by a grounding model
```

Go single module, CGO-free default build (modernc.org/sqlite), two binaries plus `stoxicity mcp`. Python is for offline training/labelling experiments only, not for the running product. laya is an optional pluggable `Scorer`, not the default.

### 1.2 Top 10 decisions

| # | Decision | Confidence | Why (short) |
|---|---|---|---|
| 1 | Build on **Bubble Tea v2.1.1 / Lip Gloss v2.0.6 / Bubbles v2.2.1** (`charm.land/.../v2`), not v1 as cvewatch does | V | v2 is current; cvewatch pins v1.3.10 / v1.1.0 [S2][S63][S70] |
| 2 | Storage: **modernc.org/sqlite v1.61.2** (SQLite 3.54.1, FTS5 on, sqlite-vec v0.1.9 bundled, pure Go), WAL, one writer connection | V | [S64][S68][S69] |
| 3 | **One SQLite file per tenant** + a shared base file attached read-only; scaffold's `TenantStore` interface stays | I | removes the "forgot the tenant filter" bug class |
| 4 | **Primary sources first**: EDGAR (10 req/s cap, declared UA, bulk files), Fed/ECB-type feeds; treat news APIs as optional, licensed, per-user | V | [S8][S10][S15][S25]-[S30] |
| 5 | **Do not scrape** Reuters, WSJ, MarketWatch, FT; use RSS/API/regulator feeds only, show headline + link + own extractive snippet | V (robots/ToS), I (legal reading) | [S20][S18] |
| 6 | Lens = **structure priors + learned scorer on your labels + calibrated + conformal**; laya is one candidate in a bake-off, not the default | I | laya's own numbers: base checkpoints are below majority class, typed-decisions is a specialist for 4 synthetic workflows [S5][S6] |
| 7 | Event taxonomy keyed to **8-K item numbers** and Form 4/13F; materiality rubric tied to Buffett/Lynch observables (section 3.3) | V (items), I (rubric) | [S13][S38][S39] |
| 8 | Entity resolution: **identifier-first, name-second, ambiguity-gated** (SEC CIK/ticker files, cashtags, "(NASDAQ: X)" patterns, OpenFIGI/GLEIF aliases) | I | [S8][S60] |
| 9 | Own scheduler on stdlib + `x/time/rate`; adopt cvewatch's `httpx` gate, health (GREEN/YELLOW/RED) and per-source due times | V | [S2] |
| 10 | MCP server on go-sdk **v1.8.0**, spec **2026-07-28**, read-only by default, feed fields fenced as untrusted | V | [S73][S74] |

### 1.3 The five biggest ways the scaffold is wrong or risky

1. **laya config chooses the wrong checkpoint and an unsafe bind.** `model: typed-decisions` is a specialist for four synthetic workflows (invoices, security incidents, customer service, agent traces) and "may perform at or below the base" elsewhere; base checkpoints score 0.362/0.352 on its benchmark against a 0.461 majority-class baseline [S5][S6]. `laya-serve` also binds `0.0.0.0` by default (`LAYA_HOST`) [S3]. The scaffold's "stays off until calibrated" rule is right, but the claim that laya answers are "probabilities" is only true after you refit temperatures [S5].
2. **The sanitizer is not enough for hostile feed text.** I ran the scaffold's `sanitize.Text` on probes: CSI/OSC/DCS/APC sequences lose their ESC byte, but bidirectional overrides (U+202E), zero-width characters and Unicode tag characters (U+E0000 range, usable for invisible LLM prompt injection) pass through unchanged [S80]. This matters for the TUI, for email/webhook delivery, and for any LLM stage.
3. **Event taxonomy and materiality are too thin for the stated Lynch/Buffett goal.** 12 kinds, no bankruptcy/delisting, restatement (Item 4.02), cybersecurity (Item 1.05), impairment (Item 2.06), dilution (Item 3.02), ownership (13D/G, 13F), litigation, credit events, and no written rubric for "materiality 1..5" [S13]. Without a rubric the labels will not be consistent and evaluation is meaningless.
4. **Source handling assumes feeds behave like cvewatch's.** The EDGAR "current filings" Atom feed works [S14] but is a rolling window, has no ETag/Last-Modified (`cache-control: no-cache`), is ISO-8859-1, and a busy form type (8-K, Form 4) returned a full 100 entries at `count=100`, so at a 30-minute interval entries can be lost unless you page with `start=`. Conditional-GET and "since" back-fill assumptions from cvewatch do not hold for this feed [S14][S2].
5. **Legal/licensing exposure is unmodelled.** Free-tier news APIs forbid redistribution or business use (Finnhub: delete data when the subscription ends; personal plans not for any business use; Tiingo "internal use only"; Massive "individual use only") [S28][S29][S30]. A "multi-tenant, omnichannel" product that pushes news to others turns a personal tool into redistribution, and in the EU interacts with the press-publisher right and the MAR rules on investment recommendations [S35][S36]. The scaffold has no per-source licence field and no "may we show/store/forward this?" flag.

Also stale: CI uses `actions/checkout@v4`, `setup-go@v5` (current v7.0.1 / v7.0.0) [S81]; the YAML library cvewatch uses (`gopkg.in/yaml.v3`) comes from an archived repo [S83]; a Teams "adapter" must target Workflows, since Office 365 connectors were retired in May 2026 [S77]; tag-cloud tiers are linear, which collapses on heavy-tailed counts (I).

---

## 2. Scaffold audit

Where: README = README.md, CFG = app/config/*.yaml, SRC = app/src/<pkg>, CI = .github/workflows/ci.yml, MOD = app/go.mod.

| # | Item | Where | Verdict | Evidence |
|---|---|---|---|---|
| 1 | Go version `1.27` | MOD | Holds | Go 1.27.3 and 1.26.10 are the stable releases; local toolchain is 1.27.1 [S82] |
| 2 | `actions/checkout@v4`, `actions/setup-go@v5` | CI | Wrong (stale) | Current: checkout v7.0.1 (2026-07-20), setup-go v7.0.0 (2026-07-16), goreleaser-action v7.3.0, golangci-lint v2.15.0 [S81]. cvewatch has the same staleness [S2] |
| 3 | CI = `make vet test build` on Ubuntu only | CI | Weak | cvewatch runs a 3-OS matrix with `-race`, gofmt check and govulncheck [S2] |
| 4 | "Layout follows cvewatch" | README | Holds, with limits | Package layout, two binaries, YAML config mirror cvewatch; its store and ledger do not transfer (3.0) [S2] |
| 5 | SEC source URL `.../browse-edgar?action=getcurrent&type=10-K&output=atom` | CFG sources | Holds (works) with caveats | HTTP/2 200, `application/atom+xml`, 5 `<entry>` at 11:21 EST 2026-11-03, titles carry company name and CIK. Caveats: ISO-8859-1 prolog; `cache-control: no-cache, no-store`; no ETag/Last-Modified returned; `count=100` returns 100 entries for 8-K and Form 4 so busy types overflow a 30 min poll [S14] |
| 6 | Fed URL `https://www.federalreserve.gov/feeds/press_all.xml` | CFG sources | Holds | HTTP/2 200, RSS 2.0, `etag` and `last-modified` present (Cloudflare cache HIT), so conditional GET works [S15] |
| 7 | Both sources `enabled: false` "check each before enabling" | CFG | Holds | Both verified above; enabling is safe if the User-Agent is declared (SEC) |
| 8 | `domain: sec.gov` and "Domains match exactly" | CFG, SRC/watch | Risky | EDGAR content is served from `www.sec.gov`, `data.sec.gov`; exact string match will split one publisher in the domain aggregate. Use the registrable domain (public suffix list) (I) |
| 9 | Tiers 1/2/3 (primary, reporting, commentary) | SRC/model | Holds (design) | Sound; no evidence needed, but needs a per-source licence/usage field (see 1.3 #5) |
| 10 | `fetch.interval: 30m` for everything | CFG | Unverified / blunt | No source states a polling interval. SEC states only the 10 req/s cap [S8]. Use per-source intervals and conditional GET (I) |
| 11 | `refresh.per_host: 2`, `max_backoff: 6h` | CFG | Holds | Far below SEC's 10 req/s; cvewatch's gate also honours Retry-After and a 429 cooldown [S2][S8] |
| 12 | `httpx` "User-Agent" (doc only) | SRC/httpx | Gap | SEC asks for a declared agent with contact details and blocks undeclared automation; I triggered the block page myself [S8][S10][S14] |
| 13 | `since: 2026-11-01` back-fill | CFG | Partly holds | EDGAR submissions API/daily-index can go back; RSS feeds cannot (cvewatch notes the same limit for its RSS sources) [S2][S9] (I) |
| 14 | laya "typed choice / score / noul questions" | SRC/lens/laya | Holds | `choice`, `score`, `noul` in README and serve.py; `MAX_SCORE_LEVELS=32` [S3] |
| 15 | `POST /v1/systemone/batch` | SRC/lens/laya | Holds | Body fields `states`, `questions`, `model`, `max_len`, `head_max_len`, `task`, `lang`, `min_confidence`, `batch_size`, `sort_by_length`; one `asyncio.Lock` serialises inference [S3] |
| 16 | `model: typed-decisions` | CFG laya | Wrong for this task | Card: "specialist for four synthetic workflows", "may perform at or below the base laya elsewhere"; base 0.362 vs majority 0.461 [S5][S6] |
| 17 | `url: http://127.0.0.1:8000` | CFG laya | Holds, but server default is unsafe | `LAYA_HOST` default `0.0.0.0`; `LAYA_API_KEY` optional [S3]. Must start with `LAYA_HOST=127.0.0.1` |
| 18 | "Keep each `choice` under about 20 options" | SRC/laya | Holds | Option budget `head_max_len` 192 (English) / 256 (others); banking77 (77 labels) falls to 0.425 [S5][S3] |
| 19 | "Checkpoints read 512–1024 tokens" | SRC/laya | Holds | English 512, multilingual 1024 (up to 8192 via `max_len`), typed-decisions 1024; beyond ~4000 tokens accuracy varies [S4][S7] |
| 20 | `min_confidence: 0.6` leaves low-confidence fields at zero | CFG, SRC/laya | Risky | laya's `confidence` is 1 minus normalised entropy; a threshold from another system does not transfer; gate on `answer_confidence` and fit on held-out data at your option counts [S3 README] |
| 21 | "calibration before trusting" | README | Holds | Checkpoints ship over-confident: ECE 0.466→0.081 (laya), 0.314→0.106 (multilingual) after temperature refit; typed-decisions ECE 0.213 [S5][S6] |
| 22 | `laya-train` exists | SRC/labels | Holds | Console script `laya-train` in 0.4.0 entry points [S3] |
| 23 | laya is Apple-silicon capable | README/CFG | Holds | Device order CUDA→MPS→CPU; Apple-silicon run of sort-by-length: 41.25 s→29.23 s per 128 reviews; MPS first call ≈13 s kernel compile (measured on an Intel Mac Pro with AMD GPU, not Apple silicon) [S3] |
| 24 | laya licence | README | Holds (code, cards) | Apache-2.0 on GitHub, PyPI metadata and model cards [S4][S6][S7]. Training-data provenance of the checkpoints is not stated on the cards (U) |
| 25 | Installed laya 0.4.0 | environment | Stale | Upstream is v0.4.22 (published 2026-11-03), 22 patch releases ahead; repo created 2026-09-18 [S4]. Pin and re-check before relying on it |
| 26 | `Score.Relevance` "calibrated 0..1" | SRC/model | Design, not yet true | Needs labels + temperature/conformal step (3.2) |
| 27 | Keyword lens (`boost`: earnings, 10-K, moat...; `demote`: price target...) | CFG lens | Unverified | No evidence the list separates relevant from noise; "earnings" and "dividend" occur in most routine items. Treat as L0 feature, measure it (I) |
| 28 | 12 `EventKind`s | SRC/model | Incomplete | 8-K defines items for bankruptcy 1.03, cybersecurity 1.05, impairments 2.06, delisting 3.01, unregistered equity sales 3.02, accountant change 4.01, non-reliance 4.02, control change 5.01, officer/director change 5.02 [S13] |
| 29 | `Materiality 1..5` with no rubric | SRC/model | Gap | See 3.3 for a proposed rubric (design, not sourced) |
| 30 | `Article.ID` = hash of canonical URL | SRC/model | Holds, incomplete | Needs URL canonicalisation; for filings use the accession number (visible in the EDGAR feed: `AccNo: 0000320193-26-000026`) [S14] |
| 31 | `Entity` fields (ticker, exchange, CIK, aliases) | SRC/model | Incomplete | No FIGI, ISIN, LEI, MIC; SEC says its ticker files' "accuracy and scope are not guaranteed" [S8] |
| 32 | cluster: canonical URL → title shingles | SRC/cluster | Holds (design) | Add SimHash/MinHash and an embedding pass (3.5) |
| 33 | `timeline.Daily` buckets by UTC midnight | SRC/timeline | Questionable | US filings and market days follow exchange time; late-evening ET filings land on the next UTC day (I) |
| 34 | `tagcloud` linear tiers | SRC/tagcloud | Wrong for real data | Term counts are heavy-tailed; a linear map puts nearly everything in tier 1. Use log or rank-based tiers (I) |
| 35 | `sanitize.Text` | SRC/sanitize | Partially holds | Probe results [S80]: OSC/CSI/DCS/APC leave no live escape; unterminated OSC leaves payload text; **bidi override, zero-width space, bidi isolates, Unicode tag characters survive** |
| 36 | Base/TenantStore split | SRC/store | Holds (design) | Better enforced as separate DB files (3.6); cvewatch's whole-state "replace everything" Save does not scale to news volume [S2] |
| 37 | Webhook channel has a "Teams adapter" | SRC/channel | Wrong target | Office 365 connectors (incoming webhooks) were retired from May 2026; use Workflows and Adaptive Cards [S77] |
| 38 | Email via SMTP | SRC/channel | Unverified | Not researched here (deliverability, SPF/DKIM, provider app-password rules) |
| 39 | Feed channel = Atom | SRC/channel | Holds | Fine; WebSub is optional (W3C Recommendation 2026-06-02) [S17] |
| 40 | `tui.View.Render(w,h) string` | SRC/tui | Fits as an inner interface | Bubble Tea v2 `View()` returns `tea.View` (declarative fields for alt-screen, mouse, cursor), so the shell wraps your strings [S70] |
| 41 | `tab`/`shift+tab`/`1-9` for watch switching | SRC/tui | Holds | Bubble Tea v2 reports richer keys (kitty protocol where available); fall back gracefully [S70] |
| 42 | MOD has no dependencies yet | MOD | Note | YAML: `gopkg.in/yaml.v3` (cvewatch's choice) lives in an archived repo (archived 2025-04-01); maintained fork `go.yaml.in/yaml/v3` v3.0.6 (2026-10-13); `goccy/go-yaml` v1.19.2 [S83] |
| 43 | "Evaluation of its own quality" (precision@k, Brier, ECE) | SRC/eval | Holds, incomplete | Add PR-AUC, nDCG, reliability bins, coverage for conformal sets, per-source precision (3.9) |
| 44 | Labels only from what the user sees | SRC/labels, TUI keys | Risk | Selection bias: precision is measured only on top-ranked items; add an exploration slice (3.9) (I) |
| 45 | Per-tenant labels, shared base scores | README | Design tension | A shared base score cannot reflect per-tenant labels; either train per-tenant heads on shared embeddings, or accept the base score as a prior (3.2) (I) |
| 46 | `Lens.Threshold == 0` means "use global" | SRC/watch | Minor | A watch cannot ask for threshold 0.0 deliberately (code reading) |
| 47 | `bin/` holds built binaries | tree | Note | Built 2026-11-03; project is not a git repo, so no ignore rule is in force |

Tests: `go test ./...` passes for `channel, dashboard, eval, model, nav, sanitize, tagcloud, tenant, timeline, tui, watch`; the other packages have no tests [local run 2026-11-03]. The README's "working and tested" list matches this.

---

## 3. Findings by question

### 3.0 The reference project (cvewatch v2.0.0): what to adopt, what not

Facts read with `gh api` at tag v2.0.0 (published 2026-10-31, MIT) [S2]: go 1.27.3; bubbletea v1.3.10; lipgloss v1.1.0; go-sdk v1.8.0; modernc.org/sqlite v1.61.0; yaml.v3 v3.0.1. Current: bubbletea v2.1.1, lipgloss v2.0.6, bubbles v2.2.1, modernc v1.61.2, so **its TUI libraries are one major version behind** [S63].

| cvewatch pattern | Adopt for news? | Notes |
|---|---|---|
| `httpx`: per-host rate, retries with backoff, `Retry-After`, `X-RateLimit-*` parsing, 429 cooldown, 16 MiB body cap, ≤5 redirects, redirect refusal, private/loopback/link-local denied by default (SSRF), honest UA, URL redaction in logs | **Yes, copy the design** | Add robots.txt (RFC 9309) and per-source conditional-GET state [S2][S18][S19] |
| Daemon wakes at the next due project, jitter-free `minSleep`, heartbeat for status, watches its config files and re-judges on change | **Yes** | News analogue: per-source due times; "re-score on lens version bump" |
| Health GREEN/YELLOW/RED with a persistent problems list (stable key, level, hint, since) | **Yes** | Maps to the scaffold's `/sources` screen; a stale or failing source is a problem until fixed |
| Settings screen with schema, preview diff, atomic save + backup, comments kept, secrets never shown | **Yes (later)** | Good fit for watch editing |
| `doctor`, `.env` loading, `auth_env` bearer from an env var | **Yes** | Needed for API keys; only the variable name is ever logged |
| MCP: read tools always on, write tools behind `-allow-write`, instruction text tells the model that fields are untrusted | **Yes** | Exact pattern to copy; stdio transport |
| GoReleaser, `CGO_ENABLED=0`, trimpath, tar.gz/zip, checksums, image with provenance+SBOM, CHANGELOG-driven release notes | **Yes** | Stays valid only while the build is CGO-free (modernc yes; DuckDB/ONNX no) |
| TUI: breadcrumb, jump box, context chip, mouse, Esc/Backspace = back, footer written from the same table as the keys | **Yes** | Matches the scaffold's `nav` and key table |
| Whole-state JSON store, optimistic `Rev`, "Save replaces every row" | **No** | Its own comment says ceiling is "thousands of records"; news is millions of rows [S2] |
| Ledger of findings with open/fixed/ignored per project × environment, evidence packs, SARIF, hash-chained audit | **No** (maybe a light alert-decision log) | News is a stream with read/unread and labels, not a remediation workflow |
| Version-range matching, CPE, lockfile/SBOM matrices, KEV/EPSS priority | **No** | No analogue |
| `vendor` grouping and `access` grants by `vendor:acme` | **Maybe** | Reasonable template for tenant access rules if tenants ever see each other's watches |
| Feed overrides: a file in `sources/` with a shipped id overrides only the fields it names | **Yes** | Use for source config (user overrides catalog) |

### 3.1 Data sources, access rules, licensing

**Decision table (primary sources)**

| Option | Evidence | Access rules / limits | Licence / reuse | Cost | Fit | Verdict |
|---|---|---|---|---|---|---|
| SEC EDGAR current-filings Atom | Works today, 5 10-K entries, 100 for 8-K/Form 4 at `count=100`; no validators; ISO-8859-1 [S14] | Declare UA (company + admin email); max 10 req/s regardless of machines; automated tools outside policy "managed"; may block IPs [S8][S10] | "Public information... may be copied or further distributed... without the SEC's permission"; cite SEC; no SEC logos; EDGAR names are trademarks [S10] | Free | Core | **Use**, page with `start=`, dedupe by accession |
| `data.sec.gov` submissions | Endpoint `submissions/CIK##########.json`, typically <1 s latency; no API key; no CORS [S9] (gzip served, `content-encoding: gzip` [S14]) | Same 10 req/s | Same | Free | Core for per-company history and back-fill | **Use** for followed CIKs |
| XBRL companyfacts / companyconcept / frames | Updated typically <1 min after filing [S9] | Same | Same | Free | Fundamentals for stock pages, Lynch category inputs | **Use** |
| Bulk: `submissions.zip`, `companyfacts.zip` | Rebuilt nightly about 03:00 ET [S9] | Download once, not per request | Same | Free | Initial load, offline | **Use** for bootstrap |
| daily-index / full-index, Feed, Oldloads | Listed under "Accessing EDGAR data" [S8] | Same | Same | Free | Complete back-fill for `since` | **Use** |
| EDGAR full-text search (EFTS) | Covers filings since 2001 incl. exhibits; page documents no API, no rate limit, no programmatic-use statement; contact `textsearch@sec.gov` [S11] | Undocumented | Same | Free | Ad hoc only | **Do not build on it** |
| SEC 13F data sets | Quarterly ZIPs, flattened XML-derived; latest June–Aug 2026; page updated 2026-08-31 [S12] | Download | Same | Free | "Who bought" signals | **Use** (quarterly) |
| Form 4 / Section 16 | Structured data set not confirmed on the page I read [S12] | - | - | - | Insider buying is central to Lynch/Buffett lens | **Parse Form 4 XML from the feed** (format details U) |
| SEC rule watch | Optional semiannual reporting proposed 2026-05-05 (new Form 10-S), proposal stage, comments closed ~2026-07-06; no final rule found [S16] | - | - | - | Changes event cadence if adopted | Track; don't design around it |
| Federal Reserve press RSS | 200, RSS 2.0, ETag + Last-Modified [S15] | Not stated | Not stated (U) | Free | Macro | **Use** |
| ECB | RSS for press/speeches/statistics exist; statistics reuse requires crediting the source and not modifying the data and metadata; page on press-release copyright not retrieved [S23] | `robots.txt` allows crawling with language-variant disallows [S20] | Statistics: free reuse, conditions above | Free | Macro | **Use** statistics feeds; check press terms (U) |
| UK FCA National Storage Mechanism | FCA user guide (V11.0, Oct 2026): no supported programmatic access today; API and bulk download planned for late 2027 (search-result excerpt) [S22] | Web UI only | - | - | UK regulatory news | **Not yet**; revisit 2027 |
| EU ESAP | Legal duty to be live by 2027-07-10; API access planned, free, bulk download; data collection began 2026-07-10 [S21] | - | Open terms intended | Free | EU filings in one place | **Plan for mid-2027** |
| Japan EDINET API v2 | API key required on every request; spec updated June 2026; mechanical scraping of the site prohibited except through the API; FSA may suspend heavy traffic without notice [S24] | key | Secondary use incl. commercial reportedly allowed (older source) (U) | Free | Japan filings | **Use if Japan matters** |
| Company IR feeds | No standard; each company differs | per site | per site | Free | Primary news for held names | **Curate one by one** in `sources.yaml` |
| Others (HKEX, ASX, SEDAR+, BoE, BoJ, India BSE/NSE, newswires' own feeds) | Not researched | - | - | - | - | **U** |

**Push delivery.** WebSub is a W3C Recommendation dated 2026-06-02 (publisher/hub/subscriber, `hub.challenge` verification, optional HMAC `X-Hub-Signature`, hubs enforce leases) [S17]. I found no regulator or central bank in my reading that advertises a WebSub hub; EDGAR documents none [S8][S9]. So poll primaries and consider WebSub only for the stoxicity feed channel you publish (I).

**Commercial / free news and market-data APIs (what the terms allow)**

| Provider | Free / entry | Paid | Terms evidence | Verdict for stoxicity |
|---|---|---|---|---|
| Alpha Vantage | 25 requests/day [S25] | 75–1,200 req/min at $49.99–$249.99/month (second set ≈10× is probably annual, U) | Real-time/15-min US data "personal use", separate entitlement; news endpoint not confirmed on page [S25] | Optional prices only |
| Marketaux | 100 requests/day, 3 articles/request [S26] | $29–$199/month (page shows monthly/annual inconsistently) | Terms page not read; whether text is full or snippet not stated [S26] | Optional news metadata; read terms first |
| Finnhub | Free tier exists; limits not retrievable (JS page) [U] | Not retrieved | ToS: delete data when subscription ends; no redistribution of data "or derived results" without written approval; personal plan not for any business use even internal [S28] | Personal use only; **never** feed other tenants |
| Tiingo | $0: 50 req/h, 1,000/day, "internal use only" [S29] | Power $30/month; commercial internal $50/month | News: 3 months queryable on the footnote; no display/sharing to others [S29] | Personal use only |
| Massive (massive.com; I believe the former Polygon.io, U) | no free tier shown | Stocks Advanced $199/month, "Individual use only"; Benzinga news $99/month per dataset [S30] | Individual use | Too costly for the value |
| Alpaca Market Data | Basic plan free; 200 calls/min free; Benzinga-sourced news from 2015, ~130 items/day (2021 launch post; may be outdated) [S31] | Algo Trader Plus $99/month | Account-bound | Optional, good for per-ticker headlines if you hold an account |
| GDELT | Free; "unlimited and unrestricted use... commercial" with citation and link; page says daily updates (the 15-minute cadence I expected is not on the page I read) [S27] | - | Redistribution allowed with attribution | Good for **discovery of domains/themes**, not for ticker-grade relevance (I) |

**Independent brokers: what they expose** (official APIs only)

| Broker | What is exposed | Limits | Integration verdict |
|---|---|---|---|
| Interactive Brokers | Web API (OAuth 2.0 / 1.0a) and Client Portal: account data, positions, watchlist endpoints, news; TWS API news needs separate news subscriptions [S32] (IBKR docs returned 403 to my fetcher; this is from a search excerpt of their pages) | 10 req/s per authenticated user; per-endpoint tighter limits (e.g. `/portfolio/accounts` 1 per 5 s); 429 and 10-minute IP penalty box [S32] | **Read-only import of positions and watchlists** to seed watches; do not use as a news source |
| Trading 212 | Public API: positions 1 req/s, orders, instruments; rate limits per account; Invest and Stocks ISA only, not SIPP [S33] | headers `x-ratelimit-*` | Positions import only |
| Saxo | OpenAPI; 120 req/min per session per service group, 10M/day per app; 429 on excess [S34] (news and watchlist endpoints not confirmed) | | Optional |
| Alpaca | Market data + news (above) | | Optional |

**What is safe to scrape.**

- **Safe (V):** regulator and central-bank feeds and APIs that publish for reuse (SEC [S10], Fed RSS [S15], ECB statistics with attribution [S23]); GDELT with attribution [S27]; licensed APIs inside their terms.
- **Not safe (V):** Reuters `robots.txt` says collection "through automated means is prohibited unless you have prior written consent"; WSJ and MarketWatch say the same and disallow `*`; FT `robots.txt` "expressly" prohibits use for machine learning or AI purposes [S20]. RFC 9309 says robots rules are not access authorisation, so absence of a Disallow is not permission either [S18]. Bloomberg disallows search/account paths only, so a robots-only reading would allow article pages, but its terms were not read (U).
- **Headlines and snippets:** whether a headline or short summary is protected depends on jurisdiction. EU: the press-publisher right covers press publications for two years from 1 January after publication, excludes hyperlinks and "very short extracts", and a pending CJEU referral (C-250/25, Budapest) has an Advocate General opinion that RAG-style use by a chatbot is an online use under Article 15(1) [S35]. US: I did not retrieve case law on headline copyright (U; Associated Press v. Meltwater is the case I would read next). Conservative design: store headline + URL + your own extractive sentence ≤ ~30 words, never full article text from unlicensed sources (I).
- **Text-and-data-mining opt-out** in the EU is by "appropriate" means such as machine-readable signals (Article 4(3)); I could not retrieve the paragraph text (U), so honour `robots.txt` and `ai.txt`-style opt-outs as policy (I).

**Recommendation (V for facts, I for plan).** Phase 1 uses EDGAR + Fed/ECB-style feeds + 10–20 hand-picked company IR feeds, all free and reusable. Treat any news API as a per-user optional plugin with a `licence:` field on the source (`personal`, `internal`, `redistributable`) that the channel layer enforces: personal-licence items never go to another tenant's webhook, email or feed.
*Cheapest test:* enable the two scaffold sources with a declared UA, run for 24 h, count duplicates, missed accessions (compare to `daily-index`), and 304/200 ratio.

### 3.2 Relevance scoring ("lens")

**What primary sources actually say**

- laya: 3 checkpoints, 421M/322M/421M params; Apache-2.0; single forward pass; T4 latency 32.8 ms for one question (multilingual), 103–332 questions/s batched [S4][S5][S6][S7]. Self-reported: base checkpoints score 0.362 and 0.352 on its typed-decisions benchmark, below the 0.461 majority-class baseline; the fine-tuned specialist scores 0.766 (ECE 0.213, over-confident); "all of the capability on this benchmark comes from fine-tuning" [S5]. Closest task to ours in its own table: **RAG passage relevance 0.625** (training mix included it) [S5]; AG News 0.950 (4 coarse topics) [S5]. No financial-news result is reported anywhere [S5][S6]. Known issues listed on the card: ordinal `score` is the weakest primitive (SST-5 0.372), `noul` can follow option labels (#156) [S7]. The model card calls laya "a fast base to specialise, not a zero-shot decision engine" [S7].
- Adoption signals: repository created 2026-09-18 with 34,948 stars and 90 open issues on 2026-11-03; HF shows 4,716 downloads last month for the typed-decisions card [S4][S6]. I cannot judge what that ratio means; I only note that maturity is weeks, not years (U on community validation).
- Encoders: ModernBERT (arXiv 2412.13663, Dec 2024; 8192-token native context; 2T training tokens) [S41]; repo licence Apache-2.0 [S78]. A 2026 finance fine-tune (Modern-FinBERT-large, Apache-2.0, ModernBERT-large base, 0.4B) publishes no metrics [S43]. ProsusAI FinBERT: ~0.1B, **CC BY-NC-SA 4.0** and trained on Financial PhraseBank (CC BY-NC-SA 3.0): non-commercial, share-alike [S42].
- Finance-specific vs general: a July 2026 paper over twelve sentiment models reports that general LLMs match finance-specific transformers without fine-tuning, that better accuracy did not mean stronger economic relationships, and that **no model's sentiment is significantly associated with next-day returns** (abstract only; numbers not retrieved) [S44]. FinMTEB (EMNLP 2025, 64 datasets, 7 tasks): general-benchmark scores correlate weakly with finance performance, domain-adapted models beat general ones, and a **bag-of-words baseline beat dense embeddings on finance STS** [S50]. FinBen (36 datasets, 24 tasks, 15 LLMs): LLMs do well on extraction and textual analysis, struggle with reasoning and forecasting [S53].
- Embeddings: Qwen3-Embedding-0.6B, Apache-2.0, 32K context, MRL 32–1024 dims, MTEB English v2 mean 70.70 / multilingual 64.33 [S47]. EmbeddingGemma-300m: Gemma licence (gated, accept terms), 2048-token input, 768 dims with MRL to 512/256/128, MTEB English v2 69.67 / multilingual 61.15, QAT int4/int8 variants [S46].
- Zero-shot NLI: `deberta-v3-large-zeroshot-v2.0`: base MIT, but this variant's training data includes non-commercial sets (use the `-c` variants for strict licensing); zero-shot mean f1 0.673 over 28 tasks, 0.846 with ≤500 examples per class; 512-token window [S45].
- Few-shot encoders: SetFit fine-tunes a sentence transformer on a small number of pairs and reports training an order of magnitude faster than comparable PEFT/PET methods (abstract; exact shots-per-class not retrieved) [S52].
- Small local LLMs: Gemma 4 (E2B, E4B, 26B A4B, 31B), Apache-2.0; on a MacBook Pro M4 GPU E2B decodes 160 tok/s (prefill 7,835), E4B on M4 Max GPU 101 tok/s (prefill 2,560) [S48]. Qwen3.5-4B (Feb 2026), Apache-2.0, 262K context, vendor-reported benchmarks only [S49].
- Grounding checker: Vectara HHEM-2.1-Open, Apache-2.0, ~0.1B (flan-t5-base), under 600 MB RAM, ~1.5 s per 2k-token input on x86 CPU; balanced accuracy 76.55% AggreFact-SOTA, 64.42% RAGTruth-Summ, 74.28% RAGTruth-QA [S59]. It is a screen, not a guarantee.
- Calibration: temperature scaling is "surprisingly effective" on most datasets (Guo et al., ICML 2017) [S54]; split-conformal gives distribution-free coverage guarantees (Angelopoulos & Bates) [S55].

**Decision table**

| Option | Evidence | Licence | Apple-silicon | Latency | Fit | Verdict |
|---|---|---|---|---|---|---|
| Keyword lens (scaffold) | none | own | yes | µs | Weak alone | Keep as L0 *feature* |
| Structure priors (form type, 8-K item, source tier) | 8-K items are defined by SEC form [S13] | public | yes | µs | Very high precision for filings | **L0, build first** |
| BM25/TF-IDF + logistic regression on user labels | FinMTEB: BoW beats dense on finance STS [S50] | own | yes | ms | Strong baseline; transparent | **Mandatory baseline** |
| Embedding (Qwen3-Embedding-0.6B or EmbeddingGemma) + LR/MLP head | MTEB numbers [S46][S47]; finance gap [S50] | Apache-2.0 / Gemma terms | yes via ONNX/MLX/GGUF (I) | tens of ms/article on M-series (I, untested) | Likely best cost/quality | **L1 candidate** |
| Fine-tuned ModernBERT-base/large classifier | No finance-news metrics found [S43] | Apache-2.0 | PyTorch MPS / ONNX | ~100 ms (I) | Needs ≥1–2k labels | Test after labels exist |
| laya fine-tuned on your labels | Fine-tuning is where its accuracy comes from [S5] | Apache-2.0 | MPS (V) | 33–146 ms/question class [S5] | Typed multi-question output is attractive; maturity weeks | **Bake-off entrant**; Go ONNX route exists [S76] |
| laya zero-shot (any checkpoint) | Below majority baseline on its own benchmark [S5] | Apache-2.0 | yes | - | No | **Reject** |
| FinBERT (ProsusAI) | CC BY-NC-SA 4.0 [S42] | non-commercial | yes | ~50 ms | Sentiment only, not relevance | Avoid in anything redistributed |
| Zero-shot NLI (DeBERTa) | f1 0.673 zero-shot [S45] | MIT base; training-data caveat | yes | 100s ms/label | Weak alternative to a learned head | Reject as primary |
| Small LLM judge (Gemma 4 E4B / Qwen3.5-4B) | Throughput [S48]; judging quality on this task untested | Apache-2.0 | yes | ~0.3–1 s/item (I from tok/s) | Best for label bootstrapping and uncertain band | **L2 only** |
| Frontier API LLM | not evaluated (no paid calls made) | vendor | n/a | n/a | Privacy/cost | Optional, off by default |

**Recommendation (I).** Architecture is a cascade: L0 structure → L1 learned head → L2 judge for the uncertain band. Calibrate L1 with temperature scaling on a held-out labelled split, then use split-conformal to produce a three-way output (relevant / not / abstain) with a stated coverage; route "abstain" to L2 and to the user's label queue. Because the user said "best", measure rather than assume: define the bake-off as the **cheapest experiment**:

1. Collect 2–3 weeks of ingest (≈2–5k items), label 600–1,000 with the section 3.3 rubric (two annotators on ≥150 for agreement).
2. Candidates: L0 only; BM25+LR; Qwen3-Embedding-0.6B+LR; EmbeddingGemma+LR; laya-multilingual and laya fine-tuned (via `laya-train`, or the Apple-silicon script) on 70% of labels; Gemma 4 E4B zero-shot as judge.
3. Report PR-AUC, precision@20/day, ECE (10 bins) before/after temperature scaling, Brier, abstain rate at 90% conformal coverage, p95 latency on the user's Mac.
4. Decision rule (proposed): adopt the simplest candidate within 0.02 PR-AUC of the best; laya wins only if it beats embedding+LR by ≥0.05 PR-AUC *and* the sidecar/ONNX cost is accepted.

**Active learning and drift (I; standard practice, not re-verified here).** Label queue = 60% uncertainty (score within the conformal abstain band), 30% cluster-diverse (one per cluster), 10% uniform random (unbiased precision estimate; fixes audit row 44). Retrain weekly; keep a frozen "golden" set never used for training. Drift signals: PSI of score deciles, mean embedding centroid shift, per-source relevant-share (the scaffold's `domains` aggregate doubles as a drift detector), and monthly re-label of 50 random items.

### 3.3 Operationalising Lynch and Buffett

**Sources used.** Buffett: Berkshire owner's manual (read from the PDF text) [S38]; 1986 letter, owner earnings and franchise discussion [S39]. Lynch: *One Up on Wall Street* (Simon & Schuster, 1989, with John Rothchild); the book text is under copyright and was **not read**; the six-category structure was confirmed only from secondary summaries and Wikipedia [S40] → treat Lynch details as **U**.

**Criteria extracted (own words)**

| Criterion | Source | Machine-observable proxy | Event kinds to watch |
|---|---|---|---|
| Judge by growth in per-share intrinsic value, not size | Owner's manual [S38] | Share count trend, buybacks, dilution, per-share metrics from XBRL | capital-allocation, dilution |
| Intrinsic value = discounted cash that can be taken out over the business life; book value is a rough tracker | [S38] | Free cash flow series (companyfacts) | earnings, guidance |
| Owner earnings: reported earnings plus depreciation/amortisation/other non-cash, minus average capex needed to keep competitive position and volume, minus any extra working capital | 1986 letter [S39] | Net income + D&A − (maintenance capex proxy) − ΔNWC; maintenance capex is an estimate | earnings, impairment |
| Reported "cash flow" that ignores maintenance spending misleads | [S39] | Flag items citing EBITDA/"cash flow" only | filing text |
| Retained earnings must create at least $1 of market value per $1 retained (rolling test) | [S38] | Retention vs market-cap change over 5 yr | capital-allocation |
| Use debt sparingly; refuse opportunities rather than over-lever | [S38] | Net debt/EBITDA, interest coverage, new facility/covenant | 2.03/2.04 items, credit |
| Issue stock only when business value received equals value given | [S38] | Item 3.02 unregistered sales, ATM programmes, convertibles, option grants | dilution |
| Acquisitions only if they raise per-share intrinsic value; no empire-building | [S38] | 2.01 acquisitions, goodwill, price vs earnings | m&a |
| Franchise vs commodity: strong franchises keep pricing power; commodity-like businesses pass changes to customers | [S39] | Gross-margin stability, pricing-power language, share changes | product, competitive |
| Candour: report the facts the owner would want | [S38] | Non-reliance (4.02), auditor change (4.01), restatements, tone of disclosures | accounting |
| Patience: no interest in selling good businesses; reluctance to sell laggards | [S38] | Disposal announcements (2.01 dispositions), divestiture of core | m&a, management |
| Management with owner orientation | [S38] | Insider purchases (Form 4), pay structures (5.02), tenure | insider, management |
| Lynch: classify each stock into six types so you know what to expect | [S40] (U) | Category tag from XBRL growth/cyclicality/asset data | all |

The six Lynch types and their usual expectations (slow growers, stalwarts, fast growers, cyclicals, turnarounds, asset plays) appear in the secondary summaries; the quantitative thresholds people attribute to him (for example growth bands) were not verified (U).

**Proposed event taxonomy** (replaces the 12 kinds; design, `V` only where an SEC item is cited [S13])

| Group | Kind | Primary signal | Typical base materiality |
|---|---|---|---|
| Results | `results` | 8-K 2.02, 10-K/10-Q | 3 |
| Guidance | `guidance` | 8-K 7.01/2.02 text | 3–4 |
| Capital allocation | `buyback`, `dividend`, `issuance` (3.02), `debt` (2.03, 2.04), `spinoff` | 8-K, press release | 3–5 |
| Corporate actions | `acquisition` (2.01), `agreement` (1.01/1.02), `restructuring` (2.05), `impairment` (2.06) | 8-K | 3–5 |
| Governance/people | `officer_change` (5.02), `control_change` (5.01), `bylaws` (5.03), `vote` (5.07) | 8-K | 2–5 |
| Integrity | `non_reliance` (4.02), `auditor_change` (4.01), `cyber` (1.05), `delisting` (3.01), `bankruptcy` (1.03) | 8-K | 4–5 |
| Ownership | `insider_buy`, `insider_sell`, `13d_13g`, `13f_change` | Form 4, SC 13D/G, 13F | 2–4 |
| Business | `product`, `competitive`, `regulatory_company`, `litigation` | press, filings | 2–4 |
| Macro | `rates`, `inflation`, `regulation_sector` | central banks | 2–4 |
| Noise | `price_chatter`, `analyst_target`, `other` | demote | 1 |

Item numbers verified from the SEC Form 8-K PDF text (1.01–1.05, 2.01–2.06, 3.01–3.02, 4.01–4.02, 5.01–5.08, 7.01, 8.01, 9.01); four-business-day filing deadline appears in the form's general instructions [S13].

**Materiality rubric (design; I).** 5 = plausibly changes an owner-earnings or per-share intrinsic-value estimate or the integrity of the numbers (non-reliance, bankruptcy, cyber with material impact, large dilutive issuance, control change, acquisition > ~10% of market cap). 4 = changes durability or capital allocation (buyback/dividend policy, CEO/CFO change, guidance change beyond the range, large debt raise). 3 = new information on an expected path (earnings in line, routine acquisition). 2 = context. 1 = noise (price targets, intraday moves, options flow). The ~10% threshold and the exact bands are mine, not sourced: calibrate by labelling.
*Cheapest test:* label 150 items twice with this rubric; target Krippendorff alpha ≥0.6 on materiality and ≥0.7 on relevance before training anything (thresholds proposed, I).

### 3.4 Entity resolution

| Source | Evidence | Terms | Use |
|---|---|---|---|
| SEC `company_tickers.json`, `company_tickers_exchange.json`, `company_tickers_mf.json`, ~13 MB CIK list | listed on the access page; "accuracy and scope are not guaranteed" [S8] | SEC reuse terms [S10] | US names, CIK, exchange |
| OpenFIGI v3 | mapping 25 req/min and 10 jobs/request without key, 250/min and 100 jobs with key; search/filter 5/min, 20/min with key; "free and open to the public"; licence text not shown on the page [S60] | ToS not read (U) | Map ISIN/ticker/SEDOL to FIGI for non-US |
| GLEIF LEI API | API built on the Golden Copy; rate limits and licence not on the page I read [S61] | U | Legal-entity names/parents |
| Nasdaq Trader symbol directory | `nasdaqlisted.txt`, `otherlisted.txt`, fields incl. ETF and test-issue flags, updated through the day; terms not on page [S62] | U | US listed universe |
| Exchange lists elsewhere / ISO 10383 MIC | not researched | U | |

**Method (I).** Layered, ambiguity-gated:
1. **Hard identifiers (precision ≈ 1):** CIK in EDGAR titles (`Apple Inc. (0000320193)`) [S14]; ISIN/LEI/FIGI; cashtags `$AAPL`; press pattern `(NASDAQ: AAPL)`.
2. **Dictionary match** with an Aho-Corasick automaton over legal names (suffix-stripped) and curated aliases; case-sensitive; minimum length; ticker-only matches require context.
3. **Ambiguity gate:** an alias that is a common word (Apple, Target, Visa, Block, Ford, ALL, IT, ON, NOW, A) is accepted only with a hard signal, a nearby ticker/exchange token, or an embedding-similarity check between the sentence and the company description.
4. **Per-user alias file** that overrides the shipped catalogue field by field (cvewatch's override pattern) [S2].
Go libraries: `cloudflare/ahocorasick` last updated 2024-09, `petar-dambovaliev/aho-corasick` 2025-04 [S63]; the algorithm is ~100 lines if you prefer no dependency.
*Cheapest test:* build a 500-mention gold set stratified over ambiguous names; require ≥0.98 mention precision and ≥0.90 recall on hard-identifier-free text before enabling discovery (thresholds proposed).

### 3.5 Dedup, clustering, trends, summaries

- **Near-duplicates.** Simhash for web crawling is Manku, Jain & Das Sarma, WWW 2007, pp. 141–150 [S57]; the 64-bit/Hamming-3 operating point I remember is **not** on the page I read (U). Go libraries are stale: `mfonda/simhash` 2015, `ekzhu/minhash-lsh` 2019, `dgryski/go-minhash` 2019 [S63]. Implement SimHash (title + first 300 words) and MinHash with LSH yourself, then an embedding pass inside a 48 h window with a cosine threshold tuned on labelled pairs (I).
- **Novelty.** RavenPack's published scores include an Event Novelty Score on a 0–100 scale within a 24-hour window and a Relevance score with 75 commonly used as the cutoff (from academic papers quoting its guide; vendor docs not retrieved) [S79]. Copy the concept: the first cluster member gets novelty 100, later copies are discounted, and the scaffold's "Original gets the credit" rule is the same idea.
- **Burst detection.** Kleinberg's burst model (KDD 2002) is the classic reference (paper not re-read, certificate error) [S58]. A practical choice for daily counts per entity/topic: Poisson or negative-binomial z-score against a 28-day baseline with a minimum-count prior and a minimum number of distinct domains (to avoid syndicated bursts) (I). Run it on *clusters*, not articles.
- **Topics.** BERTopic = embeddings → clustering → c-TF-IDF (arXiv 2203.05794) [S56], Python only. In Go, implement c-TF-IDF over clusters yourself (simple), keep the embedding model shared with the lens.
- **Summaries that do not hallucinate.** Keep the scaffold's extractive default (sentence ids kept). If an abstractive layer is added: the model may only output sentences with citations to ids, then check each against its cited text with HHEM-2.1-Open [S59] and drop what fails; balanced accuracy of 64–77% on public benchmarks means the check reduces but does not remove errors (V for numbers, I for design).

### 3.6 Storage and search from Go

| Option | Version (2026-11-03) | Evidence | Licence | Fit | Verdict |
|---|---|---|---|---|---|
| **modernc.org/sqlite** | v1.61.2 (2026-11-02) wraps **SQLite 3.54.1**; `SQLITE_ENABLE_FTS5`, JSON1, RTREE, STAT4 on; bundles **sqlite-vec v0.1.9**; signed provenance, SBOM, THREAT-MODEL.md [S64] | BSD-3 (SQLite public domain, sqlite-vec MIT) | Pure Go, CGO-free, matches GoReleaser pattern | **Pick** |
| mattn/go-sqlite3 | v1.14.52 (2026-09-05) | MIT (I) | cgo; vec via loadable extension | Alternative if you need exact upstream SQLite and accept cgo |
| ncruces/go-sqlite3 | v0.36.0 (2026-10-29), pre-1.0 | not read (U) | WASM-based, no cgo | Watch |
| DuckDB | duckdb-go v2.20000.0 (2026-10-22) for DuckDB **2.0.0** (2026-10-21); MIT; cgo with prebuilt libs [S67] | MIT | Column-store analytics; single-writer process model (I) | Not needed at this scale; could be an offline analysis tool |
| Embedded Postgres | `embedded-postgres` GitHub releases v2.0.0/v2.0.1 (2026-10-19); Go proxy `@latest` still v1.34.0 (a `/v2` path is likely, U) | | Downloads/ships a server; heavy for a desktop tool | Reject for v1 |
| Turso/libSQL Go | turso-go v0.2.2 (2025-10), go-libsql pseudo-version 2026-04 | | Young rewrite; unverified | Reject for v1 |
| sqlite-vec standalone | latest tagged **v0.1.10-alpha.4** (2026-05-18), "pre-v1, expect breaking changes", Apache-2.0/MIT [S65] | | | Use only through modernc's bundled v0.1.9, or skip it |

- **Vectors.** At 100k articles × 256 dims (MRL-truncated: Qwen3-Embedding supports 32–1024, EmbeddingGemma 128–768 [S46][S47]) float32 is ≈100 MB; brute-force dot products over that are milliseconds in Go. Start with a plain BLOB column and an in-memory scan; use sqlite-vec only if you cross ~1M rows (I). modernc also exposes a pure-Go `vtab` package for custom virtual tables [S64].
- **FTS5.** `bm25()` ranking with column weights, `snippet()`/`highlight()`, `porter`/`unicode61`/`trigram` tokenizers, contentless-delete tables (3.43+) and external-content tables that you must keep in sync with triggers [S69]. The scaffold's Google-style filter maps to FTS5 `MATCH` for phrases, `NOT`, `OR`; `site:`, `ticker:`, `after:` become column filters.
- **WAL with several processes.** Supported on one host; one writer at a time; readers do not block writers; not on network filesystems; long-lived readers starve checkpoints so WAL grows unless readers pause; a WAL-reset corruption bug affected 3.7.0–3.51.2, fixed in 3.51.3 (backports 3.44.6, 3.50.7), so modernc's 3.54.1 is past it [S68][S64]. cvewatch's DSN pragmas (`journal_mode(WAL)`, `busy_timeout(5000)`, `synchronous(NORMAL)`) are a good start [S2]; add a single write connection per process.
- **Tenancy.** Option A: `tenant_id` column plus a bound `TenantStore` (the scaffold). Option B: one file per tenant, base attached read-only. B isolates by construction, makes delete/export/backup a file operation and keeps tenant write load off the base file; cost is cross-database joins through `ATTACH` (I). Recommend B; the `store.Store` interface stays.
- **Migrations.** `goose` v3.29.0 (2026-10-11) or `golang-migrate` v4.20.1 (2026-09-09) [S63]; for one binary with embedded SQL, `PRAGMA user_version` plus ordered embedded files is enough (I).

### 3.7 TUI

| Item | Current state | Evidence |
|---|---|---|
| Bubble Tea | **v2.1.1** (2026-10-28); import `charm.land/bubbletea/v2`; `go 1.26`; declarative `View` struct (alt-screen, mouse, focus), "Cursed" renderer, kitty keyboard enhancements, built-in colour downsampling via colorprofile, Lip Gloss no longer does I/O [S63][S70] |
| Lip Gloss | **v2.0.6** (2026-08-11) `charm.land/lipgloss/v2` [S63] |
| Bubbles | **v2.2.1** (2026-08-24) `charm.land/bubbles/v2`; components: list, table, viewport, textinput, textarea, help, key, progress, spinner, paginator, filepicker, tree, timer, stopwatch, cursor [S63]. A `github.com/charmbracelet/bubbles` v1.0.0 (2026-02-09) also exists |
| cvewatch pins | bubbletea v1.3.10, lipgloss v1.1.0 (both v1 lines) [S2] |
| tview / tcell | tview v0.42.0 (2025-08-27, last push 2026-08-11), tcell v2.13.10 (2026-05-06) [S63][S78]; widget toolkit, no declarative redraw |
| ratatui | v0.30.2 (2026-06-19) is Rust; I did not find an official Go binding (U) |
| Terminal charts | `NimbleMarkets/ntcharts/v2` v2.9.0 (2026-10-29), MIT, for Bubble Tea v2: sparkline, braille line/time-series, bar, OHLC; picture widgets with Kitty graphics fallback [S72][S63] |
| Markdown | glamour v1.0.0 (2025-11-24) [S63] |
| Capability detection | `colorprofile.Detect(os.Stdout, os.Environ())` yields TrueColor/ANSI256/ANSI/NoTTY-style profiles and degrades colours; built into Bubble Tea v2 [S71][S70] |

**Recommendation.** Bubble Tea v2 + Lip Gloss v2 + Bubbles v2 + ntcharts v2. Tag cloud: words laid out in wrapped lines, weight by **log or rank tiers**, conveyed by case/bold/underline/`▁▂▃▅▇` prefix *and* colour so it survives `NO_COLOR` and monochrome terminals. Timeline: per-day braille/block bars (relevant vs total) plus an event list; ntcharts sparkline for stock/topic pages. Accessibility (I): a non-interactive `--plain` and `--json` mode for every screen (screen readers handle alt-screen TUIs poorly), keyboard-only operation, no colour-only meaning, honour `NO_COLOR`/`TERM=dumb`, avoid emoji width assumptions. NO_COLOR specifics were not fetched (U); Bubble Tea v2's own grapheme/width handling was not verified (U).
*Cheapest test:* port the scaffold's `Home` view to a 150-line Bubble Tea v2 program and run it in Terminal.app, iTerm2, tmux and over SSH; check wide-char alignment, colour fallback, and resize.

### 3.8 Architecture and operations

- **Polite polling.** Conditional GET with `If-None-Match`/`If-Modified-Since` (RFC 9111) [S19]; heuristic freshness is only a "typical 10%" example, not a rule [S19]. robots.txt (RFC 9309): 4xx = unavailable (may crawl), 5xx = unreachable (must treat as disallow-all, may relax after long persistence), cache ≤24 h, parse limit ≥500 KiB, not an access control [S18]. Honour `Retry-After`; cap per host; EDGAR total ≤10 req/s [S8]. cvewatch already encodes these [S2]. **EDGAR current-filings has no validators** [S14], so rely on accession-number dedupe.
- **Scheduling.** Own min-heap scheduler with per-source due time, jitter, exponential backoff and a circuit breaker (the scaffold's `scheduler` doc already says so); `golang.org/x/time` v0.16.0 for rate limiting [S63]. `gocron` v2.23.0 and River v0.51.0 exist [S63] but add a dependency (River needs Postgres, I).
- **Events to the TUI.** In-process channel when the scheduler runs inside the TUI process (lock file as cvewatch does); otherwise TUI polls a `changes(seq)` row every 1–2 s (cheap under WAL) and cvewatch-style re-reads every 30 s as fallback [S2]. SQLite's `PRAGMA data_version` could replace the poll (not verified here, U).
- **Python sidecar vs in-process.**

| Route | Evidence | Pros | Cons |
|---|---|---|---|
| `laya-serve` over HTTP | FastAPI; env config; 503 beyond `LAYA_MAX_CONCURRENT` (16); inference serialised by one lock; binds 0.0.0.0 by default [S3] | Zero Go code | torch 2.14 + transformers 5.19 in the venv; ≈2.3 GB checkpoints (README, 3 checkpoints); hub download at first use; 13 s MPS warm-up [S3] |
| laya-go + nested ONNX module | pure-Go parts (lang, schema, tokenizer, `agent`, `router`); `onnx` module is cgo and needs ONNX Runtime ≥1.29 shared library; SHA-256 digest pins for checkpoints [S76] | In-process, pinned | Younger code path; cgo breaks CGO-free releases |
| hugot v0.8.5 (Apache-2.0) | pure-Go GoMLX backend for small models (≈32/batch; `GOEXPERIMENT=simd` helps), ORT backend (fastest, build tag), XLA backend [S75] | Embeddings and classifiers in Go | Model must be exported to ONNX |
| onnxruntime_go v1.38.0 | [S63] | Direct control | cgo + shared lib |
| llama.cpp via yzma v1.31.0 | Go bindings for local inference on Linux/macOS/Windows/WASM; GitHub reports licence NOASSERTION (check) [S75] | GGUF LLM judge in-process | Licence to verify |
| OpenAI-compatible local server (llama.cpp/Ollama/MLX) over localhost | (I) | Swappable, keeps Go CGO-free | A process to manage |

  **Recommendation (I):** define a `Scorer` interface with batch calls and a version string; ship a pure-Go L0/BM25 implementation first; add an ONNX embedding scorer (hugot/ORT behind a build tag, or a localhost embedding server); put LLM judging behind localhost HTTP. Keep laya as an optional adapter that speaks the `/v1/systemone/batch` protocol with `LAYA_HOST=127.0.0.1`, `LAYA_API_KEY` set, `HF_HUB_OFFLINE` after the first download.
- **Packaging and releases.** GoReleaser v2.19.0 (2026-10-22); copy cvewatch's `.goreleaser.yaml` (two builds, trimpath, archives, checksums) and release workflow (CHANGELOG-driven notes, provenance + SBOM) but update the actions to current majors [S2][S81]. `govulncheck` in CI. modernc ships its own SBOM and signed provenance, which helps supply-chain review [S64].
- **Observability.** `log/slog` (stdlib) + the GREEN/YELLOW/RED health model [S2]; OpenTelemetry Go v1.47.0 and Prometheus client_golang v1.25.0 are available [S63] but optional for a desktop tool. MCP 2026-07-28 documents `traceparent` propagation in `_meta` [S73].
- **MCP.** Go SDK **v1.8.0** (2026-09-14) supports spec **2026-07-28** (latest) plus 2025-11-25, 2025-06-18, 2025-03-26, 2024-11-05; v1.8.0 adds input-size and nesting caps, `ServerOptions.SupportedProtocolVersions`, `SetCacheable`; two `MCPGODEBUG` escape flags go away in v1.9.0 [S74]. Spec 2026-07-28 removes protocol sessions and the initialize handshake (stateless), adds `server/discover`, `subscriptions/listen`, required `ttlMs`/`cacheScope` on list/read results, deprecates Roots, Sampling, Logging, HTTP+SSE, and Dynamic Client Registration [S73]. Plan: stdio first, read-only tools (`search`, `get_stock`, `get_topic`, `list_watches`, `domain_stats`), `label` and `edit_watch` behind `--allow-write`, instruction text that states feed fields are untrusted (cvewatch does exactly this) [S2].

### 3.9 Evaluation and quality

Best practice (I; standard methods, not individually re-verified):

- **Golden set.** Frozen, stratified by source tier, event kind and score decile; ≥600 items to start; refresh 10% monthly; never used for training.
- **Agreement.** Two annotators on ≥25% of a batch; report Cohen's κ (two raters) or Krippendorff's α (more); resolve disagreements to refine the rubric, not to maximise the number.
- **Metrics.** Relevance: PR-AUC, precision@k per day (k = what the user reads), recall on a high-materiality slice; ranking: nDCG; calibration: reliability bins, ECE, Brier (the scaffold has precision@k, Brier, ECE); conformal: empirical coverage vs target; ops: per-source precision (the `domains` aggregate, but computed from labels), latency.
- **Shadow deployment.** The scaffold already stores `LensVersion` with each score; score every new item with candidate and champion, show only champion, compare on labels and on the disagreement set; promote when the candidate wins on the golden set *and* the disagreement audit.
- **Drift.** PSI on score deciles, embedding-centroid shift, per-source relevant-share, weekly random label audit; alert in the health screen.
- **Bias.** The k/d keys label only what the user sees; add the 10% random exploration slice to estimate recall.
- **Existing systems.** RavenPack publishes Relevance and Novelty scores (academic papers quote them) [S79]; I did not find an open-source system that continuously evaluates news relevance against human labels in a TUI loop (search limited, U). That loop is a real differentiator if it works.

### 3.10 Prior art

| Project | What it is (V from repo metadata) | Better than the scaffold at | Weaker |
|---|---|---|---|
| alphai-tui (MIT, Rust/ratatui, pushed 2026-10-31, 89 stars) | Terminal dashboard: quotes, charts, AI-scored news (1–10 relevance), Form 4 [S78] | Working news view, charts | Needs a vendor key; no tenants/watches/eval |
| gloomberb (2,625 stars, licence NOASSERTION) | Terminal finance workspace: quotes, filings, news, portfolios, alerts [S78] | Breadth, working UI | No lens, no eval; licence unclear |
| OpenBB (75,055 stars, licence NOASSERTION) | Data platform for analysts/agents [S78] | Connectors and ecosystem | Not a news-relevance product |
| FinGPT (MIT, 21,520 stars) | Open financial LLMs [S78] | Models | Research code |
| RavenPack | Commercial analytics with relevance/novelty/event taxonomy [S79] | Scale, history, taxonomy | Closed, costly |
| AlphaSense, Bloomberg, Feedly, Koyfin | Not fetched | - | U |

**What is genuinely new here (I):** (a) a *shared base with per-tenant watches and labels* in a local-first single binary; (b) investor-philosophy lens tied to filing-item structure; (c) per-domain *yield* (relevant/total per source, from your own labels) as a first-class screen; (d) built-in calibration/shadow evaluation. I could not confirm that none of these exists elsewhere (U).

### 3.11 Risks

**Legal (depends on jurisdiction; I assumed none).**
- *Copyright/data:* see 3.1. Free-tier terms bar redistribution and business use [S28][S29][S30]; EU press-publisher right and an unresolved CJEU question on RAG-style use [S35]; US headline/snippet law not retrieved (U).
- *Investment-related information:* US: *Lowe v. SEC* (1985) held that impersonal, bona fide publications fall outside "investment adviser" in the Advisers Act; personalised advice does not [S37]. A tool that tells a specific user what to buy/sell, or recommends within a watch, moves toward advice. EU: MAR Article 20 requires objective presentation and disclosure of interests for persons who produce or disseminate investment recommendations, with a journalism carve-out; ESMA assesses substance over label, and Delegated Regulation 2016/958 separates facts from opinions [S36]. UK, Hungary, other national regimes: not researched (U). **What depends on jurisdiction:** whether delivering to other people (tenants) is "dissemination", whether a score or rubric is an "investment recommendation", data-protection duties for tenants' watchlists (personal data), and email-marketing rules for digests.
- *Safe default:* personal use, no recommendations, label outputs "relevance to a long-term reader, not advice", log provenance, no redistribution of licensed items.

**Security.**
- Terminal injection: tested the scaffold sanitizer [S80]; add stripping/escaping for bidi controls (U+202A–202E, U+2066–2069), zero-width characters (U+200B–200F, U+2060, U+FEFF) and tag characters (U+E0000–E007F), and render titles via a function that never emits raw bytes. Apply the same cleaning before text reaches MCP clients or an LLM stage, where hidden tag characters are an instruction channel.
- SSRF: adopt cvewatch's default-deny for private/loopback/link-local, no plain http, redirect refusal/limit, and DNS rebinding check on the dialled address [S2].
- Resource limits: 16 MiB body cap, XML/JSON depth caps (MCP SDK now caps JSON at 1,000 levels) [S74], archive size caps for bulk files, per-source item caps, per-request deadlines.
- Sidecar: `laya-serve` binds all interfaces by default; set loopback + API key; pin model revisions and digests (laya-go supports SHA-256 pins [S76]); run offline after download.
- MCP: write tools off by default; fence feed text as untrusted data.

**Cost.** Free primary sources; paid APIs run $29–$250/month at entry level and carry personal-use limits [S25]-[S30]. Local compute: embeddings and small LLMs fit a laptop (Gemma 4 E4B 3.65 GB file, ~100 tok/s on M4 Max GPU) [S48]. Storage: ~100k items/month is a few hundred MB with vectors at 256 dims (I). Time cost is labelling: ~1,000 labels ≈ 8–10 hours (I).

---

## 4. Phased build plan

| Phase | Build | Cheapest experiment that settles an open decision | Acceptance tests |
|---|---|---|---|
| 0 (2–3 days) | Declared-UA HTTP gate (copy cvewatch `httpx`), enable the two scaffold sources, store raw items in modernc SQLite WAL | **Source reality test:** run 24 h; compare ingested accessions with `daily-index`; measure duplicates and 304 ratio; decide poll intervals | 0 missed 10-K accessions vs daily-index; no 429/403 from SEC; SEC rate ≤10 req/s asserted in a test |
| 1 (1–2 wks) | EDGAR adapters (paged current feed, submissions, 8-K item parser), Fed/ECB RSS, sanitize v2, canonical key (accession/URL), exact + SimHash dedupe, entity resolver v1 (CIK/ticker/cashtag), L0 structure lens, FTS5 + filter parser, CLI `fetch/digest/search` | **Entity gold set** (500 mentions) and **sanitizer fuzz** | Entity precision ≥0.98; sanitizer test corpus incl. bidi/zero-width/tag characters returns only printable text; filter parser round-trips 50 Google-style queries |
| 2 (1–2 wks) | Bubble Tea v2 TUI: home, stocks, stock page, topics, domains, search, sources/health, watches switcher (tab/1–9/w), breadcrumbs; auto-refresh via change counter | **TUI portability test** (Terminal.app, iTerm2, tmux, SSH, NO_COLOR) | Golden-frame tests at 80×24 and 200×60; watch switch keeps route; refresh never blocks input >50 ms |
| 3 (2–3 wks) | Labelling loop (k/d + exploration slice), eval harness, **lens bake-off**, calibration, conformal band, shadow versions | **The bake-off in 3.2** | Chosen model beats BM25+LR on PR-AUC on held-out labels or is dropped; ECE ≤0.05 after temperature scaling; coverage within ±3 points of target; p95 scoring latency documented on the user's Mac |
| 4 (1–2 wks) | Tenants as files + base attach, watches, alerts, quiet hours, cooldown, dedupe key; channels: file, feed, email, webhook (Slack/Discord/Teams Workflows) | **Tenant isolation test:** attempt cross-tenant reads | Zero cross-tenant rows in a property test; alert fires once per cluster per watch; Teams payload accepted by a Workflows webhook |
| 5 (2 wks) | Trends/bursts, discovery candidates, c-TF-IDF topics, extractive summaries (+ optional abstractive with HHEM check), stock pages with XBRL fundamentals, Lynch category tag | **Burst detector backtest** on two weeks of clusters | Known multi-source events flagged within 1 poll; syndication-only bursts suppressed; every summary sentence links to a source id |
| 6 (1 wk) | MCP read-only server, broker position import, GoReleaser, CI matrix + govulncheck, docs | **MCP client smoke test** against spec 2026-07-28 | `server/discover` works; write tools absent without flag; `govulncheck` clean; release builds on macOS arm64 and Linux |

Open-decision shortlist: lens model (bake-off), tenant layout (A vs B), vector index (brute-force vs sqlite-vec), sidecar vs in-process (latency/memory on the user's Mac), news-API plugin (only if terms allow).

---

## 5. Questions only the user can answer

1. **Jurisdiction(s)** of the user and of any other tenant: decides copyright reading, MAR/MiFID-style rules, data protection, email rules. I assumed none.
2. **Who are the tenants?** Only you; family/friends; the public. This is the line between personal tool and distribution service (3.11).
3. **Budget** for data: $0, up to ~$30/month, up to ~$250/month? Decides whether any API is worth enabling.
4. **Brokers actually used** (IBKR, Trading 212, Saxo, other) and whether read-only position/watchlist import is wanted.
5. **Data licences already held** (Bloomberg, Refinitiv/LSEG, FactSet, newspaper subscriptions, newswire feeds): they change what is lawful to ingest.
6. **Markets and languages:** US only, or EU/UK/Japan/other? Multilingual raises the laya-multilingual / Qwen3-Embedding case.
7. **Hardware and privacy:** Apple-silicon model and RAM; is any text allowed to leave the machine (cloud LLM judge)?
8. **Labelling capacity:** minutes per day you can give; this caps what active learning can achieve.
9. **Always-on?** A daemon on a server vs refresh only while the TUI is open (changes poll interval and WAL design).
10. **Investment universe constraints** (sectors out of scope, circle of competence, min market cap), to seed the lens and the Lynch category rules.

---

## 6. Unverified claims and gaps

- Lynch book content and thresholds (book not read; categories from secondary summaries only) [S40].
- Simhash operating point (64-bit, Hamming ≤3) and Kleinberg paper details (certificate error) [S57][S58].
- Finnhub free-tier limits and endpoints (docs pages are JavaScript-only); Marketaux terms; whether massive.com is the former Polygon.io; Alpaca news limits today (2021 post).
- IBKR documentation pages returned 403 to my fetcher; IBKR facts are from search-result excerpts; news and watchlist endpoint limits for individual accounts not confirmed.
- EU DSM Article 4(3) text and MAR Article 20 exceptions (EUR-Lex blocked direct fetch); UK, Hungary, other national rules; US headline copyright case law.
- GLEIF and OpenFIGI licence text, Nasdaq Trader terms, SEC Form 4 structured data set, SEC EFTS rate limits (undocumented).
- ECB press-release copyright; Fed feed terms; HKEX/ASX/SEDAR+/BoE/BoJ/India feeds.
- Whether laya checkpoints' training data are real or synthetic beyond the typed-decisions workflows; community validation of a repo created 2026-09-18 with ~35k stars; performance on Apple-silicon M-series for the 421M models (the published MPS numbers are from an Intel Mac Pro with AMD GPUs).
- Qwen3.5/Gemma 4 quality as relevance judges on financial text (only throughput and vendor benchmarks read).
- Latency estimates marked (I) for embeddings and judges on the user's Mac.
- NO_COLOR/accessibility specifics, Bubble Tea v2 wide-character handling, `PRAGMA data_version` semantics.
- Commercial prior art (AlphaSense, Bloomberg, Feedly, Koyfin); novelty of the combined design.
- EDGAR current-filings window size (whether `count=100` is capped by a rolling window) was not isolated.
- Materiality thresholds, agreement targets, conformal coverage targets and decision-rule margins in this file are proposals, not sourced.

---

## 7. Source list

All read on 2026-11-03 unless noted. "raw" = I read the bytes myself; "summary" = WebFetch small-model summary; "search" = excerpt returned by WebSearch.

- S1 Scaffold files under `/Users/botond/Projects/Sandbox/scripts/news/stoxicity` (README.md, PROTOCOL.md, Makefile, `.github/workflows/ci.yml`, `app/go.mod`, `app/config/**`, `app/src/**`) (raw).
- S2 cvewatch v2.0.0: https://github.com/b070nd/cvewatch (release body, `app/go.mod`, `httpx.go`, `daemon.go`, `store/*.go`, `mcpserver.go`, CI, release, `.goreleaser.yaml`, tree) via `gh api` (raw).
- S3 Installed `laya` 0.4.0: `/Users/botond/Projects/work/laya/.venv/lib/python3.14/site-packages/laya/` and `laya-0.4.0.dist-info/` (METADATA, `serve.py`, `router.py`) (raw).
- S4 https://github.com/NandhaKishorM/laya (repo metadata, releases v0.4.20–0.4.22, tree) via `gh api` (raw).
- S5 https://github.com/NandhaKishorM/laya/blob/main/BENCHMARKS.md (raw).
- S6 https://huggingface.co/convaiinnovations/laya-typed-decisions (summary).
- S7 https://huggingface.co/convaiinnovations/laya (summary).
- S8 https://www.sec.gov/search-filings/edgar-search-assistance/accessing-edgar-data (summary).
- S9 https://www.sec.gov/search-filings/edgar-application-programming-interfaces (summary).
- S10 https://www.sec.gov/about/privacy-information (summary).
- S11 https://www.sec.gov/edgar/search/efts-faq.html (summary).
- S12 https://www.sec.gov/data-research/sec-markets-data/form-13f-data-sets (summary).
- S13 https://www.sec.gov/files/form8-k.pdf (raw via `pdftotext`).
- S14 Live probes of https://www.sec.gov/cgi-bin/browse-edgar?action=getcurrent&type=10-K&output=atom (also `type=8-K`, `type=4`, `count=100`), https://data.sec.gov/submissions/CIK0000320193.json, and a `robots.txt` request answered by the rate-threshold page (raw, curl).
- S15 https://www.federalreserve.gov/feeds/press_all.xml (raw, curl headers and body).
- S16 https://www.sec.gov/newsroom/press-releases/2026-42-sec-proposes-amendments-permit-optional-semiannual-reporting-public-companies and https://www.sec.gov/rules-regulations/2026/05/s7-2026-15 (search).
- S17 https://www.w3.org/TR/websub/ (summary).
- S18 https://www.rfc-editor.org/rfc/rfc9309.html (summary).
- S19 https://www.rfc-editor.org/rfc/rfc9111.html (summary, first 100k chars).
- S20 robots.txt of reuters.com, wsj.com, marketwatch.com, ft.com, bloomberg.com, cnbc.com, finance.yahoo.com, seekingalpha.com, ecb.europa.eu (raw, curl).
- S21 ESAP: https://www.esma.europa.eu/esmas-activities/data/european-single-access-point-esap and related results (search).
- S22 https://www.fca.org.uk/markets/primary-markets/regulatory-disclosures/national-storage-mechanism and https://www.fca.org.uk/publication/primary-market/nsm-investor-user-guide.pdf (search).
- S23 https://www.ecb.europa.eu/stats/ecb_statistics/governance_and_quality_framework/html/usage_policy.en.html and Bundesbank reuse page (search).
- S24 https://disclosure2dl.edinet-fsa.go.jp/guide/static/disclosure/download/ESE140206.pdf and terms page (search).
- S25 https://www.alphavantage.co/premium/ (summary).
- S26 https://www.marketaux.com/pricing (summary).
- S27 https://www.gdeltproject.org/about.html (summary).
- S28 https://finnhub.io/terms-of-service (summary).
- S29 https://www.tiingo.com/about/pricing (summary).
- S30 https://massive.com/pricing (summary).
- S31 https://docs.alpaca.markets/us/docs/historical-news-data and https://alpaca.markets/blog/introducing-news-api-for-real-time-fiancial-news/ (search).
- S32 https://www.interactivebrokers.com/docs/web-api/trading/usage-and-availability/pacing-limitations and IBKR Web API pages (search; direct fetch returned 403).
- S33 https://docs.trading212.com/api (positions, rate limiting pages) (search).
- S34 https://www.developer.saxo/openapi/learn/rate-limiting (search).
- S35 Directive (EU) 2019/790: https://eur-lex.europa.eu/eli/dir/2019/790/oj/eng (search excerpt; direct fetch blocked); CJEU C-250/25 (search).
- S36 Regulation (EU) 596/2014 Art. 20 https://eur-lex.europa.eu/eli/reg/2014/596/oj/eng; Delegated Regulation 2016/958; ESMA Q&A and social-media warning (search).
- S37 https://www.law.cornell.edu/supremecourt/text/472/181 (*Lowe v. SEC*, 1985) (summary, first 100k chars).
- S38 https://www.berkshirehathaway.com/ownman.pdf (raw via `pdftotext`).
- S39 https://www.berkshirehathaway.com/letters/1986.html (summary).
- S40 Lynch, *One Up on Wall Street*: https://en.wikipedia.org/wiki/One_Up_on_Wall_Street and secondary summaries (search; book not read).
- S41 https://arxiv.org/abs/2412.13663 (summary).
- S42 https://huggingface.co/ProsusAI/finbert (summary).
- S43 https://huggingface.co/beethogedeon/Modern-FinBERT-large (summary).
- S44 https://arxiv.org/abs/2609.20198 (summary of abstract).
- S45 https://huggingface.co/MoritzLaurer/deberta-v3-large-zeroshot-v2.0 (summary).
- S46 https://huggingface.co/google/embeddinggemma-300m (summary).
- S47 https://huggingface.co/Qwen/Qwen3-Embedding-0.6B (summary).
- S48 https://developers.google.com/edge/litert-lm/models/gemma-4 (summary).
- S49 https://huggingface.co/Qwen/Qwen3.5-4B (summary).
- S50 https://arxiv.org/abs/2502.10990 (FinMTEB) (summary).
- S51 https://arxiv.org/abs/1908.10063 (FinBERT paper; abstract has no figures) (summary).
- S52 https://arxiv.org/abs/2209.11055 (SetFit) (summary).
- S53 https://arxiv.org/abs/2402.12659 (FinBen) (summary).
- S54 https://arxiv.org/abs/1706.04599 (Guo et al.) (summary).
- S55 https://arxiv.org/abs/2107.07511 (Angelopoulos & Bates) (summary).
- S56 https://arxiv.org/abs/2203.05794 (BERTopic) (summary).
- S57 https://research.google/pubs/detecting-near-duplicates-for-web-crawling/ (bibliographic data only) (summary).
- S58 https://www.cs.cornell.edu/home/kleinber/bhs.pdf (not read: certificate rejected).
- S59 https://huggingface.co/vectara/hallucination_evaluation_model (summary).
- S60 https://www.openfigi.com/api/documentation (summary).
- S61 https://www.gleif.org/en/lei-data/gleif-api (summary; terms not shown).
- S62 https://www.nasdaqtrader.com/trader.aspx?id=symboldirdefs (summary).
- S63 Go module proxy queries (`proxy.golang.org/<module>/@latest`, `.mod` files) for all versions named in this file, plus https://go.dev/dl/?mode=json (raw).
- S64 modernc.org/sqlite: https://gitlab.com/cznic/sqlite (README, CHANGELOG, doc.go) and https://gitlab.com/cznic/libsqlite3/-/raw/master/generator.go (build flags) (raw); https://sqlite.org (current 3.54.1).
- S65 https://github.com/asg017/sqlite-vec (repo metadata, releases, README) via `gh api` (raw).
- S66 https://github.com/ncruces/go-sqlite3 (version via proxy only; README not read).
- S67 https://github.com/duckdb/duckdb-go (release v2.20000.0, README) and https://github.com/duckdb/duckdb (v2.0.0) via `gh api` (raw).
- S68 https://www.sqlite.org/wal.html (summary).
- S69 https://www.sqlite.org/fts5.html (summary, first 100k chars).
- S70 https://github.com/charmbracelet/bubbletea (release v2.0.0 notes, UPGRADE_GUIDE_V2.md, releases list) via `gh api` (raw).
- S71 https://github.com/charmbracelet/colorprofile README (raw).
- S72 https://github.com/NimbleMarkets/ntcharts README (raw).
- S73 https://modelcontextprotocol.io/specification/2026-07-28/changelog (summary).
- S74 https://github.com/modelcontextprotocol/go-sdk README and release v1.8.0 notes via `gh api` (raw).
- S75 https://github.com/knights-analytics/hugot README, https://github.com/hybridgroup/yzma metadata via `gh api` (raw).
- S76 https://github.com/NandhaKishorM/laya/tree/main/laya-go README via `gh api` (raw).
- S77 https://devblogs.microsoft.com/microsoft365dev/retirement-of-office-365-connectors-within-microsoft-teams/ and Teams webhooks docs (search).
- S78 GitHub metadata via `gh api`: makeev/alphai-tui, vincelwt/gloomberb, OpenBB, AI4Finance-Foundation/FinGPT, answerdotai/ModernBERT, rivo/tview, NimbleMarkets/ntcharts (raw).
- S79 RavenPack relevance/novelty scores: academic papers and WRDS/vendor pages surfaced by search (vendor documentation not retrieved) (search).
- S80 Local probe of the scaffold's `sanitize.Text` (copied regex and function into a scratch program in the session scratchpad; 11 hostile strings; output reported in 1.3 and audit row 35) (raw).
- S81 Latest releases via `gh api repos/<repo>/releases/latest`: actions/checkout, actions/setup-go, golangci/golangci-lint, goreleaser/goreleaser-action, ratatui/ratatui (raw).
- S82 https://go.dev/dl/?mode=json (go1.27.3, go1.26.10) and local `go version` 1.27.1 (raw).
- S83 yaml libraries: `gh api repos/go-yaml/yaml` (archived 2025-04-01), proxy for `go.yaml.in/yaml/v3` v3.0.6 and `github.com/goccy/go-yaml` v1.19.2 (raw).
