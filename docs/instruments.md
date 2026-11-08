# Instruments and the capability registry

Story US-004 of [the production plan](production-plan-mode.md). Nothing here places
an order, reads a broker or calls a data vendor.

## What an instrument is

Three levels, because one of them is not enough:

| Level | Identified by | Holds |
|---|---|---|
| issuer | an ID (CIK and LEI where known) | the legal entity |
| security | an ID, and its ISIN | one share class, or one fund share class: kind, class name, distributing or accumulating, domicile, UCITS |
| listing | venue (MIC), ticker and first day | the currency it trades in, **the unit its price is quoted in**, the last day, broker contracts |

A fund's accumulating and distributing classes are two securities. A cross-listed security is
one security with several listings. A London line quoted in pence (`GBp`) and one in pounds (`GBP`)
are a factor of 100 apart, so a listing without a quote unit is an error, and a unit that is not its
currency is highlighted in every table. Tickers are reused, so a listing has a first day, and
two listings of the same ticker on one venue may not overlap in time. A broker's contract ID names
a listing; the route an order is sent through is stored beside it and is not the primary venue.

## Commands

```bash
stoxicity instruments example > instruments.yaml        # invented fixtures, not a data source
stoxicity instruments check -file instruments.yaml
stoxicity instruments import -file instruments.yaml -source "my broker export 2026-11-08"
stoxicity instruments list [-at 2026-11-08T00:00:00Z]
stoxicity instruments resolve -mic XLON -ticker BA -day 2026-11-08
stoxicity instruments resolve -isin IE00... -day 2026-11-08 -security
```

An import validates the whole merged set first and writes nothing if anything is an error. A record
that is identical to the stored one adds no row; a changed one becomes a new revision with the time it
was recorded, so `list -at` shows what was known then. Records are append-only (database triggers
refuse update and delete), and a record the file leaves out is kept. Imported facts are what the file
says: they are not verified against any register.

A security may carry `link_ticker`: it then takes the ID of the security that already carries that
ticker in the evidence history, and its listings follow, so events recorded earlier keep their identity.

## Resolution never guesses

`resolve` takes any of ISIN, venue, ticker, currency and a broker contract. Every identifier given must
fit (they are conjunctive), and the day is required. The answer is one of:

- `resolved`: exactly one listing (or security) fits.
- `ambiguous`: more than one fits; the candidates are listed with what tells them apart (venue, currency,
  share class, distribution). A fund's ticker on two venues is ambiguous until the venue is named.
- `not_found`, `conflict` (each identifier matches something, not the same thing), `invalid` (malformed
  ISIN with a bad check digit, venue, or day). Each comes with its reasons.

The exit code is non-zero unless the answer is `resolved`.

## Capability registry

```bash
stoxicity sources capabilities                                   # every provider and entitlement
stoxicity sources capabilities -data prices_eod -market DE -kind stock
```

For each provider and data type the registry states the markets and instrument kinds, the history,
delay, how currencies and units are stated, the cost class (`free`, `free_key`, `account`, `paid`),
the key variable, the quota and the rights its terms give. Rules:

- **Unknown stays unknown.** A fact nobody checked is written `unknown`, and an unknown market is not coverage.
- **Nothing unwired is usable.** An entitlement the product has no code for is `not_wired`, however good the vendor is.
- **Free before paid; paid is off.** A paid provider is never chosen unless named with `-allow-paid`, and
  there is no automatic fallback to one. An account provider needs its account set up, which this
  command cannot claim.
- **Import is labelled.** Where only a structured import can supply the data, the answer says import-only.
- **No verification is claimed.** Every provider shows `verified: never` until a dated check exists.

Today the wired entitlements are SEC filings and fundamentals, a US-stock price and corporate-action source
that needs a free key, and the instrument import. ECB reference rates, prices by import and the broker
are listed but have no code. European prices have no automatic coverage and say so. The command opens no database.

## What this does not do

It does not look up securities: there is no register of European securities in the product, so
instruments come from your file. It does not value or trade anything. Corporate actions arrive with later stories; FX and price observations are in [fx-prices.md](fx-prices.md).
