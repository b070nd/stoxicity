[PRD]
# PRD: Production research terminal, portfolio and Plan mode

Status: proposed implementation specification; no features in this document are claimed implemented.
Prepared: 2026-11-08. Scope: personal-use CLI/TUI, US and European equities and eligible UCITS ETFs, EUR reporting, free-data-first, manual/paper and explicitly confirmed IBKR execution.

## 1. Overview and decisions

Turn stoxicity from an evidence-oriented news terminal into an investment planning and portfolio management tool. Plan mode answers **what is worth buying, at what price, with how much capital, for what thesis and horizon, what to monitor, and what would justify holding, reducing or selling**. It must also answer **wait**, **insufficient evidence**, and **not affordable after costs**.

User decisions from this conversation:

- Support manual investing, a local paper portfolio, and IBKR orders with confirmation. No unattended order submission.
- Support long-term investing and shorter event-driven trading as separate sleeves, with explicit budgets and no silent transfer between them.
- Starting context: roughly EUR 1,000 and a five-year goal, as recorded in PROTOCOL.md section 9. Make capital, contributions and actual goal dates configurable; do not treat this amount as a permanent limit.
- No paid-data dependency. Include US and European instruments; display incomplete coverage rather than inventing values or silently excluding entire markets.
- Begin conservatively, build a portfolio first, and allow explicitly approved higher tiers as readiness develops.
- Emphasize understandable businesses, financial resilience, sensible valuation, sustainable dividends and patient ownership, inspired by Lynch/Buffett-style investing. These are design principles, not claims that either investor endorses the system or that the system reproduces their judgment.
- Prioritize dividend quality and timely awareness of declared distributions, with editable preferences.
- Full engineering, operational, financial-accounting, historical-validation and interactive TUI gates.

“One shot” means **one implementation programme with a dependency graph and a complete acceptance contract**. Implement its software end to end in order. Real account permissions, unavailable data, independent labels and months of forward observations remain explicit external dependencies; never fill them with fixtures and call them verified.

## 2. Current baseline and production gaps

Use PROTOCOL.md section 12 as the latest recorded implementation status, not older completion claims. Reinspect source when implementation starts; the working tree is already substantially modified.

| Existing foundation | Production work still required |
|---|---|
| Immutable observations, revisions, stable securities, source-origin assessments | More precise event subjects/counterparties; held-out real-news evaluation; rule/content version completeness |
| Resumable SEC reconciliation, coverage reporting | Real-source contract checks, operational monitoring, coverage goals for followed holdings |
| Price provenance, split handling, point-in-time class valuation | Sourced FX, dividends and other corporate actions, total-return series, European coverage, execution-grade quote distinctions |
| Tenant-bound stores, watches, durable alert state | Cash/lot ledger, accounts, portfolios, plans, order lifecycle and broker reconciliation |
| CLI/TUI and offline tests | Plan/Portfolio/Income workflows, no-network rendering, interactive QA, consistent financial semantics |
| Online backup, verification and restore script | Scheduled retention, independent/off-device copy option, locked/quiescent coherent portfolio backup, restart/recovery drill with committed progress |
| Synthetic claim challenge corpus | Independent held-out real samples and prospective investment evaluation; the existing 39 cases are regression tests, not proof of accuracy |

No strategy profitability, calibrated return forecast, broad European financial coverage, or live IBKR compatibility is established by the existing Go tests. README.md also contains stale early-status and navigation descriptions; update it as part of this release.

### Definition of production

Track readiness separately for each capability: `implemented`, `fixture_verified`, `source_verified`, `account_verified`, and, where applicable, `investment_evaluated`. These are not interchangeable.

Release modes:

1. **Research/paper:** installed product operates reliably, reports coverage, reproduces plans and accounts correctly with available inputs.
2. **Manual portfolio:** real holdings/imports reconcile; user executes externally and records/imports fills. Recommendations identify whether a strategy is experimental or evaluated.
3. **Confirmed IBKR:** a specific account, instrument and adapter have passed integration checks; each order requires an unexpired explicit confirmation and current preflight.
4. **Evaluated strategy:** a versioned strategy has passed its preregistered investment-evaluation contract. An unevaluated strategy remains visibly experimental even if the broker integration works.

## 3. Goals and measurable outcomes

- Every plan has an immutable input manifest, policy and strategy version, evidence references, account context, freshness states, sizing derivation, horizon, review schedule and exit conditions.
- Identical input snapshot, configuration, strategy version and clock produce the same decision and semantic output hash.
- Every monetary balance, dividend and fill is traceable to ledger entries; no unexplained reconciliation difference is accepted.
- A correction to evidence, split, dividend, FX observation or policy can identify and invalidate every dependent active plan and unsubmitted order intent.
- New exposure cannot exceed shared account cash and risk reservations across sleeves, plans or concurrent processes.
- No subscription purchase or chargeable data request occurs in the default configuration. Provider capabilities and quotas are visible.
- US, European and ETF coverage is measured per capability; missing inputs stay unknown and prevent only the decisions that require them.
- Zero unconfirmed API submissions and zero duplicate external submissions in tested restart/timeout scenarios; an ambiguous external result is reconciled, never blindly retried.
- Report total return, drawdown, income, costs, FX attribution and contributions separately. Do not present contributions or dividends twice as investment gains.

## 4. Quality gates

These commands must pass for every completed story, from the appropriate directory:

- Formatting: run `gofmt -w` on changed Go files; confirm `gofmt -l` reports none of those files.
- From `app/`: `go vet ./...`.
- From repository root: `./build.sh`.
- From `app/`: `go test -race -shuffle=on ./...`.
- From repository root: `git diff --check` scoped to intended non-secret files; never inspect secret diffs.

Feature-specific gates:

- Property/invariant tests for ledger balance, fixed-point rounding, reservations, entitlement, and deterministic replay.
- Tenant/account isolation tests; account and paper/live identity checked at every order boundary.
- As-of tests excluding future publications, observations, mappings, strategy settings, classifications and broker state.
- Migration and restore drills on copies of real databases; preserve all legacy row counts and evidence. Interrupt after a verified durable write and resume; a timed kill with unknown write progress is insufficient.
- Broker contract/replay suite for partial fills, executions before acknowledgements, corrections, cancellation races, warning/reply flows, session expiry, maintenance, rejection and ambiguous submission.
- Held-out real-news evaluation by stage and event kind; freeze labels and thresholds before evaluating. An independent review process must be used for disputed labels. Existing synthetic tests still pass.
- Cost-aware, point-in-time walk-forward evaluation with investable benchmarks, survivorship checks and stress tests. Missing history yields `insufficient_history`, not a passing result.
- Interactive TUI QA at narrow and wide widths, with keyboard navigation, long names, terminal sanitization, explicit confirmation, errors, offline mode and restart. Automated snapshots supplement this check.
- Source/account integration gates are opt-in, use fixtures by default, and state when they were not run. Ordinary CI sends no broker orders and needs no secrets or paid services.
- No new code may silently enable network access from rendering, search completion, plan explanation, replay or offline evaluation.

## 5. Full feature set

### 5.1 Onboarding, investment policy and portfolio construction

Collect account currency, available investment cash, contribution schedule, investment goal and withdrawal dates, liquidity needs, acceptable losses, risk limits, tax residence, account type, broker permissions and instrument exclusions. Time zone alone does not establish tax residence. Record which fields are user statements, imported facts, estimates or unknown.

Separate emergency/non-investment money from investable capital. Show a five-year contribution and goal scenario with editable assumptions, not a promised endpoint. De-risking toward an approaching withdrawal date is a configurable policy and must account for available non-equity choices/cash.

The initial portfolio builder must:

- Compare a simple, eligible diversified core fund, a small individual-stock portfolio, and keeping cash/collecting contributions until fees become reasonable.
- Model broad funds and dividend-focused funds separately. A dividend tilt may increase sector concentration; it is not automatically lower risk.
- Prefer distributing share classes when the user wants cash income, while displaying accumulating alternatives and tax/cost trade-offs. Never call an accumulating fund's internal income a cash dividend to the account.
- Respect fractional eligibility, minimum order sizes, share steps and venue tick sizes. Never assume every IBKR instrument supports fractions.
- Optimize contribution allocation before selling existing holdings to rebalance; include taxes and costs when known.
- Expose issuer, sector, currency, country, instrument and sleeve concentrations. ETF look-through is used only with dated holdings; unknown look-through stays visible.
- Enforce issuer exposure across multiple listings and share classes, without treating them as unrelated diversification.
- Produce either a feasible allocation or a specific infeasibility explanation, rather than relaxing constraints silently.

### 5.2 Two investment sleeves

**Core/value/income:** intended ownership over months to years, typically a configurable 3–5+ year thesis horizon, bounded by the user's liquidity goal. Review quarterly, on filings/distribution changes, on major thesis events and when valuation/risk limits change. Review dates are not compulsory sale dates.

**Event-driven:** paper-first, normally several sessions to several weeks with a configured maximum holding period. Use eligible, understandable events, tradable liquidity and explicit invalidation. No same-headline instantaneous trade assumption. If free data cannot support timing, the plan remains a slower research/paper candidate.

Both sleeves share account-level constraints, reserves and execution costs. One broker position can contain distinct lot-level sleeves, but total exposure and sellable quantity are account-wide. Transferring a losing trade into the core sleeve requires a new thesis and a recorded user decision; never do it automatically.

### 5.3 Explainable Lynch/Buffett-inspired research

Use a dimensioned scorecard, not news count or a single unexplained AI score:

| Dimension | Required treatment |
|---|---|
| Business understanding | Business description, revenue drivers, competitive position, cyclicality, user circle-of-competence notes with cited sources |
| Financial quality | Multi-year cash generation, margins, returns on capital where inputs support them, earnings/cash conversion and dilution |
| Resilience | Debt maturity/liquidity, leverage and interest coverage; distinguish unsupported sector metrics from failed tests |
| Valuation | Bear/base/bull assumptions, comparable definitions, owner-earnings/DCF or relevant multiple range, sensitivity and margin of safety |
| Dividend quality | Sustainable ordinary payout, coverage, history, leverage and reinvestment needs; use section 5.4 |
| Governance/capital allocation | Sourced issuance, buybacks, acquisitions and insider actions; a purchase/sale is not automatically good/bad |
| Evidence and risks | Current source-backed claims, contradictions, independence, alternatives and reasons the thesis may fail |
| Portfolio fit | Cost, existing exposure, income preference, horizon and policy compatibility |

Provide separate templates for ordinary operating companies, banks/insurers, REITs and ETFs. For unsupported templates, offer research with a reason, not an ordinary-company ratio applied incorrectly. ETF evaluation uses cost, tracking/structure, domicile, distribution policy, diversification and eligible share class, not the fund's P/E as if it were a company's accounting earnings.

Qualitative moat/management claims are sourced annotations, not fabricated numeric facts. Exclude non-recurring accounting distortions only through explicit versioned adjustments. Do not annualize one dividend or one quarter without an identified rule. Negative/unknown denominators produce a labelled unavailable metric, not a favorable ratio.

All weights, lookbacks and thresholds are configurable and versioned. A policy preview shows which proposals change before activation. A user label can add a note or change a preference; it cannot convert missing evidence into verified data.

#### Starter strategy contract

Ship named, versioned example strategies so implementation does not invent a new financial rule for each screen. The following are **unvalidated initial research settings**, inactive until reviewed, with all numeric thresholds editable:

- `quality-income-core-v1`, ordinary operating-company template: at least three comparable completed fiscal years, positive most-recent earnings and free cash flow, positive free cash flow in at least two of those three years, sourced business-understanding checklist, and no unresolved current financial/identity contradiction.
- Example resilience limits for that template: net debt/EBITDA <= 3 and EBIT/interest >= 4 when those metrics have valid comparable positive denominators. Missing required inputs block this template; debt-free cases have an explicit exception with sourced cash/debt, not division by zero. Financial institutions and REITs use different templates.
- Dividend preference requires a declared ordinary distribution, known currency and credible payout history. Example payout ceilings: ordinary cash payout <= 80% of comparable free cash flow and <= 70% of earnings. A non-dividend business can qualify for the value strategy, but cannot receive the income-preference boost. Exceptional distributions do not qualify as sustainable ordinary yield.
- Example company score components: business understanding 20, financial quality 25, resilience 20, valuation 20, sustainable income 15 points. Each component is a published checklist of binary/graded tests, with an explicit point mapping; persist every test/result. Unknown required tests block eligibility; unknown optional tests remain unknown and receive no awarded points, with coverage printed. Do not renormalize missing components upward or compare company and ETF template scores as if equivalent.
- Example proposal threshold: >= 70/100 plus all mandatory checks. The score is a policy ordering, not a probability or evidence of outperformance. An otherwise eligible company outside the entry band is WATCH, not BUY.
- Entry ceiling comes from a versioned conservative valuation scenario minus a user-approved margin of safety (example 20%). Growth, discount rate, terminal assumptions, normalization and valuation method must be explicit; missing assumptions produce research-only output rather than an invented precise price. A maximum buy limit additionally respects cash/fees and current execution validation.
- Dividend timing is a tie-breaker among independently qualifying proposals, after portfolio fit and cost. Do not assign points merely for a high headline yield or a nearer ex-date.
- `diversified-core-fund-v1`: a separate eligible broad-fund template comparing diversification, documented ongoing costs, tracking/structure, domicile, currency exposure and distribution policy. It proposes allocation under the user's constraints; it is not forced through the company's earnings/payout template. Dividend-tilted funds are compared separately to a broad core alternative.
- `earnings-followthrough-paper-v1`: an initial explicitly experimental event template requiring a completed, source-verified earnings event attributed to the correct issuer; comparable financial results with no unresolved contradiction; a split-consistent daily-price/volume series; and an independent price-follow-through filter. Example filter: close above the previous 20 completed sessions' maximum close, with volume >= 1.5 times their mean. These are arbitrary testable settings, not a claimed earnings edge.
- That event template proposes entry no earlier than the next tradable session, uses the event-session low as an initial invalidation reference only when a valid lower price exists, and defaults to a 10-session review/time exit with a configurable 5–20-session range. A 2R profit-review level is optional; it is a rule, not an expected return. If chronology, liquidity, spreads, price coverage or affordable risk sizing fail, output paper/research-only or WAIT. No template infers positive price impact merely from a material event or a count of favorable words.

Implement score/test definitions in strategy code plus versioned configuration/schema, with fixture examples for every threshold and unknown state. Expose these values in policy preview. Do not activate an event strategy for real-account suggestions just because its synthetic rule tests pass; retain experimental status and the separately configured readiness/eligibility controls.

### 5.4 Dividend and income engine

Support ordinary cash dividends and ETF distributions first, with special, stock, scrip, return-of-capital and due-bill cases explicitly classified. For each observation keep security/share class, amount, currency, declaration date, ex-date, record date, payment date, distribution type, status, original source, observed/recorded times and revisions.

Features:

- Calendar for the next 7/30/90 days; early alerts when a declaration arrives, before the eligible purchase window, on cuts/cancellations, and when a payment is missing.
- Distinguish declared, estimated, historical and cancelled amounts/dates. Estimates never become confirmed income or an execution deadline.
- Flag candidates likely to be relevant to the user's income goals before their confirmed ex-date, but only after quality, valuation, affordability and policy checks. Earlier awareness does not mean rushing a purchase.
- Sustainable payout tests: ordinary dividends versus period-aligned earnings/cash flow, payment history, cuts, leverage and business-specific coverage. Sector-specific measures such as AFFO require sourced comparable inputs.
- Distinguish trailing ordinary yield, declared-forward yield, special distributions, yield on cost and portfolio income. High yield receives a risk diagnostic, not an automatic rank boost.
- Gross, actual withheld and actual net income; estimated net income only under an explicit user-configured tax scenario. Unknown residence/treaty/account status means net estimates remain unavailable or clearly conditional. No fabricated tax reclaim or tax-return advice.
- Project income for held quantities and proposed allocations separately, with schedule and currency assumptions. Display income in EUR and original currency, expected versus received.
- Entitlement derives from actual eligible fills and event terms, not an unfilled order, plan approval or record date alone. Use authoritative announced ex-dates, venue calendars and broker-specific fractional treatment where available.
- Corrections and split adjustments must not double-count payments. A receivable is not settled cash. Upon payment, transfer receivable to cash; do not book income a second time.
- Cash reinvestment, broker DRIP and contribution-batched reinvestment are separate modes. Record DRIP fills and fees; no assumption of commission-free reinvestment.
- Ex-date price movement is part of total return. Dividend capture is not assumed to create free profit. Do not require ownership until payment date where ordinary entitlement rules do not require it.
- Special/due-bill and other exceptional cases require exact terms; if unsupported, disable automated entitlement advice for that event while keeping it visible for manual review.

For ordinary US cash dividends, buying on/after the ex-date normally does not earn the next payment; special distributions can follow different rules. Do not generalize US settlement/date rules to all European venues. Use section 15 sources and contract fixtures.

### 5.5 Plan decision pipeline

Keep eligibility, evidence, valuation, risk and action separate:

1. Freeze an as-of snapshot: stored evidence sequence, observations/revisions, prices, FX, corporate actions, portfolio version, account capabilities and configuration.
2. Resolve instrument identity, listing, currency, share class and broker contract; flag ambiguity.
3. Check data availability, age, currency/adjustment basis, source rights and corporate-action completeness for the intended calculation.
4. Build a thesis scorecard and scenario valuation using only the snapshot's information.
5. Generate raw actions for unheld and held instruments: `BUY`, `ADD`, `HOLD`, `REDUCE`, `SELL`, `WATCH`, `WAIT`, `ABSTAIN`.
6. Apply dividend preference after mandatory quality/risk criteria. An approaching ex-date cannot override affordability or data quality.
7. Allocate globally across candidates, existing holdings and reservations; recompute marginal diversification and fees. Sorting isolated candidates is not portfolio construction.
8. Produce entry bands, position-size bounds, expected holding/review horizon, invalidation, exit rules and expiry.
9. Persist the plan revision and full explanation; compare to the previous revision and notify only on material changes.
10. Refresh execution preflight at confirmation; a stored plan is not a standing authorization.

The proposal rank is an ordinal policy ranking, not a return probability. Do not emit probabilities or probability-weighted expected returns until that model and horizon are independently calibrated. Scenario outcomes and user assumptions are allowed if labelled as such.

Use separate fields for `action`, `data_state`, `strategy_state` and `execution_eligibility`. A `SELL` thesis review can exist with stale prices, but must not invent current proceeds; missing data never turns into `HOLD` or an automatic liquidation. An unheld instrument cannot receive `ADD`, and sell quantities cannot exceed confirmed unreserved holdings.

### 5.6 What every plan card must contain

- Plan/revision ID, as-of and generation times, expiry and input manifest hash.
- Exact security/listing/share class, broker contract if resolved, account, sleeve and strategy version.
- Action, execution eligibility and specific reasons/unknowns.
- Business thesis, supporting and contrary evidence, evidence excerpts/links and source dates.
- Quality, dividend, valuation and portfolio-fit components; how preference changes affected ranking.
- Entry price range/maximum, quantity/notional range, original currency and EUR estimate, cash reserve and before/after concentration.
- Fee, spread, FX and tax assumptions separately; unavailable estimates stay explicit.
- Bear/base/bull valuation or outcome scenarios, with assumptions and sensitivity. They are not guaranteed targets.
- Holding horizon range, next review, catalyst/deadline and conditions for extension.
- Thesis invalidation, reduction/sell rules and priority, including what requires human judgment.
- Dividend schedule and entitlement status where relevant; projected annual ordinary income versus next declared payment.
- What would change the decision and why alternative candidates/cash were preferred or rejected.

Lifecycle: `draft -> proposed -> accepted | rejected | expired | superseded`; accepted plans can create order intents. Positions and order states are separate lifecycles. Refreshes append revisions and cannot silently rewrite an accepted thesis.

### 5.7 Buy, hold and sell framework

**Buy/add:** financial quality and business-understanding requirements pass; valuation is inside a justified entry band; portfolio improves or remains within limits; costs are acceptable; data supports the decision. Stage entries only if resulting commissions and minimums permit it. Falling prices alone do not justify averaging down.

**Hold:** thesis is intact and expected benefit of retaining the position exceeds the specified alternative after switching costs, within policy. No requirement to sell a good business merely because a target date arrived. Show overdue reviews distinctly from fresh hold decisions.

**Reduce/sell, ordered by reason:**

1. User withdrawal/liquidity need or explicit user instruction.
2. Confirmed thesis break, permanent impairment, dividend cancellation/cut inconsistent with the recorded thesis, material governance deterioration or credit risk.
3. Account/issuer/sleeve concentration breach; propose the smallest feasible risk reduction and account for pending orders.
4. Valuation no longer offers acceptable prospective return under the user's assumptions; compare alternatives net of costs and known taxes.
5. Event trade invalidation, catalyst completion, configured maximum holding period or tested exit trigger.
6. Scheduled rebalance/contribution allocation when benefits exceed a configured friction threshold.

A dividend cut triggers a thesis review, not an unconditional sale in every strategy. Sell explanations must identify the rule and conflicting conditions. A technical stop is an execution/risk instruction, not proof that a company is impaired. Stops can gap and do not guarantee a maximum loss. Never extend a stop/time limit merely to avoid recognizing a losing trade.

### 5.8 Position sizing, costs and constraints

Use exact decimal/fixed-point money, price and quantity types with explicit currency, instrument precision and rounding; existing market-price float storage can remain an ingestion input, converted at a defined validated boundary. No binary-float cash ledger.

Let `E` be account equity in base currency, `C` available settled spendable cash after open-order reservations, fees and the user reserve. Let `P`, `r` and `d` be explicit user policy limits, not learned profit promises.

- Value/income size is bounded by available sleeve allocation, account/issuer limits, cash and liquidity/lot constraints. Do not create a fake stop distance merely to compute it.
- For event trades with an evidenced invalidation level, planned loss per unit is entry minus invalidation plus cost allowance; proposed quantity is at most `E * r / planned_loss_per_unit`, additionally bounded by the other constraints. Convert currencies with the stated rate basis. This is planned loss, not guaranteed maximum loss.
- Where invalidation is qualitative, use capped notional and stress scenarios; mark stop-based loss sizing unavailable.
- Reserve a worst-permitted limit-order notional plus fee/FX buffer, using broker account rules and lot precision. Do not use an optimistic historical price for a cash reservation.
- Include known commission minimums, exchange/transaction taxes, spread assumptions, fund expenses and FX conversion costs. Chargeable snapshots count as data spend.
- Unknown trading costs mean no claim that a small trade is affordable or has a positive net edge. The user may enter a sourced schedule/estimate and regenerate.
- A cost/benefit screen can reject a trade or batch contributions. Do not compare fees to a made-up expected return.

Illustration only: EUR 1,000 at an assumed 3% annual gross yield produces EUR 30 before taxes/costs, if the payout and value assumptions hold. A EUR 5 monthly service is EUR 60 annually, 6% of initial capital before trading costs. This explains the zero-subscription default; it is not a return forecast.

### 5.9 Configurable progression tiers

Separate **operational permissions** from **investment-risk limits**. More reliable order handling does not justify more investment risk. Winning trades alone never unlock a tier. Every change requires a preview, recorded acknowledgement, policy revision and regeneration of affected plans.

Example template, inactive until reviewed by the user:

| Tier | Permissions and intent | Illustrative constraints |
|---|---|---|
| 0: Learn/build | Paper, research, onboarding and initial portfolio design | No live API submission; compare cash and diversified core; event sleeve paper only |
| 1: Conservative ownership | Manual real portfolio, reconciliation and contribution planning | Long-only/cash-funded; event real allocation 0%; example cash reserve 20%, individual issuer cap 10%, direct-equity sector cap 25% |
| 2: Confirmed execution | IBKR with confirmation; independently eligible small event sleeve | Event real allocation up to 5%; example per-event planned risk 0.25% of account equity; all account limits still apply |
| 3: Broader discretionary | Expanded user-approved limits after readiness review | Event real allocation up to 10%; example per-event planned risk 0.5%; no automatic margin, derivatives or shorting |

These numbers are editable starting examples, not calibrated, guaranteed-loss or universally low-risk settings. A diversified equity fund can suffer a large drawdown; core-fund caps are distinct from direct-issuer caps and unknown look-through must not be hidden. At small capital, fee and minimum-size constraints may make all event trades infeasible.

Unlock readiness includes completed configuration, reconciled real/paper records, no unresolved broker discrepancies, passed failure scenarios, adequate strategy/data evaluation for the requested use and explicit user risk acceptance. An illustrative observation floor is 30 completed market sessions for operational review; it is not enough by itself to establish investment skill or strategy edge, and must not encourage unnecessary trades. Tier 3 needs a new review and explicit policy, not merely elapsed time.

Policy drawdown thresholds, stale/unhealthy execution inputs, failed reconciliation or account-permission changes suspend **new risk** and revoke unsubmitted confirmations. Existing protective orders, open orders and positions remain visible; do not cancel protective orders or liquidate simply because the app becomes unhealthy. Risk reduction/cancellation must be explicit, account-correct and executable through the broker's normal controls.

### 5.10 Portfolio ledger and accounting

Tenant-owned accounts contain separate real, local-paper and broker-paper books. Store immutable journal transactions and entries with currency, source/import ID and event time/recorded time. Support deposits, withdrawals, buys, sells, commissions, fees, FX conversions, dividend receivables/payments, withholding, interest, splits, DRIP, transfers and corrections/reversals. Balanced accounting includes securities/cost basis and realized gain entries under documented conventions; multi-currency transactions retain native amounts and explicit FX legs.

Track tax lots, open-order reservations, unsettled versus settled cash, fraction precision and statement reconciliation. Cost-basis method is explicit and reporting-oriented; do not claim jurisdiction-compliant tax reporting from a configurable FIFO display.

Performance includes realized/unrealized gains, income, trading/FX costs, time-weighted return, money-weighted return with supported-solver diagnostics, drawdown and matched-cash-flow benchmark comparison. Handle deposits, stale marks, missing FX, zero/negative valuation bases and multiple/no IRR solutions explicitly. Daily total-return series must not count both adjusted-close dividends and ledger dividends. Use raw/split-normalized prices plus separate cash distributions under one documented convention.

Corporate actions not yet automatically supported create reconciliation tasks and block affected computations/orders as needed; they must not erase holdings. Merge/spin-off/delisting/manual action adjustments retain links and reversal history.

### 5.11 IBKR and execution

Choose the retail Web API/Client Portal Gateway adapter as the first broker implementation, subject to verification with the user's actual account. Keep transport/authentication behind a broker interface; use the documented supported retail flow. Do not assume OAuth onboarding, paper access, real-time quotes, API access, fractional orders or a particular order type are available to every account. The official documentation reviewed for this plan specifies a funded IBKR Pro live account for individual Web API access, including its associated paper account; reverify before implementation.

Support:

- Explicit setup/status with session, read-only/trading mode, paper/live account, entitlements and permission checks; human login/2FA where required, no bypass or password handling shortcuts.
- Instrument resolution to broker contract plus security/listing/currency, with primary venue and order routing separated.
- Read-only sync of accounts, balances, positions, open orders and executions; stable import IDs and statement comparison.
- Local paper engine independently of IBKR paper access; imported manual fills remain first-class.
- Initial live order subset: cash-funded long-only limit DAY orders during supported regular sessions, at valid quantity/tick precision. Broader types require separate explicit capability and tests.
- An order ticket shows account/environment, contract, side, quantity, limit, estimated maximum cash commitment, known fees, data delay, market session and policy checks. No one-key action from a news row submits an order.
- Confirmation is bound to a hash of exact order terms, account, plan revision, policy and reservation; it expires. Any change to those terms requires reconfirmation.
- Broker warning/reply messages are displayed for explicit acknowledgement; never suppress/automatically approve them as a shortcut.
- Preflight rechecks current broker positions/cash/orders, account permissions, plan expiry, reservations, price collar and source freshness. A stale quote cannot masquerade as current. If current executable quote validation is unavailable, keep API submission unavailable with a reason and allow a manual order workflow; a delayed planning close is not execution-grade data.
- Durable local intent/reservation before external submission; broker reference/correlation IDs; reconcile external state after timeout before considering a retry. Do not claim exactly-once broker execution from a local transaction.
- Order state captures draft, confirmed, submitting, acknowledged/open, partially filled, filled, cancel requested, cancelled, rejected, expired and unknown/reconciliation-required. Fills can arrive before acknowledgements, after cancellation requests, or as corrections.
- Explicit cancel/replace with fresh confirmation for changes that add risk; no assumption cancellation succeeded until broker confirmation/reconciliation. Release only truly unused reservations.
- A durable new-order-disable control, clear broker-session health and restart reconciliation. Broker-held orders survive process exits; local alerts are not broker-held protection.

Credentials/tokens stay in existing secret mechanisms or OS keychain, not config exports, the ledger, logs or Git. Bind a local gateway to loopback; verify TLS/certificate configuration rather than adding a global insecure-skip option. New order submission is disabled by default after installation and restart until broker state has reconciled.

### 5.12 TUI, CLI and daily workflow

Add **Plan**, **Portfolio** and **Income** sections using the existing keyboard-navigation model. On narrow terminals, make sections scroll/selectable rather than inaccessible. Preserve News/Stocks research and watch semantics.

- Plan: actions/reviews due first, then buy candidates, watch/blocked items, cash/contribution proposal, tier/policy status and change history. Filters: account, sleeve, action, market, currency, dividend status and data readiness.
- Portfolio: holdings/lots, allocation, cash/reservations, costs, income, risk, performance and reconciliation exceptions.
- Income: declared upcoming events, entitlements, estimated schedule, received payments, cuts and missing receipts.
- Plan detail: complete card from section 5.6; keyboard access to evidence, what-if parameters, previous versions and exact calculation inputs.
- Settings: versioned policy editor with validation and before/after proposal diff; reset to example profile never silently activates higher risk.
- Orders: draft ticket, explicit confirm, status, cancel/replace, ambiguous outcome and reconciliation view. Clearly distinct paper/live environments throughout.
- Full names, rationale and identifiers remain accessible without abbreviation. Use horizontal scrolling or expanded detail; no hidden essential order terms. Colors are semantic and accompanied by words; no purchase-green/sale-red judgment.
- Alerts: materially changed actions, thesis invalidation, review due, dividend declaration/ex-date/payment, policy breach, missing data, missing income and broker exceptions. Deduplicate by plan/event revision and rule, honor quiet hours except user-configured critical operational alerts.
- Export a plan/portfolio/income report in Markdown and versioned JSON, excluding secrets and respecting tenant source visibility/licensing.

Daily workflow: inspect health and reconciliation -> review changes/holdings -> inspect next income dates -> review ranked proposals -> run what-if -> accept/reject -> paper/manual/confirmed ticket -> import/reconcile fills -> journal reasoning -> review outcome on its actual horizon.

Proposed commands (implement help, exit codes and JSON schema consistently):

```text
stoxicity portfolio init|show|lots|cash|import|reconcile|performance
stoxicity policy show|check|preview|activate
stoxicity plan build [-account ID] [-sleeve core|event|all] [-at TIME]
stoxicity plan list|show|diff|accept|reject|review|export
stoxicity income calendar|quality|entitlements|received
stoxicity broker status|connect|sync|contracts
stoxicity order draft|preview|confirm|submit|list|show|cancel|replace
stoxicity paper run|fills|reset
stoxicity eval strategy|portfolio
stoxicity doctor [-scope research|portfolio|broker]
```

`plan build` uses stored snapshots and never places orders. `-at` cannot submit orders or read today's broker state as if it were historical. `paper reset` requires an explicit book ID, archives history and cannot affect real accounts. Offline commands must not open/migrate an unrelated real book as a side effect.

## 6. Free-data and market coverage contract

Do not promise universal free live data or complete European fundamentals. Implement a capability registry per provider/market/instrument/data type, with documented rights, source URL, quota, latency, history depth, currencies, cost and verification date. A free API key is acceptable if the free entitlement is documented and optional; no automatic upgrade, scraping around access controls or hidden fallback to a billed endpoint.

| Need | Initial path | Required fallback/state |
|---|---|---|
| US filings/fundamentals | Existing SEC clients, expanded capability checks | Missing/late facts and coverage explicit; quota-aware background reads |
| EUR reference FX | ECB dated observations for reporting/scenarios | Not an executable broker FX rate; actual conversions use broker fills |
| Prices | Existing documented API when user has usable free entitlement; entitled IBKR observations where supported | Dated CSV/manual observation with provenance; no free coverage -> no price-dependent recommendation |
| European instruments | Verified broker contracts/listings, ISIN where available, MIC, currency/quote unit and issuer identity | Explicit curated/imported instruments, not a claim of a complete European master |
| European fundamentals | Issuer/regulatory documents and normalized source-linked imports; documented public machine-readable feeds when verified | Include as research-only when metrics are not sufficiently comparable/complete |
| Dividends/distributions | Issuer/fund declarations and official venue/broker observations where accessible | Source-linked structured manual import with revision/reconciliation; conflicting dates block entitlement advice |
| ETF metadata/holdings | Dated issuer factsheets, distribution/KID metadata and supported public files | Unknown fees/holdings/eligibility explicit; no invented look-through |
| Settlement/calendar/halt | Dated venue rules and calendar overrides; broker session status for execution | No unobserved halt presented as observed open status |

European scope includes a tested EUR equity, an eligible UCITS ETF share class, and at least one non-EUR quote-unit case (for example pence versus pounds) in fixtures and supported import paths. Broker account permissions and relevant product documents determine actual trade eligibility; availability in a catalogue is not proof a retail account can buy it. No blanket rule that every US-listed ETF is accessible to every European account.

At least one real documented free price path for the user's selected instruments must be source-verified before claiming automatic daily price coverage. If that is unavailable, the manual/import path is usable but automatic coverage is explicitly pending. Import-only support must be labelled import-only.

Track source completeness, successful fetches and data age separately. An empty successful response is not proof of no dividend/news. Followed holdings and held securities receive quota priority; cache raw responses with rights-aware retention, backoff and resumable checkpoints. Publish endpoint changes and source failures in doctor/status.

## 7. Architecture and persistence

Preserve the modular Go/SQLite architecture. Use narrow interfaces and pure calculations; do not build a separate microservice platform for a personal EUR 1,000 portfolio.

```text
shared base
  observations/events/security identity + dated prices/FX/distributions
              |
       immutable as-of snapshot
              |
tenant/account policy + portfolio ledger + broker capabilities
              |
    research scorecards -> strategies -> constrained allocator
              |
       versioned plans + review rules
              |
 manual journal | local paper | confirmed broker intent/outbox
              |
      fills / income / statement reconciliation -> ledger
```

Suggested package seams, names to reconcile with actual repository conventions:

- `app/src/money`: fixed-point amounts, quantity/price rounding and currency/quote units.
- `app/src/fx`: observed rates, source provenance and as-of conversion; reporting versus execution rates.
- `app/src/income`: dividend observations, quality, entitlement, receivables and schedules.
- `app/src/portfolio`: accounts, immutable journal/lot projections, reservations, allocations and returns.
- `app/src/policy`: versioned investment constraints, eligibility, tiers and change preview.
- `app/src/research`: sourced scorecards and valuation scenarios; keep separate from existing class-capitalization calculations in `valuation`.
- `app/src/plan`: snapshot, strategy interface, proposals, allocation, explanations and review lifecycle.
- `app/src/broker` and `app/src/broker/ibkr`: capabilities, contracts, sync, order lifecycle and adapter.
- `app/src/paper`: deterministic simulated execution using the same intent/fill contracts.
- Extend existing `marketdata`, `exchange`, `store`, `workspace`, `channel`, `eval`, `nav`, `tui` and `cmd/stoxicity` at their existing boundaries.

Core interfaces should be shaped around use cases: `SnapshotReader`, `Strategy.Evaluate(snapshot, policy, portfolio)`, `Allocator.Allocate(proposals, constraints)`, `PortfolioLedger`, `BrokerCapabilities`, `OrderPreview`, `OrderSubmitter`, `ExecutionReconciler`. Keep a read-only broker client separately usable from a trading client. Inject the clock, deterministic seed where applicable, and context cancellation.

Shared-base additive tables: FX observations/revisions, distributions/revisions and evidence, provider capabilities/verification, additional instrument/listing/quote-unit facts. Reuse existing security IDs and observation/revision conventions. Do not duplicate current event evidence into a second mutable source of truth.

Tenant additive tables: accounts, portfolio books, immutable journal transactions/entries, lot lineage/projections, imports and cursors, policy versions/activations, tier decisions, plan snapshots/revisions/evidence links, user decisions/reviews, order intents/reservations/status events, broker references/executions/revisions, reconciliation runs/differences and alert outbox state.

Journal and order events are append-only; derived projections can be rebuilt. Enforce uniqueness on tenant/account/environment/external import IDs and durable intent keys. Store exact schema/config/strategy/code hashes, publication/receipt/recorded times and as-of basis in manifests. Hash the keyword tables as well as configuration: current lens versioning does not cover those rules.

The shared base and tenant DB are separate transaction domains. Generate plans against stable read snapshots and atomically persist an input high-water mark/hash with the tenant result; detect source changes. Perform reservations and intent persistence in the same tenant transaction. External broker calls occur after durable intent commit; use an outbox/reconciliation protocol, not a pretend cross-database/broker transaction.

Migrations are additive, independently numbered for base and tenant; choose numbers from the actual current tree at implementation time. Preserve prices with unknown historical fetch times as unknown. Never backdate newly computed plans, revised mappings or rebuilt event decisions to make backtests appear historically available.

## 8. Evaluation framework

Three distinct reports:

1. **News interpretation:** extraction, polarity, attribution, subject/counterparty, temporal status, event naming and abstention on independent held-out articles. Contradictions and multilingual/paraphrase cases have dedicated slices.
2. **Decision process:** data completeness, policy adherence, explanation coverage, stability, sensitivity, affordable position construction and review timeliness.
3. **Investment outcomes:** net return, total-return income, drawdown, turnover, costs, exposure, adverse outcomes and suitable benchmark comparison at each strategy horizon.

Historical evaluation requirements:

- Walk forward with frozen train/tune/test boundaries; record every tested strategy/parameter family and reserve untouched periods. Do not optimize against the final test set.
- Use point-in-time universe, delisted/failed securities, filings, currencies and corporate actions. Report survivorship and coverage bias if unavailable.
- Separate **installation-known replay** from **public-history reconstruction**. A filing that was public earlier but ingested today may appear in the latter only, labelled correctly; it was not known to this installation then.
- Start executable paper fills no earlier than the next permitted session/bar after the information/decision becomes available. No same-bar lookahead, guessed limit fills from daily OHLC, or hindsight-adjusted histories masquerading as tradable prices.
- With daily bars, use documented conservative fill rules; if a bar hits both entry and stop/target, apply a conservative sequence or mark ambiguous. Account for gaps, liquidity, partial fills and delistings; never assume a stop's exact price.
- Include distributions, withholding scenarios, FX, commissions/minimums, spread/slippage sensitivity and cash interest only if actually available/assumed explicitly.
- Compare to cash and an eligible diversified fund baseline, with matched contributions, currency, fees and distribution treatment. Evaluate dividend preference versus a comparable no-tilt portfolio.
- Report uncertainty intervals with dependence-aware methods, period/sector breakdown, sample count, horizon maturity, worst outcomes and effects of parameter changes. Do not treat overlapping trades as independent observations.
- Preregister strategy-specific thresholds, downside limits, required data coverage and sample sufficiency before holdout evaluation. A profitability/edge gate needs uncertainty-aware net-of-cost evidence, not a positive point estimate. Insufficient evidence is a valid result.
- Forward paper observation records every proposal, rejected/blocked/no-trade decision and later revision; no cherry-picking only executed winners. Core five-year claims cannot be proven by a few weeks of paper results.

The existing dataset begins around November 2026 and cannot by itself supply years of point-in-time validation. The implementation must accept externally sourced documented history, but unavailable history is a named blocker for validation, not something generated from synthetic candles. No model-driven claim reader or return forecaster is enabled by this PRD. Existing advisory-only Laya behavior remains the default; any future model must pass independent stage-specific gates.

## 9. Operational production requirements

- Reproducible builds with version/code/dependency/config hashes and platform artifacts; vulnerability/dependency scan and release notes.
- User-service setup for macOS/Linux with graceful stop/restart, single-writer/job leases where required, versioned config validation and resumable jobs.
- Structured, redacted logs and health metrics for ingestion lag, reconciliation backlog, data age/coverage, plan jobs, ledger differences, outbox backlog, broker state and duplicate prevention.
- Alerts include context and runbook action; no silent empty screens on provider failure.
- Bounded disk/cache retention, low-disk handling, SQLite busy/retry policy, integrity checks and maintenance documented.
- Backups: scheduled online backups and verification, configurable rotation, protected permissions and optional encrypted independent/off-device destination. Individual online SQLite backups are not automatically a consistent multi-database portfolio snapshot: stop writers or use a documented generation/checkpoint and restore/reconcile protocol. Harden existing row-count comparison for concurrent writes; do not misreport a coherent copy as corruption solely because the live source changed afterward.
- Restore drill starts from an independently verified snapshot, reconstructs projections and reconciles broker-side orders/fills before new submissions. Never restore old broker state and blindly resend intents.
- Error budgets and initial engineering targets: on a declared reference machine with a fixed fixture of at least the current approximately 12,000-security catalogue, cached TUI interactions p95 <= 100 ms; no network in render; interactive rebuild ceiling <= 250 ms measured consistently with existing performance tests. A 100-candidate stored-snapshot plan build target is <= 5 s; benchmark without provider latency and state the hardware/instrument mix.
- Recovery targets for local records: no loss of acknowledged local durable transactions under process crash; latest verified daily independent backup RPO <= 24 h for device failure; documented local restore/reconcile target <= 30 min on the reference fixture. Broker-originated transactions can be recovered only within the broker/reporting history actually available. Measure these targets rather than declaring them by configuration.
- New-risk suspension on bad execution health; read-only research, evidence access and manual broker controls remain accessible where possible.

## 10. Implementation stories and dependency order

Each story must be independently reviewable and satisfy section 4. Split a story further if its verified change exceeds one focused implementation session. `Depends` establishes execution order; all belong to one coordinated release.

### US-001: Capability inventory and production doctor
**Description:** As an operator, I want an accurate capability inventory so that I know which workflows are available.
**Depends:** none.
**Acceptance criteria:**
- [ ] Inventory current shared/tenant migrations, sources, stale README claims, rule-version gaps and existing working-tree edits without touching secrets/live data.
- [ ] Define readiness/data-state enums and a machine-readable doctor report; fixtures distinguish implemented, configured, verified, stale, unsupported and unknown.
- [ ] Define deterministic reference fixtures and benchmark hardware/report format before performance comparisons.

### US-002: Exact financial primitives
**Description:** As a portfolio owner, I want exact monetary accounting so that cash and quantities reconcile.
**Depends:** US-001.
**Acceptance criteria:**
- [ ] Money, FX direction, quote units, quantities, tick/lot precision and rounding are explicit; invalid/overflow/NaN inputs are rejected at ingestion boundaries.
- [ ] Property tests cover balanced entries, EUR/USD round trips under stated rounding, pence/pound scaling, fractional quantities and price tick rules.

### US-003: Policy, onboarding and revision preview
**Description:** As an investor, I want editable goals and constraints so that proposals fit my circumstances.
**Depends:** US-002.
**Acceptance criteria:**
- [ ] Persist account/goal/contribution/risk/income preferences and unknown tax/permission fields with provenance.
- [ ] Example profiles remain inactive until reviewed; invalid constraints and infeasible allocations have precise errors.
- [ ] Preview/activate creates policy versions and invalidates affected proposals/confirmations; no future policy leaks into replay.

### US-004: Multi-market identity and capability registry
**Description:** As an investor, I want US and European listings resolved correctly so that I evaluate the intended instrument.
**Depends:** US-001, US-002.
**Acceptance criteria:**
- [ ] Resolve stable security, listing, venue, share class, currency/quote unit, and ISIN/broker contract where supplied; ambiguity is explicit.
- [ ] Capability/rights/quotas/cost metadata exist per data type; paid endpoints are disabled by default.
- [ ] Fixtures cover a US stock, EUR stock, UCITS distribution/accumulation share classes, dual listing and non-EUR subunit quote.

### US-005: FX and price source integration
**Description:** As a EUR investor, I want sourced prices and currency conversion so that valuations and costs are comparable.
**Depends:** US-002, US-004.
**Acceptance criteria:**
- [ ] Store dated/public/received FX observations and quote latency/adjustment provenance; ECB reporting rates are distinct from broker fills.
- [ ] Provide a documented free/entitled price adapter and structured import path, with capability checks and no billed fallback.
- [ ] Reject stale/conflicting/unit-ambiguous values for affected actions; correct as-of queries and GBP/GBp cases are tested.
- [ ] Publish which real endpoints were contract-tested and which coverage remains import-only/unavailable.

### US-006: Dividend observations and quality
**Description:** As an income-oriented investor, I want reliable distribution data and sustainability analysis.
**Depends:** US-004, US-005.
**Acceptance criteria:**
- [ ] Append declared/estimated/cancelled distribution observations with exact share class, type, dates, source and revisions.
- [ ] Calendar and quality metrics distinguish ordinary/special payouts, comparable periods, unsupported sectors and unknown coverage.
- [ ] Import/revision tests cover a cut, cancellation, split, conflicting ex-date and accumulating ETF; no estimate becomes confirmed cash.

### US-007: Accounts and immutable portfolio ledger
**Description:** As an owner, I want real and paper accounts kept accurately and separately.
**Depends:** US-002, US-003.
**Acceptance criteria:**
- [ ] Add tenant/account/environment-isolated journal/lot/import schema and transactions for section 5.10.
- [ ] Idempotent fills, reversals, transfers, fees, unsettled cash and FX legs rebuild to the same balanced projections.
- [ ] Oversells, cross-tenant reads/writes and real/paper reset contamination are rejected in adversarial tests.

### US-008: Dividend entitlement and income accounting
**Description:** As an owner, I want expected and received income to match actual holdings and event terms.
**Depends:** US-006, US-007.
**Acceptance criteria:**
- [ ] Entitlement tests include pre/on/post ex-date fills, unfilled orders, partial fills, sale after ordinary entitlement, fractional treatment and correction.
- [ ] Receivable/payment/withholding/DRIP reconcile without double income; unsupported due-bill cases remain explicit.
- [ ] Gross/net scenarios distinguish unknown tax status from actual broker withholding and payments.

### US-009: Fundamentals and sourced research scorecards
**Description:** As an investor, I want understandable business and financial assessments before a buy proposal.
**Depends:** US-004, US-005, US-006.
**Acceptance criteria:**
- [ ] Multi-year metrics, source-linked manual/European imports, sector templates and qualitative annotations preserve provenance and missingness.
- [ ] No ordinary-company ratios for unsupported funds/banks/REITs; negative/unknown denominator and period/currency mismatches have fixtures.
- [ ] Keyword tables/extractor/rule versions participate in content hashes and plan manifests.

### US-010: Valuation scenarios and core strategy
**Description:** As a long-term investor, I want price-aware quality/income proposals with explicit assumptions.
**Depends:** US-003, US-009.
**Acceptance criteria:**
- [ ] Bear/base/bull valuation, sensitivity and entry bands derive from documented inputs; no uncalibrated win probability.
- [ ] Core proposal includes thesis, counterevidence, dividend preference and qualitative invalidation; news volume alone cannot produce BUY.
- [ ] Cash and a diversified eligible fund are comparison alternatives; no-data cases produce WATCH/ABSTAIN with specific reasons.

### US-011: Initial portfolio and contribution allocator
**Description:** As a small-account investor, I want a feasible portfolio instead of an unaffordable list of stocks.
**Depends:** US-003, US-007, US-010.
**Acceptance criteria:**
- [ ] Allocate across proposals with issuer/sector/currency/sleeve/cash constraints, fractions/minimums and realistic fees.
- [ ] EUR 1,000 fixtures include feasible allocation, fee-driven batch/wait, unknown fund look-through and no feasible purchase.
- [ ] Contributions are considered before sales; no automatic transfer of event risk into the core sleeve.

### US-012: Immutable plan snapshots and explanations
**Description:** As an investor, I want every recommendation reproducible and reviewable.
**Depends:** US-005, US-008, US-010, US-011.
**Acceptance criteria:**
- [ ] Persist full card/manifest, lifecycle, exact strategy/policy/account/input versions and evidence relationships.
- [ ] Identical frozen inputs produce identical semantic outputs; as-of generation excludes later inputs/mappings/decisions.
- [ ] Material revision diff and downstream invalidation work without rewriting accepted historical plans.

### US-013: Hold/reduce/sell and review lifecycle
**Description:** As an owner, I want clear monitoring and exit conditions throughout ownership.
**Depends:** US-012.
**Acceptance criteria:**
- [ ] Implement ordered exit/review rules from section 5.7, with quantities bounded by actual free holdings.
- [ ] Missing data, ordinary dividend cut, thesis break, concentration, approaching goal, and event-time expiry produce distinct reasons.
- [ ] Review date does not force a core sale; a late review is visibly overdue rather than a fresh HOLD.

### US-014: Shared reservations and order intent state machine
**Description:** As an owner, I want concurrent proposals prevented from spending the same money.
**Depends:** US-007, US-012.
**Acceptance criteria:**
- [ ] Durable intent and cash/quantity reservation commit atomically in the tenant DB and respect shared account limits.
- [ ] State machine covers section 5.11 and out-of-order events; no external call occurs before durable intent.
- [ ] Concurrent process, crash, partial-fill and cancel-race tests preserve cash/quantity invariants.

### US-015: Local paper engine
**Description:** As an investor, I want to practice the complete workflow without a funded broker account.
**Depends:** US-008, US-013, US-014.
**Acceptance criteria:**
- [ ] Use the real proposal/intent/fill/ledger contracts in an isolated paper book.
- [ ] Document conservative daily-data fills, costs, gaps and ambiguous bars; no same-bar/limit-touch guaranteed fills.
- [ ] Seed/clock/replay are reproducible; reset archives only the specified paper book.

### US-016: Tier progression and suspension
**Description:** As an investor, I want deliberate progression and adjustable risk without automatic escalation.
**Depends:** US-003, US-013, US-015.
**Acceptance criteria:**
- [ ] Separate operational permissions and investment limits; user approval and versioned readiness evidence required for activation.
- [ ] Profit, elapsed time or a streak alone cannot unlock a tier; illustrative thresholds are labelled and editable.
- [ ] Health/drawdown/permission changes revoke new-risk eligibility and unsubmitted approvals without erasing holdings or cancelling protective orders.

### US-017: IBKR connection and read-only reconciliation
**Description:** As an IBKR user, I want my actual account and contracts verified before trading.
**Depends:** US-004, US-007, US-014.
**Acceptance criteria:**
- [ ] Implement supported retail session flow, redacted status, explicit account/environment and contract resolution.
- [ ] Sync balances, positions, open orders and executions idempotently; mismatches are actionable records, not overwritten local assumptions.
- [ ] Session competition/expiry, pacing/maintenance and unsupported entitlements have contract fixtures; real-account verification status is separately reported.

### US-018: Confirmed broker order preview and submission
**Description:** As an IBKR user, I want exactly the order I reviewed submitted only after confirmation.
**Depends:** US-014, US-016, US-017.
**Acceptance criteria:**
- [ ] Explicit limit-order preview includes exact terms, freshness and account; confirmation binds the payload/version and expires.
- [ ] Preflight invalidates changed plans/cash/permissions/quotes; warnings require explicit acknowledgement.
- [ ] Ambiguous timeout enters reconciliation-required with no blind retry; restart cannot duplicate external submission.
- [ ] Default/offline/test mode never submits an external order; paper/live separation is checked at adapter boundaries.

### US-019: Broker order monitoring and cancel/replace
**Description:** As an owner, I want fills and cancellations reflected correctly after failures and races.
**Depends:** US-018.
**Acceptance criteria:**
- [ ] Handle partial fills, executions before ack, fills during cancellation, rejection, fee corrections and duplicate messages.
- [ ] Cancel is not treated as completed until confirmed; risk-increasing replacements need fresh confirmation.
- [ ] Sync/restart reconciles broker reality, releases only unused reservations and exposes disable-new-orders controls.

### US-020: Event-driven strategy
**Description:** As a discretionary investor, I want separate time-bounded event ideas without polluting the core thesis.
**Depends:** US-009, US-012, US-013, US-015, US-016.
**Acceptance criteria:**
- [ ] Eligible event templates have role-specific evidence, known timing, explicit catalyst/invalidation/horizon and capped allocation.
- [ ] Unresolved issuer/counterparty or timing/price limitations yield research/paper-only status; no headline-only live trade.
- [ ] Test time-stop, gap, correction, duplicate origin and cost-infeasible trade cases.

### US-021: Portfolio performance and benchmarks
**Description:** As an investor, I want truthful outcomes distinct from deposits and dividend receipts.
**Depends:** US-005, US-008, US-015.
**Acceptance criteria:**
- [ ] Total return, TWR/MWR, costs, income, FX and drawdown reconcile to ledger/marks under documented conventions.
- [ ] Fixtures cover cash flows, negative/no IRR solutions, stale marks, splits and no double counting of distributions.
- [ ] Baselines use matched cash flows, currency and costs; unavailable benchmark data is explicit.

### US-022: Held-out claims and event precision
**Description:** As an investor, I want actual news interpretation errors measured independently.
**Depends:** US-001, US-009.
**Acceptance criteria:**
- [ ] Add support for a licensed/source-linked held-out real corpus with independent labels, disagreement records and stage/group metrics.
- [ ] Address per-issuer/counterparty event identity and contradiction fixtures while preserving/superseding stable history explicitly.
- [ ] Reports separate current synthetic regressions from independent results; missing external labels cannot count as a passed gate.

### US-023: Strategy replay and walk-forward evaluation
**Description:** As an investor, I want the proposed framework compared honestly to simple alternatives.
**Depends:** US-015, US-020, US-021, US-022.
**Acceptance criteria:**
- [ ] Implement manifest-based known/reconstructed replay, frozen splits, strategy-trial registry and section 8 metrics/bias diagnostics.
- [ ] Costs, delayed fills, delistings, distributions and missing history have adversarial fixtures.
- [ ] Emit evaluated/experimental/insufficient-history status against preregistered criteria; no synthetic-profit validation.

### US-024: Plan, Portfolio, Income CLI and exports
**Description:** As a CLI user, I want the complete workflow available without the full-screen interface.
**Depends:** US-012, US-013, US-016, US-019, US-021, US-023.
**Acceptance criteria:**
- [ ] Implement section 5.12 command family, versioned JSON/Markdown, usage/error contracts and capability-aware doctor output.
- [ ] Offline explanation/export/replay does not fetch data, submit orders or open unrelated live books.
- [ ] Tenant source rights and secrets are respected in reports.

### US-025: Plan, Portfolio, Income TUI
**Description:** As a TUI user, I want the daily workflow and full reasoning keyboard-accessible.
**Depends:** US-024.
**Acceptance criteria:**
- [ ] Implement sections/detail views, before/after policy preview, evidence and order confirmation using existing navigation conventions.
- [ ] Narrow/wide/Unicode and resize tests preserve essential values; full text is available without undisclosed clipping.
- [ ] Correct purchase/sale color semantics, paper/live banners and stale/unknown labels; no network in render.
- [ ] Complete interactive QA and report the checked scenarios separately from synthetic rendering tests.

### US-026: Change alerts, journal and daily review
**Description:** As an investor, I want relevant changes delivered once with a record of my decisions.
**Depends:** US-013, US-019, US-025.
**Acceptance criteria:**
- [ ] Plan revision, review, dividend, income and broker exceptions use durable dedupe/outbox state with tenant/channel rules.
- [ ] User accepts/rejects/annotates without rewriting underlying evidence; journaling links later outcomes to the original thesis.
- [ ] Restart, quiet-hour, correction and failed-delivery cases preserve alert intent and avoid false delivery acknowledgements.

### US-027: Production operations and recovery
**Description:** As an operator, I want restartable jobs and verifiable recovery of portfolio state.
**Depends:** US-019, US-024, US-026.
**Acceptance criteria:**
- [ ] Implement redacted health/metrics, service setup, quotas, job leases, low-disk handling and measured performance/recovery targets.
- [ ] Backup rotation/independent copy and coherent multi-DB restore protocol preserve manifests and reconcile broker state before enabling submissions.
- [ ] Migration/restore, committed-progress interruption and timeout drills run on copies and report measurable results.

### US-028: Release, documentation and end-to-end acceptance
**Description:** As an owner, I want an installable product with clear verified and pending capabilities.
**Depends:** US-027 and all previous stories.
**Acceptance criteria:**
- [ ] Update README, PROTOCOL, CLI/TUI/configuration/data-source/accounting/order/recovery documentation to match actual behavior.
- [ ] Publish scenario-by-scenario engineering results, source/account verification and investment-evaluation reports; list external blockers precisely.
- [ ] Run section 11 scenarios, full gates and reproducible artifact build; preserve unrelated edits and leave changes uncommitted unless requested.

Recommended execution waves: 001–005 foundations; 006–009 income/accounting/research; 010–016 complete core/manual/paper workflow; 017–020 broker and separate event sleeve; 021–023 measurement; 024–026 interfaces/daily use; 027–028 production hardening and release. Within a wave obey each story's dependencies. Implement one vertical smoke path early (import account -> sourced distribution -> core plan -> paper fill -> income -> reconciliation) before broadening adapters.

## 11. End-to-end acceptance scenarios

1. Empty EUR 1,000 portfolio, no contributions, no data keys: onboarding and cash/portfolio design work; missing prices/dividends stay explicit; zero network from browsing, zero charges or orders.
2. US dividend stock and EUR ETF with sourced imported facts: plan shows sustainable income, valuation assumptions, EUR/original currency, correct eligibility, costs and core horizon.
3. Proposed order before an ordinary confirmed ex-date, partial fill before/on ex-date: only eligible filled quantity earns the receivable; sale after ordinary entitlement does not remove it; payment/withholding reconciles once.
4. Special distribution/due-bill, a cancelled dividend and conflicting ex-dates: specific unsupported/review states, no false income or deadline advice.
5. EUR/GBP subunit quotation, split and revised filing: no 100x quote error, phantom gain, inconsistent basis or lookahead in replay.
6. A quote is stale, a provider quota expires or FX is missing: affected action degrades with reason; unaffected evidence/portfolio records remain accessible.
7. Two buy intents in different sleeves race for the same cash; another process attempts the same import: no overspend or duplicate journal entry.
8. Broker accepts an order but the response is lost; restart and late partial fill arrive: reconcile to the single external order; no duplicate submission or oversell.
9. User changes account, quantity, price, tier or policy after preview: old confirmation is unusable; real/paper account crossover is rejected.
10. News correction or thesis invalidation after acceptance: append plan revision, expire pending approval, alert once; do not silently alter an already-submitted broker order.
11. Fees make a small trade uneconomic or allocation constraints infeasible: output WAIT/batch/cash with the exact constraint, no invented smaller-lot workaround.
12. Long-term holding reaches review date with thesis intact: due-review workflow, not forced sale. Event holding reaches time stop: distinct exit proposal in the event sleeve.
13. Tier unlock after lucky gains fails without readiness/consent; policy activation is audited; unhealthy broker state prevents new risk without deleting open orders.
14. Contribution, dividend, FX conversion, fee, sale and withdrawal reconcile to statement; performance does not count contributions or dividends twice.
15. Restore on a copy after committed progress/interruption: balanced projections and preserved history; synchronize broker reality before allowing any new submissions.
16. Held-out/replay reports lack sufficient real history: research remains usable, strategy stays experimental and report names the missing evidence.

## 12. Functional requirements

- FR-1: All proposals are conditional, versioned decisions linked to evidence and an account policy.
- FR-2: A proposal can abstain or recommend no trade without being considered a failed job.
- FR-3: Dividend preference changes ranking only within mandatory data, quality, affordability and risk eligibility.
- FR-4: Every buy/add includes a size bound, entry condition, horizon, review and invalidation; every sell/reduce is bounded by free actual holdings.
- FR-5: Holdings, cash, income and performance derive from an auditable exact ledger, not mutable hand-edited totals.
- FR-6: Each sleeve has a budget; all sleeves share account constraints and reservations.
- FR-7: Higher tiers require explicit consent and readiness evidence; outcomes alone do not unlock them.
- FR-8: A broker order requires a current exact-payload confirmation and preflight; no plan refresh constitutes consent.
- FR-9: An unknown submission outcome is reconciled; no blind external retry.
- FR-10: Paper, broker-paper and real books remain isolated in storage, interface and execution.
- FR-11: No paid provider or chargeable request is enabled by default; no unsupported data is synthesized as real coverage.
- FR-12: Every output states its data age, valuation/price basis, original/base currency and relevant uncertainty.
- FR-13: Point-in-time evaluations exclude later knowledge and identify reconstruction and selection bias.
- FR-14: Rendering and offline workflows make no hidden fetches, repairs or broker calls.
- FR-15: Backup/restore, migrations, errors and restart preserve durable intentions, evidence and local account history.
- FR-16: Coverage, software correctness, source/account compatibility and investment performance are separately reported readiness dimensions.

## 13. Boundaries

This release is a personal-use decision and confirmed-execution terminal. Exchange-speed HFT, unattended trading, leverage, short selling, options/futures, crypto, complete global institutional feeds and automatic tax filing are separate products. Sector/corporate-action cases without a correct template remain visible with a manual/import path and specific unsupported calculation state.

No general-purpose LLM is required to rank buys, invent a thesis, extract financial amounts or decide exits. Optional future language assistance may explain source-backed calculations, but does not change deterministic financial facts or bypass policy/execution gates.

## 14. Configuration still needed from the user at setup

These are runtime configuration requirements rather than reasons to postpone implementing the framework:

- Actual capital, contribution schedule, goal/withdrawal date, reserve and acceptable loss/exposure limits.
- Tax residence/account treatment and whether income is desired as cash, DRIP or batched reinvestment.
- Exact IBKR entity/account/type, permissions, account currency, free data entitlements and supported retail login flow.
- Initial European venues/instruments and any excluded sectors, companies or fund structures.
- Broker's actual commission/FX/minimum schedule and permissible quote source/freshness.
- Approval of example tier parameters, comparison benchmark and preregistered investment-evaluation criteria.

Unknown fields stay unknown. Onboarding must explain which features they affect and allow research/paper work to continue where those fields are not needed. Jurisdiction-specific tax estimates and execution permissions cannot be inferred from locale.

## 15. Primary-source checks and implementation references

Reviewed for planning on 2026-11-08; these are documentation checks, not live API/account validation:

- [IBKR Trading Web API](https://www.interactivebrokers.com/campus/ibkr-api-page/web-api-trading/): retail gateway/authentication, individual-account requirements, sessions, pacing, order preview/reply flows and portfolio/execution resources. Reverify the current API/auth path and actual account capabilities before enabling the adapter. API access cost does not imply free live market-data entitlements.
- [Investor.gov: Ex-Dividend Dates](https://www.investor.gov/introduction-investing/investing-basics/glossary/ex-dividend-dates-when-are-you-entitled-stock-and): ordinary US entitlement and exceptional distribution/due-bill timing. Use announced event terms and venue-specific rules, not a universal hard-coded record-date offset.
- [ECB euro reference exchange rates](https://www.ecb.europa.eu/stats/policy_and_exchange_rates/euro_reference_exchange_rates/html/index.en.html): dated reporting/reference conversion. ECB states these are for information and discourages transaction use; do not present them as executable broker FX quotes.

Repository integration references: `PROTOCOL.md` sections 9–12; `app/src/cmd/stoxicity/commands.go`; `app/src/marketdata/candles.go`; `app/src/store/tenant.go` and base/tenant migration directories; `app/src/tui/backend.go`; `workspace`; `channel`; `docs/events.md`; `docs/universe.md`; `docs/configuration.md`; `docs/tui.md`; `backup.sh`.

## 16. Implementation handoff contract

Implement this PRD in the existing repository as one coordinated, dependency-ordered programme. Start by reading repository instructions and inventorying current uncommitted changes. Preserve user work, secret files, live databases, observations and append-only history. Do not commit unless explicitly requested. Use fixtures and database copies for migrations/replay/recovery. Do not execute live orders as an implementation test.

For each story, record status, touched modules, verification and exact blockers. Finish the core/manual/paper vertical path before connecting confirmed IBKR execution. Validate real endpoint/account capabilities explicitly and never substitute synthetic outcomes for those gates. Update docs from actual behavior, then produce a final matrix of implemented/verified/pending features, commands to use each workflow, and the remaining external setup or observation requirements.

Completion means all specified software workflows and engineering gates are delivered, with unavailable external inputs honestly represented. It does not mean five-year investment performance can be established in a single coding run.
[/PRD]
