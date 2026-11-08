# Alerts, the decision journal and the daily review

Three commands: `alerts` tells you once about the changes that matter and is honest about what reached you; `journal` keeps your reasons next to the plan's thesis as it stood and lets you look back; `daily` lists what asks for your attention today and ends with the journal.

```
stoxicity alerts scan [-deliver] [-json]
stoxicity alerts status [-all] [-json]
stoxicity alerts deliver [-retry] [-json]
stoxicity alerts answer ALERT accepted|rejected|noted -by NAME [-note TEXT]
stoxicity alerts config show [-json]
stoxicity alerts config set [-quiet-start HH:MM -quiet-end HH:MM -zone Z | -no-quiet] [-critical RULES|none] [-channels "RULE=kind,kind;..."] [-attempts N] [-retry-minutes N] [-dividend-days N] [-review-days N]

stoxicity journal add PLAN[.rN] -decision accepted|rejected|deferred|noted -reason TEXT -by NAME [-horizon DAY] [-alert ID] [-apply]
stoxicity journal outcome ENTRY -result held|broken|mixed|early -note TEXT -by NAME [-day DAY]
stoxicity journal show ENTRY [-json]
stoxicity journal list [-due] [-json]

stoxicity daily [-json]
```

## What raises an alert

`alerts scan` reads stored records (plans, books, income, broker differences, orders). It fetches nothing and sends nothing.

| Rule | Raised when | Same fact, new revision when |
|---|---|---|
| `plan_revision` | the latest revision of a plan is a draft or proposed | a newer revision of the plan exists |
| `review_due` | an accepted plan is past its review date, or a holding has gone `-review-days` (default 91) since its last `review done` (or first purchase) | the due day changes |
| `dividend_ex_date` | a held security goes ex within `-dividend-days` (default 7), or went ex that recently | the ex-date or the gross amount changes |
| `income_missing` | `income check` finds a missing accrual or an overdue payment | the figures change |
| `income_difference` | `income check` finds any other difference | the figures change |
| `broker_exception` | a `broker.difference` record is open | the difference comes back as a new episode |
| `order_unknown` | an order is in the `unknown` state | never: one alert per order |

An alert is identified by rule, subject and revision. Scanning again, or from two terminals, raises it once: the alert and its outbox entries are stored in one transaction. Alerts carry facts about your plans, books and orders. They never carry article text.

Not raised, because nothing detects them yet: a thesis invalidation, a policy breach, missing or stale market data, a dividend being declared or paid. `review_due` and `income_missing` stand in for the last two in the cases the books can show.

## The outbox: what "delivered" means

Every alert has one outbox entry per channel. The states, and what each guarantees:

- `pending`: waiting to be sent.
- `sending`: a send was started. It is written **before** the channel is called.
- `sent`: the channel returned success. This is the only proof of delivery; nothing else is ever shown as sent.
- `failed`: the channel returned an error. The attempts and the next try are kept (the wait doubles; after `-attempts` it stops and says it gave up until you run `alerts deliver -retry`).
- `held`: quiet hours; sent when they end. A rule you list under `-critical` goes through quiet hours. Only `order_unknown` is critical to begin with; that is a starting choice for you to change.
- `uncertain`: a `sending` entry found at the next start. The program stopped before it knew the result. It is sent again with the title prefix "May repeat an earlier message", and is never counted as sent without proof.
- `superseded`: a newer revision of the same fact replaced it before it was sent. If an earlier revision was sent on that channel, the newer one goes out as "Correction: ..." naming the one it replaces.
- `withdrawn`: the situation ended before it was sent (only when the producer for that rule ran to the end, so a failed look never withdraws anything), or you accepted or rejected the alert first. If the situation comes back before anyone was told, the alert reopens.

A channel that is not configured leaves the alert waiting with that reason. It is never marked sent.

## Tenants and channels

Each tenant has its own alerts, outbox, configuration and answers. By default alerts go to the `file` and `feed` channels only (local files and an Atom feed). `email` and `webhook` leave the machine, so a rule uses them only if you list them for that rule with `-channels`; setting them prints a note saying so. The terminal-interface channel needs no delivery: `alerts status` and `daily` show every open alert.

Channels are built from the same variables as for watches (`STOXICITY_FILE_DIR`, `STOXICITY_FEED_PATH`, the webhook and Gmail settings). A file or feed delivery of the same alert overwrites its own file, so a repeat does not duplicate it.

## Answering an alert

`alerts answer` records accepted, rejected or noted next to the alert. It never changes the alert, the plan or the evidence behind either. Accepted and rejected also withdraw the copies not yet sent (you have seen it); a note does not. For a plan revision the answer does not move the plan: use `journal add -apply` to decide the plan and write down why.

## The journal

`journal add` records a decision on a plan revision with your reason, in your own words (required), and a horizon, the day to look back (default: the plan's review date). It copies the plan's thesis, counter-evidence, invalidation and exit rules as they stood in that revision, and names the revision's manifest hash. It will not record `accepted` or `rejected` unless the plan is in that state; `-apply` moves the plan with the ordinary lifecycle (and its staleness checks) first. The same decision twice stores nothing new.

`journal outcome` is a later look: held, broken, mixed, or early (too soon to say, which leaves the entry due). Outcomes are appended and linked to the entry. `journal show` prints the thesis as decided, the thesis in the plan's latest revision with what is no longer stated and what is new, and every look back. The journal holds no prices and grades nothing by return.

## The daily review

`daily` prints health (policy tier, active health signals), alerts waiting, plans waiting for a decision, reviews due, income dates in the next 14 days with any difference, orders that need attention (an unknown order first), and the journal entries past their horizon. It reads stored records and changes nothing, and ends by asking for the reasoning behind today's decisions and the outcomes that are due.

## Limits

- Nothing runs `alerts scan` for you. `ops service launchd|systemd` writes a timer definition for `alerts scan -deliver` every 15 minutes; you install it.
- The terminal interface does not yet have an alerts or journal screen.
- Delivery to email and webhook is covered by the channel tests, not by a live send in this story. The file and feed channels are exercised for real in the tests.
- Two deliveries of one tenant are kept apart by a job lease (docs/operations.md): the second says another delivery is running and sends nothing. The lease is advisory and runs out after ten minutes if the first dies.
