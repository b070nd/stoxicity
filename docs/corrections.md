# Corrections, retractions and thesis rules

An accepted plan used to stay actionable after the evidence behind it changed, until you ran `plan generate` yourself. `corrections` closes that gap: it compares each live plan with the versions of the evidence it cited, the figures it used and the thesis rules you recorded, and acts only when something that matters changed.

```
stoxicity corrections scan [-json] [-no-alert]
stoxicity corrections list [-json]
stoxicity corrections show CHANGE
stoxicity corrections rule add -security S -id R -kinds K,K -effect broken|review -note TEXT -by NAME [-roles subject,counterparty,mentioned]
stoxicity corrections rule list
stoxicity corrections rule retire -security S -id R -by NAME
```

Run `corrections scan` on a schedule, like `alerts scan`. Nothing runs it for you.

## What a plan depends on

When a revision is made (`plan generate`, `review run -save`, or a correction) the program writes down, once and never again for that revision:

- **Events.** The events about the security in which it is the subject or the counterparty (a mention is not a dependency), each at the revision read, with a fingerprint of what the event says and the head of each article that supplies one of its claims. The plan card lists them under "evidence cited". They are context the plan cites; **they do not change the strategy's score**, which reads figures, assumptions, prices and payouts. A correction therefore changes what the plan cites and says, and the plan is then reassessed from its recipe, but a corrected event does not by itself move a BUY to a WAIT.
- **Thesis rules.** The rules in force and the events that already matched them.
- **Its recipe.** The candidate and fee files (with their hashes), the rates, the contribution and the validity, so the plan can be generated again without anyone typing the command.
- **Figures.** The plan manifest already names the scorecard, assumptions and recorded decisions it was computed from.

A plan made before this was kept has no dependencies. `corrections scan` lists it as untracked and does nothing to it; generate it again to track it.

## What counts as a change

| Finding | When | Effect |
|---|---|---|
| `retracted` | a cited event was withdrawn | reassess |
| `corrected` | a cited event no longer says what the plan read: its kind, day, title or status changed (for example it became "corrected"), a claim the plan rested on is gone or was replaced, or a security it named is no longer named in that role | reassess |
| `source_changed` | an article that supplies a claim was changed at its source, and the event has not been derived again, so whether the claim stands is not known | review |
| `figure_revised` | a figure or note of the scorecard, or the valuation assumptions, changed after the plan was made | reassess |
| `figure_revised` (decisions) | your recorded thesis, instruction or review for the holding changed | review |
| `thesis_rule` | an event matches a rule you recorded and it is new since the plan was made | the rule's effect: broken or review |

Not a change: another article joining an event, a new claim, a new security or topic in the event, an article fetched again with the same text, a correction to an event the plan does not cite, a new event no rule names. The scan prints these as "not material" so you can see they were looked at.

## A thesis is broken only by a rule you recorded

`rule add` records, for one security, the event kinds that break its thesis (or need a look), the role the security must have in the event (default: subject), what the effect is and why. A rule never sells and never records a decision for you. When it matches a new event the plan gets a revision that proposes nothing and says which rule matched. Anything that looks like a broken thesis but matches no rule is "review", never "broken". Rules are versioned; retiring one keeps its history.

## What a change does

One change is one plan and one set of findings (its id, `chg_...`, does not depend on when it was found). The scan:

1. **Writes the change down** with the day the new revision is made for, so an interrupted scan finishes the same revision later.
2. **Appends a revision.** If the change is a clear one and the recipe still works, the plan is generated again from its recipe. Otherwise (a review or broken finding, no recipe, a missing file, a plan that is no longer produced) the earlier card is kept, its action becomes ABSTAIN, its execution eligibility research only, and it says what must be looked at: **a plan that cannot be reassessed is never shown as a fresh HOLD**, and it cannot be accepted. The card lists the findings under "why this revision exists".
3. **Keeps the earlier revision.** The accepted revision and your decision about it stay in the record. The earlier revision is now out of date: its evidence hash no longer matches, so it cannot be drafted into an order. The new revision is a draft; you propose and accept it again.
4. **Withdraws the confirmation of every order of that plan that was confirmed and not sent**, in any environment. The order stays a draft with its hold; confirm it again after reviewing the new revision.
5. **Leaves every order that was already sent exactly as it is.** The program never modifies or cancels it. It is listed in the change and in the alert.
6. **Records the alert** (`plan_correction`), named by the plan and the change. Scanning again, starting again or finishing an interrupted scan records no second alert and no second revision. The new revision is not announced twice: the generic "plan revision" alert skips a revision a correction made.

## Quiet hours and urgency

`plan_correction` is not critical by default. It is urgent only when two things hold: you listed `plan_correction` under `-critical`, and an order of the plan was sent, is still open or unknown, and its book is a real one (live exposure that nobody has decided about). A correction that only blocks a workflow, or touches a paper book, waits for the end of quiet hours like any other alert.

## What is and is not verified

- Verified by tests on fixtures: a corrected event revises a plan and withdraws the unsent confirmation while the sent order stays open; a retraction and a rule match produce a review-required revision that cannot be accepted; an unrelated correction and a joining article change nothing; reprocessing, starting again and an interrupted scan make no second revision or alert.
- Not verified: against real articles and events from the pipeline. The tests write events directly; whether the pipeline produces a revision in the situations that matter (it writes a "corrected" status when a source changes its text; it withdraws an event whose claim is denied) was not checked against live data.
- Events never move the score. A retracted event on which your thesis depended is reassessed on the same figures; only a rule you recorded can mark the thesis broken.
- Thesis rules match event kinds only. There is no rule on a price, a dividend cut or a figure.
- `review run -save` plans have no recipe: a change to one of them always gives a review-required revision.
- A scan is a job you schedule; there is no watcher.
