# Investment policy (US-003)

The policy is the investor's written constraints: finances, goal, preferences, limits and account facts. It is
data. Checking, previewing and activating it never places an order or reads a broker.

```text
stoxicity policy example [-profile conservative-eur-1000]   # a YAML file to start from
stoxicity policy check    -file policy.yaml                  # findings and unknown facts; reads no database
stoxicity policy preview  -file policy.yaml [-json]          # against the policy in force: changes, risk, effects
stoxicity policy activate -file policy.yaml -confirm HASH [-accept-risk]
stoxicity policy show [-at 2026-11-08T12:00:00Z] [-json]     # the policy in force now or at a time
stoxicity policy history
```

## Workflow

1. `policy example > policy.yaml`. The example is marked `example: true` and every value is a starting point.
2. Read each value. Replace what is wrong. Give each fact you know a source: `user` (you stated it), `imported`,
   `estimate` or `example`. Leave the rest `unknown` by omitting the value. Set `example: false`.
3. `policy check`, then `policy preview`. The preview prints a 64-character hash of exactly what it read.
4. `policy activate -confirm HASH`. The hash must match the file, so what is activated is what was read. A change that
   raises risk (higher caps, lower reserve, lower evidence floor, wider markets or instruments, a dropped exclusion,
   a higher tier) also needs `-accept-risk`. A change that only tightens needs nothing more.

## What is checked

- Format and range: fractions are written 0.10, not 10; amounts are `1000.00 EUR` with the currency's digits; amounts must be
  in the base currency because a policy holds no exchange rates; dates are `YYYY-MM-DD`; tax residence is a country code
  (a time zone does not establish it); unknown keys in the file are errors.
- Contradictions: the sector cap below the issuer cap, a planned event-trade loss above the whole event sleeve, an event
  sleeve larger than what the cash reserve leaves, an income objective with no dividend preference, a contribution that
  ends before it starts, a goal date in the past.
- Provenance: a source without a value, or a value without a source, is an error.
- Feasibility (warnings, never relaxed): when the issuer cap, fund cap, instruments and minimum position make it impossible to
  place the money meant for the core (for example a 150 EUR minimum position under a 10% cap of 1000 EUR), the finding
  gives the numbers. It checks necessary conditions only; the allocator decides with real instruments.
- Unknown facts are allowed. Each is listed with the features that wait for it (tax residence: net income estimates;
  commission schedule: affordability after costs and wait-or-batch decisions; and so on). News, evidence, research and
  local paper workflows do not depend on them.

## Versions and replay

Each activation appends a row (the policy version is stored once per distinct content hash) with the consent, the changes, the
risk notes and the effects. Rows cannot be updated or deleted: database triggers refuse it. The activation's sequence number is the
**policy epoch**. `policy show -at T` returns the policy that was in force at T, so a replay never sees a policy that was activated
later. An activation made against an epoch that is no longer the newest is refused (`preview again`), so two previews cannot
both be activated, and an activation dated before the previous one is refused.

## What invalidation means today

Activating a policy that differs in content makes the epoch newer. By design, plan proposals and order confirmations record
the epoch they were made under and are stale when it is no longer current (`policy.Stale`). Plans and order confirmations now exist (docs/plans.md, docs/orders.md, docs/broker.md): activating a different policy makes
open plans stale and a confirmation made under the old epoch unusable (`cmd.TestPolicyReplacedAfterConfirmationInvalidatesTheOrder`). The preview lists these effects before you confirm.
Renaming a policy changes nothing else.

## Not here

Holdings and cash per currency belong to the ledger (US-007). What each tier permits belongs to the tier story (US-016); the policy
only records and versions the number. The policy file in the example is not advice and not a tested setting.
