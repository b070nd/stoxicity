# The local paper engine

`stoxicity paper` lets you practice the whole workflow (plan, order, fill, ledger, income) without a funded
broker account. It uses the **same contracts as any other book**: an order is drafted, confirmed and submitted
with `stoxicity order` on a `paper_local` book, and the engine then reports fills, expiries and
cancellations about it with the same `order report` path a broker or a statement would use. The ledger,
holds and invariants are the real ones (see [orders.md](orders.md)). It never talks to IBKR, and a paper
book cannot be mixed with a real one.

```text
paper run BOOK -bars bars.csv [-fees fees.json] [-seed N] [-participation 0.01] [-min-fill 0.25]
                [-settle-days 2] [-through DAY] [-json]
paper fills BOOK [-json]
paper reset BOOK-ID -name NEW
paper example
```

## What a fill means here

Daily bars cannot show what happened inside a day. The engine therefore leans **against** the order, and no
fill is ever guaranteed:

| Situation | Rule |
|---|---|
| Which bar | The first bar **after the day the order was acknowledged**. Nothing fills in the bar of submission (no same-bar fills). |
| Lifetime | DAY: what has not filled when that bar ends **expires**. |
| Buy limit | Fills only if the bar's **low is strictly below** the limit. A sell needs the **high strictly above**. |
| Touch | A day that only touches the limit is ambiguous (the order may have queued behind others): **no fill**, the order expires. |
| Price | **The limit, never better.** A gap through the limit gives no price improvement. |
| Size | At most `participation` of the day's volume (default 1%), times a seeded fraction between `min-fill` (default 0.25) and 1. Whole shares unless the order is fractional. No volume, no fill. |
| Partial fill | The part that fits fills; the rest of the DAY order expires. |
| Cancel | Asked for before the session starts: cancelled, no fill. Asked for during or after it: the session runs, then the rest is cancelled. |
| Costs | The fee schedule you give (`-fees`, same file as `allocate`, in the book's base currency), else the **fees stated on the order**, in proportion to the part filled, rounded up. Spread, taxes and market impact are **not** modelled. |
| Settlement | Cash settles `settle-days` weekdays after the fill (weekends only; exchange holidays are not known). |
| Missing days | The bars file has no calendar. If the next bar is more than five days after submission, nothing is decided: a missing day cannot be told from a holiday. |
| Bad bars | A high below the low, a price outside the day's range, a repeated day, a non-positive price: the file is refused. Bars must be **as traded**, not adjusted; splits and other corporate actions between submission and the session are **not** handled. |

A fill here is a **simulation**. It is practice for the workflow and the bookkeeping, not evidence of what a
market would have given, and it must not be used to claim a strategy's performance.

## Reproducible

The seed, participation, min-fill and settlement days are **stored with the book on the first run that
decides something** and cannot change afterwards (`paper.config` record). The draw depends only on the
seed, the order ID and the bar day, never on the wall clock, and fills have deterministic IDs
(`paper:ORDER:DAY`), so running the same bars again changes nothing and the same steps in another place give the same
fills. The engine only uses bars up to the day before today (`-through` cannot be later).

## Reset

`paper reset` takes the **exact book ID** (`bk_...`), refuses anything that is not a `paper_local` book
(a real account or a broker's paper account is never touched), **archives** the book with its journal, orders and fills
intact, and opens a new empty one. Orders still open in the archived book stay in its history.

## Not here

* Intraday bars, spreads, queue position, tax, FX conversion of a fill.
* Fills for orders the broker side would have received: that is US-017 to US-019.
* The bars come from a CSV you supply; the Polygon candle store is not bridged to it.
