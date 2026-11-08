# Readiness, doctor and exact money

This is the foundation of [the production plan](production-plan-mode.md)
(stories US-001 and US-002). Nothing here places an order or reads a broker.

## Five readinesses, never one score

`stoxicity doctor -scope research|portfolio|broker` lists every capability with five separate answers:

| Dimension | Meaning |
|---|---|
| `implemented` | The code exists. |
| `fixture_verified` | Tests pass on fixtures. |
| `source_verified` | Checked against the real data source, with a dated record. |
| `account_verified` | Checked against the user's actual account. |
| `investment_evaluated` | A strategy passed its preregistered evaluation on history. |

Each is `yes`, `no`, `unknown` (nobody recorded an answer, which is not `no`) or `not_applicable`.
They are counted per dimension and never added together. A `yes` must name the file that supports it;
`src/readiness/readiness_test.go` fails if that file is missing, and it fails if any capability claims
`source_verified`, `account_verified` or `investment_evaluated`, because no record format for them exists yet.
Today no capability has a recorded source check, so those stay `unknown` or `no`. That is a listed gap, not an oversight.

`doctor -json` prints schema `stoxicity.doctor/1`: the checks (each with a state: `implemented`, `configured`,
`verified`, `stale`, `unsupported`, `unknown` or `failed`), the database schema versions, the capabilities
and the known gaps. The inventory is in `app/src/readiness/inventory.go` and changes in the same change as the story it describes.

## doctor never opens the databases

`doctor` used to build the stock universe while checking the watches, and that opened `base.db`,
creating and migrating it. It no longer opens the store at all (`app.noStore`). Its `schema` line reads the
`schema_migrations` table through a read-only connection and says when the file is behind this binary:
the first command that opens it migrates it, and there is no way back, so `./backup.sh backup` and
`./backup.sh verify` come first. A database newer than the binary is a FAIL.

## Benchmark report format

`readiness.BenchReport` (schema `stoxicity.bench/1`) is the format for every performance claim: machine
(OS, architecture, CPUs, Go version), the deterministic fixture (`ReferenceCatalogue(seed, n)`, invented securities),
and per operation the sample count, p50, p95 and max against a target. Provider latency is always excluded.

## Exact money (`app/src/money`)

- `Decimal` is a fixed-point number (int64 coefficient, up to 18 digits after the point) built on `math/big` for
  every intermediate, so no operation wraps. Parsing accepts plain decimals only: no exponent, no `+`, no NaN or Inf.
  `FromFloat` is the one place a float enters, at the boundary, and it reads the float's shortest decimal form.
- Every operation that can lose digits takes the target scale and a rounding mode (`Exact` fails instead of rounding;
  `Down`, `Up`, `Floor`, `Ceiling`, `HalfEven`, `HalfUp`, `HalfDown`). Nothing rounds by default.
- `Amount` is whole minor units of one currency. Currencies come from a fixed table with their minor-unit digits (JPY 0,
  EUR 2); an unlisted code is refused, never assumed to have two digits. Adding different currencies is an error.
- `Allocate` splits an amount in proportion to weights so the parts add up exactly; leftover cents go to the largest remainders.
- `Balanced` checks that an entry's postings sum to zero in every currency separately, so an FX conversion needs
  clearing legs in both currencies.
- `Rate` has a direction: 1 Base = Value Quote. `Convert` refuses an amount in the wrong currency instead of inverting;
  `Invert` takes the scale and rounding explicitly. A rate carries a basis: a `reference` rate (such as the ECB's)
  is not executable; only a `fill` rate is.
- `QuoteUnit` keeps `GBP` (pounds) and `GBp` (pence) apart; the case is never folded. `Price.Major` and `Notional` leave the
  unit exactly, so 100 shares at 1234.5p is 1234.50 GBP, and a 100x error cannot happen silently.
- `QtyRules` (step and minimum) and `TickTable` (price bands) validate and snap quantities and limit prices; snapping uses an
  explicit direction (Floor for a buy limit, Ceiling for a sell) and is checked again across a band edge.

Property tests (`money_test.go`, fixed seeds) check parse/print round trips, add/sub laws, multiplication and
division against `big.Rat`, every rounding mode on ties, allocation sums, balanced entries, FX round trips within one minor
unit for rates from 0.5 to 5, and tick idempotence.
