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

## How the caps are measured

Your answer to the caps question replaced the starter rules. Four different things are measured and none stands in for another:

- **Company cap** (`max_issuer`): one operating company, counting its shares in every class and listing and, for funds whose dated holdings you supplied (`look_through` in the fund facts, no older than the staleness limit), the company's weight inside them. A fund that gives no usable holdings is counted as itself, and the plan says the coverage is unknown rather than guessing.
- **Single-fund cap** (`max_fund`): one underlying fund, all of its share classes and listings together (the facts name the fund with `portfolio`). A broad core fund is held to this cap and not to the company cap, so a 10% company cap does not limit a diversified core ETF to 10%.
- **Provider concentration** (`max_provider`, optional): the funds of one provider together. It is always reported; it limits a purchase only when you set it (zero means no cap). It must be at least the single-fund cap.
- **Sector cap** (`max_sector`): against total portfolio equity, meaning cash plus holdings of the defined investment portfolio (money outside it is not in the book). The share of the invested holdings is shown beside it so cash cannot hide a concentrated equity allocation: with 1,000 in total, 800 cash and 200 technology, technology is 20% of equity and 100% of the invested holdings.

Every exposure is measured after the trade, counting open buy orders that have not filled yet, and a basket is checked one line at a time so that two purchases cannot each fit under a cap they share. The plan prints an exposure table (before, after, cap, and what is unknown).

## Decisions to confirm

- Fees come from a schedule you enter; the policy's `commission_schedule` is free text and is not parsed.

## Not done here

- Selling or rebalancing (US-013), the immutable plan snapshot that stores a result (US-012).
- Tax effects, trade-time price movement and liquidity beyond the minimum position.
- Look-through of a fund's holdings into sector and country caps: only the company cap uses look-through. Sector and country are counted for the fund as itself, and an unknown look-through is reported as unknown.
