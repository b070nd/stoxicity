# Which platform is closest to stoxicity?

Research date: **2026-11-08**. Primary official sources only. Rankings are comparative judgments about documented scope, not software audits or investment-performance rankings.

## Short answer

- **Closest investor product workflow: Stock Rover.** Fundamental screening/scoring, dividend research, portfolio analysis, target-weight rebalancing and explicit trade planning most closely resemble the investor-facing loop described for stoxicity. Its brokerage linking is documented as position synchronization; that is not evidence of order execution. [SR1] [SR2] [SR3]
- **Closest open-source decision/execution architecture: QuantConnect LEAN.** Its explicit universe → signals → portfolio targets → risk → execution pipeline, local backtesting and walk-forward support make it the strongest architectural reference for stoxicity's programmable allocation/replay/paper/broker side. [L1] [L2] [L3]
- **Closest broad integrated local workstation: Fincept Terminal**, specifically **`Fincept-Corporation/FinceptTerminal`**. Its documented desktop research, news, portfolio, analytics and trading breadth is unusually close, but evidence confidence is lower and its current license text contradicts its ordinary-AGPL branding. Treat the reviewed repository as **source-available with restrictive/conflicting licensing**, not confidently as standard AGPL open source. [F1] [F2] [F3]
- **Closest open-source research-data foundation: OpenBB Open Data Platform.** Its current emphasis is integrating public, licensed and proprietary data into Python, CLI and API consumers. That is a strong ingestion/reference-layer comparison, not a demonstrated replacement for the complete portfolio and decision system. [O1]
- **Closest local portfolio-accounting product: Portfolio Performance**, especially for foreign-currency accounts, EUR reporting, dividends, acquisition costs and allocation/rebalancing. [P1] [P2] [P3] [P4]

**No reviewed source establishes a single equivalent to the whole combination of immutable observations, revisioned claims/events, source-origin provenance, point-in-time research, a double-entry portfolio ledger and policy-constrained plans.** This is an evidence gap in this review, not a claim that no such product exists.

## Baseline and method

The baseline was initially supplied to the research task, then cross-checked against the code-only graph and selected source definitions by the main agent. This is a structural comparison, not a fresh runtime or accounting audit:

1. Go local CLI/TUI; immutable source observations and event/claim revisions.
2. SEC filings/news ingestion, source-origin provenance and point-in-time research.
3. Fundamental quality/value/dividend scorecards.
4. EUR multi-currency portfolio double-entry ledger, lots and income.
5. Policy-constrained allocation and buy/hold/sell plans.
6. Local paper trading; a confirmation-gated IBKR adapter **fixture-tested only**.
7. Strategy replay/walk-forward mechanics; **no established real trading performance**.

The comparison prioritizes the fundamental-investor research → portfolio → plan workflow, then programmable/local architecture and replay/execution overlap. It does not award similarity merely for feature counts, AI claims, stars or the word “terminal.” Individual subsystem winners are separated below because changing these priorities changes the winner.

Official manuals and concrete API/source definitions provide stronger capability evidence than README feature lists; neither proves operational correctness. “Not established” means not verified in the reviewed sources. In particular, historical charts do not establish as-known-at-the-time data; transaction tracking does not establish double-entry accounting; a risk setting does not establish stoxicity-equivalent policy enforcement; and paper execution/backtests do not establish returns.

Existing local documentation was used only to locate `docs/research/`; it was not read or graphed as project evidence. No application runtime, broker connection or live financial API was exercised.

### Code-only baseline cross-check

The separate graph includes **597 code files: 561 Go sources/tests, 34 SQL migrations and two shell scripts**, with all 597 represented. Its final build contains **9,576 nodes, 24,122 graph relationships and 484 detected communities**. Raw extraction retains 28,646 relationships before the graph representation collapses endpoint duplicates. See `graphify-out/CODE_SCOPE.json`, `verification.json` and `GRAPH_REPORT.md` (local build output, kept out of the repository). The source manifest includes uncommitted code; it is not a HEAD-only snapshot. These counts describe the verified snapshot, not subsequent edits.

Specific implemented definitions supporting the comparison:

- Immutable plan revisions, lifecycle and semantic hashes: [`app/src/plan/plan.go:1–22`](../../app/src/plan/plan.go#L1). Explicit action, evidence, scenario, sizing and exit fields: [`plan.go:76–169`](../../app/src/plan/plan.go#L76).
- Portfolio environments, signed double-entry conventions, settlement, lots and reservations: [`app/src/portfolio/model.go:1–24`](../../app/src/portfolio/model.go#L1), [`model.go:35–160`](../../app/src/portfolio/model.go#L35).
- Broker session/account/quote/order contracts and explicit preview/place/execution interfaces: [`app/src/broker/broker.go:25–160`](../../app/src/broker/broker.go#L25). These definitions establish a seam, not successful operation against IBKR.
- Implemented alert producers explicitly enumerate plan revisions, reviews, income, broker exceptions and unknown orders: [`app/src/workflow/alerts.go:30–53`](../../app/src/workflow/alerts.go#L30). Do not infer automatic article-correction/thesis detection merely from an alerts module.
- Persisted event revisions and evidence relationships: [`app/src/store/migrations/base/0012_events.sql:26–95`](../../app/src/store/migrations/base/0012_events.sql#L26).
- Append-only persisted order events and account/environment controls: [`app/src/store/migrations/tenant/0008_orders.sql:7–41`](../../app/src/store/migrations/tenant/0008_orders.sql#L7).

No docs, requirements, JSON fixtures, runtime financial data or external-product pages were included in the graph. Graphify's cross-file short-name matching can misresolve Go calls (`strings.Repeat` versus a local `repeat`, `errors.Is` versus `policy.Is`); inferred edges and generated “surprises” are review candidates, not established architecture. The ranking uses observed module/type responsibilities, not those uncertain call links.

## Ranked shortlist: overall resemblance

This is a qualitative ranking under the priorities above, not a claim of interchangeability. Each row's links support the factual overlap; the ordering and fit labels are analysis.

| Rank | Candidate | Strongest documented overlap | Main limit on the comparison |
| --- | --- | --- | --- |
| **1** | **Stock Rover** — proprietary service | Fundamental scores/research plus portfolio rebalancing, dividend metrics and trade planning. [SR1] [SR2] | No verified local evidence/revision store, double-entry ledger or programmable walk-forward engine. |
| **2** | **QuantConnect LEAN** — open-source engine plus commercial services | Programmable portfolio/risk/execution pipeline, local backtests, walk-forward optimization and documented IBKR integration. [L1] [L2] [L3] [L5] | Framework/building block rather than the supplied fundamental-investor product; data vintages and service entitlements matter. [L4] [L6] |
| **3** | **Fincept Terminal** — restrictive/conflicting public-source terms; proprietary Enterprise | Integrated native research/news/portfolio/analytics/paper-and-live-trading workstation, as documented by its maintainers. [F1] [F3] | Feature operation untested; public/Enterprise boundaries and license contradiction materially reduce confidence. [F1] [F2] |
| **4** | **OpenBB Open Data Platform** — open source; separate Workspace offering | Provider integrations, local Python/CLI/API surfaces, SEC filing fetcher and company-news schema. [O1] [O3] [O4] | Data infrastructure is not the complete ledger → policy → execution loop. |
| **5** | **Portfolio Performance** — open source | Local transaction history, foreign currencies, dividends, cost basis, returns and allocation/rebalancing. [P1] [P2] [P3] [P4] | Strong portfolio subsystem comparison; evidence-driven company research and strategy execution not established. |
| **6** | **Koyfin** — proprietary service | Fundamental financial analysis, screening, filings/news, portfolio analytics and model portfolios. [K1] [K2] | Relevant advanced workflows cross paid and advisor tiers; provenance/revision and accounting equivalence unverified. [K2] |
| **7** | **Simply Wall St** — proprietary service with published model documentation | Explicit value/health/dividend analysis plus transaction-based portfolio and dividend tools. [S1] [S2] [S3] | Published methodology is not an open-source application or validated trading system. |
| **8** | **IBKR TWS + PortfolioAnalyst** — proprietary platform/services | Research, portfolio/risk monitoring, paper/live execution, APIs and consolidated portfolio/income analysis. [I1] [I2] [I3] | Broker-centered ecosystem, not a verified local, source-versioned research system. |
| **9** | **Sharesight** — proprietary service | Broker-imported transactions, performance/FX, dividends/corporate actions, income and tax reporting. [SH1] [SH2] | Official FAQ explicitly says it is not a trading platform. [SH2] |
| **10** | **Ghostfolio** — open source plus paid hosted offering | Self-hosted multi-account buy-and-hold tracking, dividend-inclusive performance, transaction import/export and portfolio risk analysis. [G1] | Narrower wealth-tracking fit; company-research, policy plans and replay/execution equivalence not established. |

## Candidate findings and access constraints

### 1. Stock Rover: strongest ready-made investor workflow match

Its official feature catalog includes stock ratings, fair value/margin of safety, screening, historical screening, dividend-income projection and portfolio tools. More decisively, the rebalancing manual documents percentage targets, drift tolerances and calculated trades, plus a quantity-based trade-planning mode and actual-versus-model comparisons of risk, valuation, dividend yield and sector allocation. These are specific workflow parallels rather than generic “analytics” overlap. [SR1] [SR2]

Brokerage Connect uses Yodlee to synchronize actual brokerage positions, normally nightly. Do not read this as an execution adapter. The Trade Evaluator examines historical trading decisions against a benchmark; that is not the same as replaying a strategy against revisioned point-in-time inputs. [SR3] [SR4]

**Market-scope limitation for this user:** the official FAQ lists supported North American exchanges/OTC boards, not a European-listed equity universe. It also explicitly says trades cannot be executed through Stock Rover, and says restated financial values replace displayed values when available. Thus it is the closest *fundamental-investor workflow* comparison, not an established replacement for EUR/European coverage, an immutable as-known evidence archive or broker execution. The FAQ's exchange-count wording is internally inconsistent, so this report relies on the listed geography rather than repeating its numeric count. [SR6]

**Access:** proprietary service with a limited Free account and paid tiers. The current plans page advertises a time-limited trial; historical/equation screening, expanded scores, research and history depth vary by tier. Trial CSV table exports are restricted. Exact rebalancing entitlements were not conclusively established here. EUR accounting and stoxicity-equivalent ledger/policy guarantees remain unverified. [SR5]

### 2. QuantConnect LEAN: strongest open-source architecture/reference engine

The Algorithm Framework explicitly separates universe selection, alpha signals, portfolio construction, risk management and execution. The manual says risk management adjusts targets before execution, making this a concrete analogue for separating allocation constraints from order placement. The engine README documents local backtesting, optimization, research and live deployment; the walk-forward manual provides scheduling and trailing-window examples. [L1] [L2] [L3]

**Point-in-time caveat:** Morningstar fundamentals documentation discusses filing dates and as-originally-reported figures, but also explicitly records a **2026-09-23 feed migration affecting the whole history**, changed values/dating and non-reproducibility of backtests using the old feed. This is unusually relevant: a backtesting engine is not by itself an immutable historical evidence archive. Data snapshots and provider versions remain essential. [L4]

**Access/license:** LEAN's repository is Apache-2.0. The supported **LEAN CLI workflow currently requires membership in a paid-tier organization**, despite the engine being open source; local data must be obtained separately or supplied by the user. Do not imply the source-built engine itself requires that subscription. The IBKR manual documents local/cloud integration and operational limitations, including ARM local-deployment restrictions. No integration was tested here. [L6] [L5] [L7]

### 3. Fincept Terminal: closest breadth, lower-confidence equivalence

**Identity verified:** the official `fincept.in` manual links to `Fincept-Corporation/FinceptTerminal`, and the repository identifies `fincept.in` as its homepage. This is the candidate, not similarly named forks. [F4] [F5]

The README describes a native C++20/Qt6 desktop application with embedded Python analytics, DCF and portfolio optimization, data connectors, news/research screens, paper trading and broker integrations. Its architecture document describes a modular monolith, local SQLite persistence, an in-process pub/sub data plane and broker adapters. However, that document explicitly mixes **current** and **target** design and is dated May 2026; it is architectural documentation, not proof that every boundary or feature works. [F1] [F3]

**Critical licensing finding:** at reviewed commit `623c61db169ea550b4351dd6a7fb69fb608ba2cc`, the README calls the public build AGPL-3.0-or-later and says the separate commercial license is no longer sold. The `LICENSE` instead says the AGPL option is unavailable for commercial use and requires a commercial license for business/internal-company use, including modified forks. Those are substantive usage restrictions, not ordinary AGPL copyleft. Therefore this report does **not** classify the reviewed snapshot as unambiguously open source. The documents conflict; the review does not resolve their legal enforceability or the licenses of historical releases. [F1] [F2]

**Access/status:** maintainers say daily development now targets proprietary Enterprise, with the public build moving to monthly releases. README tables put deeper history/point-in-time data and managed controls in Enterprise and advertise public-build paper/live trading. These are publisher assertions, not tested execution guarantees. Data/LLM keys can add costs; Enterprise features must not be credited automatically to the public build. The public manual landing page describes a different version from the README, further limiting exact edition equivalence. [F1] [F4]

### 4. OpenBB: best data-platform analogue, not automatically the whole terminal

The current README positions **Open Data Platform** as a “connect once, consume everywhere” integration layer serving Python, Workspace/Excel, MCP and REST consumers. It documents Python installation, a CLI package and a localhost API server, and identifies Workspace as a separate enterprise UI. This current positioning matters more than assuming every capability associated with older OpenBB Terminal versions remains in the present package. [O1]

Official source confirms a SEC company-filings fetcher with CIK/symbol and date/form filters, accession numbers, acceptance/filing metadata and source document URLs. The standardized company-news model includes publication date, title, body/excerpt and article URL. Those support ingestion/source-link comparisons; they do **not** establish immutable retention, claim revisions or research-state replay. [O3] [O4]

**Access/license:** the actual repository license is **Apache-2.0**, although GitHub's metadata returned `NOASSERTION`; the text takes precedence over automated identification. Public, licensed and proprietary inputs are explicitly distinguished, so free software does not imply free data. Workspace access/paid boundaries were not independently verified beyond the README's separate enterprise positioning. [O2] [O1] [O5]

### 5. Portfolio Performance: best local portfolio subsystem comparison

Official documentation covers multi-account transaction history, taxes/fees, dividends, time-/money-weighted returns, asset allocation and rebalancing. The desktop site documents local XML storage, CSV/JSON export and foreign-currency accounts using ECB exchange rates; the currency manual explicitly illustrates EUR and USD base-currency reporting. Acquisition-cost documentation explains FIFO and moving-average calculations. [P1] [P2] [P3] [P4]

**Access/license:** open-source desktop software under **EPL-1.0**; external quote/data-provider terms are separate. Its documented cash/security accounts, paired operations or cost-basis calculations should not be called an equivalent general double-entry ledger without a specific accounting-model audit. There is no established match here for stoxicity's immutable company evidence, constrained recommendation engine or broker-execution/replay mechanics. [P1] [P5]

### 6. Koyfin: research terminal and portfolio-analysis alternative

Its official feature pages describe financial analysis, screeners, watchlists, customizable dashboards, company snapshots and portfolio/model-portfolio tools. The plans page explicitly includes filings, transcripts and premium news above Free. This is a strong discretionary research/monitoring comparison. [K1] [K2]

**Access:** proprietary freemium service. Free has limited history, screens/watchlists and news; longer financial history and filings/news are paid, advanced personal portfolio analytics are higher-tier, and model/client portfolios and custodian/report workflows sit in advisor tiers. The full advertised feature set should not be attributed to a free individual account. No local evidence ledger, immutable revisions or strategy replay was verified. [K2]

### 7. Simply Wall St: scorecard/product-explanation analogue

Its official published model explains value, future/past performance, health and dividends, including DCF/relative valuation and dividend sustainability checks. Its data-source page identifies S&P Global normalized data, end-of-day prices and update latency after results. That is useful transparency, but naming upstream sources is different from preserving every observation and claim revision locally. [S1] [S2]

The plans page lists transaction-based portfolios, returns, dividend analysis, notes and alerts. **Access:** proprietary freemium service; company-report access, portfolio/holding capacity, history and dividend detail are limited on Free and expand on paid tiers. The public model document is not evidence that the hosted application or its data is open source. Model validity, as-of reproducibility and actual investment outcomes were not established. [S3] [S1]

### 8. IBKR TWS and PortfolioAnalyst: execution/integration benchmark

TWS documents multi-asset execution, conditional/algorithmic orders, research/news/fundamentals, portfolio risk and paper trading. Its public API overview documents trading, account reporting, historical/streaming market data and programmatic orders. PortfolioAnalyst adds consolidated account performance/risk analysis and projected income. This comparison concerns the combined ecosystem, not a single standalone local module. [I1] [I2] [I3]

**Access/license:** TWS/PortfolioAnalyst are proprietary offerings. TWS advertises no platform fee, but commissions and market-data/research entitlements are separate; PortfolioAnalyst advertises free standalone access and additional features for IBKR clients. The API overview now says the latest **TWS API** is GPL open source; that does not make TWS or IBKR's hosted services open source. The API license/version itself was not independently inspected. [I1] [I2] [I3]

Broker/platform capabilities do not validate stoxicity's adapter. Its supplied status remains fixture-tested only; neither live connectivity nor successful reconciliation/execution was established by this research.

### 9. Sharesight: portfolio/income reporting alternative

Official pages describe imported broker trades, automatic dividend/corporate-action updates, performance and currency reporting, diversification and tax reports. This is strong overlap for portfolio/income recordkeeping, but the FAQ explicitly says users cannot trade or deposit/hold/transfer funds through Sharesight. [SH1] [SH2]

**Access:** proprietary freemium service. Free is limited to one portfolio and ten holdings **including closed positions**. The pricing FAQ places Future Income and Multi-Currency Valuation reports on Standard/Premium, and tax reports on paid plans. The parsed page contains some contradictory summary wording about basic tax reporting, so the explicit tax-report FAQ is the basis for that statement. Jurisdiction-specific tax correctness and lot/ledger equivalence were not tested. [SH2]

### 10. Ghostfolio: self-hosted wealth tracking alternative

The official README targets buy-and-hold investors and documents multi-account transaction management, dividend-inclusive total-return reporting, static risk analysis and transaction import/export. Its published import schema carries currencies and buy/sell/dividend/interest/fee activity types. It is a self-hostable TypeScript web application using PostgreSQL and Redis, rather than a Go CLI/TUI. [G1]

**Access/license:** **AGPL-3.0** open source with free self-hosting and a separate paid Premium cloud offering. The README marks the `GHOSTFOLIO` data-source option as Premium-only; software availability and hosted/provider entitlements are distinct. Its documented mutable transaction CRUD is not an immutable source-observation model. Broader research ingestion, enforceable trade plans and walk-forward execution remain unverified. [G1] [G2]

## Repository identity, licensing and freshness snapshot

These observations come from `gh api repos/...`, README and license retrieval on 2026-11-08. `pushed_at` is repository activity, **not** a release date, successful build, active support commitment or production-readiness measure. All five repositories returned `archived: false` and `disabled: false`. [O5] [L7] [F5] [P5] [G2]

| Repository identity returned by GitHub | Last push reported (UTC) | License assessment for reviewed repository |
| --- | --- | --- |
| `openbq-org/OpenBB` (API request to `OpenBB-finance/OpenBB` redirected here) | 2026-11-07 00:09:44 | Apache-2.0 in actual LICENSE; metadata `NOASSERTION`. [O2] [O5] |
| `QuantConnect/Lean` | 2026-11-06 21:21:19 | Apache-2.0; distinguish engine from commercial CLI/service entitlements. [L7] [L6] |
| `Fincept-Corporation/FinceptTerminal` | 2026-11-08 12:01:32 | Metadata `NOASSERTION`; AGPL branding conflicts with restrictive actual license. [F5] [F1] [F2] |
| `portfolio-performance/portfolio` | 2026-11-08 05:58:37 | EPL-1.0 for application; manual content has its own license. [P5] [P2] |
| `ghostfolio/ghostfolio` | 2026-11-08 06:15:56 | AGPL-3.0; commercial hosted service is separate. [G1] [G2] |

OpenBB evidence is pinned where useful to commit `8c69d8d7632a965f765218162667b32074003fea`; Fincept's conflicting documents are pinned to `623c61db169ea550b4351dd6a7fb69fb608ba2cc`. Other public documentation and API metadata can change after this date.

## Which comparison should guide which decision?

| Question | Best comparison from this review | Boundary |
| --- | --- | --- |
| “What product most resembles my fundamental-investor daily workflow?” | **Stock Rover**, then **Koyfin / Simply Wall St**. [SR1] [SR2] [K1] [S1] [S3] | Similar research/portfolio surfaces do not imply equivalent data lineage or accounting. |
| “What open-source architecture resembles a programmable decision-to-execution loop?” | **LEAN**. [L1] [L2] [L3] | It supplies framework mechanics; the investor-specific evidence and ledger layers still need separate evaluation. |
| “What local app attempts the broadest integrated workstation?” | **Fincept Terminal**. [F1] [F3] | Lower confidence, edition drift and substantive licensing conflict. |
| “What should I compare ingestion/provider normalization against?” | **OpenBB**. [O1] [O3] [O4] | Source URLs/metadata do not alone establish an immutable provenance store. |
| “What should I compare EUR/FX, transactions, lots and income against?” | **Portfolio Performance** locally; **Sharesight** as a hosted reporting product. [P1] [P3] [P4] [SH1] [SH2] | Verify accounting invariants and acquisition-cost rules separately. |
| “What is the broker/paper/live platform reference?” | **IBKR**, plus **LEAN's documented IBKR integration**. [I1] [I2] [L5] | No implication that stoxicity's fixture-tested adapter is live-validated. |

**Positioning synthesis:** stoxicity is best described, on the supplied baseline, as a **local evidence-aware fundamental-investing and portfolio decision system**. “Stock Rover-like investor workflow + Portfolio Performance-like portfolio concerns + LEAN-like replay/execution mechanics, with an explicit provenance/revision layer” is more accurate than naming any one product as a full equivalent. OpenBB and Fincept are especially useful research-platform comparisons, for different reasons.

## Adjacent institutional products and scope limits

- **Bloomberg Terminal:** its official product page documents integrated data/news/research, portfolio analytics and execution/order-management workflows. It is a relevant broad institutional reference, but the page does not establish a self-contained local evidence/ledger equivalent or precise access cost; no exact subscription price is asserted here. It is outside the ten ranked candidates because this review emphasizes the supplied individual-investor/local-system baseline. [B1]
- **AlphaSense:** the official platform page at **`alpha-sense.com`** describes qualitative/structured/internal research, financial screening, monitoring and auditable AI research outputs. It is a relevant research-evidence workflow comparator; this review did not establish stoxicity-equivalent ledger, allocation-policy, paper/live execution or historical revision semantics. It is not ranked as a complete substitute. “Auditable outputs” is the vendor's description, not an independent correctness finding. [A1]
- Snowball Analytics and OpenAlgo were not investigated deeply enough to rank. No negative capability or licensing conclusion is drawn about either.

### Access failures and unresolved verification

- OpenBB's [filings reference](https://docs.openbb.co/python/reference/equity/fundamental/filings), [company-news reference](https://docs.openbb.co/python/reference/news/company) and [platform page](https://docs.openbb.co/platform) returned no usable extracted body. Specific filing/news claims above instead use official repository source retrieved with `gh`.
- IBKR's [older Campus TWS API documentation URL](https://www.interactivebrokers.com/campus/ibkr-api-page/twsapi-doc/) and [currently linked introduction](https://www.interactivebrokers.com/docs/tws-api/doc/introduction) returned HTTP 503. Detailed API restrictions and license/version were not verified from those pages; accessible official product/API overviews support only the narrower claims made above.
- An initial `https://www.alphasense.com/platform/` request returned 404; that is not the investment-research vendor's verified domain. The correct official `https://www.alpha-sense.com/platform/` was successfully retrieved. [A1]
- An attempted [Open Source Definition page](https://opensource.org/osd) fetch returned HTTP 403. Fincept's classification here is based on its explicit commercial/internal-use prohibition and contradictory first-party documents; no claim is made that OSI issued a determination about this project. [F1] [F2]
- No paid account, authenticated feature demonstration, external platform test suite, broker session, accounting audit or investment track record was examined. Reported advertised capabilities remain documentation-level evidence, especially for Fincept. stoxicity's own real-trading performance remains unestablished.

## Official sources

All accessed 2026-11-08. Repository sources were retrieved through GitHub CLI/API; detailed product documentation through public official pages. Reference labels are reused adjacent to factual claims.

[SR1]: https://www.stockrover.com/features/ "Stock Rover official feature catalog"
[SR2]: https://www.stockrover.com/help/rebalancing/rebalancing-overview/ "Stock Rover rebalancing and trade planning manual"
[SR3]: https://www.stockrover.com/help/brokerage-connect/brokerage-connect-basics/ "Stock Rover brokerage position synchronization"
[SR4]: https://www.stockrover.com/help/trade-evaluator/trade-evaluator-overview/ "Stock Rover historical Trade Evaluator"
[SR5]: https://www.stockrover.com/plans/ "Stock Rover current plans and trial constraints"
[SR6]: https://www.stockrover.com/support/faq/ "Stock Rover official FAQ: North American instrument coverage, no trade execution and restated financial values"
[L1]: https://www.quantconnect.com/docs/v2/writing-algorithms/algorithm-framework/overview "LEAN Algorithm Framework architecture"
[L2]: https://github.com/QuantConnect/Lean#readme "LEAN official README and local engine workflows"
[L3]: https://www.quantconnect.com/docs/v2/writing-algorithms/optimization/walk-forward-optimization "QuantConnect walk-forward mechanics"
[L4]: https://www.quantconnect.com/docs/v2/writing-algorithms/datasets/morningstar/us-fundamental-data "Morningstar fundamentals, availability dating and 2026 feed migration"
[L5]: https://www.quantconnect.com/docs/v2/lean-cli/live-trading/brokerages/interactive-brokers "LEAN IBKR integration and deployment limitations"
[L6]: https://www.quantconnect.com/docs/v2/lean-cli/key-concepts/getting-started "LEAN CLI paid-tier requirement, local data and engine/CLI distinction"
[L7]: https://api.github.com/repos/QuantConnect/Lean "GitHub official LEAN identity, license and activity metadata"
[F1]: https://github.com/Fincept-Corporation/FinceptTerminal/blob/623c61db169ea550b4351dd6a7fb69fb608ba2cc/README.md "Fincept pinned README: capabilities, editions, maintenance and AGPL branding"
[F2]: https://github.com/Fincept-Corporation/FinceptTerminal/blob/623c61db169ea550b4351dd6a7fb69fb608ba2cc/LICENSE "Fincept pinned LICENSE: restrictive commercial/internal-use terms conflicting with README"
[F3]: https://github.com/Fincept-Corporation/FinceptTerminal/blob/623c61db169ea550b4351dd6a7fb69fb608ba2cc/docs/ARCHITECTURE.md "Fincept architecture, explicitly current versus target and dated May 2026"
[F4]: https://fincept.in/manual "Fincept official manual landing page and official repository link"
[F5]: https://api.github.com/repos/Fincept-Corporation/FinceptTerminal "GitHub official Fincept identity, homepage, activity and license metadata"
[O1]: https://github.com/openbq-org/OpenBB/blob/8c69d8d7632a965f765218162667b32074003fea/README.md "OpenBB pinned README: ODP, Python, CLI, API and separate enterprise Workspace"
[O2]: https://github.com/openbq-org/OpenBB/blob/8c69d8d7632a965f765218162667b32074003fea/LICENSE "OpenBB actual Apache-2.0 license"
[O3]: https://github.com/openbq-org/OpenBB/blob/8c69d8d7632a965f765218162667b32074003fea/openbb_platform/providers/sec/openbb_sec/models/company_filings.py "OpenBB official SEC filings provider implementation"
[O4]: https://github.com/openbq-org/OpenBB/blob/8c69d8d7632a965f765218162667b32074003fea/openbb_platform/core/openbb_core/provider/standard_models/company_news.py "OpenBB official company-news standard model"
[O5]: https://api.github.com/repos/OpenBB-finance/OpenBB "GitHub OpenBB metadata: redirected repository identity and activity"
[P1]: https://www.portfolio-performance.info/en/ "Portfolio Performance desktop capabilities, local storage, foreign currencies and license"
[P2]: https://help.portfolio-performance.info/en/ "Portfolio Performance manual: accounts, dividends, returns and rebalancing"
[P3]: https://help.portfolio-performance.info/en/reference/file/currency/ "Portfolio Performance EUR/USD and base currency reporting"
[P4]: https://help.portfolio-performance.info/en/concepts/cost-methodology/ "Portfolio Performance FIFO and moving-average acquisition costs"
[P5]: https://api.github.com/repos/portfolio-performance/portfolio "GitHub Portfolio Performance identity, EPL-1.0 and activity metadata"
[K1]: https://www.koyfin.com/features/ "Koyfin official research and portfolio features"
[K2]: https://www.koyfin.com/pricing/ "Koyfin Free, investor and advisor feature boundaries"
[S1]: https://github.com/SimplyWallSt/Company-Analysis-Model/blob/master/MODEL.markdown "Simply Wall St official model methodology"
[S2]: https://simplywall.st/analysis-and-financial-data-sources "Simply Wall St official normalized data sources, end-of-day data and update timing"
[S3]: https://simplywall.st/plans "Simply Wall St portfolio/report/dividend features and free/paid limits"
[I1]: https://www.interactivebrokers.com/en/trading/tws.php "IBKR TWS capabilities, paper trading and platform-fee statement"
[I2]: https://www.interactivebrokers.com/en/trading/ib-api.php "IBKR API offerings and statement about latest TWS API GPL release"
[I3]: https://www.interactivebrokers.com/en/portfolioanalyst/overview.php "IBKR PortfolioAnalyst capabilities and free/client access distinction"
[SH1]: https://www.sharesight.com/features/ "Sharesight official portfolio, FX, corporate actions, income and reporting overview"
[SH2]: https://www.sharesight.com/pricing/ "Sharesight free/paid/report limits and explicit non-trading FAQ"
[G1]: https://github.com/ghostfolio/ghostfolio#readme "Ghostfolio official features, technology, import schema, hosted/self-hosted access and AGPL"
[G2]: https://api.github.com/repos/ghostfolio/ghostfolio "GitHub Ghostfolio identity, AGPL-3.0 and activity metadata"
[B1]: https://www.bloomberg.com/professional/products/bloomberg-terminal/ "Bloomberg official integrated Terminal, portfolio and execution overview"
[A1]: https://www.alpha-sense.com/platform/ "AlphaSense official research, financial data, monitoring and AI workflow overview"
