# Broker connection

The program can read from an Interactive Brokers account and compare what the
broker says with what the ledger says. It is **read-only by default**, it **never
logs in**, and the default is **offline**: with no broker configured nothing is
read from a broker and nothing is sent.

## What is verified, and what is not

| What | State |
| --- | --- |
| Adapter against recorded contract fixtures (`src/broker/ibkr/ibkr_test.go`) | verified: session expiry, competing session, pacing with `Retry-After`, maintenance, missing entitlement, malformed answers, paging |
| Reconciliation against a fixture broker kept in a file | verified |
| Any request to a real Client Portal Gateway | **not made** |
| Any real account, paper or live | **not touched** |

The request and answer shapes in the adapter were written from the gateway's
published endpoint list as recalled when it was written. They were **not**
re-checked against the current documentation or a running gateway. Before a real
account is connected, someone must run `broker status` and `broker sync` against
a **paper** account and compare, field by field, with the list below. The
readiness report keeps the account and source dimensions at "not applicable"
until that has been done and recorded.

Assumptions in `src/broker/ibkr/ibkr.go`:

- `GET /v1/api/iserver/auth/status` returns `authenticated`, `connected`, `competing`, `message`.
- `GET /v1/api/portfolio/accounts` must be called before the other portfolio endpoints; it returns `id`/`accountId`, `accountTitle`, `type`.
- `GET /v1/api/portfolio/{account}/ledger` returns one object per currency with `currency`, `cashbalance`, `settledcash` as decimal numbers in major units, plus a `BASE` line that repeats them converted (ignored).
- `GET /v1/api/portfolio/{account}/positions/{page}` returns up to 100 positions per page with `conid`, `ticker`, `position`, `currency`.
- `GET /v1/api/iserver/account/orders?force=true` returns `{orders: [...]}` with `orderId`, `order_ref` (the client reference), `conid`, `side`, `totalSize`, `filledQuantity`, `price`, `status`.
- `GET /v1/api/iserver/account/trades` returns executions with `execution_id`, `order_id`, `order_ref`, `conid`, `side`, `size`, `price`, `commission`, `currency`, `trade_time_r` (milliseconds).
- `POST /v1/api/iserver/secdef/search`, `GET /v1/api/iserver/contract/{conid}/info`, `GET /v1/api/iserver/marketdata/snapshot` (fields 31 last, 84 bid, 86 ask, 6509 availability; the first request only starts the feed, so it is repeated once).
- Paper accounts are recognised by a `DU` prefix. That is a guess about the broker's numbering. It is used only to **refuse** a mismatch between a book's environment and an account, never to allow one.

## Safety properties

- The gateway address must be `https` on a loopback address (`localhost`, `127.0.0.1`, `::1`). Anything else is refused when configured.
- The gateway's certificate is self-signed, so it is trusted explicitly with `-ca FILE`. Certificate verification is never switched off; without the file a gateway is "unavailable", not trusted.
- Account numbers are redacted (`****567`) in every status line and error. Credentials are never stored, logged or requested: the program has none.
- A failure is classified: `session_expired`, `competing_session`, `pacing`, `maintenance`, `entitlement`, `refused`, `unavailable`, `malformed`. Pacing and expiry are reported with what to do; nothing is retried blindly.
- A book of `paper_broker` can only be bound to an account that reports itself as paper, a `real` book only to one that does not. The check is repeated on **every** use, because the broker's answer can change.
- One broker account has one book.

## Commands

```bash
stoxicity broker fixture example            # a broker kept in a file, for practice and tests
stoxicity broker config set -transport fixture -fixture FILE -by NAME
stoxicity broker config set -transport ibkr -url https://localhost:5000 -ca gateway.pem -by NAME
stoxicity broker status                     # session, accounts (redacted), mode, verification status
stoxicity broker bind main/paper -account DU1234567 -by NAME
stoxicity broker contracts AAPL             # contracts the broker knows; which are in the instrument register
stoxicity broker sync main/paper            # read balances, positions, orders, executions; compare; record
stoxicity broker differences [BOOK] [-all]
```

## Reconciliation

`broker sync` reads the account and compares it with the book. It applies only
what the broker states about **orders this program sent** (matched by the client
reference, which is the local order ID): an acknowledgement, an execution, a
cancellation, a rejection. These go through the same reports as any other source
and are keyed by the broker's own IDs (`ibkr:<execution_id>`), so running the
sync again changes nothing.

Everything else is a **difference**, recorded and shown with what to do, never
overwritten on either side:

| Difference | Meaning |
| --- | --- |
| `cash_mismatch` | the ledger and the broker disagree on total cash in a currency (settled and unsettled together: settlement timing differs for a day or two after each trade) |
| `position_mismatch` | shares held differ for a security |
| `unmapped_position` | the broker holds a contract the instrument register does not know |
| `foreign_order`, `foreign_execution` | the broker has an order or execution that this program did not send |
| `missing_at_broker` | a sent order is not listed by the broker |
| `order_terms_differ`, `execution_terms_differ`, `filled_mismatch` | the broker holds something other than what was confirmed |
| `unmapped_execution` | an execution of one of our orders for a contract the register cannot place |

Contracts are matched through the instrument register
(`contracts: [{broker: ibkr, conid: "265598"}]` on a listing), never by symbol.

Any open difference raises the `reconciliation_failed` signal, which suspends
**new risk** (see [tiers.md](tiers.md)). Holdings and open orders are untouched.
Clearing the signal is a person's act (`tier signal clear`), after the
differences are explained. `maintenance` and `malformed` answers raise
`broker_unhealthy`; pacing and an expired or competing session are reported and
do not.

## Preview, confirm and submit

Sending an order is a chain of separate, recorded steps, and every step can say no.

```bash
stoxicity broker limits show|set|review     # quote age, collar, spread, preview validity
stoxicity broker preview ORDER              # the checks and the broker's own preview; records them
stoxicity order confirm ORDER -by NAME      # bound to the previewed payload; broker warnings need -ack "broker: ..."
stoxicity broker mode trading -by NAME      # the explicit choice to allow sending
stoxicity order control enable -env paper_broker -by NAME -reason TEXT
stoxicity order submit ORDER -by NAME
```

`broker preview` checks, and shows each result:

| Check | Fails when |
| --- | --- |
| contract | the security has no (or more than one) broker contract in the register, or the contract's currency is not the order's |
| account | the account is not visible, or paper/live does not match the book's environment |
| policy | the policy that governed the draft is no longer the active one |
| plan | the plan is no longer accepted, has expired, or an input other than the book changed since it was generated |
| hold | the order's hold is gone |
| funds / shares | settled cash at the broker is below what the order can use, or the broker holds fewer shares than a sell |
| quote, quote real time, quote age | there is no execution-grade quote: none available (no subscription), delayed or frozen, or older than the limit |
| spread, collar | the market is not two-sided, too wide, or the limit reaches further past it than the collar allows |
| preview | the broker refuses the order or its preview |

If any check fails the order **cannot be confirmed for the broker**, and a quote
that is missing is a reason, not a guess: submission is unavailable until an
execution-grade quote exists.

`order confirm` repeats the checks, then binds the confirmation to a **payload
hash**: the exact request (account, contract, side, quantity, limit, TIF,
client reference), the account's description, the contract, and the broker's
warnings and commission. The broker's warnings must be acknowledged by exact text
(`-ack "broker: ..."`). The confirmation lasts at most the preview validity
(five minutes unless changed). `-ticket` confirms for placing by hand instead,
which can never be sent from here.

`order submit` runs the whole check again against the broker as it is **now**,
recomputes the payload, and only then records `submit_started`. It refuses, without
recording anything as sent, when:

- the connection is `read_only` (the default), or the limits are unreviewed;
- the tier is below 2, new risk is suspended, or new orders are disabled for the environment;
- the confirmation expired, or the payload is not the one confirmed (the broker's warnings, the account, the contract or the terms changed);
- any check above fails.

The intent is durable **before** the broker is called, and an order that was
sent once is never sent again, by this command or after a restart. If the answer
is lost the order is `unknown`: it may be working at the broker. `broker sync`
finds it by its client reference (the local order ID) and acknowledges it; an
order the broker does not list needs `order resolve` after you have looked.

When the broker answers with a question instead of placing the order, the
program answers it only if the question is one of the warnings you acknowledged,
word for word. Any other question leaves the order unknown and **unanswered**.
A definite refusal from the broker rejects the order and releases its hold.

Default, offline and test use never send: with no broker configured, or in
`read_only`, nothing leaves this machine; the fixture broker is a file.

## Monitoring, cancel and replace

- **Partial fills, fills before the acknowledgement, fills during a cancellation, duplicate messages.** `broker sync` applies each execution by its ID (`ibkr:<execution_id>`) through the order engine, which accepts facts in any order and any number of times. Each partial fill consumes the hold and holds the rest; a fill that arrives after a cancel request is kept and noted.
- **Fee and execution corrections.** If the broker later reports an execution already booked with a different quantity, price or commission, the booked fill is **not** changed. It becomes an `execution_corrected` difference (and suspends new risk) until a person reads the statement and corrects the ledger by reversal and re-entry.
- **Cancel.** `order cancel` asks the broker only in `trading` mode, and an order is not cancelled until the broker reports it cancelled. Until then it is `cancel_requested`, its hold stays and a fill may still arrive. When the cancellation is reported, only the part of the hold that was not used is released.
- **Replace.** There is **no replace command**. Changing an order means cancelling it and drafting a new one, which needs a new preview, a new confirmation and new checks, so a replacement can never increase risk without a fresh confirmation.
- **Restart.** `order recover` marks an order caught mid-submission as unknown and switches the broker environments' new orders off; `broker sync` then reconciles against the broker. Re-enabling new orders is a manual act.
- **Disabling.** `order control disable` stops new orders for an environment at any time; cancelling and selling are never blocked by it.

Verified against fixtures: lost answers, refusals, unanswered questions, stale,
delayed and missing quotes, changed policy and funds, tier gating, duplicate and
corrected executions, partial fills during a cancel. Not verified: any of this
against a real gateway or account.
