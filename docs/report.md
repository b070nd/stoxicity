# Exporting a book or a plan

`report export` and `plan export` write down what the program holds, for you to keep, to send to an adviser or accountant, or to feed to another program.

```
stoxicity report export BOOK [-as-of DAY] [-format md|json] [-out FILE] [-force]
stoxicity plan export PLAN[.rN] [-format md|json] [-out FILE] [-force]
```

Markdown is for a person. JSON is for a program and names its schema in `schema`: `stoxicity.report/1` and `stoxicity.plan-export/1`. A change that removes or renames a field gets a new schema string, so a program can refuse a version it does not know.

## What a report holds

The book (account, environment, base currency, a fingerprint of the exact ledger), the policy epoch and tier in force, the broker as a transport and a mode only, cash per currency (settled, unsettled, held for open orders, available), holdings with cost and lots, the holds, the orders of that book (an order whose outcome is unknown is marked so), the latest revision of each plan of that book with its effective state, the income the ledger holds with the findings of the income check, and the product's own readiness inventory level by level. A paper book is marked "not real money" on its first line.

## What it never holds

- No article text, headline or snippet. Plans refer to evidence by identifier, so a source licence that forbids redistribution is not breached by sharing a report. The report says so.
- No broker address, certificate path, fixture path, broker account number, key or token.
- No other book of the tenant.
- No prices and no valuation of the holdings, because producing one would need a fetch. Nothing is fetched to make a report.

## Offline, by construction

An export reads stored records and nothing else. It does not call a data source or a broker, and it sends no order. A test runs it on an HTTP client whose transport refuses every request and counts none. If the books do not balance the export refuses to describe them (run `portfolio verify`).

## Files

`-out FILE` writes the file and keeps an existing one: the command fails unless you add `-force`. Without `-out` the report goes to the screen. Two exports of the same records are byte-identical in JSON.

## Limits

The income findings are those of `income check` at the day given. The readiness section is what this program claims, not a verdict on an investment. A plan export carries the whole revision, including its manifest hash, so a reader can see whether the stored revision still matches the inputs it was made from (`intact`).
