# Exchange rates and prices

Story US-005 of [the production plan](production-plan-mode.md). Nothing here places an
order or reads a broker. Values are exact decimals; no float is stored.

## Observations

Every value is one row that is never changed or deleted (database triggers refuse it). A
correction is a new row, so what was known at a past time can be read back with `-at`.

| | Exchange rate | Price |
|---|---|---|
| says | 1 base = value quote | value in a quote unit (`GBp` pence, `GBP` pounds) |
| for | a day (`day`) | a listing and a day |
| kind | `reference` (ECB-style, for reporting), `fill` (a rate a broker gave, must point at its statement) or `user` (typed assumption) | close or last; adjustment `raw`, `split_adjusted`, `total_return` or `unknown`; delay `eod`, `realtime`, `delayed` or `unknown` |
| unit | units per 1 EUR for the ECB | `stated` by the source, or `listing` (the source was silent; the instrument record's unit was used and is marked) |
| provenance | source, source reference (URL or file hash), time received | same |

`received` is when this system stored the value, not when the source published it. Nothing here
claims a publication time.

## Commands

```bash
stoxicity fx fetch                       # ECB daily file; -feed 90d | history for more
stoxicity fx import -file rates.csv -source "my notes"       # base,quote,day,rate[,basis,ref]
stoxicity fx list
stoxicity fx rate -from EUR -to USD -day 2026-11-06 [-max-age 3] [-at TIME] [-basis reference]

stoxicity prices import -file prices.csv -source "broker export"   # mic,ticker,day,price[,unit,kind,adjustment,delay]
stoxicity prices at -mic XLON -ticker BA -day 2026-11-06 [-max-age 4] [-adjustment raw] [-require-stated-unit]
```

Imports are all-or-nothing: every row is checked and the problems listed together, and any problem stores
nothing. Re-importing the same file adds nothing; a changed value from the same source is a revision.
A price row resolves its listing through the instrument registry on that day, so an ambiguous or unknown
instrument is a row error. An import file with no `adjustment` stores `unknown`, never `raw`: a file that
does not say is not trusted to be unadjusted. A rate with no `basis` is a `user` assumption.

## When a value is refused

`fx rate` and `prices at` answer `ok` or one of these, and exit non-zero otherwise. Nothing is repaired.

- **missing**: nothing of that kind is dated on or before the day and received by `-at`. Values of another basis
  or adjustment may exist; they are counted but never substituted: a reference rate is not answered with a fill, a
  split-adjusted price is not answered with a raw one.
- **stale**: the newest value is older than `-max-age` calendar days. The default is 0 (dated that day). Weekends and
  holidays are not guessed; the caller decides how old is too old.
- **conflicting**: after taking the newest revision of each source for the newest day, two sources disagree. The
  same source correcting itself is a revision, not a conflict.
- **unit_ambiguous** (prices): a price states a unit other than the listing's (12.345 `GBP` for a `GBp` line is the
  factor-of-100 error), or, with `-require-stated-unit`, the source did not state the unit. Actions that size money
  should set it.

A rate is used in the direction asked or inverted from the opposite pair (12 digits, half-even, and the answer says
so). A cross rate through a third currency is never derived. A reference rate is for reporting; the answer says it
is not a rate anything was or can be traded at, and `Basis.Executable()` is true only for a fill.

## Sources and what is verified

| What | Path | State |
|---|---|---|
| ECB euro reference rates (free, no key) | `fx fetch` | read by fixture tests only. `quotes/live_test.go` fetches the real file; it is opt-in (`STOXICITY_LIVE_CONTRACT=1 STOXICITY_CONTACT=you@example.com go test ./src/quotes -run Live -v`) and **has not been run**, so the endpoint is not contract-tested |
| Prices and rates from your own files | `prices import`, `fx import` | wired; the file is as good as its author |
| US end-of-day candles (Polygon / Massive, free key) | `universe quotes` | a separate, older store of floats that does not state a currency. It is not converted into price observations, so it is not part of this path |
| European prices, live prices | none | no automatic coverage: import only, and the capability registry says so |
| Broker data (IBKR) | none | arrives with a later story |

Currencies the ECB publishes that this system has no minor-unit table for (for example IDR) are skipped and counted,
not guessed. Valuation does not read these observations yet; it still rejects foreign-currency quotes. That joins up
in later stories.
