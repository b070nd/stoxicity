# Tiers: what the program may do, apart from how much risk the money takes

A **tier** is an operational permission of the program. The **investment limits** (issuer, sector, currency
and country caps, the cash reserve, the cost burden, the drawdown threshold) are the policy's, and a higher
tier loosens none of them. More reliable order handling is not a reason for more investment risk.

| Tier | The program may | Real orders |
|---|---|---|
| 0 learn and build | paper trading, research, onboarding | none: drafting an order for a `real` or `paper_broker` book is refused |
| 1 conservative ownership | a real portfolio managed by hand, reconciliation, contribution planning, tickets for orders you place yourself | tickets only; no broker submission |
| 2 confirmed execution | a broker with your confirmation; an independently eligible small event sleeve | broker submission (US-017 to US-019) |
| 3 broader discretionary | as 2, with limits you approved in a new review | as 2 |

The tier is the `tier` of the **active policy**. With no active policy the tier is 0. The numbers in the plan's
example template are editable starting points, not calibrated and not a guarantee of anything.

```text
tier status [-json]
tier ready [TIER] [-json]
tier evidence set -target N -item ITEM -ref SOURCE -by NAME [-met yes|no] [-note TEXT] | show
tier config example | set -file F | review | show
tier unlock TIER -by NAME -accept-risk [-note TEXT]
tier signal set|clear KIND -reason TEXT -by NAME | show
tier check -equity FILE.csv
```

## Unlocking is evidence plus your acceptance, never an outcome

To raise the tier, **all** of this must hold:

1. For the target tier, each readiness item has a **sourced** statement that it is met (`tier evidence set`
   needs `-ref`, a pointer to the proof, and your name). Statements are versioned: an older one stays in the
   history. Tier 1: `config_complete`, `records_reconciled`. Tier 2 adds `no_broker_discrepancy`,
   `failure_scenarios`, `evaluation_adequate`. Tier 3 adds `tier3_review`.
2. The thresholds in `tier config` (the observation floor of 30 completed sessions is the plan's
   illustration) have been **read and reviewed by you**. `tier config set` always stores them as not reviewed.
3. The observation floor is met. It is a **necessary** condition among the others and counts weekdays since
   the first transaction (weekends left out, exchange holidays not known).
4. You record your acceptance of the risk text with `tier unlock -accept-risk`. The decision is bound to the exact
   evidence it was made on: **new evidence makes it stale**; it also expires (`decision_valid_days`, 7 by default).
5. A policy that carries the new tier is previewed and activated (`policy activate` checks 1 to 4 and refuses
   otherwise). Plans built on the old policy are stale and are regenerated.

Nothing in this code takes a return, a profit or a count of wins as input. A lucky streak or a year that
passes cannot unlock a tier; the tests build a long, profitable history and show the door staying shut.
Lowering the tier is never blocked, and revokes unsent buy confirmations.

## Health signals suspend new risk

`tier signal set` records a problem: `drawdown`, `stale_execution_inputs`, `reconciliation_failed`,
`account_permissions_changed`, `broker_unhealthy` or `manual`. Anything active **suspends new risk** for
`real` and `paper_broker` books:

* a buy cannot be drafted, confirmed or submitted, and the **unsent confirmations of buys are revoked**
  (the order becomes a draft again, keeping its hold);
* **nothing else changes**: holdings stay, orders a broker holds are not cancelled, sales can still be
  drafted and confirmed, cancelling and reporting still work. The program never liquidates or cancels because it is unhealthy;
* a local paper book is never gated.

`tier check -equity FILE` computes the largest peak-to-trough fall of the account value from a day,equity
series you supply, exactly (no floating point), and sets the `drawdown` signal if it reaches the policy's
`drawdown_review`. It can only suspend. **Resuming is your act** (`tier signal clear`), and revoked
confirmations stay revoked until you confirm them again.

## Not here yet

* The broker adapters will set `broker_unhealthy`, `stale_execution_inputs`, `reconciliation_failed` and
  `account_permissions_changed` themselves (US-017 to US-019); today you set them.
* Equity history comes from a file you supply; marks and performance are US-021.
* Whether an evidence item is truly met is your attestation with a source; the program cannot judge a failure drill.
