# Replay and walk-forward evaluation

`stoxicity walkfwd` replays what a strategy **proposed** against finished daily bars and says, against criteria
fixed beforehand, how much that shows. Its answer is one of three, and the third is a normal answer:

| Status | Meaning |
| --- | --- |
| `evaluated` | Every preregistered criterion was met on the test (or reserve) period of a **known** replay on non-synthetic, point-in-time data. Evidence about that period, not a forecast. |
| `experimental` | The sample was sufficient but something caps it: the edge is not shown at the required confidence, the downside limit was breached, fills were too few, the data is synthetic or reconstructed or not free of survivorship, the test period was read before, or the period is a tuning one. |
| `insufficient_history` | Too few matured trades, too few independent clusters, thin coverage, or no proposal in the period. |

A profit on synthetic data can never be `evaluated`.

## Rules

* **Frozen splits.** Train, tune, test and an untouched reserve, in order, separated by an embargo of at least one
  day (state it as long as your longest holding horizon, so a trade held across a boundary cannot leak). Splits
  and criteria freeze together (`walkfwd freeze`); after that they cannot change. The test period can be read
  only after the freeze; the second read of it is flagged as optimisation against it; the reserve is read once.
* **Every trial is registered** (`walkfwd register`) before it runs, with its strategy and parameters, and is never
  edited. The number of trials sets the confidence: the interval is at `1 - alpha / trials` (Bonferroni), so the more
  that was tried the more a result must show. A trial registered after the freeze counts too.
* **Known or reconstructed.** The manifest says which. A reconstructed replay (public history rebuilt today) shows
  what the world could have known, not what this installation knew, and is capped at `experimental`.
* **No lookahead.** A proposal whose information became known after the decision is excluded and counted. An entry
  is executable no earlier than the first session after both the decision and the day the information was known.
* **Decisions not to trade are kept** (quantity 0), so coverage and fill rate are not computed on winners only.
  `walkfwd from-events` turns the saved event-sleeve proposals, WAIT and RESEARCH included, into proposals.
* **Fills follow the paper engine's rules on daily bars**, without its seeded draw: a buy fills only if the low went
  strictly below the limit, at the limit, limited to a share of the volume and refused below a minimum fill. If one
  bar could have filled the entry and hit the stop, the stop is taken. A stop never fills at its own price: a gap
  fills at the open, and slippage is added. A time exit is decided at the close and executed at the next open.
* **Delisting** without a terminal value is a total loss; with one, that value. A position whose exit is not in the
  data is **censored** and left out of the matured figures, never marked as a result.
* **Distributions** go to a buyer before the ex-day, net of a stated withholding rate. **Fees** come from a sourced
  schedule (`-fees`). Sensitivity to extra slippage and to the withholding rate is printed.
* **Uncertainty.** Trades that overlap in time are not independent. They are grouped into clusters of overlapping
  holding windows and the interval is a bootstrap over clusters (2,000 resamples, seeded from the manifest hash, so a
  rerun gives the same interval). The preregistered number of clusters is a requirement.
* **Comparison.** Each trade is compared with the diversified fund over the same holding window, net of the fund's
  own trade cost, and with cash at a stated rate (default 0, said so). The edge criterion is the **lower end** of the
  net excess return over the fund.
* Also reported: matured, censored and delisted counts, fill rate, coverage, mean, median, hit rate, worst trade and
  5th percentile, fees, turnover, holding time, breakdown by quarter and by sector, and a list of bias diagnostics.

## Commands

```
walkfwd example
walkfwd register -file trial.json
walkfwd splits -file splits.json        walkfwd criteria -file criteria.json
walkfwd freeze
walkfwd run -trial ID -period test -manifest m.json -proposals p.json -bars bars.csv [-dists d.json] [-delist x.json] [-config c.json] [-fees f.json]
walkfwd status
walkfwd from-events -trial ID           saved event-sleeve proposals as replay proposals
walkfwd from-playbook -trial ID -playbook PB [-days N | -from DAY -to DAY] [-currency C] [-fund SEC] -bars-out FILE
                                        playbook signals as replay proposals, and the bars they need as a CSV
```

## What is not covered

* The strategies themselves are not re-run over history here. This package replays the **proposals** they made (the
  event sleeve's own saved proposals, or a file). Re-running the core strategy as of past dates needs point-in-time
  scorecards, prices and fees for those dates, which this installation does not hold.
* Bars come from files. `from-playbook` writes such a file from the stored daily bars, on the split-consistent basis of the newest bar (not the as-traded price), so the replay can be fed from the database; `run` itself is unchanged. A holiday calendar is not known, so a gap larger than the tolerance (default 4 weekdays)
  marks missing history.
* The criteria and every number in the default configuration are arbitrary starting points that you must approve.
* Nothing here turns a result into permission to trade: tiers and readiness are separate, and `evaluated` never
  raises one.

## Replaying a playbook

`from-playbook` makes the proposals for the signals of a playbook in a window. A rule must carry a `trade`
stanza for this (see [research-engine.md](research-engine.md)): the notional per trade, how far above the reference
close the limit lies and how far below it the invalidation lies. The numbers are yours and arbitrary.

* A **buy** becomes a sized proposal: the limit is the reference close times one plus the limit offset, the quantity
  is the whole shares the notional buys at the limit. The reference close is the last bar on or before the day the
  information was known; the entry is the first session after that day, so no bar from the entry session or later is
  read.
* A **watch** or **sell** signal becomes a decision not to trade (quantity 0), kept so that coverage is honest. A sell
  means sell or avoid, never a short.
* A buy that cannot be sized (no bars, bars not on one basis, no earlier bar, a stale close, an unknown currency, a
  notional that buys no whole share) is also quantity 0 and is listed with its reason on stderr, never dropped.

The command registers no trial and runs nothing; the trial, the frozen splits and `walkfwd run` stay as they are.
The event sleeve (`stoxicity event`) is not replaced: it sizes under the policy and carries the paper-only guard
and the tests of its template, which a rule file does not.
