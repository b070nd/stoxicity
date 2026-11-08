# Dividend entitlement and income

Story US-008 of [the production plan](production-plan-mode.md). It answers one question: **given the
fills a book has recorded and the payouts that are declared, how much income is the book owed, and
does the ledger say the same?** It fetches nothing and places no order. The payouts are the
observations you imported with `dividends import`; the fills are the ledger. **Nothing here was
checked against a real broker or custodian statement**: `portfolio reconcile` compares a book with a CSV you write, and
`broker sync` compares it with a fixture broker (docs/broker.md); a real account has never been read.

## The entitlement rule

Stated once, in the package documentation, and tested:

- A share earns an **ordinary** payout when it was **held on its trade date before the ex-date**.
  A fill whose event date is the ex-date or later does not earn that payout.
- A **sale** on or after the ex-date does not remove the entitlement; a sale before it does.
- An **open order is not a holding**: a reservation moves no shares and earns nothing. A partly
  filled order earns on the filled part.
- A **split** after the ex-date does not change what was held at it; one before it does.
- The gross amount is shares × per-share amount, **rounded once, half-even, on the total**.
- Only a **declared ordinary** payout is computed. An *estimated* one is a forecast (no income), a
  *cancelled* one is none, a *conflicting* one (sources disagree) needs a decision, and *special*,
  *return of capital* and *unknown* payouts are reported as **unsupported**: they can carry due-bill
  or other terms this program does not model, so book them by hand when the broker pays.

Event time is what counts, not the order of recording, so a late statement line is judged by the day
the trade happened.

## Three lines, one income

Income is booked **once** per payout, whichever way the money arrives. The payout key
(`security|type|period`, for example `sec_apple|ordinary|2026Q4`) is written on each line and the
ledger refuses a second booking:

| Phase | Entries | When |
|---|---|---|
| accrue | receivable +gross, income −gross | on the ex-date, from the entitlement |
| pay | cash +(gross − withheld), receivable −gross, withholding +withheld | when the money arrives |
| reinvest (DRIP) | new lot at gross − withheld, receivable −gross, withholding +withheld | when it arrives as shares |
| direct | cash, withholding, income | a payout that was never accrued |

A *pay* or *reinvest* line must clear exactly the accrued receivable and book no income; a direct line
must not be used on an accrued payout. An accrual cannot be reversed while it is paid (reverse the
payment first). Withholding is **only what you record**; nothing is estimated and booked.

## Commands

```bash
stoxicity income expected BOOK [-as-of DAY] [-json]     # what each known payout is worth to this book
stoxicity income accrue BOOK [-as-of DAY] [-payout KEY] # book every entitled payout past its ex-date, once
stoxicity income receive BOOK -payout KEY -date D [-withheld "15.00 USD"] [-import-id ID]
stoxicity income receive BOOK -payout KEY -date D -security S -qty Q      # reinvested
stoxicity income receive BOOK -payout KEY -date D -gross "100.00 USD"     # never accrued
stoxicity income check BOOK [-as-of DAY] [-json]       # exit 1 when the ledger and the entitlement differ
```

`accrue` is idempotent (import ID `accrual:<key>`), `receive` is idempotent by `-import-id`, and a
repeat with different content is refused.

## What `check` reports

| Finding | Meaning |
|---|---|
| missing accrual | entitled and past the ex-date, nothing booked |
| accrual differs | booked, but a corrected fill changed the shares held before the ex-date |
| accrual unbacked | booked for a payout the book is no longer (or not yet) entitled to |
| payment overdue | accrued, the pay date has passed and no payment is recorded |
| unknown payout | booked for a payout that is not among the observations |

Nothing is corrected automatically. The answer to a finding is a recorded **reversal** of the wrong
line and a new one, as for any other correction (see [portfolio.md](portfolio.md)).

## Gross and net

Every figure is **gross** unless it says otherwise. `income.OutlookFor` returns the tax basis with the
number: *unknown* (no net is shown), *actual* (what was withheld on the payment) or *assumed* (a rate you
supply, labelled a scenario that is never booked). It is not a tax estimate for any jurisdiction.

## Not done here

- Due-bills, special and return-of-capital payouts, scrip dividends, spin-offs and other corporate actions.
- Tax residency, treaty rates, reclaims and reporting.
- Reading a broker's dividend records (US-017).
