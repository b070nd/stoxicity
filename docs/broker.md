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
| `cash_mismatch`, `settled_cash_mismatch` | the ledger and the broker disagree on cash in a currency |
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
