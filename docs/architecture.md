# Architecture

How the code is organised. The [README](../README.md) is the place to start; this page is the map of
`app/src` for people who want to read or change the code.

Scoring runs in two stages: keyword rules first, then optionally a
[laya](https://huggingface.co/convaiinnovations/laya) sidecar. Laya stays off until it has been
calibrated on your own labels.

## Package map

```
app/
  config/                   config.yaml, sources.yaml
  src/
    cmd/stoxicity/          CLI: fetch, run, digest, search, watch, tenant, sources, policy, instruments, fx, prices, dividends, portfolio, income, scorecard, strategy, allocate, plan, review, order, paper, tier, broker, event, performance, heldout, walkfwd, report, alerts, journal, daily, ops, doctor
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
    ops/                    health and metrics without opening a database, leases, disk limits, coherent backups with manifests, restore with a reconcile hold, service files, drills
    alerts/                 alerts raised once, a durable outbox per channel (sent only on proof), quiet hours, urgent alerts, reminders, restarts
    corrections/            what a plan depends on (events, articles, thesis rules, recipe), and what counts as a material change
    journal/                the decision journal: reasons beside the thesis as it stood, later outcomes linked to the entry
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
