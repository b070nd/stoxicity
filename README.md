# stoxicity

A news aggregator for long-term stock investors, covering news from 2026-11-01.
It reads only sources worth a Peter Lynch / Warren Buffett style investor's time
and answers:

- which stocks to look at (discovery)
- which themes are gaining coverage (trends)
- which domains produce how much relevant news each day (domains)

News is filtered with a Google-style query (`"free cash flow" -rumor ticker:AAPL site:sec.gov`).

The layout follows [cvewatch](https://github.com/b070nd/cvewatch): a CLI, a
separate full-screen TUI binary, and YAML configuration.

**Status: early.** The CLI works end to end (see Usage); the full-screen
interface is still a stub. Working and tested so far: `nav` (routes,
breadcrumbs, back/forward, watch switching), `watch` (validation, matching,
alert evaluation), `channel` (quiet hours, cooldown, dedupe), the event
taxonomy and source licences in `model`, `sources` (licence checks and
visibility), `httpx` (User-Agent), `tagcloud`, `timeline`, `eval` (precision@k,
Brier, ECE), `sanitize` (strips escape sequences and invisible Unicode from
feed text) and the TUI key map.

The design decisions, with sources, are in
[docs/research/bleeding-edge-design.md](docs/research/bleeding-edge-design.md).
Read its "Unverified" section before relying on any of it.

## Before you fetch anything

1. Put your email address in `.env.local` as `STOXICITY_CONTACT` (copy
   `.env.local.example` on a fresh checkout). It goes into the User-Agent; the
   SEC blocks clients that do not identify themselves.
2. Settings (time zone `Europe/Berlin`, mail server, paths) are in `.env`, which
   is safe to commit. Credentials go only in `.env.local`, which is git-ignored.
   See [docs/configuration.md](docs/configuration.md).
3. Every source in `sources.yaml` states its licence; only `redistributable`
   sources are shown to every tenant.

How fetchers and the SEC EDGAR reader are meant to work:
[docs/fetchers.md](docs/fetchers.md). The full-screen interface and its keys:
[docs/tui.md](docs/tui.md). The stock universe (every ticker, profiles, lists,
tags and the company filters): [docs/universe.md](docs/universe.md). The production plan's foundations: [docs/readiness.md](docs/readiness.md) (doctor, exact money) and [docs/policy.md](docs/policy.md) (the investment policy) and [docs/instruments.md](docs/instruments.md) (securities, listings, and what each data source could supply) [docs/fx-prices.md](docs/fx-prices.md) (exchange rates and prices, and when they are refused) and [docs/dividends.md](docs/dividends.md) (dividend and distribution observations, calendar and history quality) and [docs/portfolio.md](docs/portfolio.md) (accounts and the immutable ledger: real and paper books, lots, unsettled cash, reversals, statement reconciliation) and [docs/income.md](docs/income.md) (dividend entitlement, accruals, withholding and the check that income is booked once) and [docs/scorecards.md](docs/scorecards.md) (sourced multi-year research scorecards, sector templates and why a ratio is blank) and [docs/strategy.md](docs/strategy.md) (valuation scenarios, the quality-income core strategy and the fund template) and [docs/allocation.md](docs/allocation.md) (turning proposals into quantities under your limits and a sourced fee schedule, or saying to wait) and [docs/plans.md](docs/plans.md) (immutable plan revisions with their exact inputs, a lifecycle, a revision diff and staleness) and [docs/review.md](docs/review.md) (hold, reduce or sell what you own by ordered rules, bounded by free shares, with an overdue review never shown as a fresh HOLD) and [docs/orders.md](docs/orders.md) (the local record of orders: exact terms, an atomic hold on the cash or shares, a confirmation bound to those terms, and a state machine that accepts facts in any order; nothing reaches a broker) and [docs/paper.md](docs/paper.md) (the local paper engine: conservative, seeded and reproducible fills from finished daily bars, with no same-bar or touch fills) and [docs/tiers.md](docs/tiers.md) (what each tier lets the program do, apart from your risk limits; sourced evidence and your recorded acceptance to raise it; health signals that suspend new risk without touching what you hold) and [docs/broker.md](docs/broker.md) (the broker connection, read-only by default: session, accounts, balances, positions, orders and executions compared with the ledger; differences recorded and never overwritten; order preview, confirmation bound to the payload, submission that never resends, cancel and fills; what is verified and what is not) and [docs/event-sleeve.md](docs/event-sleeve.md) (the separate, experimental event sleeve: an earnings follow-through template that only proposes paper candidates, shows every test, falls to research when a fact is unknown, and exits by a distinct time-stop or invalidation review) and [docs/performance.md](docs/performance.md) (what a book earned apart from deposits: gain by component that adds up, time- and money-weighted return with every IRR root, drawdown, and baselines replaying the same cash flows; nothing is filled in for a missing price) and [docs/heldout.md](docs/heldout.md) (news reading measured against independent labels on a licensed, source-linked corpus: disagreements recorded and settled by a third person, thresholds and labels frozen first, synthetic regressions kept apart, and no pass without external labels) and [docs/walkfwd.md](docs/walkfwd.md) (replay and walk-forward evaluation: frozen splits, a registry of every trial tried, conservative fills, clustered uncertainty and preregistered criteria; evaluated, experimental or insufficient_history) and [docs/report.md](docs/report.md) (exporting a book or a plan as Markdown or versioned JSON from stored records only: no secrets, no article text, nothing fetched or sent) and [docs/tui-workflow.md](docs/tui-workflow.md) (the Plan, Portfolio and Income sections of the terminal interface: plans and their full cards, books, income, orders and the policy; a confirmation you type, that sends nothing; what was checked in a terminal and what only by test).

macOS and Linux only; Windows is out of scope.

## Screens

Every screen has a route, and the breadcrumb bar shows the trail:
`Home › Topics › AI infrastructure › NVDA`.

| route | screen |
|---|---|
| `/` | today's digest, tag cloud, 14-day timeline |
| `/stocks`, `/stock/NVDA` | stocks in relevant news; one stock with summary, fundamentals, timeline |
| `/topics`, `/topic/ai-infra` | topics as a cloud and trend table; one topic aggregation |
| `/domains`, `/domain/sec.gov` | relevant against total per domain; one domain |
| `/day/2026-11-03` | one day |
| `/search?q=...` | Google-style query results with facet counts |
| `/sources` | source health (shared, belongs to no watch) |
| `/watches` | the tenant's watches: unread, channels; create, edit, mute |
| `/w/<watch>/...` | any screen above, pinned to one watch |

## Tenants, watches and channels

```
            shared information base                 per tenant
  sources → pipeline → articles, clusters,    →  watches   labels   alert state
            (once)     entities, base scores      │                  read markers
                                                  ▼
                                       tui · email · webhook · file · feed
```

- **One information base.** Ingest runs once and writes articles, clusters,
  entities and versioned base scores. No tenant or watch is involved, so adding
  a tenant adds no fetching or scoring cost.
- **Tenants** own watches, labels (keep/drop), alert hits and read markers. They
  are reachable only through a `store.TenantStore` bound to one tenant. A single
  user has the tenant `default` and never sees any of this.
- **Watches** are saved views over the base, one YAML file each in
  `app/config/tenants/<tenant>/watches/`. A watch selects subjects (tickers,
  topics, sectors), narrows them (domains, event kinds, relevance and
  materiality floors, a Google-style query), sets alerts (conditions, channels,
  quiet hours, cooldown), an optional digest, and the dashboard widgets. Watches
  never copy or re-score articles, so switching is instant.
- **Switching** is one key: `tab` / `shift+tab` cycle watches, `1`-`9` jump, `w`
  opens a finder with unread badges. The screen you are on stays; only the
  watch behind it changes (`Home › Topics › AI` stays on the AI topic page).
- **Channels** deliver the same notification everywhere a watch names: `tui`,
  `email`, `webhook`, `file` (Markdown), `feed` (Atom).

Try `stoxicity watch check` after editing a watch: it reports every problem in
one pass. Copy any `*.yaml.example` to start a new watch.

## Layout

```
app/
  config/                   config.yaml, sources.yaml
  src/
    cmd/stoxicity/          CLI: fetch, run, digest, search, watch, tenant, sources, policy, instruments, fx, prices, dividends, portfolio, income, scorecard, strategy, allocate, plan, review, order, paper, tier, doctor
    cmd/stoxicity-tui/      full-screen interface
    instrument/             issuer, security, listing identity and the resolver
    capability/             what each data source could supply, where, at what cost
    distribution/           dividend and fund-distribution observations: declared, estimated, cancelled, conflicting; calendar and history quality
    csvtable/               the strict CSV reader the importers share
    scorecard/              sourced multi-year figures, sector templates, ratios that say why they are blank, notes, versioned content hashes
    strategy/               the scored core-strategy checklist, bear/base/bull valuation and entry ceiling, the fund template; proposals only
    allocate/               quantities under policy limits and a sourced fee schedule; wait or no purchase are results; no sale
    plan/                   immutable plan revisions: card, input manifest, hashes, lifecycle, revision diff, staleness
    review/                 ordered hold, reduce and sell rules bounded by free shares; missing data blocks, a late review is overdue
    order/                  order intents as an event log: terms, holds, confirmation, state machine, reports in any order
    paper/                  conservative seeded fills from daily bars, reported through the order contracts
    tier/                   operational tiers apart from risk limits, sourced readiness, health signals that suspend new risk
    broker/                 the broker seam; fixture/ (a broker in a file), ibkr/ (Client Portal Gateway adapter, loopback only), recon/ (compare and record differences)
    eventstrat/             the experimental earnings follow-through template: tests, paper-only proposals, time-stop and invalidation review
    perf/                   gain by component, time- and money-weighted return, drawdown, baselines with matched cash flows; missing prices make a figure unavailable
    heldout/                held-out news evaluation: corpus with licences, independent labels and disagreements, frozen thresholds, stage and group metrics with intervals
    walkfwd/                replay of proposals on daily bars: frozen splits, trial registry, conservative fills, bootstrap over clusters, preregistered status
    report/                 export of a book or a plan as Markdown and versioned JSON: no secrets, no article text
    workflow/               reads plans, books, income, orders and policy from the stores for the CLI and the terminal interface; the one confirm
    income/                 which payouts a book is entitled to (shares held before the ex-date), and the check that the ledger agrees
    portfolio/              books, the immutable double-entry journal, tax lots, unsettled cash, reservations, replay and statement reconciliation
    quotes/                 exact FX and price observations, the ECB reader, and the rules that refuse stale, conflicting or unit-ambiguous values
    config/                 load the configuration folder
    model/                  shared types (Source, Article, Score, Entity)
    sources/ httpx/         source registry and licences, shared HTTP client
    feeds/ rss edgar api    fetchers, one package per source kind (docs/fetchers.md)
    envfile/                loads .env and .env.local
    scheduler/              background refresh, backoff, circuit breaker, events
    sanitize/               strip escape sequences from untrusted text
    pipeline/               ingest stages that build the shared base
    tenant/                 workspaces that share the base
    watch/                  watch spec, validation, matching, alert evaluation
    channel/                delivery kinds, quiet hours, cooldown, dedupe
    dashboard/              widgets a watch's home screen can show
    entity/                 company mentions to tickers
    company/                SEC and Nasdaq ticker lists, industry records
    universe/               every known stock: profiles, lists, tags, queries
    exchange/               the exchanges: operator, hours, holidays, open now?
    refdata/                the self-refreshing reference database of stocks
    cluster/                group syndicated copies of one story
    store/                  persistence: shared Base + tenant-bound TenantStore
    filter/                 Google-style query language
    lens/                   keyword relevance scoring
    lens/laya/              laya-serve client, second scoring stage
    labels/                 keep/drop verdicts, the gold set; the queue of what to judge next
    eval/                   precision@k, Brier, calibration error, precision and recall
    marketdata/             daily candles (Polygon/Massive)
    fundamentals/           revenue, profit and shares of every filer (SEC XBRL frames)
    insider/                insider trades from Form 4 filings: keeper, 90-day figures
    summarize/              extractive summaries with sources
    discover/ trends/       new stock candidates, rising topics
    domains/ digest/        per-domain counts, the daily summary
    tagcloud/ timeline/     cloud tiers, bars per day and event list
    nav/                    routes, breadcrumbs, back/forward
    tui/                    full-screen interface: screens, model, key map
    workspace/              opens config, stores and one tenant for the TUI
```

Scoring in two stages: keyword rules first, then optionally a
[laya](https://huggingface.co/convaiinnovations/laya) sidecar. Laya stays off
until it has been calibrated on your own labels.

## Usage

Build and check the installation:

```bash
make build                  # ./build.sh: every package, and every program under app/src/cmd into bin/
./bin/stoxicity version
./bin/stoxicity doctor      # OK/WARN/FAIL per check; exit 1 if any FAIL
./bin/stoxicity doctor -scope portfolio   # also the readiness table of that scope (research, portfolio, broker)
./bin/stoxicity doctor -json              # the same as a versioned JSON report
```

Without a build, run from `app/`: `go run ./src/cmd/stoxicity doctor`.

Back up before the first run of a new binary: the databases migrate when they are
opened and there is no downgrade.

```bash
./backup.sh backup                     # data/ -> backups/<UTC time>/ (online SQLite backup, checked)
./backup.sh verify backups/<time>      # checksums, integrity check, row counts
./backup.sh restore backups/<time>     # with the program stopped; the replaced folder is kept aside
```

```bash
stoxicity fetch [source-id...]      # poll once, print new articles per source
stoxicity run                       # poll until Ctrl-C, one line per poll; starts and stops the laya server
stoxicity digest [-day 2026-11-03]  # the day's top news, domains, candidates, trends
stoxicity search [-from D] [-to D] [-limit N] 'free cash flow -rumor ticker:AAPL'
stoxicity stock NVDA [-days 30] [-limit 10]   # one stock: summary, timeline, trend, domains, related topics
stoxicity stocks [-days 14]         # stocks in relevant news with mentions, materiality, tone
stoxicity topic ai-infra [-days 30] [-limit 10]   # one topic: its stocks, domains, timeline, top articles
stoxicity topics [-days 14]         # all topics as a tag cloud with counts and trend lift
stoxicity label keep|drop ID... [-sampled]   # record your verdict on articles
stoxicity label list [-n 20]        # your latest labels
stoxicity label sample [-n 10] [-days 7]   # random unlabelled articles to judge, then label with -sampled
stoxicity eval [-days 90] [-threshold X]   # precision, recall, Brier, ECE of the lens against your labels
stoxicity rescore [-days N | -from D -to D]   # score stored articles again after changing the lens
stoxicity relink [-days N]          # give stored SEC filings the tickers of their CIK (all of them by default)
stoxicity watch list                # the tenant's watches and unread hits
stoxicity watch check [id...]       # validate watch files and their queries
stoxicity watch test <id> -since 24h   # show matches, write nothing
stoxicity tenant list
stoxicity sources list              # kind, licence, last health from the database
```

Global flags come before the command: `-config-dir DIR`, `-env-file FILE`
(repeatable), `-tenant ID`, `-v` (debug log). Days are read in the configured
time zone (`Europe/Berlin`). Exit codes: 0 ok, 1 error, 2 usage. Output goes to
stdout, diagnostics to stderr. Put flags of `search` before the query; use `--`
if the query starts with `-`.

The stock and topic pages say that price and fundamentals are not available:
no market data provider is configured. Tag clouds are plain text: one asterisk
on each side of a term per size step above the smallest, capitals for the two
largest steps, and the article count in brackets, for example `**ai-infra**(5)`.
Label a few articles from `label sample` with `-sampled` before trusting
`eval`; labels taken from the top of the ranking alone make the lens look better
than it is, and `eval` prints the sampled labels' precision and recall apart.
`rescore` adds a score for the current lens version and keeps the older ones.

- **Settings.** `.env` and `.env.local` are found by walking up from the current
  directory; `config.yaml` and `sources.yaml` in `app/config`. See
  [docs/configuration.md](docs/configuration.md).
- **Where data goes.** `data/` next to `.env` (set `STOXICITY_DATA_DIR` or
  `data_dir` to move it): `base.db` is the shared base, `tenants/<id>.db` holds
  each tenant's hits and labels. It is git-ignored.
- **Network.** `fetch` and `run` refuse to start without `STOXICITY_CONTACT`
  (your email or an https URL); it goes into the User-Agent. SEC EDGAR needs it
  in particular and is limited to 10 requests a second. Offline commands work
  without it. Naming a source on the `fetch` command line polls it even if it is
  disabled.
- **Not verified.** The EDGAR formats the fetchers read are written from the
  documented formats and have not been checked against live data. Ticker
  resolution uses the SEC and Nasdaq lists once `stoxicity universe update` has
  loaded them; before that it knows only the `watchlist` and the tickers named
  in watches. The universe covers US listings; other markets only through
  `extra:` in `stocks.yaml`.
