# Public history for 12M and 10Y

The compact performance strips fetch their history from Yahoo Finance's public
chart endpoint. No API key, subscription or contact email is needed for this
history client. The existing daily-candle source remains independently configured.

## Fetch and inspect

```sh
stoxicity universe history AAPL MSFT NVDA
stoxicity universe history -max 100       # due stocks, priority names first
stoxicity universe history -max 0        # every due stock, until completion/throttle
stoxicity universe history -fill -max 100 # resume batches and wait through throttles
stoxicity universe history -force AAPL   # explicitly refresh a recent snapshot
stoxicity universe history -status
stoxicity universe history -status AAPL
```

Without explicit symbols, the command uses the stored quote universe. `-fill`
finishes when no due symbols remain to attempt; failed/unavailable symbols stay
visible in status. A blocked provider stops the command; throttles are respected.
The command resumes from durable checks after a restart. Explicit symbols can
be fetched before the reference universe has been downloaded.

## Automatic filling

Both `stoxicity run` and an open `stoxicity-tui` start a bounded history worker.
It prioritizes missing snapshots, with followed/listed/tagged/recent-news stocks
before the rest. Successful snapshots refresh after 24 hours; unavailable
symbols retry after seven days, other failures after an hour. A provider throttle
does not mark a stock as unavailable. SQLite leases coordinate simultaneous workers.

```yaml
company:
  history: yahoo
  history_per_cycle: 100
  history_every: 5m
```

`company.enabled: false`, `history: off`, or `history_per_cycle: 0` disables the
automatic worker. Manual fetching is available when history is enabled. Reads
remain usable during a backfill and good snapshots survive failed refreshes.

## What is fetched

One request per stock obtains monthly history from January of the year preceding
the earliest visible 10Y year, through now. That supplies the annual baseline,
year-end closes, the latest 12 months and their previous-month baseline. Monthly
timestamps identify **period starts** in the exchange's stated time zone; they
are not daily sessions. The latest duplicate current-month sample wins.

The charts use the provider's split-adjusted `quote.close`, not dividend-adjusted
`adjclose`. A refreshed snapshot replaces all its months atomically, avoiding
mixed pre/post-split bases. Truncated or older responses retain the previous
snapshot. Null periods are not interpolated, and listing age/source gaps remain
explicit. The first visible annual bar compares with the preceding year's close;
the current month/year are partial and marked `*`.

Base migration `0028_price_history.sql` stores provider/basis metadata, monthly
closes and resumable checks in separate history tables. These rows are never
inserted into `candle`: monthly OHLC is not a daily candle. The UI reads a bounded
set of monthly/yearly endpoints and keeps its existing short-lived view cache.

The public chart endpoint is unofficial and has no verified unlimited allowance.
The client identifies itself, paces requests, honors durable HTTP throttles and
configured `fetch.quotas`, and stops at access blocks. No paid fallback is used.
Coverage is per symbol, not a promise that every instrument has ten years of data.

## Source and verification

- Public endpoint: `https://query1.finance.yahoo.com/v8/finance/chart/<symbol>`
- [Yahoo adjusted-close explanation](https://help.yahoo.com/kb/SLN28256.html):
  adjusted close includes both applicable splits and dividends; it is distinct
  from the price-only close used here.
- 2026-11-08: the public AAPL monthly endpoint returned HTTP 200 with the
  application's self-identifying User-Agent and no credentials.
- Live CLI verification in an isolated scratch workspace fetched AAPL, MSFT and
  NVDA: 131 monthly closes each (393 total), zero failures, latest session
  2026-11-06. The production calendar reader/builder resolved all 12 monthly
  changes and all ten annual changes for each symbol. A second CLI invocation
  made zero requests, confirming durable freshness checks across restarts.
- Fixtures exercise identity/time-zone/null/duplicate handling, both chart
  baselines, coherent adjustment, durable leases/restarts, throttling, partial
  failure preservation and an end-to-end keyless fetch that leaves DAY unchanged.
