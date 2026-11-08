# Accounts and the portfolio ledger

Story US-007 of [the production plan](production-plan-mode.md). Nothing here places an order,
reads a broker or fetches a price. The ledger holds what you enter or import and adds it up;
every figure it shows is a sum of recorded lines. **Nothing in it was checked against a real
account**: reconciling a book with a broker is `portfolio reconcile` against a file you supply,
and a connection to the broker comes with US-017.

## Books

A tenant owns accounts; an account holds **books**, and each book is exactly one environment:

| Environment | Meaning |
|---|---|
| `real` | what you own. Never archived, reset or deleted by this program |
| `paper_local` | simulated here |
| `paper_broker` | simulated at a broker (later stories) |

There is no default environment: `portfolio book create` refuses to guess, so a real book is
never an accident. A book also fixes its reporting currency and its lot method (`fifo` or
`lifo`), and whether settled cash may go below zero (`-allow-negative-cash`, off by default).

Isolation is built in three places, not one:

- **Tenant.** Each tenant has a database file of its own, and every book also records its tenant,
  so a file copied to another tenant shows no books. The book ID is derived from tenant, account
  and name, so two tenants' books of the same name have different IDs and different transaction
  IDs.
- **Environment.** Every journal row carries its book's environment, and a foreign key ties the
  two together: the database refuses a paper transaction in a real book and the reverse. Cash and
  shares only move between books of the same environment; real and paper never exchange anything.
- **Reset.** A paper book is "reset" by archiving it and opening a **new** book. The old journal is
  untouched and takes no more lines; the new book shares nothing with it. A trigger refuses to
  archive a real book by any route, and refuses any change to a book other than archiving a paper
  one.

## The journal

Transactions are immutable, double-entry and signed: a debit (assets, expenses) is positive, a
credit (income, equity, gains) negative. **Every transaction sums to zero in every currency on
its own.** A currency conversion therefore goes through an `fx:clearing` account with a leg in
each currency, and the native amount of each leg is kept. Entries post only to accounts the
transaction's kind may use: a deposit cannot book income.

| Account | Holds |
|---|---|
| `cash` | settled cash, per currency |
| `unsettled` | trades not yet settled: negative is owed, positive is due |
| `securities:<id>` | shares, with the cost carried by each lot |
| `income:dividend/interest/other`, `expense:withholding` | income at gross, tax withheld at source |
| `expense:commission`, `expense:fee` | costs, never added to cost basis or taken from proceeds |
| `gain:realized` | gain on sales (a credit) |
| `equity:contributions`, `equity:transfers`, `fx:clearing` | the other side of deposits, transfers and conversions |

Each transaction records **event time** (when it happened) and **recorded time** (when it was
written; never earlier than the book's previous line), a source, an optional import ID and a
memo. Replay is in the order of recording, never of event time, so a statement line that arrives
late cannot change the cost of an earlier sale. Reading a book "as of" a moment works on either
clock: `-basis event` is what had happened by then, `-basis recorded` what was known by then.

The kinds: deposit, withdrawal, buy, sell, fee, commission, income (dividend, interest, other),
reinvestment (DRIP), FX, split, settle, transfer out/in, reversal, and the order holds reserve and
release. Fractions are exact to eight digits; money is whole minor units.

### Lots, cost and the unsettled cycle

- A buy opens a **lot** with its own acquisition date and cost. A sale consumes lots by the book's
  method and writes the cost removed, the proceeds and the realised gain in full. A lot's cost is
  removed in proportion, rounded half-even to a minor unit, and the last share of a lot takes the
  remainder, so nothing is lost or invented. **Cost basis is a display convention for reporting;
  it is not tax reporting for any jurisdiction.**
- A trade posts to `unsettled`; a `settle` line moves exactly that amount into cash, once. Commission
  is paid from cash at once. Settled cash cannot go negative (unless the book allows it), and cash
  already committed to an unsettled buy cannot be spent twice.
- A lot is held at cost in one currency and is sold in that currency; a sale in another is refused.
  To end up holding another currency, sell and then record the conversion with its explicit legs.
- A **split** rescales every lot opened before it, with cost unchanged. A result that cannot be
  written exactly is refused (cash in lieu is not modelled).
- A **reinvestment** is a dividend that became shares: the new lot costs gross less withholding,
  and income is booked once.

### Open orders

A reservation holds cash or shares for an order and moves no money. Nothing else can spend what it
holds; the fill that names the reservation (`-order` on a buy or sell) consumes it. A reservation ID
is never used twice, even after release.

### Corrections

Nothing is edited. A **reversal** posts the exact opposite of a transaction and links to it. A
reversal is refused if it could not restore the state: the original was settled (reverse the
settlement first), a later live transaction changed the same lot (reverse that first), or it would
overdraw. Reservations, releases and reversals themselves cannot be reversed.

### Idempotent imports

Every line of an import file has an `id`. Importing the same line again changes nothing; the same
`id` with different content is refused and the **whole file is rejected** (all-or-nothing). Identity
is the business content (kind, time, security, quantity, amounts, references), not the file name or
the memo. The engine's content hash leaves out what the ledger derives from its own state (which
lots a sale used, the cost they carried, the gain), so a sale imported again after other trades is
still recognised.

## Commands

```bash
stoxicity portfolio book create -account main -name core -env real|paper_local|paper_broker [-base EUR] [-method fifo|lifo]
stoxicity portfolio book list | archive BOOK | reset BOOK -name NEW         # archive and reset: paper books only
stoxicity portfolio add BOOK KIND -date D [-import-id ID] [-memo M] [columns]   # one line
stoxicity portfolio import BOOK -file lines.csv                                 # all-or-nothing
stoxicity portfolio transfer -from BOOK -to BOOK -date D (-amount "500.00 EUR" | -security S -qty Q) [-import-id ID]
stoxicity portfolio show BOOK [-as-of TIME] [-basis event|recorded] [-lots] [-json]
stoxicity portfolio journal BOOK [-limit N] [-json]
stoxicity portfolio verify [BOOK...]
stoxicity portfolio reconcile BOOK -file statement.csv -as-of TIME
```

`BOOK` is the ID or `account/name`. A bare date in `-as-of` means the end of that day; use an RFC 3339
time to cut inside a day.

The columns of a line, and what each kind uses (a column a kind does not use must be empty):

| Kind | Columns |
|---|---|
| deposit, withdrawal, fee, commission | `amount` ("1000.00 EUR") |
| buy, sell | `security`, `qty`, `gross`; optional `commission`, `settle_day`, `price`, `order` |
| settle | `ref` (the import ID or transaction ID of the trade) |
| dividend, interest, other | `gross`; optional `withheld` |
| reinvest | `security`, `qty`, `gross`; optional `withheld` |
| fx | `amount` (sold), `to` (bought); optional `commission` (a fee), `rate` (as stated) |
| split | `security`, `ratio` as NEW:OLD (`2:1`) |
| reverse | `ref` |
| reserve_cash | `order`, `amount` |
| reserve_shares | `order`, `security`, `qty` |
| release | `order` |

A CSV has the header `id,date,kind` followed by any of those columns, one transaction per line, in the
order they are to be recorded. Every form error in a file is reported at once, with line numbers, before
anything is written. A security must already be in the instrument register
(`stoxicity instruments import`), so a typo cannot create a position.

## Proving a book is intact

`portfolio verify` replays the journal under the same rules that accepted it, checks that every currency
balances, and checks two things the database keeps beside the lines:

- each transaction carries a **chain hash** over its content and its predecessor's, so a line edited
  outside the program no longer matches;
- the lot records written when lots opened must be exactly the lots the journal opens.

The rows are append-only in the database as well (triggers refuse update and delete, a sequence gap, a
write to an archived book and a recorded time that goes backwards), but a person with the file can
drop a trigger. The chain is what notices.

## Reconciling with a statement

`portfolio reconcile` compares a statement you supply (settled cash per currency and shares per security,
as of a moment) with the book as of that moment. It reports every difference and changes nothing; the
answer to a difference is a recorded transaction with its own source. Settled cash that differs by exactly
what the ledger holds unsettled is called out as such. A match shows the ledger agrees with that file, not
that the file is right.

## Not done here

- Dividend entitlement, receivables and withholding reconciliation are in [income.md](income.md). Due-bills
  are not modelled.
- Mark-to-market, performance, time- and money-weighted return, benchmarks (US-021).
- Merger, spin-off, delisting and other corporate actions beyond splits. They must be entered as recorded
  adjustments; nothing detects them, so a statement difference is the signal.
- Reading a broker or custodian, and the order-intent state machine that uses reservations (US-014, US-017).
- Tax lots by jurisdiction, wash-sale or holding-period rules.
