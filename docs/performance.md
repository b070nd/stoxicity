# Performance: what a book earned, apart from what was paid in

`stoxicity performance BOOK -marks bars.csv [-rates rates.csv] [-from D] [-to D] [-benchmark a.json,b.json]`
derives everything from the immutable ledger and from prices you supply. Nothing is stored, and a figure
that cannot be computed honestly is shown as **unavailable with the reason**, never as zero.

## Conventions (also printed in the JSON report)

* **Value** at the end of a day is cash, unsettled cash and dividend receivables in the base currency, plus
  every held security at the latest mark on or before that day.
* **Marks are raw closes as traded** (`-marks` takes the same CSV as the paper engine). They must not be
  adjusted for dividends or for later splits. The ledger's split transaction changes the quantity on the split
  day, so a raw series is continuous in value. `-adjusted` is refused: an adjusted close already contains the
  dividends and the ledger's income would then be counted a second time.
* **Distributions come only from the ledger.** On an ex-date the price falls by about the dividend and the
  income arrives: the return of that day is about zero, not a loss plus a gain.
* **Deposits, withdrawals and transfers are external flows** at the end of their day. They are never gain and
  never loss. A transferred security is a flow at its mark, and what it was carried at is shown separately.
* **A correction (reversal) takes effect on the day of the transaction it reverses**, so history is restated
  and no phantom flow appears on the day the correction was typed.
* **A held security with no mark on or before a day, or an amount in another currency with no rate, gives that day
  no value.** Anything that needs it is unavailable. A mark or rate older than `-max-age` days (default 5) is
  used, and listed as stale.

## Gain and its components

`gain = end value - start value - paid in + taken out`, split into realised gain, unrealised change, gross
income, withholding, trading costs and transfers at market. They add up to the gain: what is left over is shown
as **FX and translation** and is exactly zero for a book in one currency. Costs come from commission and fee
entries; the report does not know about costs hidden in a spread.

## Returns

* **Time-weighted:** `(V_d - F_d) / V_(d-1) - 1` chained daily, with the flows of a day at its end, so timing of
  deposits does not matter. A day that starts with no capital is skipped and said so; a day that starts with
  negative value makes it unavailable; so does a return below -100%. Annualised only for 365 days or more.
* **Money-weighted (IRR):** the annual rate on Actual/365 that makes the flows, the starting value and the
  ending value net to zero. The whole range from -99.99% is searched and **every** root is reported. Several roots
  means none is the answer and the report says so; no sign change in the flows means no rate exists (total loss
  is named as such). Over less than a year the annual rate is an extrapolation, and the period-equivalent is shown.
* **Drawdown** is on the time-weighted index, so a large deposit cannot hide a fall or fake a recovery. The
  peak, trough and recovery day are given.

Rates are floating point fractions (0.05 is 5%). Amounts are exact minor units.

## Baselines with the same cash flows

`-benchmark` files replay the book's starting value and every external flow, on the same days, into:

```json
{"name": "world fund", "kind": "fund", "security": "fund_x", "currency": "EUR",
 "trade_cost": "0.001", "cost_source": "my broker's schedule",
 "distributions": [{"ex_day": "2026-06-18", "per_unit": "0.42"}]}
{"name": "cash", "kind": "cash", "annual_rate": "0.02", "rate_source": "my savings account"}
```

* A fund buys units at the mark on or before each flow day, pays the stated cost on each flow, sells units for
  withdrawals, and reinvests distributions gross at the ex-day mark on the units held before that day. The marks
  of the fund come from the same `-marks` file and must be raw.
* A cost that is not stated is **not assumed zero**: the result says it is gross of costs. An unsourced cost or
  rate is refused.
* A benchmark in another currency is converted with the same rates; with none, it is unavailable. A missing price
  is unavailable and names the day.
* The result shows the end value, the gain, the IRR of the same flows, the TWR of the benchmark, and the book's
  end value less the benchmark's. That difference is one period of one comparison, not skill.

## What is not covered

* Marks and rates come from files you give it. They are not read from the stored quote or candle stores, and
  nothing here says where a price came from.
* Withholding on a benchmark's distributions is not assumed, and tax is not modelled.
* Corporate actions other than splits are not modelled; the ledger must carry them.
* Which benchmark is suitable, and the criteria for judging a strategy against it, are yours to approve.
