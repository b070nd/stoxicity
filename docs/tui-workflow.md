# The Plan, Portfolio and Income screens

`stoxicity-tui` has three more sections after Label: **Plan** (`p`), **Portfolio** (`f`) and **Income** (`m`). Orders and Policy are reached from them. The news screens and the watch semantics are unchanged; the facets toggle on the Companies screen moved from `f` to `=` to free the letter.

| Section | Key | What it shows |
|---|---|---|
| Plan | `p` | The policy and tier in force (enter: what the last activation changed, before and after), then the plans: due now, buy candidates, watch/wait/blocked, settled. Enter opens a plan. |
| Plan detail | | The whole card: state, data/strategy/execution, score and coverage, versions (enter opens an earlier one) and what changed, quantity and cost, scenarios, concentration, reasons, unknowns, thesis, counter-evidence, invalidation, exit rules, alternatives, every test, costs, the manifest and its hashes, the history of the revision and the orders drafted from it. |
| Portfolio | `f` | The books, each marked REAL MONEY or PAPER in words, with links to Orders and Income. |
| Book | | Cash per currency (settled, unsettled, held, available), holdings, holds, the book's orders and plans, income with the income check, and what the screen leaves out and why. |
| Income | `m` | Declared and estimated payouts by status (entitled first), with the check that income is booked once. Gross, before withholding. |
| Orders | from Portfolio | Every order with its state, REAL/PAPER in words, and an unknown outcome called out. |
| Order | | The exact terms a confirmation covers, the warnings, why, late facts, the plan it came from, and the confirm row. |
| Policy | from Plan | The policy epoch, tier, the settings the last activation changed (before -> after), what it did to plans and orders, risk notes. |

Filters: `/` on a list screen takes `key:value` words and plain words (Plan: `account:`, `sleeve:`, `action:`, `currency:`, `data:`, `strategy:`, `execution:`, `state:`; Orders: `state:`, `env:`, `side:`, `security:`, `book:`; Income: `book:`, `status:`, `security:`; Portfolio: `account:`, `env:`, `currency:`, `status:`). The filter is kept in the route, so back and forward return to it, and the screen says how many of how many are shown. There is no market filter: a plan card does not carry one.

## Reading

Every line is wrapped to the terminal's width at its spaces; a word wider than the line is broken, not cut. Nothing essential is shortened, and a long card is read with the arrows, Page Up/Down, Home and End (every line holds the cursor). Wide characters count by cell. Terminal control sequences in any text are removed before they are drawn.

Colour carries meaning and is always accompanied by words. A buy is not green and a sale is not red: actions are drawn in the plain style. Yellow means something waits for you or is stale or unknown; red is for a failure (an outcome unknown, a revision that does not match its hash, an income difference); dim is settled. REAL MONEY is bold; paper is yellow and says "not real money".

Drawing reads nothing: the screens are built from what was loaded when you opened them, and `r` reloads. Nothing in the interface fetches data or contacts a broker.

## Confirming an order

On an order whose book is not bound to a broker account and which is still a draft, the row "Confirm this order" asks you to type the order's ID. A wrong ID, or Esc, records nothing. The right ID records your go-ahead through the same order service, tier gate and limits as `order confirm`, acknowledging the warnings the screen showed, for 15 minutes. **It sends nothing.** An order of a book bound to a broker, a lapsed draft, and any order that is not a draft say why they cannot be confirmed here; a broker-bound order needs `order confirm`, which checks the broker's preview first. Submit, cancel, resolve and report stay command-line acts.

## What is not here

- What-if. A plan card is offline; change an input and run `plan generate`. The screen says so.
- A policy editor. The Policy screen shows the last activation's before and after; `policy preview -file` shows a candidate and `policy activate` applies it. The TUI never activates anything.
- Value, allocation, risk and performance of a book: they need prices and rates, which a screen never fetches. `performance BOOK -marks FILE -rates FILE` computes them.
- Reconciliation exceptions: `broker differences` and `portfolio reconcile`.
- A cancel/replace ticket, and a broker-connected confirmation.

## How it was checked

*Synthetic rendering tests* (`go test ./src/tui`, `./src/workflow`, `./src/cmd/stoxicity`): screens built from canned data at widths 40, 60, 100 and 160 with wide characters, every row within the width and every essential value present; grouping and the colour rules; a card that can be read to its end; the confirm prompt (wrong ID, Esc, right ID, the lines under the row, a book bound to a broker, a lapsed draft); drawing makes no backend read; control characters removed; the cursor kept across a resize; the shared layer against the real stores.

*Interactive checks* of the built `stoxicity-tui` in a real terminal (tmux, 100x34, 170x30, resized to 40x22), against a scratch copy of a fixture workspace (three plans, a real-money book, a paper book, three orders): Plan list, plan detail read to its end with End, a record opened from a continuation line, a filter that matches nothing and one that matches one plan, Portfolio, Orders, an order that had lapsed (not confirmable, with the reason), a live paper draft confirmed with a wrong ID (refused), Esc (cancelled), the right ID (confirmed; `order show` and `order list` agree; nothing sent), the Policy screen, the tab bar at 40 columns, and a resize from 170 to 40 columns. The checks found four defects, all fixed and each now under a test: a card with no row to stop on could not be scrolled; a lapsed draft was offered for confirmation; the book was shown by its ID; the confirmation message was wiped by the reload.

Not checked interactively: the Income screen with payouts (the fixture holds none), a plan that is still open, and a real-money order with a policy at tier 1 (checked by test and by the CLI, not in the terminal).
