# Release and acceptance (US-028)

What is built, what was run, and what only you or time can supply. This is the closing document of
[the production plan](production-plan-mode.md). It reports engineering results: every scenario
below was exercised **on fixtures and invented data**. Nothing here was run against a real broker,
a real account, a paid data source or a multi-year history, and nothing here is evidence that a
strategy earns money.

`stoxicity doctor -scope portfolio` prints the same readiness table from the inventory
(`app/src/readiness/inventory.go`). The five readinesses stay apart (docs/readiness.md): *implemented*
and *fixture verified* are `yes` for every story below; *source verified*, *account verified* and
*investment evaluated* are `not_applicable`, `unknown` or `no` everywhere, because no dated record of a
check against the real thing exists (see [Source and account verification](#source-and-account-verification-report)).

## 1. Stories

| Story | What it delivers | Doc | Pending (specific) |
|---|---|---|---|
| US-001 | Capability inventory and `doctor` that never opens a database | readiness.md | no recorded source/account checks |
| US-002 | Exact money, rates, quote units (GBp), quantity rules, tick tables | readiness.md | |
| US-003 | Versioned policy, onboarding example, preview, consent to activate | policy.md | tax, entity, permissions stay unknown until you give them |
| US-004 | Issuers, securities, listings, resolver, provider capability registry | instruments.md | no European register is wired; instruments come from your files |
| US-005 | FX and price observations; stale, conflicting, unit-ambiguous values refused | fx-prices.md | ECB live contract test written, not run; Polygon candles not bridged; valuation does not read observations |
| US-006 | Dividend and distribution observations, calendar, history quality | dividends.md | payouts only from files you import |
| US-007 | Accounts, immutable double-entry ledger, lots, unsettled cash, reservations, paper separation, statement reconciliation | portfolio.md | reconciled only against CSV you provide |
| US-008 | Entitlement from fills, accrual, withholding, DRIP; income booked once | income.md | due-bill/special/return of capital reported unsupported |
| US-009 | Sourced multi-year scorecards, sector templates | scorecards.md | |
| US-010 | Valuation scenarios, core strategy, fund template | strategy.md | thresholds unvalidated; config starts unreviewed |
| US-011 | Allocator under policy limits and a sourced fee schedule | allocation.md | cap interpretations await you; fees are what you enter |
| US-012 | Immutable plan revisions, input manifest, lifecycle, staleness | plans.md | |
| US-013 | Hold/reduce/sell review, overdue reviews | review.md | rule 6 has no target weights |
| US-014 | Order event log, atomic holds, confirmation bound to terms | orders.md | no `order replace` by design |
| US-015 | Local paper engine, conservative seeded fills | paper.md | bars from CSV only |
| US-016 | Tiers apart from risk, evidence, suspension | tiers.md | thresholds illustrative |
| US-017 | Broker seam, fixture broker, loopback IBKR adapter, read-only reconciliation | broker.md | **never run against a real gateway or account** |
| US-018, US-019 | Preview, payload-bound confirmation, durable-before-external submit that never resends, cancel, fills, corrections | broker.md, orders.md | the same: contract fixtures only, no live order |
| US-020 | Experimental earnings follow-through sleeve, paper and research only | event-sleeve.md | settings illustrative; never for a real account |
| US-021 | Performance from ledger and raw marks, TWR/MWR, drawdown, matched baselines | performance.md | marks and rates from your CSVs; benchmark to approve |
| US-022 | Held-out news evaluation gate | heldout.md | **no corpus exists**; the gate reads `no_external_labels` |
| US-023 | Replay and walk-forward evaluation | walkfwd.md | no real history; status stays `insufficient_history` or `experimental` |
| US-024 | Report and plan export | report.md | |
| US-025 | Plan, Portfolio and Income sections in the terminal interface | tui-workflow.md | confirm limited to non-broker drafts; no what-if or policy editor |
| US-026 | Alerts with a durable outbox, decision journal, daily review | alerts-journal.md | must be scheduled; some producers not detected (below) |
| US-027 | Redacted health/metrics, leases, limits, coherent backups, restore with reconcile hold, drills | operations.md | no real migration exercised; targets illustrative |
| US-028 | This release document, the scenario results, the acceptance tests | acceptance.md | external inputs (section 5) |

## 2. End-to-end scenarios (section 11 of the plan)

Result column: **covered** means the behaviour the scenario names is exercised by the listed tests and
they pass; **partly** says exactly what is not. A test is named package.TestName
(`cmd` is the command-line package). `go test` for every one of them is part of the gate, and
`app/src/readiness/acceptance_test.go` fails if a name here stops being a real test.

| # | Scenario | Result | Tests | What is not covered |
|---|---|---|---|---|
| 1 | Empty EUR 1,000 portfolio, no keys: onboarding and design work, gaps explicit, no network, no orders | covered | `cmd.TestAcceptanceEmptyEURPortfolioMakesNoRequest` (a transport that fails the test on any request; books, policy, plan, income, performance, orders, tiers, doctor, ops, alerts, daily, broker status), `allocate.TestFeasibleAllocationOfOneThousandEuro`, `allocate.TestNothingSpendableAboveTheReserve`, `policy.TestExampleLeavesAccountFactsUnknownAndSaysWhatTheyBlock`, `tui.TestDrawingReadsNothing`, `perf.TestMissingMarksMakeValuesUnavailableAndStaleMarksAreListed` | "zero charges" is true because nothing is configured to charge |
| 2 | US dividend stock and EUR ETF with sourced facts: sustainable income, valuation assumptions, currencies, eligibility, costs, horizon | covered | `cmd.TestPlanSnapshotsAndLifecycle`, `plan.TestCardRulesHoldWhateverProducedIt`, `allocate.TestFundsUnknownLookThroughAndTheThreeWayComparison`, `allocate.TestForeignCurrencyNeedsARateAndPaysTheFXCost`, `strategy.TestBuyOnlyWhenEligibleReviewedAndInsideTheEntryBand`, `strategy.TestFundTemplateChecksAreNotAScore`, `distribution.TestQualityFindsACutAGapAndCountsSpecialsApart`, `distribution.TestCoverNeedsASupportedSectorAndInputs` | facts come from files you import; no source was read live |
| 3 | Order before an ex-date, partial fill: only filled shares earn; a later sale keeps the receivable; payment and withholding reconcile once | covered | `income.TestEntitlementFollowsRecordedFillsAroundTheExDate`, `income.TestUnfilledOrdersEarnNothingAndPartialFillsEarnOnTheFilledPart`, `income.TestAccrualPaymentAndWithholdingReconcileOnce`, `cmd.TestIncomeWorkflow`, `cmd.TestAcceptanceVerticalPath` | |
| 4 | Special/due-bill, cancelled dividend, conflicting ex-dates: specific states, no false income or deadline advice | covered | `income.TestOnlyDeclaredOrdinaryPayoutsAreComputed`, `distribution.TestResolveDeclaredEstimatedCancelledAndConflicting`, `distribution.TestAConflictingPeriodIsNotMissed`, `distribution.TestEstimatesNeverBecomeDeclaredOrAddedToDeclared` | a due-bill is recognised only because the payout is typed `special`; no venue rules are modelled |
| 5 | EUR/GBP subunit, split, revised filing: no 100x quote, no phantom gain, inconsistent basis or lookahead | covered | `money.TestPenceAgainstPounds`, `quotes.TestResolvePriceRefusesWrongUnitsStaleAndConflicting`, `portfolio.TestSplitsRescaleEveryLotAndKeepCost`, `perf.TestASplitChangesQuantityNotValue`, `distribution.TestSplitsRestateEarlierAmounts`, `scorecard.TestPointInTimeReadingIgnoresLaterFilingsAndUndatedFigures`, `fundamentals.TestAsOfShowsOnlyWhatHadBeenFiledAndTheRestatementOnlyAfterIt`, `walkfwd.TestLookaheadIsExcludedAndInformationLateInTheDayWaitsForTheNextSession` | corporate actions beyond splits are not modelled |
| 6 | Stale quote, expired provider quota, missing FX: the action degrades with a reason; unaffected records stay readable | partly | `quotes.TestResolveFXAgeConflictAndRevision`, `plan.TestStaleInputsBlockAcceptanceButNotReading`, `allocate.TestForeignCurrencyNeedsARateAndPaysTheFXCost`, `perf.TestForeignCurrencyEffectIsTheRemainderAndMissingRatesAreExplicit`, `cmd.TestFetchReportsErrorClass`, `cmd.TestLeaseStopsASecondDeliveryAndDiskLimitsStopHeavyWorkOnly` | a provider quota is not modelled: an exhausted quota arrives as a fetch error of some class; the only quotas are disk free and data size |
| 7 | Two buys race for the same cash; another process repeats an import: no overspend, no duplicate entry | covered | `order.TestConcurrentProcessesCannotSpendTheSameMoney`, `order.TestConcurrentFillsAndCancelsKeepTheInvariants`, `store.TestImportingTheSameRecordTwiceWritesOnce`, `portfolio.TestImportIsIdempotentAndConflictsAreRefused`, `ops.TestLeaseHeldRenewedExpiredAndTakenOver` | |
| 8 | Broker accepts, the answer is lost; restart and a late partial fill: reconcile to the single external order, no duplicate, no oversell | covered on fixtures | `cmd.TestAnswerLostAfterPlacementIsUnknownNeverResentAndFoundBySync`, `order.TestCrashBetweenIntentAndAnswerIsNeverResent`, `order.TestOverfillAndUnpostableFillsNeedAPerson`, `recon.TestSentOrderThatTheBrokerDoesNotListIsFlagged`, `recon.TestExecutionsBecomeIdempotentFillsInTimeOrder`, `cmd.TestFillsDuringCancellationAndCorrectionsReconcileWithoutOverwriting` | the answers are recorded contract fixtures; no real gateway was asked |
| 9 | Account, quantity, price, tier or policy changed after preview: the confirmation is unusable; real/paper crossover rejected | covered | `order.TestTermsHashBindsEveryTermAndIgnoresTrailingZeros`, `order.TestAConfirmationCoversOnlyWhatTheBrokerWasShown`, `cmd.TestPolicyReplacedAfterConfirmationInvalidatesTheOrder`, `cmd.TestTierOneConfirmsTicketsAndTierTwoIsNeededToSend`, `cmd.TestBrokerBindKeepsPaperAndLiveApart`, `portfolio.TestTransfersNeverCrossEnvironmentsOrHalfHappen` | |
| 10 | News correction or thesis invalidation after acceptance: append a revision, expire the approval, alert once, never alter a submitted order | covered on fixtures | `cmd.TestACorrectionRevisesTheAcceptedPlanOnceAndLeavesASentOrderAlone`, `cmd.TestARetractionWithoutAWayToReassessAsksForReviewAndNeverBecomesHold`, `cmd.TestAThesisIsBrokenOnlyByARuleYouRecordedAndAnythingElseNeedsReview`, `corrections.TestMoreEvidenceIsNotACorrection`, `corrections.TestACorrectionARetractionAndAChangedSourceAreTold`, `corrections.TestTheSameChangeHasTheSameIdentity`, `order.TestRevokeWhereWithdrawsOnlyPickedUnsentConfirmations`, `workflow.TestOnlyAnOpenSentOrderWithRealMoneyIsLiveExposure`, `store.TestArticleHeadsMoveOnlyWhenASourceChangesItsText` | `corrections scan` is a job you schedule; the tests write events directly and the pipeline was not run against live articles; events are cited context and do not move a score; a thesis is called broken only by an event-kind rule you recorded |
| 11 | Fees make a trade uneconomic or constraints infeasible: WAIT/batch/cash with the exact constraint, no invented smaller lot | covered | `allocate.TestFeeDrivenBatchAndWait`, `allocate.TestNoFeasiblePurchaseExplainsEveryCandidate`, `allocate.TestUnknownFeesMeanNoClaimOfAffordability`, `policy.TestFeasibilityNamesTheConstraintThatCannotBeMet`, `eventstrat.TestCostInfeasibleAndUnaffordableTradesWait` | |
| 12 | Long-term holding at its review date: a due review, not a sale; an event holding at its time stop: a distinct exit | covered | `review.TestPlainHoldAndTheNextReviewDate`, `review.TestALateReviewIsOverdueNeverAFreshHold`, `review.TestEventPositionsExpireAndInvalidate`, `eventstrat.TestTimeStopIsADistinctExitAndGapsCannotExtendIt` | |
| 13 | A tier unlock after lucky gains fails without evidence and consent; policy activation is audited; an unhealthy broker blocks new risk without deleting open orders | covered | `cmd.TestElapsedTimeAndProfitNeverUnlockATier`, `cmd.TestTierDoorNeedsEvidenceReviewAndAcceptance`, `policy.TestActivationNeedsTheExactConsent`, `cmd.TestHealthSignalsSuspendNewRiskAndTouchNothingElse`, `cmd.TestBrokerSyncRecordsDifferencesAndSuspendsNewRisk`, `order.TestGateLimitsNewRiskOnlyAndSparesPaperSalesAndCancels` | automatic health signals exist only for maintenance and malformed answers; others are set by hand |
| 14 | Contribution, dividend, FX, fee, sale and withdrawal reconcile to a statement; performance counts neither contributions nor dividends twice | covered | `portfolio.TestStatementReconciliationReportsEveryDifference`, `portfolio.TestForeignExchangeKeepsNativeLegs`, `portfolio.TestIncomeWithWithholdingAndReinvestment`, `perf.TestGainSeparatesFlowsFromIncomeCostsAndPriceChange`, `perf.TestAnExDateDropOffsetByIncomeIsNotAReturnAndNotCountedTwice`, `cmd.TestAcceptanceVerticalPath` | the statement is a CSV you write; no broker statement format was parsed |
| 15 | Restore on a copy after committed progress or interruption: balanced projections, preserved history; sync with the broker before any new submission | covered | `ops.TestRestoreReplacesWithoutDestroyingAndSuspendsOrders`, `ops.TestRestoreStoppedAtAnyPointLeavesAUsableFolder`, `ops.TestDrillsRunOnCopiesAndReportMeasurements`, `cmd.TestOpsRestoreHoldsBackNewOrdersUntilTheBrokerIsReconciled`, `cmd.TestRestoredFolderRefusesNewRiskThroughTheSharedGate`, `portfolio.TestRandomJournalsAlwaysRebuildAndBalance` | no real migration is pending in the drill copies; the broker sync is the fixture broker's |
| 16 | Held-out and replay reports lack real history: research stays usable, the strategy stays experimental, the report names what is missing | covered | `heldout.TestNoExternalLabelsIsNeverAPass`, `walkfwd.TestNotEnoughHistoryIsAnAnswerNotAFailureToAnswer`, `walkfwd.TestStatusIsCappedByWhatTheDataIs`, `cmd.TestHeldoutNoExternalLabelsIsNotAPassAndFreezingLocksEverything`, `cmd.TestEventSleeveIsResearchUntilReviewedAndNeverReal` | |

Scenario 6 is the only one that is only partly met; its gap is listed again in section 5. Scenario 10 is covered on fixtures ([corrections.md](corrections.md)).

## 3. The vertical smoke path

`cmd.TestAcceptanceVerticalPath` is the path the plan asked for before anything was broadened, run end to end through the
command line: instruments imported, a paper book opened and funded, an order drafted, confirmed and submitted, a conservative
simulated fill from finished bars, a sourced dividend imported, the income the filled shares earned accrued and received once,
a statement that disagrees until the purchase is settled (the difference is named as unsettled trades and nothing is
changed), the settlement booked, and the statement reconciled. To run it:

```bash
cd app && go test -count=1 -run 'TestAcceptance' -v ./src/cmd/stoxicity
```

## 4. Commands for each workflow

| Workflow | Commands |
|---|---|
| Set up | `policy example`, `policy check -file F`, `policy preview -file F`, `policy activate -file F -confirm HASH`; `instruments example` / `import`; `portfolio book create`, `portfolio add BOOK deposit ...` |
| Research | `fetch`, `digest`, `search`, `stock`, `stocks`, `topic`, `scorecard import|annotate|show`, `strategy assumptions set`, `strategy propose` |
| Income facts | `dividends import|calendar|quality`, `fx fetch|import|rate`, `prices import|at` |
| Decide | `allocate plan`, `plan generate|list|show|diff|propose|accept|reject`, `review run`, `journal add|outcome` |
| Execute (paper) | `order draft|confirm|submit|cancel|show`, `paper run`, `order control` |
| Execute (broker, fixtures only today) | `broker config|mode|bind|sync|preview`, then `order confirm` / `order submit` |
| Account | `portfolio show|journal|verify|reconcile`, `income expected|accrue|receive|check`, `performance BOOK` |
| Watch | `alerts scan -deliver`, `alerts status`, `daily`, `tier status`, `doctor -scope portfolio` |
| Evaluate | `heldout ...`, `walkfwd ...`, `event ...`, `tier ready` |
| Export | `report export BOOK`, `plan export PLAN` |
| Operate | `ops health`, `ops backup -keep 7 -keep-weeks 4 -mirror DIR`, `ops verify`, `ops restore`, `ops reconciled`, `ops drill`, `ops service launchd|systemd` |
| Terminal | `stoxicity-tui` (keys `p` plans, `f` portfolio, `m` income; docs/tui-workflow.md) |

## 5. What is not done, and what you must supply

### Source and account verification report

No capability has a dated record of a check against its real source or account. By design `doctor` will not say `yes`.

| Subject | State | What would verify it |
|---|---|---|
| ECB reference rates | contract test written, **not run** | `STOXICITY_LIVE_CONTRACT=1 STOXICITY_CONTACT=you@example.com go test -run TestECBLiveContract ./src/quotes` |
| SEC EDGAR formats | written from the documented formats, not checked against live data (README) | a recorded live run |
| IBKR Client Portal gateway (sessions, accounts, balances, positions, orders, executions, preview, reply flows) | adapter tested against **recorded contract fixtures and `httptest`** only; response shapes are unverified | your entity, account type and permissions; a paper account; a dated record of a read-only run, then of a paper order, before any real one |
| IBKR market-data entitlements and quote freshness | not asked | your entitlements; the quote age and collar limits in `broker limits` are illustrative |
| Broker fee schedule | none: fees are what you type into the schedule file | your broker's commission, FX and minimum schedule |
| Dividend and distribution facts | no source supplies them; only files you import | the files, with their source and date |
| Polygon/Massive candles | stored but not bridged to the paper engine or valuation | a decision on which bars to trust |
| European venues and instruments | no register; instruments come from your files | your list of venues, instruments and exclusions |

### Investment-evaluation report

No strategy has been evaluated, and none can be from this repository alone.

- **Held-out news evaluation.** No corpus of real articles with independent labels exists. The gate reads `no_external_labels` and cannot pass. The 39-case claim corpus is invented and guards regressions only. To start: a licensed corpus, at least two independent annotators, a third person for disputes, thresholds you approve (the 30 items / 0.8 / 0.7 defaults are illustrative), then `heldout freeze`.
- **Walk-forward evaluation.** The tests prove the mechanics (frozen splits, trial registry, conservative fills, delisting, costs, clustered uncertainty). No real history was replayed, so the status of every strategy is `insufficient_history` or `experimental`. The preregistered criteria, splits, embargo and the fund baseline are placeholders for you to set before the first real run.
- **Core strategy and event sleeve.** Starter thresholds are unvalidated and unreviewed. The event sleeve is experimental, paper and research only, and refused for real accounts.
- **Five-year performance.** It cannot be established in a coding run. It needs five years of your own dated records, then `performance` against a benchmark you approve.

### External inputs still needed (section 14 of the plan)

Capital and contribution schedule; goal or withdrawal date; reserve and acceptable loss or exposure limits; tax residence and account treatment; whether income is wanted as cash, DRIP or batched; the exact IBKR entity, account, type, permissions, currency, entitlements and login flow; European venues, instruments and exclusions; your broker's fee schedule and a permissible quote source and freshness; approval of tier parameters, the comparison benchmark and the preregistered evaluation criteria. Unknown fields stay unknown, and the features they affect say so.

### Limits that remain in the software

- Scenario 6 as described above. A correction or a broken thesis is detected by `corrections scan`, which you schedule; nothing watches continuously, and it was checked on fixtures, not on live articles.
- Alerts are produced by `alerts scan` (schedule it with `ops service`); policy breach, missing data and dividend declared/paid are not detected (a thesis rule is checked by `corrections scan`); email and webhook delivery were never sent live.
- The terminal interface confirms only non-broker drafts and sends nothing; it has no what-if, policy editor, market filter or reconciliation screen, and no alerts or journal screen.
- Leases are advisory; the coherent-backup check fingerprints commits and is not a lock; the process check uses `pgrep`; Unix only.
- Operations targets (5 min / 15 min / 5 min, 48 h backup age) are illustrative and were measured on one machine on fixtures.
- Corporate actions beyond splits, spread and tax estimates are not modelled.

## 6. Gates and the build

Run on 2026-11-08 on macOS (Go toolchain of this machine) from the working tree just before the commit that adds this document:

| Gate | Command | Result |
|---|---|---|
| Formatting | `cd app && gofmt -l .` | prints nothing |
| Vet | `cd app && go vet ./...` | clean |
| Build | `./build.sh` | every package compiled; `bin/stoxicity` and `bin/stoxicity-tui` built |
| Tests | `cd app && go test -race -shuffle=on -count=1 ./...` | 86 packages `ok`, none failed |
| Whitespace | `git diff --check` (the secret `.env` excluded and never read) | clean |
| Scenarios | `go test -count=1 -run 'TestAcceptance' ./src/cmd/stoxicity ./src/readiness` and the tests named in section 2 | pass (they are part of the run above) |
| Reproducible build | `./build.sh` twice, then `shasum -a 256 bin/*` | identical checksums both times |

Build reproducibility: `build.sh` uses `CGO_ENABLED=0`, `-trimpath` and a version stamped from the commit (`dev-<commit>`, with
`-dirty` while tracked files are modified), so the same commit and toolchain give byte-identical binaries. This was checked on
one machine, with one toolchain; it was not checked across machines. A tagged release build (`./build.sh tag vX.Y.Z`) refuses a
dirty tree. Nothing was tagged or released here.

Interactive checks that tests cannot make (a terminal at narrow and wide widths) were done for the terminal sections on a
scratch workspace and are reported in docs/tui-workflow.md, apart from the rendering tests.
