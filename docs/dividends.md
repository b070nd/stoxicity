# Dividends and fund distributions

Story US-006 of [the production plan](production-plan-mode.md). Nothing here places an order,
reads a broker or fetches a data vendor. **Nothing here is cash**: a declared payout is the
issuer's promise per share, an estimate is a guess, and cash exists only when a ledger records it
arriving (a later story). No command turns one into the other.

## Observations

One row is one source's statement about one payout of one share class. Rows are never changed or
deleted (database triggers refuse it); a cut, a cancellation or a moved ex-date is a new row from the
same source, so what was known at a past time is readable with `-at`.

| Field | Meaning |
|---|---|
| security | the share class (not the listing): an accumulating and a distributing class are two securities |
| type | `ordinary`, `special`, `return_of_capital` or `unknown`. A source that does not say is `unknown`, never ordinary; unknown payouts stay out of the run-rate |
| period | what the payout is for: `2026`, `2026H1`, `2026Q3` or `2026-11`. Type plus period identifies the payout and gives the same period a year earlier to compare with |
| status | `declared` (the issuer announced it), `estimated` (a forecast) or `cancelled` |
| amount, currency | per share, exact, in the major unit of the stated currency. The currency is always stated by the row, never taken from the listing (a fund can pay in a currency other than the one it trades in) |
| ex_day, record_day, pay_day, declared_day | dates; only the ex-date is required. The order record >= ex and pay >= record is checked |
| source, source_ref, received | who said it, a file hash or URL, and when this system stored it |

## Commands

```bash
stoxicity dividends import -file payouts.csv -source "company site"
   # type,period,status,amount,currency,ex_day  + isin  or  mic,ticker  [+ record_day,pay_day,declared_day]
stoxicity dividends calendar [-ticker T -mic M | -isin I] [-from D] [-to D] [-at TIME] [-splits FILE]
stoxicity dividends quality  (-ticker T -mic M | -isin I) -day D [-splits FILE] [-sector S] [-fcf-per-share X -fcf-currency C]
```

Import is all-or-nothing. The security is resolved through the instrument registry on the ex-date. **A row for a
class the instrument record says accumulates is refused**: the file or the record is wrong, and silence would hide it.

## How sources are read together

Each source's newest observation of a payout stands for it. Then:

- agreeing declared observations make a **declared** payout, and a declaration beats estimates of it;
- disagreement on ex-date, amount, currency, record or pay date makes it **conflicting** and nobody picks a winner;
  a cancellation beside a declaration from another source is also conflicting;
- a cancellation with no declaration makes it **cancelled** (estimates of it are ignored);
- estimates alone make an **estimated** payout (conflicting if they disagree) that says it is not cash.

The same source revising its own number is a revision, not a conflict.

## Splits

Per-share amounts before a split are per old share. With `-splits FILE` (`security,day,from,to`, the day being the first
day at the new price) amounts with an earlier ex-date are divided by the split ratio, exactly where possible and rounded
half-even otherwise, and the payout says it was restated. Without it, a "cut" may be a split and the report says so.
There is no split feed behind this: the file is yours.

## Calendar

Payouts by ex-date with per-share totals per currency and type. Declared and estimated are separate columns and are never
added; conflicting and cancelled payouts are listed and counted but excluded from totals; currencies are never converted.

## Quality

For one security as of a day, from declared payouts only:

- **coverage**: `observed`; `none_by_design` (the instrument record says the class accumulates, so no cash distribution
  is expected); or `unknown` (nothing on record, which is not evidence the security pays nothing);
- **frequency**: what the sources' periods say, not guessed from dates;
- ordinary and special payouts of the last 12 months, apart, per currency;
- the latest payout against the same period a year earlier (cut, flat, increase, ratio), the cuts of the last 24 months,
  cancellations, and periods missing from inside the series (a disputed period is present, not missing);
- **free cash flow cover**: trailing free cash flow per share over the declared ordinary payout, when you supply both and the
  sector is one the measure suits. Banks, insurers, financials, REITs, business development companies and partnerships are
  `unsupported_sector`; funds are unsupported; an empty or unrecognised sector is unknown, not assumed fine. A currency
  mismatch is unknown until you convert with a stated rate.

Estimates are listed apart and are in no figure; an estimate whose ex-date passed undeclared is called overdue.
Everything the analysis cannot know is listed under "unknown or unreliable".

## What this does not do

No source supplies payouts: coverage is whatever you import. Sector and free cash flow are typed by you. Payout ratios from
reported earnings, dividend safety scores and forecasts are not computed. Receivables, withholding and reinvestment arrive
with the ledger stories.
