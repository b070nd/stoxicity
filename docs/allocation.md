# The portfolio and contribution allocator

Story US-011 of [the production plan](production-plan-mode.md). It turns the strategy's
proposals ([strategy.md](strategy.md)) into **quantities** under your policy limits and a fee
schedule you supply. It places nothing, proposes **no sale**, and a stored result is not an
authorization. Nothing here was checked against a broker's real fees or lot sizes.

```bash
stoxicity allocate example
stoxicity allocate plan BOOK -candidates cands.csv -fees fees.json [-as-of D] [-contribution "50.00 EUR"] [-rates USD=0.92] [-compare] [-json]
```

## Inputs

| Input | From | Notes |
|---|---|---|
| Limits | the active policy | issuer, sector, fund, foreign-currency and country caps, cash reserve, minimum position, maximum cost burden, excluded sectors and issuers. No active policy: the command refuses, because the limits would be guessed |
| Cash and holdings | the book's ledger | cash is settled cash plus unsettled trades less open reservations. A holding with no current price is listed but **not counted**, and the plan says so |
| Candidates | a CSV you supply | `security` (required) and optional `mic`, `ticker`, `sector`, `price` ("22.50 EUR") with `price_day`, `fractional`, `step`, `min_qty`, `tradable`, `facts` (a fund's facts JSON). The register has no sector, so you give it. A price is the one you supply or the stored close for `mic`+`ticker` |
| Fees | a JSON schedule with its **source** | rate, minimum, fixed fee, FX fee, all in the base currency. An unsourced schedule is refused. No schedule: the plan **waits** and says trading costs are unknown, it does not assume zero |
| Rates | `-rates USD=0.92` | base units per one unit of the currency. A foreign-currency line without a rate cannot be sized |

A stock is a candidate only if the strategy proposes BUY or ADD for it; candidates are ranked by
that score (a policy ordering, not a probability). A fund is a candidate only if its documented
facts pass the broad core-fund checks; a fund is never compared with a company by number.

## What it does

- Sizes the largest basket that fits, then fewer positions, until the quantities satisfy every
  limit **after** costs: whole shares unless fractional trading is stated, the lot step and
  minimum quantity, the minimum position, the cost-burden limit, and the cash reserve.
- Never exceeds a cap by rounding: a quantity is the largest that stays inside every limit, and the
  note names the binding one.
- Counts what you already hold against the same caps. If you already breach a cap, the plan lists
  the breach and buys nothing that makes it worse. It never proposes selling to fix it.
- Says **WAIT** when no purchase is affordable under the cost limit, with the spendable cash at
  which the best candidate would be, and how many of your stated contributions that takes.
- Says **NO FEASIBLE PURCHASE** when nothing qualifies, with the reason per candidate. Cash is a
  valid outcome.
- `-compare` plans a diversified fund, individual stocks and holding cash side by side. The options
  are **not ranked**: they differ in cost, concentration and unknowns.

Everything is whole minor units of the base currency, rounded half-even once, so the figures add up.

## Decisions to confirm

- The sector cap is measured against investable equity (all cash plus holdings), not only direct
  holdings; the policy comment says "of direct holdings".
- The issuer cap applies to a fund's issuer too (one fund provider's funds count together).
- Fees come from a schedule you enter; the policy's `commission_schedule` is free text and is not parsed.

## Not done here

- Selling or rebalancing (US-013), the immutable plan snapshot that stores a result (US-012).
- Tax effects, trade-time price movement and liquidity beyond the minimum position.
- Look-through of a fund's holdings into sector and country caps: a fund is counted as itself, and an
  unknown look-through is reported as unknown.
