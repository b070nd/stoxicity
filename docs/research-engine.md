# The research engine: from past events to explainable signals

Three commands turn what the database already holds into a historical event
database, measure what stocks did after each type of event, and evaluate
predefined signal rules against that measurement.

```
stored events, Form 4 trades, reported figures
        │  stoxicity eventdb build
        ▼
economic events with figures (one per event, not per article)
        │  stoxicity study run ID          + daily bars, splits, benchmark
        ▼
study report: returns after costs, against the market, by condition;
              development and validation apart; every tested group counted
        │  stoxicity playbook signals ID   + new events
        ▼
signals: buy / sell / watch, every test shown
        │  the tier gate (risk layer)
        ▼
approved for a paper or real order to be drafted by you, or not approved
```

Nothing in this chain places, drafts or confirms an order. A signal is a
request; the order path with its own checks is in [orders.md](orders.md) and
[tiers.md](tiers.md).

## What it is not

- **Not a forecast.** A study describes what happened after past events. The
  word for a confirmed pattern is *evaluated*, never "strong", and the output
  says so.
- **Not intraday.** Prices are finished daily bars. An entry "15 minutes after
  publication" inside a session cannot be measured with them and is not
  pretended: the entry is the next open or the next close after the information
  plus the stated delay.
- **Not a model's opinion.** Figures are read by rules (`text-rules-1`,
  `form4-1`, `results-1`). No language model reads them. `eventfact.Vet` exists
  to check a model's proposals against the text (verbatim spans, re-read
  values) for when one is wired in; none is.
- **Not complete history.** See "What the data allows today".

## 1. The historical event database (`eventdb`)

```
stoxicity eventdb build [-days 365 | -from DAY -to DAY]
stoxicity eventdb list [-type T] [-symbol S] [-from DAY] [-to DAY] [-at TIME] [-limit 50] [-json]
stoxicity eventdb show ID [-json]
stoxicity eventdb coverage [-json]
stoxicity eventdb types
```

`build` reads the stored events (see [events.md](events.md)), the Form 4
transactions and the reported figures and writes one record per **economic
event**. Repeating it over unchanged data writes nothing; a changed record is a
new revision and the earlier one stays readable (`list -at TIME`).

| Category | Types | Read from | Figures |
|---|---|---|---|
| earnings | `earnings_reported`, `earnings_beat`, `earnings_miss` | results events (8-K 2.02, 10-Q, 10-K) and reported XBRL figures filed by that day | revenue, net income, EPS, year-over-year changes; a surprise only when the text states one |
| guidance | `guidance_issued`, `_upgrade`, `_downgrade`, `_reaffirmed`, `_withdrawn` | the event's titles and summaries | previous and new figure (midpoint and both ends of a range), change, period, metric |
| buyback | `buyback_announced`, `buyback_expanded` | the event's text | amount, shares, previous amount, financing when stated |
| contract | `contract_award`, `contract_termination` | the event's text | value, duration, annual value, counterparty, government or commercial |
| insider | `insider_buy`, `insider_sell` | Form 4, codes P and S in common shares only | value, shares, average price, share of holding, role, 10b5-1 plan |
| analyst | `analyst_upgrade`, `_downgrade`, `target_raised`, `target_cut`, `estimate_raised`, `estimate_cut` | the event's text | previous and new target or estimate, ratings, firm |

Each record keeps:

- **`key`**: the economic identity. Every article, filing and amendment that
  reports the same thing lands on one key and is listed as evidence, so one
  event is never counted twice. Results publications of one company within 30
  days are one reporting episode; a Form 4 and its amendment are one record.
  A filing concerns the issuer, and an issuer often has several tickers (the
  common share, preferred series, notes): the event is kept for one of them, a
  plain symbol before a suffixed one, then the shorter, then the alphabetically
  first (`JPM` before `JPM-PC`).
- **`published_at` and `available_at`** with a precision of `time` or `day`.
  A filing's acceptance time is used when it is known. A day is never turned
  into a time.
- **`basis`**: `known` when the installation held the publication within 72
  hours of its appearance, `reconstructed` when it was rebuilt later (a
  backfill, or an article fetched late).
- **`source`**: `official_filing`, `official_company_release`, `wire`,
  `reported` or `import`.
- **facts**, each with its unit, period and the words it was read from, and
  **labels**.
- **`extractor` and `version`**, so that a change of rules is a new revision.

The entry delay and the holding period are not part of an event: one event can
be studied at many. They are stated in the study.

## 2. Event studies (`study`)

```
stoxicity study example
stoxicity study register -file F
stoxicity study list
stoxicity study show ID [-json]
stoxicity study run ID [-synthetic] [-point-in-time] [-with-delisted] [-at TIME] [-breakdown] [-outcomes] [-json]
stoxicity study last ID [-breakdown] [-json]
```

A study is a JSON file stated before it is run (`study example` prints one):
the event types, optional conditions on figures and labels (`where`), the
holding periods in sessions (1, 3, 5, 10, 20 by default), the entry rule and
delay, costs per side in basis points, the session times, the market and sector
benchmarks, how conditions are bucketed, a development window and a validation
window, and the thresholds.

The rules of the measurement:

- **No look-ahead.** The position is opened at the first session that starts
  (`next_open`) or ends (`next_close`) strictly after the information plus the
  delay. Information known only to the day is usable from the next session.
  Conditions are computed from bars before the entry session.
- **Costs on both sides**: half the spread, slippage and fees. The benchmark is
  held without costs, which counts against the event.
- **Missing data is not bridged.** A holding period with sessions missing is
  left out; one the data does not finish is censored; an entry too long after
  the information is refused.
- **Development and validation are apart.** A development outcome whose holding
  period reaches into validation is left out. Each run of a study with a
  validation window is counted, and a later run says how often it was read.
- **Every tested group counts.** The significance of each validation group is
  corrected for the number tested (Holm). A resampling of B draws never reports
  a probability below 1/(B+1).
- **Events on nearby days are not independent.** The interval of the mean comes
  from a bootstrap over blocks of entry days.
- **Reproducible.** The same specification and data give the same report; the
  report carries the hash of both, and the seed of each resampling is derived
  from them.

The report gives, per event type, holding period and window: the number of
events, mean and median return after costs, the win rate, the mean against the
market and the sector, the interval of the mean, the adjusted probability, the
mean and deepest drawdown while held, and a status. `-breakdown` adds the same
by condition: the stock's volatility and earlier return, company size, the
market's trend and volatility. Securities without usable bars are counted by
reason; `-json` names each. `study last ID` prints the stored report of the
latest run without computing anything.

| Status | Means |
|---|---|
| `insufficient_history` | Too few events or too few independent blocks of entry days. |
| `experimental` | Measured, but not confirmed: a development group, a result not distinguishable from zero after correction, synthetic data, no benchmark, no validation window, a universe without the delisted, or events rebuilt without an attested time. |
| `evaluated` | A validation group that passed all of the above. |

**Rebuilt events.** An installation cannot have held events from before it
existed, so history is rebuilt. A rebuilt event counts toward `evaluated` only
when it is *attested*: it comes from an official filing whose acceptance time is
on record, so when it became public is the regulator's fact. A rebuilt news
article, or a filing known only to the day, keeps the group at `experimental`.
`reconcile -enrich` reads acceptance times for backfilled filings.

`-synthetic`, `-point-in-time` and `-with-delisted` are statements about the
data that the program cannot check; the report repeats them.

## 3. Signal rules (`playbook`)

```
stoxicity playbook example
stoxicity playbook check -file F
stoxicity playbook register -file F
stoxicity playbook list
stoxicity playbook show ID [-json]
stoxicity playbook signals ID [-days 7 | -from DAY -to DAY] [-save] [-json]
stoxicity playbook history [ID] [-json]
```

A playbook names the study it reads and holds rules. A rule names event types,
conditions, the evidence it needs (holding period, optional condition
dimension, fewest events, direction, how far the bound of the mean must lie
beyond zero) and an action, buy or sell.

For each new event a rule yields a signal with every test shown:

| Test | Passes when |
|---|---|
| condition | each condition of the rule holds; one that cannot be told is unknown |
| study | the stored report is of the study the playbook names |
| population | the rule tests nothing the study did not also select on |
| comparable | the report has the validation group for this type, holding period and condition |
| evaluated | that group's status is `evaluated` |
| events | it has at least the stated number of events |
| edge | the bound of its mean lies beyond the stated edge, in the rule's direction |

The action is the rule's only when every test passes; otherwise it is `watch`.
A rule whose condition is known not to hold gives no signal. A signal's ID is a
digest of the playbook, the rule, the event and the study's hashes, so the same
event under the same rules is one signal.

**The risk layer decides.** Each buy or sell signal is put to the tier gate. At
tier 0 the answer is paper only; a suspension of new risk refuses a buy.
`watch` is never put to it. An approval allows an order of that kind to be
drafted by you; size and limits are checked by the policy when it is.
The playbook package cannot place an order: it does not import the order,
broker, paper or portfolio packages.

"Sell" means sell or avoid. It does not mean a short sale.

The older `stoxicity evidence` screen (called `signals` until 2026-11-12) is a
different thing: it measures how broad the evidence for an event is and is not
a trading signal.

## What the data allows today

A study needs years of events and bars. An installation that started recently
has weeks. Until history is loaded every group is `insufficient_history`, and
that is the correct answer.

- **Bars**: `stoxicity universe quotes` keeps daily bars from the configured
  provider; the free plan allows about two years back at five requests a
  minute, one request per market day.
- **Filings**: `stoxicity reconcile -from DAY -to DAY -repair` stores past
  filings as backfills. They are `reconstructed`, with day precision unless
  `-enrich` finds the acceptance time.
- **Analyst revisions**: no source is configured, and none is free. The types
  exist, and the text rules read a revision when an article states one.
- **Survivorship**: the ticker lists are today's. A study is capped at
  `experimental` unless you state that the universe is point-in-time and the
  prices include the delisted.

## Limits

- Figures are read by rules from titles and summaries, not from the full text
  of a release. A figure the text does not state is absent.
- Speculative or negated sentences are skipped whole, so a real figure in the
  same sentence is lost.
- "$" is read as USD. Negative numbers and basis points are not read.
- A results publication is assigned to a reporting episode by time (30 days),
  not by fiscal period.
- Which ticker stands for an issuer is read from the symbol alone. Where the
  symbols do not tell (an issuer with only preferred series listed), the choice
  is stable but arbitrary. The share-class register is not consulted yet.
- Form 4 availability is the filing day (day precision): the entry is the next
  session, even when the filing appeared before the open.
- Company size is unknown to the study: market capitalisation is not joined
  yet, so the size bucket is `unknown`.
- Bars are keyed by ticker. A ticker change splits a history.
- The correction for the number of groups covers one run of one study. Studies
  registered one after another on the same history are counted and shown, not
  corrected for.
- Automated execution, including through wikifolio, is not built.

## Verification

- 2026-11-12, on a copy of one installation's database (the original was not
  opened for writing): 22 trading days of daily bars, 4,732 stored events and
  1,846 insider transactions. `eventdb build -days 400` derived 2,383 economic
  events (1,972 earnings reports, 70 insider purchases, 340 insider sales, 1
  buyback) in under a second; a second build wrote nothing. Before the
  one-ticker-per-issuer rule the same data gave 2,535 earnings records for
  1,973 issuers.
- A study of those three types over the weeks of bars available measured 1,949
  events, left out 371 with no session after the information and 62 with no
  bars, and reported every group as `experimental` or
  `insufficient_history`: there is no validation window and too little
  history. A playbook over it gave only `watch` signals, each naming the tests
  that failed, and saving them twice stored them once.
- Not verified: any result on more than weeks of history; the text rules on
  real guidance, contract and analyst articles (the stored events of those
  kinds carried no figures the rules could read); intraday timing; a tier above
  0 at the risk gate.
- Tests: `eventfact`, `eventstudy`, `playbook`, `research`, the store's
  `eventfacts_test.go` and `cmd_lab_test.go` use invented data only.
