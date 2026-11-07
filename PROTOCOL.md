# GOAL
This is anews aggregator starting from this date: 01.11.2026
Aggregate all the relevant NEWS sources for stocks exchange, like for trading with IndependentBrokers:
+ discover stocks
+ trends
+ domains
With the way of thinking of Peter Lynch / Warren Buffett
+ google-style filtering o the news
+ aggregate which domains produce how many /relevant news of the day
ONLY the relevant sources for a long term trader

# SIMILAR PROJECT - vulnerability check for projects - use for scaffolding
https://github.com/b070nd/cvewatch/releases/tag/v2.0.0

# TUI 

---

# Production-readiness red-team review

Review date: **2026-11-07**. This records findings against the working tree at
review time, including uncommitted changes. Findings are not evidence that a
subsequent version still has the same defects; revalidate them after fixes.

## Intended production goal

Build an elite, sophisticated stock-investor research and news-processing CLI/TUI
for real-life production use, including high-frequency stock-trading workflows.
The original long-term-investor goal above remains the historical starting point.
The distinction between frequent discretionary trading and exchange-speed HFT
must be resolved before defining latency and execution requirements.

## Verdict

**I would not use this app as a production trading decision system yet.** It has
useful foundations for an investor research terminal, but several paths can
silently lose news, misattribute information, or display financially incorrect
signals.

The central risk is **plausible-looking output that is wrong without an obvious
error indicator**.

There is also a requirements mismatch: the original protocol and the scoring
logic target Lynch/Buffett-style long-term investing. High-frequency trading
needs different data, timing guarantees, and measures of success. Even if the
intention is frequent discretionary trading rather than exchange-speed HFT, the
correctness issues below remain blockers.

## Review scope and confidence

Reviewed the **current working tree**, including uncommitted fundamentals,
insider, and TUI changes: ingestion, scheduling, storage, entity resolution,
scoring/AI, summaries, trends, market data, financial metrics, watches, delivery,
configuration, and relevant tests.

- Avoided `.env`, `.env.local`, credentials, tokens, and runtime databases.
- Inspected non-secret configuration fields and configuration-loading code.
- Made no changes and ran no application, tests, or network requests during the
  review. This document was updated afterward at the user's request.
- **Confirmed** below means established by static code paths. It does not mean
  runtime reproduction.
- AI exploitability and real-world performance are explicitly distinguished from
  confirmed behavior.

Paths below are relative to the repository root. Line references describe the
reviewed snapshot and may move as implementation changes.

## 1. Release blockers: lost news and corrupted provenance

### 1. Feed state can acknowledge news before the news is stored — High

**Input → wrong output**

A company feed returns a new earnings announcement and ETag `v2`. The feed pool
saves `v2` and its seen IDs. Article persistence then fails or the process
terminates.

On retry, the provider returns `304`, or the item is filtered as already seen.

**Result: the announcement never reaches the database.**

The pool persists checkpoints inside `Fetch`; the scheduler ingests only after
`Fetch` returns.

**Evidence:** [pool.go:185–212](app/src/feeds/pool/pool.go#L185),
[poller.go:508–520](app/src/scheduler/poller.go#L508).

**Required change:** Acknowledge source progress only after durable ingestion,
using transactional checkpoints or a durable staging queue.

### 2. Concurrent ingestion can put articles permanently behind the alert cursor — High

**Input → wrong output**

- Article A receives fetch timestamp `10:00:00`, then processing stalls.
- Article B receives `10:00:01`, commits first, and advances watch evaluation to
  that time.
- A subsequently commits.
- Future evaluation asks for articles strictly after `10:00:01`.

**Result: A exists in storage but never produces its intended watch hit.**

Millisecond timestamp collisions create a related failure.

**Evidence:** [pipeline.go:128–163](app/src/pipeline/pipeline.go#L128),
[fetched.go:39–50](app/src/store/fetched.go#L39),
[ledger.go:176–190](app/src/ledger/ledger.go#L176).

**Required change:** Use a transactionally assigned ingestion/change sequence.
Wall-clock timestamps are not commit-order cursors.

### 3. One feed can replace another source's content while retaining trusted provenance — High

**Input → wrong output**

A trusted source publishes URL `U`. Another ingested feed later links to the same
URL but supplies a fabricated title and summary.

RSS identity is derived from the URL. The conflicting article update replaces
text and classifications while preserving the existing source/domain fields.

**Result: fabricated content can appear under the original trusted source's
identity.**

This also arises accidentally through syndication; no hash collision is needed.

**Evidence:** [rss.go:158–170](app/src/feeds/rss/rss.go#L158),
[base.go:71–81](app/src/store/base.go#L71).

**Required change:** Separate immutable **source observations** from canonical
stories. One publisher must never mutate another publisher's observation.

### 4. Corrections at an existing URL are ignored — High

**Input → wrong output**

A publisher corrects:

> "Profit rises 20%"

to:

> "Correction: profit falls 20%"

The URL remains unchanged, and the server returns a fresh response.

The reader skips the URL-derived ID because it has already been seen.

**Result: the old, opposite claim remains authoritative in the app.**

Different releases sharing a landing-page URL can also collapse together.

**Evidence:** [rss.go:118–124](app/src/feeds/rss/rss.go#L118),
[rss.go:166–172](app/src/feeds/rss/rss.go#L166).

**Required change:** Persist revisions, content hashes, correction relationships,
and revision events. "Already observed" must not mean "cannot change."

### 5. Backlog limits can silently discard the older tail — High

**Input → wrong output**

Following an outage, 700 unseen EDGAR filings are available for a form. The default
reader retrieves at most 500 and reports success. On a subsequent poll,
encountering a known filing near the top terminates traversal.

**Result: the older 200 can remain unrecovered.**

RSS similarly truncates a response while accepting validators for the whole
representation. Separately, one malformed entry in a full EDGAR page can reduce
its parsed length and make pagination incorrectly conclude that it reached the
end.

**Evidence:** [current.go:152–184](app/src/feeds/edgar/current.go#L152),
[current.go:237–246](app/src/feeds/edgar/current.go#L237),
[rss.go:108–143](app/src/feeds/rss/rss.go#L108).

The intended daily EDGAR reconciliation is only a stub:
[index.go](app/src/feeds/edgar/index.go).

**Required change:** Durable continuation state, explicit incomplete/backlog
status, raw-entry-aware pagination, and independent reconciliation.

### 6. Durable ingestion depends on a lossy wake-up event for evaluation — High

**Input → wrong output**

Articles and source state commit. The process terminates before publishing its
scheduler event—or the fixed-size event channel drops the event.

After restart, polls find no new items. The run loop evaluates watches only on
successful events with `New > 0`; its periodic tick only delivers already-pending
work.

**Result: stored news may remain unevaluated until unrelated fresh news arrives.**

**Evidence:** [poller.go:485–502](app/src/scheduler/poller.go#L485),
[cmd_fetch.go:259–265](app/src/cmd/stoxicity/cmd_fetch.go#L259),
[cmd_fetch.go:319–332](app/src/cmd/stoxicity/cmd_fetch.go#L319).

**Required change:** Treat events as hints. Consumers must independently catch up
from a durable change log at startup and periodically.

## 2. Financially incorrect or misleading screen output

### 7. Stock splits can look like crashes or rallies — High

**Input → wrong output**

Friday's stored close is `$100`. A 2-for-1 split takes effect Monday, whose close
is `$50` with no economic loss.

Refresh advances beyond the newest stored date rather than reconciling historical
adjustment changes.

**Result: the app can display `−50%`.** A reverse split can produce a false large
gain.

Requesting `adjusted=true` does not update historical rows already stored locally.
The fivefold movement guard does not catch ordinary splits.

**Evidence:** [keeper.go:264–274](app/src/marketdata/keeper.go#L264),
[candles.go:49–57](app/src/marketdata/candles.go#L49).

**Required change:** Corporate-action-aware historical reconciliation and an
explicit adjustment basis shared by prices and share counts.

### 8. "Daily change" can span missing trading sessions — High

**Input → wrong output**

Stored closes:

- Monday: `$100`
- Friday: `$120`

Tuesday–Thursday are missing; Thursday's actual close was `$120`.

The store selects the newest two rows and discards the previous row's date when
constructing the quote.

**Result: Friday shows `+20%`, although Friday's actual daily move was zero.**

**Evidence:** [candles.go:64–73](app/src/store/candles.go#L64),
[workspace.go:260–263](app/src/workspace/workspace.go#L260),
[quote.go:13–24](app/src/tui/quote.go#L13).

**Required change:** Verify consecutive venue sessions, backfill gaps, or label the
actual comparison interval.

### 9. Market capitalization can use the wrong share class or share-count definition — High

**Input → wrong output**

An issuer has:

- Class A: 1 million shares at `$100`
- Class B: 9 million shares at `$10`

A single CIK-level share count of 1 million is copied to both tickers.

**Result: B displays market cap `$10M` instead of its class capitalization `$90M`;
neither figure represents issuer capitalization `$190M`.**

Annual weighted-average shares can also substitute for outstanding shares.

**Evidence:** [figures.go:23–41](app/src/fundamentals/figures.go#L23),
[fundamentals.go:227–232](app/src/fundamentals/fundamentals.go#L227),
[fundamentals.go:319–322](app/src/fundamentals/fundamentals.go#L319).

**Required change:** Retain security class, share-count type/date, and
issuer-versus-security metric semantics. Suppress unsupported calculations.

### 10. Insider corrections and instrument identity are mishandled — High

Two independently dangerous cases:

**Correction → wrong total:** Form 4 reports 100,000 shares bought at `$20`. Form
4/A corrects it to 1,000 shares. The reader excludes amendments.

**Result: BUYS remains `$2M` rather than `$20K`.**

**Instrument → wrong signal:** An insider buys non-convertible preferred stock.
The parser discards `securityTitle` and treats non-derivative transactions
generically.

**Result: the common-stock page can present it as common-share buying.**

Additionally, codes P/S are categorically described as "open market," although
those codes also cover private transactions.

**Evidence:** [store/insider.go:17–25](app/src/store/insider.go#L17),
[ownership.go:87–103](app/src/feeds/edgar/ownership.go#L87),
[model/insider.go:55–71](app/src/model/insider.go#L55).

**Required change:** Amendment reconciliation, instrument-level identity,
preserved footnotes, and appropriately qualified transaction labels.

### 11. Unknown insider value is presented as no insider activity — High

**Input → wrong output**

A valid purchase reports shares, but its price is unavailable or footnote-based.

The detailed trade exists, but valuation aggregation excludes it.

**Result: the page can show a green purchase and also say there were no purchases
or sales.**

A ten-minute aggregate cache versus fresh detail queries creates another route to
contradictory output.

**Evidence:** [insider/figures.go:32–35](app/src/insider/figures.go#L32),
[tui/insiders.go:32–43](app/src/tui/insiders.go#L32),
[workspace.go:273–299](app/src/workspace/workspace.go#L273).

**Required change:** Distinguish **zero**, **unknown value**, **incomplete
coverage**, and **stale aggregate**.

### 12. An older asynchronous response can erase newer "Latest news" — High

**Input → wrong output**

Stock-news request A begins before an earnings release and stalls. Request B
includes the release and finishes first. A finishes later.

The separate latest-news loader lacks the generation protection used by the main
loader.

**Result: the earnings release disappears from "Latest news" after a successful
newer refresh.**

**Evidence:** [model.go:149–153](app/src/tui/model.go#L149),
[update.go:37–47](app/src/tui/update.go#L37).

**Required change:** Per-ticker/watch request generations and rejection of
superseded results.

## 3. Analysis that can confidently tell the wrong story

### 13. Negation and incidental keywords become asserted events — High

**Input → wrong output**

> "Acme denies bankruptcy; no going concern issue"

matches bankruptcy and receives an additional magnitude increment from the
explicitly negated "going concern."

> "Acme files for bankruptcy; dividend suspended"

becomes **dividend**, because the first matching rule wins and dividend precedes
bankruptcy.

The AI cannot replace an already-recognized non-`other` kind, even when it
disagrees correctly.

**Evidence:** [lens.go:134–143](app/src/lens/lens.go#L134),
[lens.go:299–307](app/src/lens/lens.go#L299),
[apply.go:112–134](app/src/lens/apply.go#L112).

**Required change:** Multiple issuer-linked assertions with polarity,
attribution, temporal status, and event priority—not one article-wide keyword
label.

### 14. Exchange-qualified symbols can resolve to the wrong security — High

**Input → wrong output**

A US `ABC` is indexed. A story says:

> "LSE: ABC reports earnings"

The matcher recognizes the exchange prefix but resolves using only `ABC`.

**Result: the London story can attach to the US security.**

**Evidence:** [match.go:237–244](app/src/entity/match.go#L237),
[index.go:89–100](app/src/entity/index.go#L89).

**Required change:** Venue-qualified, effective-dated security identities. Reject
contradictory venues and abstain on collisions.

### 15. Deduplication can remove the correction rather than the false claim — High

**Input → wrong output**

An older, higher-ranked sentence says:

> "Acme will acquire Beta for cash this year."

A newer correction says:

> "Acme will not acquire Beta for cash this year."

Their unordered word sets are approximately 89% similar, exceeding the duplicate
threshold. If the older claim ranks first, the correction is suppressed.
Shared-cluster suppression can do the same.

**Result: a literally sourced summary conveys the opposite of the updated story.**

**Evidence:** [summarize.go:84–96](app/src/summarize/summarize.go#L84),
[summarize.go:177–212](app/src/summarize/summarize.go#L177).

**Required change:** Correction- and contradiction-aware deduplication, preserving
changes in negation, numbers, and event status.

### 16. Reposts and incidental ticker mentions manufacture apparent signals — High

**Input → wrong output**

- Three syndicated copies of one story, even sharing a cluster, produce topic
  count **3** and lift **6** against an empty baseline.
- Two high-materiality stories about Acme include an incidental `$XYZ` footer.
  XYZ receives both stories' full materiality and can become a discovery
  candidate.

**Result: distribution volume masquerades as independent evidence, and mention
presence masquerades as issuer impact.**

**Evidence:** [trends.go:59–101](app/src/trends/trends.go#L59),
[discover.go:59–88](app/src/discover/discover.go#L59).

**Required change:** Independent-event/source counting and per-issuer event
roles: subject, counterparty, comparator, background mention.

### 17. AI exposure is enabled in the checked-in configuration — Conditional high risk

The YAML enables Laya with confidence threshold `0.7` and applies **kind, topics,
and tone**. This differs from the disabled Go defaults.

**Adversarial input → possible wrong output**

A feed embeds instructions asking the model to choose a configured topic or event
kind. If obeyed:

- Added topics can make unrelated articles enter topic-based watches.
- Filling an `other` kind can indirectly promote materiality and relevance, even
  though direct AI materiality/relevance application is disabled.
- Tone can mislead the display.

**Successful prompt injection was not demonstrated.** These are confirmed
downstream consequences of a successful model manipulation, not a proven exploit.

Local text clipping also removes qualifications without reliably recording that
local truncation occurred.

**Evidence:** [config.yaml:80–103](app/config/config.yaml#L80),
[apply.go:127–175](app/src/lens/apply.go#L127),
[watch/match.go:48–53](app/src/watch/match.go#L48),
[state.go:12–35](app/src/lens/laya/state.go#L12).

**Required change:** Adversarial model evaluation, explicit untrusted-content
boundaries, truncation provenance, and stronger evidence requirements for
AI-triggered alert eligibility.

## 4. Operational traps

### 18. Undelivered alerts can be recorded as delivered — High

**Input → wrong output**

A watch requests email, but the email channel cannot be constructed because
configuration is missing.

The app logs the problem, omits the sender, and marks the pending alert delivered.

**Result: configuring email later does not recover the missed notification.**

Conversely, when one channel succeeds and another fails, retries resend to all
channels. A successful email or webhook can therefore be duplicated.

**Evidence:** [app.go:551–557](app/src/cmd/stoxicity/app.go#L551),
[deliver.go:124–149](app/src/ledger/deliver.go#L124),
[deliver.go:204–233](app/src/ledger/deliver.go#L204).

**Required change:** Durable per-destination delivery state, stable event
identifiers, idempotent receivers, and explicit blocked-configuration status.

### Other material traps

| Trap | Concrete consequence |
|---|---|
| **Partial fetch failures become healthy polls** | 8-K fetching fails while 10-K succeeds; source health clears failures despite missing 8-K coverage. [current.go:107–130](app/src/feeds/edgar/current.go#L107) |
| **Mixed fiscal periods share one display label** | FY2024 revenue and FY2025 income appear together as "last fiscal year." [fundamentals.go:264–284](app/src/fundamentals/fundamentals.go#L264), [companies.go:400–415](app/src/tui/companies.go#L400) |
| **Staleness is too permissive** | Prices several days old remain ordinary screening inputs; the catalogue's newest-anywhere date can obscure older individual rows. [quote.go:26–33](app/src/tui/quote.go#L26), [companies.go:266–285](app/src/tui/companies.go#L266) |
| **Exceptional exchange closures are unsupported** | Recurring holiday rules can report "open" during exceptional closures. Scheduled hours also do not establish actual trading availability. [calendar.go:25–45](app/src/exchange/calendar.go#L25) |
| **Historical financial facts are overwritten** | A revised value replaces the previous period's fact, preventing reconstruction of what was known before the revision. [fundamentals.go:27–34](app/src/store/fundamentals.go#L27) |

## 5. Why this does not yet meet the trading objective

These are architectural gaps, distinct from individual bugs:

- **The scoring objective is wrong for short-horizon trading.** Price
  dislocations and trading chatter are deliberately demoted. A tested "shares
  plunge 12%" example falls below the default relevance threshold.
- **Market data is daily candles.** Faster refreshes cannot turn EOD data into
  actionable live quotes, spreads, liquidity, or executable prices.
- **Polling cadence is investor-oriented.** The stock-feed pools use multi-hour
  per-feed cycles; the central EDGAR path is separate. Fast-mode configuration
  is disabled in the checked-in YAML.
- **Relevance is not market impact.** Business importance, directional price
  surprise, novelty, and execution opportunity are different quantities.
- **Point-in-time replay is inadequate.** Overwritten articles and financial
  facts cannot establish exactly what the system knew at a historical decision
  time.
- **Evaluation does not measure trading correctness.** Perfect relevance labels
  can coexist with completely wrong tickers, event polarity, summaries, and
  timing. P@K/Brier/ECE do not detect those failures.

For genuine HFT, the reviewed architecture does not establish bounded end-to-end
latency or trading-execution guarantees. For discretionary intraday use, the same
provenance, freshness, and completeness defects still matter.

## 6. Blind spots—including mine and the agent's

### The application's blind spots

1. **Unknown becomes false:** unavailable insider valuation becomes "no
   purchases."
2. **Mention becomes impact:** every ticker inherits article-wide materiality.
3. **Similarity becomes equivalence:** adding "not" can disappear during
   deduplication.
4. **Successful request becomes complete coverage:** partial upstream failure
   can remain green.
5. **Model confidence becomes trust:** self-reported confidence is not calibrated
   reliability.
6. **A timestamp becomes ordering:** wall-clock time is treated as durable
   ingestion order.

### This review's blind spots

- No runtime reproduction, load testing, crash injection, or recovery drill was
  performed.
- Actual source coverage, production overrides, model availability, database
  condition, and current freshness remain unverified.
- Several parser tests use synthetic fixtures. Passing them would not establish
  compatibility with messy real filings.
- The historical live-data measurements in `docs/stock-selection-review.md` are
  **prior documented claims**, not measurements independently repeated in this
  review.
- Prompt-injection success and profitable predictive value require empirical
  evaluation.
- Static reviewers can mistake intended behavior for correct behavior. Several
  dangerous choices here are explicitly locked into tests—such as ignoring
  amendments and counting unconfigured channels as delivered.

A positive finding: there are meaningful HTTP/SSRF, body-size,
terminal-sanitization, configuration-validation, and tenant-visibility defenses.
Those are useful foundations, but they do not establish financial correctness.

## 7. Recommended remediation order

| Priority | Work | Acceptance evidence |
|---|---|---|
| **P0: ingestion integrity** | Durable change sequence, checkpoint ordering, source-isolated observations, revision history, reconciliation | Crash/reorder/backlog tests prove every observation is retained and evaluated |
| **P0: displayed correctness** | Split-safe prices, session-aware changes, class-aware capitalization, amendment-aware insiders, consistent UI snapshots | Financial counterexamples above produce correct or explicitly unavailable outputs |
| **P1: evidence model** | Issuer–event relationships, assertion polarity, correction-aware summaries, independent-event trends | Adversarial corpus covers denials, corrections, syndication, venue collisions, and incidental mentions |
| **P1: operations** | Per-destination delivery receipts, completeness/freshness status, consumer-lag monitoring | Missing configuration and partial outages remain visibly unresolved and recoverable |
| **P2: trading validation** | Separate short-horizon objective, point-in-time replay, temporal holdouts, source-to-screen latency measurements | Demonstrated quality and latency under realistic bursts, failures, and source shifts |

**Bottom line:** the next step toward an elite tool is an auditable evidence
pipeline: what was observed, from whom, when it became known, which security it
concerns, what changed, and how incomplete or stale it is. Until those contracts
are reliable, more sophisticated rankings and AI will amplify hidden errors
rather than remove them.

## 8. Remediation status

Written after the fixes below were made and tested (`go test -race -shuffle=on
./...`). **Fixed** means the counterexample in this review now produces the
right output or an explicit "unavailable", with a test. **Partial** means the
failure is reduced and what remains is named. **Open** means nothing was done,
and why. Nothing here was run against live feeds; the tests use synthetic
fixtures, and no crash-injection drill was performed.

| # | Finding | Status | What was done and what is left |
|---|---|---|---|
| 1 | Feed state acknowledged before the news is stored | **Fixed** | A pool feed's validators and seen items are saved by `Result.Commit`, which the poller runs only after `Ingest` succeeded. Tests: `TestCommitRunsOnlyAfterTheArticlesAreStored`, the pool test. A crash between the two repeats the read. |
| 2 | Articles behind the alert cursor | **Fixed** | Every change to an article takes the next number of a durable sequence (`articles.change_seq`, migration `0011`), assigned inside the committing transaction. A watch's cursor is that number (`eval_cursor.seq`, migration `0004`), not a time, so a slow worker, a same-millisecond commit or a clock set backwards cannot put news behind it. `ChangedAfter` and `ChangeSeqAt` replace the time-based reads; old time cursors are moved to a number once. Tests: `TestEachCommitGetsAFetchTimeAfterTheEarlierOnes`, the ledger evaluate tests. |
| 3 | One feed replaces another source's content | **Fixed (evidence kept)** | A second source with the same article ID cannot change a non-filing article of the first, and what it said is not lost: every distinct statement of a source is appended to `observation` (migration `0011`), keyed by article, source and content hash, with received and recorded times. Filings from two EDGAR sources still merge, on purpose. |
| 4 | Corrections at the same URL ignored | **Partial** | The RSS reader reads an entry again when its title changed; the store updates the text, writes a fresh score and gives the article a new change number so watches see it again. Every version is kept in `observation`, and an event whose source changed its own text is marked `corrected`. Not detected: a change of the summary only without a title change, and two releases on one landing page. |
| 5 | Backlog limits discard the older tail | **Fixed for EDGAR, partial elsewhere** | A cut-off read is reported as `INCOMPLETE`. For EDGAR, `stoxicity reconcile` reads the SEC's daily master index for each past weekday (once, after the day is over), stores the wanted filings in `filing_index` and compares them with the stored articles by accession; `-repair` stores the missed ones, dated to the start of their filing day (the index gives no time). Not done: other feeds have no authoritative index to reconcile against. |
| 6 | Evaluation depends on a lossy wake-up event | **Fixed** | `stoxicity run` evaluates all watches at start and on every delivery tick, as well as on events. Evaluation is idempotent (cursor, hits recorded once). |
| 7 | Splits look like crashes | **Fixed** | Polygon candles are now fetched raw (`adjusted=false`) and marked `raw`; splits come from Polygon's splits list (`split`, `split_run`, migration `0013`) and are read once a day. `marketdata.Compare` reconciles two closes with the splits between them: the move is shown on one basis, not hidden for its size. A candle of unknown basis next to a later split, or a split-like move the known splits do not cover (the coverage is recorded), is `unresolved` and shows as `split?` in the lists. Old candles stay of unknown basis until fetched again: `universe quotes -refetch 30 -max 30` fetches the last 30 days again (paced to the free plan's five requests a minute; days already raw are skipped, so an interrupted run resumes). |
| 8 | Change spans missing sessions | **Fixed** | `SessionChange` needs both closes of consecutive sessions (at most one weekday between, for a holiday). |
| 9 | Market cap from the wrong share count | **Fixed for covered issuers (section 11)** | Lists, filters, sorting, details and exports use class-aware values (`classcap`, `issuercap`; `marketcap` is issuer capitalisation) from cover-page counts read in the background and stored per class. An issuer whose cover page is not read, whose class is unmapped or ambiguous, or whose price is missing, stale or in the way of a split shows `n/a` with the reason. Left: ADR conversion ratios, non-USD, coverage that depends on `covers_per_cycle`. |
| 10 | Insider amendments and instrument identity | **Fixed** | 4/A is read and replaces the earlier forms of the same owner and period; only common stock counts; P and S are called purchases and sales. Footnotes are not read. |
| 11 | Unknown insider value shown as nothing | **Fixed** | Unpriced trades are counted and warned about; list and page summaries come from the same trades. |
| 12 | Older response erases newer "Latest news" | **Fixed** | Generation numbers; stale answers are dropped. |
| 13 | Negation and incidental keywords | **Partial** | Bankruptcy, restatement and delisting outrank dividend and buyback in one story; a denied bankruptcy, restatement, cyber incident or delisting ("denies", "no", "not", "without" within three words, same sentence) is not that event, and a denied "going concern" adds no magnitude. Not done: assertions with polarity, attribution and time per issuer; the AI still cannot replace a rule's non-`other` kind (it is recorded as "disagrees"). |
| 14 | Exchange-qualified symbols | **Partial** | A prefix such as `LSE:` must agree with the market area of the listing (MIC or exchange name); a contradiction is rejected. A ticker listed in two market areas is not guessed from a bare mention or a cashtag. The index still holds one entity per ticker, so the London listing itself is not found. |
| 15 | Dedup removes the correction | **Fixed** | Two sentences are alike only if they also agree on denial and on the numbers; a differing sentence of the same cluster is kept. Both claims are shown; neither is marked as the correction. |
| 16 | Reposts and incidental mentions | **Partial** | Trends count a cluster once per topic. Events carry observed roles per security (subject, counterparty, mentioned: `event_security`), and assessments are kept apart. The trend and list counts do not yet use the roles. |
| 17 | AI exposure enabled in the config | **Fixed (config)** | `laya.apply` is empty in the checked-in config: answers are advisory. Not done: untrusted-content boundaries and truncation provenance in the model prompt, adversarial evaluation. |
| 18 | Undelivered alerts recorded as delivered | **Fixed** | A missing channel keeps the alert (and digest) pending, counted as `Blocked` and warned about. What each channel received is stored (`delivered_channel`, migration `0003`), so a retry or a later configuration sends only to the channels that lack it. Not done: stable event IDs for idempotent receivers. |

### Other traps

| Trap | Status |
|---|---|
| Partial fetch failure reads as healthy | **Partial**: reported as `INCOMPLETE`; the health grade is unchanged. |
| Mixed fiscal periods under one label | **Fixed**: revenue and income are paired only for the same year end. |
| Staleness too permissive | **Fixed for the age**: a candle older than one missing weekday is dim. The catalogue's newest date is still not compared with each row. |
| Exceptional exchange closures | **Open**: needs a calendar source; recurring rules only. |
| Historical financial facts overwritten | **Fixed** (new data): every fact read goes to `fundamental_fact` with its accession, concept, unit, dimensions and receipt time, and a restatement is a new row. `universe fundamentals history TICKER -at TIME` answers what was known then, counting a filing as public the day after its filing day. Facts without a known filing day are left out of as-of answers. |

### Still not met

Section 5 stands: nothing here makes the application a trading tool. The
decisions that remain are listed in the questionnaire that ended the work.

## 9. Decisions (questionnaire of 2026-11-07)

- **Objective:** both long-term screening and frequent discretionary trading must
  be served, so that neither perspective hides a blind spot. HFT is a later,
  separate exploration on a virtual server with expert validation. The product is
  to be fintech/enterprise grade: no assumptions, no shortcuts. Capital in mind:
  about EUR 1,000 over five years, cost-effective. (The tool supports decisions;
  it gives no guarantee of profit and no personalised investment advice.)
- **Ingestion (findings 2 to 5):** immutable per-source observations and revision
  history, a durable ingestion sequence as the consumer cursor, and a real EDGAR
  daily-index reconciliation. On a strong signal for a stock, build a small
  knowledge graph around it (graphify style) so one peak is not overestimated;
  extend it case by case.
- **Market data, in this order:** (1) real split events with one price
  adjustment basis, raw observations kept, and an "unresolved corporate action"
  state in place of hiding a move; (2) point-in-time fundamentals (acceptance
  time and receipt time, accession, period, concept, unit, dimensions, source;
  revisions as new rows); (3) per-class share counts, with class capitalisation
  kept apart from issuer capitalisation.
- **Not committed yet:** the work above (sections 8, 10 and 11) is in the working tree only.

## 10. Evidence and event model (implemented 2026-11-07)

Articles are evidence, not events. The implementation follows the design note
given with the last request:

- **Observations** (`observation`): every distinct statement a source made about
  an article, appended with published, received and recorded times and a content
  hash. Nothing is overwritten there.
- **Events** (`event`, `event_revision`): one persistent ID per event (`evt_` plus
  its story), derived from scored and clustered articles. Many articles can be
  one event. A revision is appended only when the understanding differs
  (fingerprint), and it points to the one it supersedes. `recorded_ms` answers
  "what did the system know at time T"; the latest revision answers "what do we
  believe now".
- **Securities** (`security`, `security_ticker`): stable IDs (`sec_`) with
  effective-dated ticker labels. A ticker passed to another company closes the
  old label and starts a new security.
- **Roles** (`event_security`) are observed relationships: subject,
  counterparty, mentioned. **Assessments** (`event_assessment`) are separate,
  with their own effect, direction (`unknown` unless there is a reason),
  horizon, confidence, rationale and rule version.
- **Times** kept apart: effective day, public time (earliest evidence), received
  time (observation), recorded time (revision), assessment time.
- **Commands:** `events rebuild | timeline TICKER -at TIME | show EVENT`;
  `signals` (evidence is *primary*, *corroborated* or *unresolved* by reporting origin,
  see section 11; materiality plays no part in the verdict; a dominating episode
  is flagged; it measures breadth and independence of evidence, not predictive
  value); `graph TICKER`
  (text, json or dot); `export` writes `research/events/evt_*.md` and
  `research/securities/sec_*.md`, only when the text changed, so Git holds
  readable history and the database stays the record (no branch per stock).
- **Checked live on 2026-11-07 (one company, Moog, CIK 67887):** the company
  facts answer carries `accn`, `form`, `filed`, `start` and `end` per fact (the
  SEC documentation lists none of these). For this dual-class company the answer
  has **no** shares-outstanding fact at all, only the weighted average, which
  fits dimensional (per-class) facts being left out, so the cover page of the
  filing is the source of per-class counts. The cover page of the 10-Q wrote
  numbers as `ixt:num-dot-decimal`; the parser reads both spellings. The frames
  answer was **not** checked live. Valued at the cover counts times the stored
  closes, both classes priced, the issuer came to about 9.8 billion USD.
  Everything else in this section ran only against synthetic fixtures.

## 11. Provenance, reconciliation, class valuation and assertions (implemented 2026-11-07)

One coordinated change on top of section 10. Nothing is committed. Existing
observations, event revisions, stable security IDs and point-in-time history are
preserved: every new table is additive and append-only, and every relationship
inferred now is stamped with the time it was made, never backdated.

### 11.1 What changed, and what is new or old

| Area | Before | Now |
|---|---|---|
| `signals` | default `-min 2` removed events before the corroboration verdict | default `-min 0`; the verdict is independent of materiality; a given `-min` is labelled with the number of events left out and the warning that it can lower states. New `-at`, `-explain`. |
| Evidence states | corroborated or not | `primary` (one origin), `corroborated` (two or more origins that do not rest on each other), `unresolved` (independence not established) |
| Origins | publisher domain | publisher and reporting origin kept apart (`0015`), derivation, method, confidence, version; copies count once; unknown stays unknown |
| Episodes | any event of one kind within 72 hours merged | 72 hours is a candidate filter; a proof is needed to merge, a difference to keep apart; decisions stored with provenance (`0015`) |
| `reconcile` | seven days, daily index only, a missing index could be skipped for ever | per-day state and progress (`0016`), explicit `-from -to`, `-catchup`, quarterly indexes, separate `-enrich`, `-facts`, `-repair`, backfill marks |
| Share counts | the frames count, the first `share_class` table | class observations, mappings and cover checks (`0017`), background cover keeper |
| `marketcap` | shares times close | `issuercap`; new `classcap`; reasons for unavailable values |
| Claims | first keyword in an article | assertions per sentence, assessments of four separate dimensions (`0018`) |

New commands and flags: `signals -at -explain`; `reconcile -from -to -catchup
-no-quarterly -refetch -max-requests -enrich -facts` (`-days`, `-repair`
existed); `universe fundamentals classes -stored -at` (the command existed with
other behaviour); `universe fundamentals covers`; `universe fundamentals probe`;
queries and exports `classcap`, `issuercap`; config `company.covers_per_cycle`.
`events rebuild` now also writes assertions and assessments. Existing commands
without a listed change behave as before.

### 11.2 Materiality and corroboration (item 2)

Materiality is a keyword rule and is not evidence. It no longer filters what the
corroboration assessment sees. One filing is primary evidence; it is not
independent corroboration of its own claims, and neither are the stories that
quote it. The same semantics apply to the CLI (`signals`, `graph`), the exports
(`export`) and the documents ([docs/events.md](docs/events.md)).

### 11.3 Live check of the SEC frames (item 3)

Made on 2026-11-07 with `stoxicity universe fundamentals probe MOG-A`, read-only,
through the existing HTTP client (contact identification, rate limit and body
cap apply). It stores nothing. Fixtures, with their source URL and retrieval date,
are in `app/src/fundamentals/testdata/`.

Observed in these two answers (observations, not documented guarantees):

- Duration frame `us-gaap/RevenueFromContractWithCustomerExcludingAssessedTax/USD/CY2024`
  (2,973 records, 454,719 bytes): every record has `accn`, `cik`, `end`,
  `entityName`, `loc`, `start`, `val`. Periods seen: 321 days x1, 329 x1, 363 x227,
  364 x150, 365 x2,564, 370 x30, so a "CY2024" frame holds fiscal years of
  different lengths ending at different days. One record per CIK; every accession
  is in the `NNNNNNNNNN-YY-NNNNNN` form. The header says `pts` 2973, equal to the
  records.
- Instant frame `us-gaap/Assets/USD/CY2024Q4I` (6,280 records, 836,865 bytes): the
  same fields without `start`. `end` is the company's own balance date near the
  quarter end (for example 2024-11-30 and 2024-12-28), not 2024-12-31 for all.
  An accession carries the year of the filing that stated the value, which can be
  after the period (for example `-26-` for fiscal 2024 figures).
- Units: only `USD` named in both. **No** field named dimensions, dimension, axis,
  member or segment occurs in these answers. That is an observation of these two
  answers; it says nothing about other frames, concepts or dates, and no
  universal claim is made.
- Comparison: for Moog (CIK 67887) the instant frame record (accession
  `0001628280-25-054103`, period end 2024-12-28, value 4,181,848,000) matched the
  company's own fact history: 10-K of that accession, filed 2025-11-26, no
  dimensions. Moog does not occur in the duration frame that was read.
- No filing date or acceptance time is in a frames record; none is inferred. The
  filing day of a frame fact comes only from matching the accession against the
  company facts or the filing record.

### 11.4 Reconciliation (item 4)

See [docs/events.md](docs/events.md#reconcile-finding-filings-the-live-feed-missed).
Summary: seven-day rolling overlap kept; explicit range; per-day state
(`complete`, `partial`, `unpublished`, `nonfiling`, `error`) with `unpublished`,
`partial` and `error` days retried; catch-up after outages up to 90 days; quarterly
indexes for ended quarters with enough days to read, daily for the rest, with fall
back to daily on failure; accessions de-duplicated; index discovery, enrichment
(acceptance time from the company record) and repair are separate steps;
recovered articles are backfills (`late` within 3 days of the filing day,
`historical` after); the ledger withholds urgent alerts of any backfilled article;
fundamentals' filing days are resolved by accession for the accessions needed
only; a date-only filing keeps `precision` `day`.

### 11.5 Class valuation (items 5 and 6)

See [docs/universe.md](docs/universe.md#share-classes-and-capitalisation). The
cover page is parsed by a background keeper, never while a list is drawn.
Observations keep accession, public availability, time received, class
dimensions, shares and the count date; duplicates are counted, conflicts, revisions
and dropped classes are explicit; mappings keep their method and basis. Weighted
average shares are never used. `marketcap` = `issuercap` = the sum of every class,
only when every class can be valued; each valued class keeps `classcap`.

Cases with expected results, tested in `valuation_test.go`: Moog with both classes
listed has `issuercap` = sum and both `classcap`; Alphabet with class B unmapped
has `classcap` for GOOGL and GOOG and **no** `issuercap`/`marketcap`.

### 11.6 Reporting origin and episodes (items 7 and 8)

See [docs/events.md](docs/events.md#reporting-origin). Rules are explicit and
conservative: provider ID, wire attribution or byline, canonical reference, filing
accession, exact-copy fingerprint. Topic similarity is not independence. Episodes:
kind + 72 hours are candidates; filing/release identifiers, shared evidence and
event-specific facts decide. A release, its 8-K and coverage form one episode; two
separate acquisitions within 72 hours stay two. The 70% diagnostic is computed
after de-duplication over the reports (distinct origins) of the window, and the
numerator, denominator and window are printed.

### 11.7 Assertions and assessments (item 9)

See [docs/events.md](docs/events.md#assertions-assessments-and-the-four-dimensions).
Multiple assertions per article, roles, negation, denial (including
retractions), history, anticipation, status and attribution; filing items are
evidence of kind not size; assessments are separate and versioned; materiality,
novelty, urgency and price impact are distinct; the system abstains when evidence
is insufficient and price impact always abstains. The AI path exists as a schema
and a vetting function only (model and prompt versions, truncation and evidence
spans are stored; source-embedded instructions change nothing).

### 11.8 Migrations and what they mean

`0015` origins and episode decisions; `0016` EDGAR index days (new columns with
defaults on `filing_index_day`, one `UPDATE` that classifies the days already recorded: those recorded as
missing become `unpublished`, so they are retried, read-after-the-day ones
`complete`, the others `partial`), quarters, runs, filing metadata and backfill marks; `0017` class
observations, mappings, cover checks, plus `INSERT OR IGNORE ... SELECT` from the old
`share_class`; `0018` assertions and assessments. Nothing is dropped and no stored
evidence is rewritten; the old `share_class` table stays. They run when the
database is opened, so back up `data/` before the first run of the new binary. No
downgrade script exists.

Run on a **copy** of the live data on 2026-11-07 (the live files untouched): the
reference database went from migration 14 to 18, the two stored `share_class`
rows were copied to `share_class_obs` (2), and the tenant database was at 4 (it
has its own migration series). `events rebuild -days 365` on the copy wrote 3,233
events from 5,223 articles with 143 revisions, 4,740 claims of 3,841 articles
(`assert-1`), 18,648 assessment readings (`assess-1`) and 3,315 origin findings.

### 11.9 Verification (item 10)

Tests cover: wire republication, independent follow-up reporting, uncertain
origins, distinct nearby events, denials, corrections, incidental ticker mentions,
multi-event articles, duplicate XBRL contexts, split-adjusted counts, missing class
prices, ambiguous class mappings, revised filings, temporarily missing indexes,
interrupted backfills, repeated repairs, historical-alert suppression, Moog and
Alphabet, injection resistance of the vetting, and as-of cutoffs that exclude
information received, decided, mapped or assessed after the cutoff.

### 11.10 Unresolved limitations

- Current feeds supply no provider IDs; wire attribution is read from text and not
  verified; near-copy detection is not built, so a rewritten copy is `unknown`.
- No AI extractor is wired in. The lens's per-article score is still first-keyword;
  events use assertions. One event per story remains; several claims are kept as
  assertions. Claim importance ranking is a judgement, not calibrated.
- Market cap exists only for issuers whose cover page was read; coverage grows with
  `covers_per_cycle`. ADR conversion ratios are unavailable (ADR classes are
  rejected, not guessed). Currency is assumed USD.
- Only the newest stored session price exists, so valuation is not offered as of a
  past time. A split that the source lists late can misalign a count and a price;
  the split-list coverage rule then rejects the value.
- The holiday calendar cannot know special closures. `acceptanceDateTime` is used as
  the SEC writes it. The quarterly index can exceed the HTTP body limit; the daily
  indexes are used then.
- Still not a trading tool (section 5): nothing here predicts prices or gives
  advice.
