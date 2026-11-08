# Orders: intent, hold, confirmation and state

`stoxicity order` keeps the **local record of an order**. It does not talk to a broker. The only sender
that exists is the local paper engine's acknowledgement; a `real` or `paper_broker` book can be drafted,
confirmed and tracked, but `order submit` refuses with the reason, because no broker adapter is connected
(IBKR comes with US-017 to US-019). You place such an order yourself and record what happened with
`order report`.

## The order is a log

An order is an append-only list of events (`order_event` in the tenant database). Its **state is derived
by folding the log**, never stored, so the same facts told in a different order, or twice, give the same
state. States: `draft`, `confirmed`, `submitting`, `open`, `partially_filled`, `filled`,
`cancel_requested`, `cancelled`, `rejected`, `expired`, `unknown`.

## Draft: the intent and the hold are one write

`order draft` writes the intent **and** the hold on the cash (a buy: quantity times limit rounded up,
plus the known fees) or the shares (a sell) in **one database transaction**, with the ledger's own
reservation. Two proposals cannot spend the same money: the second is refused by the ledger, whether it
comes from the same process or another one (writes are serialized by the database). A refused draft
leaves nothing behind.

First live subset: **limit, DAY, long-only**, whole shares unless you state the account trades part
shares, a price that is a multiple of the tick when you give one. Anything else is refused.

Shared account limits, from the active policy and counted across **all books of the account**: orders
open at once (not set by the policy; the service takes it as a parameter), orders per month
(`max_trades_per_month`), and known fees as a fraction of the order value (`max_cost_burden`).
An identical open order is refused as a duplicate.

`-plan PLAN.rN` takes the security, side and quantity from an **accepted, unexpired, not stale** plan
revision. The plan's entry ceiling bounds the limit and its quantity bounds the order. Drafting holds cash,
which changes the book, so that plan revision is then stale: another order needs a fresh plan.
Plans size with a currency conversion; an order holds cash in **its own currency**, so a USD order needs USD cash.

## Confirm: bound to the exact terms

`order confirm` records a go-ahead bound to a hash of the exact terms (book, account, environment, security,
side, quantity, limit, currency, fees, plan revision, policy epoch and hash) **and the hold**. It expires.
Every warning on the ticket must be acknowledged by its exact text (`-ack`); nothing is acknowledged
automatically. Changing anything needs a new draft and a new confirmation.

## Submit: durable before external

`order submit` writes `submit_started` (durable, its own transaction), **then** calls the sender, then
writes the answer in a second transaction. If the process ends in between, the order is `submitting`;
`order recover` turns that into `unknown` and it is **never sent again**: an order that was sent once is
reconciled, not resent. A sender error or timeout is `unknown`, not a rejection. A local transaction does
not give exactly-once execution at a broker; what it gives is that nothing is sent without a durable record
and nothing is sent twice from here.

New orders are **off by default** for `real` and `paper_broker` (`order control show|enable|disable`); a
local paper book has no broker to protect. `order recover` switches the broker environments off again,
the restart rule of the plan. Cancelling and reporting stay possible whatever the switch says.

## Facts in any order

`order report -kind ack|fill|cancelled|rejected|expired` records what the broker, the paper engine or your
statement says. Each fact is idempotent by its key (an execution ID for fills).

* A **fill** is posted to the ledger. It consumes the hold and holds only what is left (a new hold ID each
  time; IDs are never reused). Fees already paid reduce what is still held for fees.
* A fill before the acknowledgement, after the cancel request, or after a cancellation is recorded as
  **out of order** and still counts: it happened.
* More shares than were ordered, a fill for an order never sent from here, a fill the ledger cannot take,
  or a rejection of an order that also filled make the order `unknown` until a person compares it with the
  other side and runs `order resolve`. The facts are kept; nothing is invented.
* A cancel request is not a cancellation. The hold stays until a cancellation is reported; **only unused
  holds are released**.

## Invariants the tests hold

The ledger's holds are exactly the holds the live orders say they have; a closed order holds nothing;
available cash never goes negative; the trial balance is zero; the journal verifies. This is checked after
concurrent drafts from eight handles on one file, concurrent reports and cancels, a crash between intent
and answer, a cancel race, partial fills and overfills (`app/src/order/service_test.go`).

## Not here yet

* Replace (cancel and draft again with a fresh confirmation is the way for now).
* Broker preflight: current positions, permissions, collar and quote freshness. The ticket says so as a warning.
* The tier gate (US-016) and the real sender (US-017 to US-019).
* Enabling new orders after a restart is a human act; tying it to a successful broker reconciliation comes with the adapter.
