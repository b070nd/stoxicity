<h1 align="center">stoxicity</h1>

<p align="center">
  <b>News and portfolio research for long-term stock investors.</b><br>
  Reads the sources worth your time, tells you what to look at, and keeps your decisions honest.
</p>

<p align="center">
  <a href="https://github.com/b070nd/stoxicity/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/b070nd/stoxicity/actions/workflows/ci.yml/badge.svg"></a>
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/license-MIT-blue.svg"></a>
  <img alt="Go 1.27" src="https://img.shields.io/badge/go-1.27-00ADD8.svg">
  <img alt="macOS and Linux" src="https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey.svg">
</p>

stoxicity is a command line program and a full-screen terminal interface that run on your own
machine. It collects filings, central-bank releases and company feeds into one local database, scores
them for a Peter Lynch or Warren Buffett style investor, and answers three questions:

- **Which stocks should I look at?** (discovery)
- **Which themes are gaining coverage?** (trends)
- **Which sources produce how much relevant news each day?** (domains)

Beyond the news it can keep a personal portfolio on exact money, turn research into a written plan,
and rehearse every trade on paper before anything near a broker. Your data stays in files on your disk.

> **Early software, read this first.** The research side works end to end on live sources. The
> portfolio, planning and broker parts are built and tested against fixtures, but **nothing has been
> checked against a real broker account and no strategy has a track record.** The program starts at
> *tier 0*: paper only, no real orders. This is a research tool, not financial advice. What is
> verified and what is not is written down in [docs/acceptance.md](docs/acceptance.md).

## Why you might like it

- **Primary sources first.** SEC EDGAR filings and press releases, the Fed, ECB, Bank of England, SNB,
  EBA, BLS and more. 24 sources are configured and 19 are on by default, each with its stated licence.
- **A query language you already know.** `"free cash flow" -rumor ticker:AAPL site:sec.gov`.
- **Local and private.** One embedded SQLite database per install, no account and no cloud service.
  Pure Go: single binaries, no C toolchain.
- **Honest numbers.** Money is exact. A ratio that cannot be computed says why instead of showing a
  guess, and a missing price is never filled in.
- **Built to refuse.** Real orders are off until you raise the tier. A confirmation you type is bound to
  the exact order terms. A restore holds new orders back until the broker is reconciled.
- **Keeps your data off GitHub.** Git hooks and a CI check refuse to commit or push databases,
  statements, prices, credentials and built binaries.

## Quick start

You need Go 1.27 and `make`, on macOS or Linux (Windows is out of scope).

```bash
git clone https://github.com/b070nd/stoxicity.git
cd stoxicity

cp .env.local.example .env.local && chmod 600 .env.local
# edit .env.local and set STOXICITY_CONTACT=you@example.com (see the note below)

make build                      # bin/stoxicity and bin/stoxicity-tui
./bin/stoxicity doctor          # OK / WARN / FAIL per check
./bin/stoxicity fetch           # poll every enabled source once
./bin/stoxicity stocks -days 7  # stocks in relevant news
./bin/stoxicity-tui             # the full-screen interface
```

`STOXICITY_CONTACT` goes into the User-Agent of every request. The SEC blocks clients that do not
identify themselves, so `fetch` and `run` refuse to start without it. A warning from `doctor` that the
optional *laya* second-stage scorer is not running is fine: the rules score alone.

The first `fetch` also loads the SEC and Nasdaq ticker lists, so tickers resolve to company names.
Keep it running with `./bin/stoxicity run` to poll in the background.

### What you get

```text
$ ./bin/stoxicity stocks -days 7
Stocks, tenant default
Window 2026-11-03 to 2026-11-09 (7 days, Europe/Berlin)
1197 articles, 780 relevant (relevance at least 0.50)

STOCK    WATCH  MENTIONS  MATERIALITY   TONE
───────  ─────  ────────  ───────────  ─────
OPTU                   4          3.0  +0.00
AHR                    3          3.0  +0.00
AIRS                   3          3.0  +0.00
AXON                   3          3.0  +0.00
```

```text
$ ./bin/stoxicity stock OPTU -days 7
Stock OPTU - Optimum Communications, Inc., tenant default
4 articles name OPTU, 4 relevant (relevance at least 0.50)

Summary
  - 10-Q/A - Optimum Communications, Inc. (sec.gov)
  - Form 10-K/A filed by Optimum Communications, Inc. (CIK 0001702780), accession 0001702780-26-000071 (sec.gov)

Timeline (relevant / total per day, with the top event)
  2026-11-05 Thu  0 / 0
  2026-11-06 Fri  4 / 4  13:01 results  sec.gov  materiality 3  10-K/A - Optimum Communications, Inc.
```

```text
$ ./bin/stoxicity tier status
tier 0: learn and build (no active policy: paper only)
permission                                            allowed
────────────────────────────────────────────────────  ───────
paper trading and research                            yes
a real portfolio managed by hand                      no
orders submitted through a broker after confirmation  no
a real event sleeve                                   no
```

(Outputs shortened. They come from a fresh clone and live sources, so yours will differ.)

## The terminal interface

`stoxicity-tui` reads what `fetch` and `run` stored. Every screen has a route, and the breadcrumb bar
shows the trail: `Home › Topics › AI infrastructure › NVDA`. `tab` and `shift+tab` switch between
watches without leaving the screen you are on. Keys and screens: [docs/tui.md](docs/tui.md).

| Route | Screen |
|---|---|
| `/` | today's digest, tag cloud, 14-day timeline |
| `/stocks`, `/stock/NVDA` | stocks in relevant news; one stock with summary, fundamentals, timeline |
| `/topics`, `/topic/ai-infra` | topics as a cloud and a trend table; one topic |
| `/domains`, `/domain/sec.gov` | relevant against total per domain; one domain |
| `/day/2026-11-03` | one day |
| `/search?q=...` | Google-style query results with facet counts |
| `/sources` | source health |
| `/watches` | your watches: unread, channels; create, edit, mute |

## From a headline to a decision

```
  sources ─► fetch ─► score ─► search · stocks · topics · digest ─► watches and alerts
                                          │
              policy ─► scorecard ─► strategy ─► allocation ─► plan ─► review
                                                                │
                                         paper engine ◄─ order (confirmed by you) ─► broker seam
                                                                │
                              ledger ─► income ─► performance ─► report · journal
```

Each step is a command and a record you can inspect, and none of it sends anything outward until
you raise the tier. The commands for each workflow are in [docs/acceptance.md](docs/acceptance.md), and every command is
listed by `./bin/stoxicity` with no arguments.

| You want to | Start with |
|---|---|
| Follow a few stocks and get told when something happens | a watch file (`app/config/tenants/default/watches/*.yaml.example`), then `stoxicity watch check` |
| Teach the scorer what you care about, and measure it | `stoxicity label sample`, `label keep` / `drop`, `eval` |
| Set limits and see what a plan would buy | `stoxicity policy example`, `allocate example`, `plan` |
| Rehearse a trade | `stoxicity paper` and `order` (paper first; real orders need a higher tier) |
| Not lose your data | `stoxicity ops backup`, `ops verify`, `ops restore` |

## Watches, tenants and channels

- **One shared information base.** Ingest runs once and stores articles, clusters, entities and
  versioned base scores. Adding a tenant adds no fetching or scoring cost.
- **Watches** are saved views, one YAML file each. A watch picks subjects (tickers, topics, sectors),
  narrows them (domains, event kinds, relevance floors, a query), sets alerts (conditions, quiet hours,
  cooldown) and an optional digest. A watch never copies or re-scores articles, so switching is instant.
- **Channels** deliver the same notification everywhere a watch names: `tui`, `email`, `webhook`,
  `file` (Markdown) and `feed` (Atom).
- **Tenants** own watches, labels and read markers. A single user has the tenant `default` and never
  notices.

`stoxicity watch check` reports every problem in a watch file in one pass.

## Configuration

| File | What goes in it |
|---|---|
| `.env` | settings: time zone, paths, mail server. Safe to commit. |
| `.env.local` | credentials and your email address. Git-ignored, keep mode `600`. |
| `app/config/config.yaml` | defaults for everything |
| `app/config/sources.yaml` | the sources, each with its licence |
| `app/config/tenants/<id>/` | tenant and watch files |

Later wins: `config.yaml` < `.env` < `.env.local` < the real environment. Every variable is listed in
[docs/configuration.md](docs/configuration.md).

**Where your data goes.** Databases, backups and tokens are real data. Keep them outside the source
folder by setting `STOXICITY_DATA_DIR` and `STOXICITY_BACKUP_DIR` in `.env.local`
([docs/data-location.md](docs/data-location.md)). Without them the program uses `data/` next to
`.env`. Back up before the first run of a new binary: databases migrate when opened and there is no
downgrade (`stoxicity ops backup`).

## Documentation

| Area | Read |
|---|---|
| **Start here** | [acceptance matrix](docs/acceptance.md) (what is built, tested and unverified), [configuration](docs/configuration.md), [data location](docs/data-location.md), [terminal interface](docs/tui.md) |
| **News and research** | [fetchers and SEC EDGAR](docs/fetchers.md), [events and evidence](docs/events.md), [coverage and source evaluation](docs/coverage.md), [the stock universe](docs/universe.md), [provider quotas](docs/provider-quotas.md), [held-out news evaluation](docs/heldout.md) |
| **Decide** | [readiness and doctor](docs/readiness.md), [investment policy](docs/policy.md), [instruments](docs/instruments.md), [FX and prices](docs/fx-prices.md), [dividends](docs/dividends.md), [scorecards](docs/scorecards.md), [strategy](docs/strategy.md), [allocation](docs/allocation.md), [plans](docs/plans.md), [review](docs/review.md), [corrections](docs/corrections.md) |
| **Account** | [portfolio and ledger](docs/portfolio.md), [income](docs/income.md), [performance](docs/performance.md), [reports](docs/report.md) |
| **Trade** | [orders](docs/orders.md), [paper engine](docs/paper.md), [tiers](docs/tiers.md), [broker connection](docs/broker.md), [event sleeve](docs/event-sleeve.md), [walk-forward evaluation](docs/walkfwd.md) |
| **Operate** | [alerts and journal](docs/alerts-journal.md), [operations: backup, restore, health](docs/operations.md), [terminal workflows](docs/tui-workflow.md) |
| **Design** | [architecture and package map](docs/architecture.md), [design decisions with sources](docs/research/bleeding-edge-design.md) (read its "Unverified" section), [conventions](docs/conventions.md) |

## Keeping real data off GitHub

Databases, statements, prices, policy and broker files, credentials and built binaries are
git-ignored, and `.githooks/` refuses to commit or push them. Turn the hooks on once per clone, and
check anything not yet pushed:

```bash
make hooks
make guard
```

CI runs the same check on every pull request. Invented fixtures belong under `testdata/`; a deliberate
exception is listed in `.githooks/allow`. Hooks can be skipped with `--no-verify`, so if the data is
sensitive, keep your fork private.

## Contributing

Issues and pull requests are welcome. Before opening a pull request:

```bash
make hooks   # once per clone
make fmt vet test
```

Use the pull request checklist, and keep real figures, keys and account numbers out of code, tests and
documents. Use fake values. Bug reports and feature requests have templates under *Issues*.

## Security

Please report vulnerabilities privately, not in public issues: see [SECURITY.md](SECURITY.md).

## License

stoxicity is released under the [MIT License](LICENSE). Copyright (c) 2026 Botond Biro.

It builds on open-source Go modules under the MIT, BSD and Apache-2.0 licences (for example
[Bubble Tea](https://github.com/charmbracelet/bubbletea) and
[Lip Gloss](https://github.com/charmbracelet/lipgloss) for the interface and
[modernc.org/sqlite](https://gitlab.com/cznic/sqlite) for the database). The full list is in
[`app/go.mod`](app/go.mod). News, filings and prices that you fetch stay under their own publishers'
terms: the licence of each source is in `app/config/sources.yaml`.

## Disclaimer

stoxicity is research software provided as is, without warranty. Nothing it prints is investment,
tax or legal advice. You alone are responsible for your decisions and your money.
