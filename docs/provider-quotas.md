# Provider request quotas (scenario 6)

The fetch HTTP client has two independent controls:

- Its existing short-term rate limiter and cooldown, per host group.
- Durable configured request budgets and recorded HTTP 429 cooldowns, shared in
  `base.db` across tenants, workers, CLI invocations and restarts.

## Configure a local budget

In `config.yaml` (the numbers below are examples, **not verified allowances**):

```yaml
fetch:
  quotas:
    api.polygon.io:
      requests: 5
      period: 1m
    sec:
      requests: 1000
      period: 24h
```

The key is a lower-case hostname without a scheme, port, path or credential.
`www.sec.gov` and `data.sec.gov` share the key `sec`. Other hosts have their own
key; mirrors and other provider domains do not automatically share an allowance.
The configuration period is a positive Go duration between `1ms` and `8784h`.
There is no provider-budget default: an unconfigured allowance is **unknown**,
not verified unlimited access. The existing SEC rate limiter still applies.

A window starts at the first reserved attempt, not UTC midnight or an inferred
subscription billing boundary. Usage is reserved before a request is sent.
Retries, redirect hops (against their destination's group), 304s and failures
consume attempts. Cancellation after reservation may consume an unused attempt;
the application does not guess whether the provider charged it. There are no
automatic refunds, upgrades, paid fallbacks or credential-based budget keys.

Changing a limit does not refund recorded attempts. A longer configured period
extends an active window conservatively; shortening it does not end an existing
window early. The next fetch persists this transition even if refused; status
projects the same transition without writing it. Removing a budget stops
enforcing that local numerical cap, but
does not remove a recorded provider throttle.

## Provider throttling

An HTTP 429 stores a cooldown even when no numerical budget is configured. A
5xx with a Retry-After exceeding the client's retry-backoff ceiling does too. A
valid `Retry-After` delta in seconds or HTTP date is retained, including delays
longer than the in-memory sleep cap. Missing/invalid/zero/past values use the
existing one-minute fallback. That fallback is a retry policy, not evidence of
the provider's actual reset. A later shorter response cannot shorten a recorded
cooldown. Provider-specific JSON error formats, remaining-credit headers and
billing reset rules are not inferred.

Exhaustion returns a typed quota cause wrapped as an HTTP `throttled` error,
with a provider key and retry time. While exhausted, requests are refused before
network IO rather than sleeping through a long budget window. A database
accounting failure refuses the request. Request keys, bodies and credentials
are not stored in quota state; errors redact URL query strings and userinfo.

## Inspect without fetching

```sh
stoxicity quota status
stoxicity quota status -json
```

Status combines configured budgets with recorded usage and cooldowns. An expired
window is shown with zero current usage; the next request starts the next window.
Reading status makes no HTTP request, although like other stored-data commands
it opens/migrates the database. `doctor` retains its no-database behavior.

## Effect on research and plans

An exhausted provider cannot refresh its data. Failure does not replace good
observations with empty data, update their received times, or change the ledger.
Stored prices, FX, articles, plans and portfolio records remain readable. Other
host groups can still fetch. Existing freshness and missing-input checks continue
to reject calculations that need unavailable/stale data; a still-valid cached
input is not automatically invalid merely because its provider is cooling down.
No background schedule or live broker configuration is changed by this feature.

The IBKR adapter has its own transport/pacing path and is **not** included in
these fetch quotas. Nor are notification delivery and the optional Laya client.

## Persistence and verification

Base migration `0027_provider_quota.sql` adds only operational request counters
and cooldowns. Tenant schemas and financial/evidence rows are unchanged. Counters
use immediate SQLite write transactions; concurrent connections cannot reserve
the same remaining request. Stored times round up to millisecond precision so
rounding cannot make a cooldown end early.

Tests cover real HTTP/accounting seams with synthetic/local responses: retries,
redirects, conditional responses, long/date/malformed Retry-After, provider
isolation, restart persistence, clock rollback, concurrent connections,
accounting failure, migration on a synthetic database copy, and CLI scenario 6
with preserved FX/portfolio data and explicit stale/missing values. No external
provider allowance, live source or IBKR account was verified.
