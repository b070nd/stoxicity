package readiness

// The inventory is written by hand and checked by a test: every "yes" points at
// a path that exists, no capability may claim source, account or investment
// evidence it cannot cite, and every story of the production plan is present.
// When a story is finished, its entry changes here in the same change.

// existing is a capability that was built before the production plan. It has
// code and tests on fixtures; nobody has recorded a check against the real
// source, so that stays unknown rather than being assumed.
func existing(id, name string, scope Scope, path, fixtures string) Capability {
	return Capability{
		ID: id, Name: name, Scope: scope,
		Levels: map[Dimension]Level{
			Implemented: Yes, FixtureVerified: Yes,
			SourceVerified: Unknown, AccountVerified: NotApplicable, InvestmentEvaluated: NotApplicable,
		},
		Evidence: map[Dimension]string{Implemented: path, FixtureVerified: fixtures},
		Blocker:  "no recorded verification against the real source",
	}
}

// planned is a capability a story of the production plan delivers and nothing
// of it exists yet.
func planned(id, name string, scope Scope, story, blocker string) Capability {
	acct, inv := NotApplicable, No
	if scope == Broker {
		acct = No
	}
	return Capability{
		ID: id, Name: name, Scope: scope, Story: story,
		Levels: map[Dimension]Level{
			Implemented: No, FixtureVerified: No, SourceVerified: No,
			AccountVerified: acct, InvestmentEvaluated: inv,
		},
		Blocker: blocker,
	}
}

// Inventory returns the capabilities with their readiness.
func Inventory() []Capability {
	caps := []Capability{
		existing("news-ingest", "News ingestion from configured sources", Research, "src/sources", "src/sources"),
		existing("evidence-events", "Append-only evidence and event history with as-of reads", Research, "src/pipeline", "src/pipeline"),
		existing("origin-corroboration", "Origin-based corroboration and near-copy suspicion", Research, "src/origin", "src/origin"),
		existing("sec-reconcile", "Resumable SEC filing-index reconciliation", Research, "src/reconcile", "src/reconcile"),
		existing("fundamentals-sec", "SEC company facts and fundamentals", Research, "src/fundamentals", "src/fundamentals"),
		existing("insider-form4", "Form 4 insider transactions", Research, "src/insider", "src/insider"),
		existing("price-candles", "Daily candles with provenance and splits", Research, "src/marketdata", "src/marketdata"),
		existing("class-valuation", "Point-in-time, currency-aware share-class valuation", Research, "src/valuation", "src/valuation"),
		existing("exchange-calendar", "Exchange sessions and calendar overrides", Research, "src/exchange", "src/exchange"),
		existing("research-tui", "News and Stocks terminal interface", Research, "src/tui", "src/tui"),
		existing("alerts-news", "Durable alert delivery for news watches", Research, "src/channel", "src/ledger"),
		existing("backup-restore", "Online backup, verification and restore script", Research, "../backup.sh", "src/store/migrate_legacy_test.go"),
	}

	claims := existing("claim-reader", "Rule-based claim reader (advisory classifier off)", Research, "src/assertion", "src/assertion/challenge")
	claims.Levels[InvestmentEvaluated] = No
	claims.Blocker = "the 39 challenge cases are synthetic regressions, not an independent held-out corpus"
	caps = append(caps, claims)

	// US-001 and US-002 are delivered by this change.
	caps = append(caps,
		Capability{
			ID: "readiness-doctor", Name: "Capability inventory and doctor report", Scope: Research, Story: "US-001",
			Levels: map[Dimension]Level{
				Implemented: Yes, FixtureVerified: Yes, SourceVerified: NotApplicable,
				AccountVerified: NotApplicable, InvestmentEvaluated: NotApplicable,
			},
			Evidence: map[Dimension]string{Implemented: "src/readiness", FixtureVerified: "src/readiness/readiness_test.go"},
		},
		Capability{
			ID: "money-primitives", Name: "Exact money, quantity, FX direction and tick primitives", Scope: Portfolio, Story: "US-002",
			Levels: map[Dimension]Level{
				Implemented: Yes, FixtureVerified: Yes, SourceVerified: NotApplicable,
				AccountVerified: NotApplicable, InvestmentEvaluated: NotApplicable,
			},
			Evidence: map[Dimension]string{Implemented: "src/money", FixtureVerified: "src/money/money_test.go"},
		},
	)

	// US-003: the policy exists and is versioned; nothing consumes its epoch yet.
	caps = append(caps, Capability{
		ID: "policy", Name: "Versioned investment policy, onboarding and preview", Scope: Portfolio, Story: "US-003",
		Levels: map[Dimension]Level{
			Implemented: Yes, FixtureVerified: Yes, SourceVerified: NotApplicable,
			AccountVerified: NotApplicable, InvestmentEvaluated: NotApplicable,
		},
		Evidence: map[Dimension]string{Implemented: "src/policy", FixtureVerified: "src/policy/policy_test.go"},
		Blocker:  "plans and order confirmations that bind to the policy epoch arrive with US-012 and US-018; until then nothing is invalidated because nothing depends on it",
	})

	// US-004: identity and the capability registry exist; no data source is wired for
	// European markets and no provider has been checked against its real service.
	caps = append(caps, Capability{
		ID: "multi-market-identity", Name: "Security, listing and venue identity with explicit ambiguity; capability registry", Scope: Research, Story: "US-004",
		Levels: map[Dimension]Level{
			Implemented: Yes, FixtureVerified: Yes, SourceVerified: Unknown,
			AccountVerified: NotApplicable, InvestmentEvaluated: NotApplicable,
		},
		Evidence: map[Dimension]string{Implemented: "src/instrument", FixtureVerified: "src/instrument/instrument_test.go"},
		Blocker:  "instruments come only from a user-supplied file: no register of European securities is wired and no provider's entitlements were checked against its terms",
	})

	// US-005: exact observations and the rules that refuse bad ones. The ECB fetcher
	// has only ever run against a fixture; its live contract test exists and has not been run.
	caps = append(caps, Capability{
		ID: "fx-prices", Name: "Exact FX and price observations, ECB reference-rate fetcher, import, as-of resolution", Scope: Research, Story: "US-005",
		Levels: map[Dimension]Level{
			Implemented: Yes, FixtureVerified: Yes, SourceVerified: Unknown,
			AccountVerified: NotApplicable, InvestmentEvaluated: NotApplicable,
		},
		Evidence: map[Dimension]string{Implemented: "src/quotes", FixtureVerified: "src/quotes/quotes_test.go"},
		Blocker:  "the ECB feed was never fetched live (src/quotes/live_test.go is opt-in and unrun); no exact price feed exists, only import; valuations do not consume these observations yet",
	})

	// US-006: payout observations and their analysis. Payouts come only from files.
	caps = append(caps, Capability{
		ID: "distributions", Name: "Dividend and distribution observations, calendar and history quality", Scope: Portfolio, Story: "US-006",
		Levels: map[Dimension]Level{
			Implemented: Yes, FixtureVerified: Yes, SourceVerified: No,
			AccountVerified: NotApplicable, InvestmentEvaluated: NotApplicable,
		},
		Evidence: map[Dimension]string{Implemented: "src/distribution", FixtureVerified: "src/distribution/distribution_test.go"},
		Blocker:  "no source supplies payouts: they come only from a file you provide, so coverage is whatever you import; sector and free cash flow are typed by the user, and nothing is cash until the ledger records it",
	})

	// US-007: the ledger. It is written by hand or from a CSV statement; nothing
	// reads a broker or a custodian, so nothing here was checked against an account.
	caps = append(caps, Capability{
		ID: "ledger", Name: "Accounts and immutable double-entry portfolio ledger with lots, unsettled cash, reservations and paper separation", Scope: Portfolio, Story: "US-007",
		Levels: map[Dimension]Level{
			Implemented: Yes, FixtureVerified: Yes, SourceVerified: NotApplicable,
			AccountVerified: NotApplicable, InvestmentEvaluated: NotApplicable,
		},
		Evidence: map[Dimension]string{Implemented: "src/portfolio", FixtureVerified: "src/portfolio/portfolio_test.go"},
		Blocker:  "the journal holds only what you enter or import: no broker or custodian statement is reconciled against it until US-017, income is booked when cash arrives (entitlement and receivables arrive with US-008), and cost basis is a display convention, not tax reporting",
	})

	// US-008 is entitlement from recorded fills and income booked once. It is checked on
	// fixtures only; the payouts are the ones the user or a fixture imported.
	caps = append(caps, Capability{
		ID: "income-accounting", Name: "Dividend entitlement from recorded fills, accruals, receivables, withholding and DRIP without double income", Scope: Portfolio, Story: "US-008",
		Levels: map[Dimension]Level{
			Implemented: Yes, FixtureVerified: Yes, SourceVerified: NotApplicable,
			AccountVerified: NotApplicable, InvestmentEvaluated: NotApplicable,
		},
		Evidence: map[Dimension]string{Implemented: "src/income", FixtureVerified: "src/income/income_test.go"},
		Blocker:  "entitlement is judged by the plain rule in the package doc (shares held before the ex-date); due-bill, special and return-of-capital payouts are reported as unsupported, tax withheld is only what you record, and nothing was checked against a broker statement until US-017",
	})

	caps = append(caps, Capability{
		ID: "research-scorecards", Name: "Sourced multi-year scorecards with sector templates, annotations and versioned content hashes", Scope: Research, Story: "US-009",
		Levels: map[Dimension]Level{
			Implemented: Yes, FixtureVerified: Yes, SourceVerified: NotApplicable,
			AccountVerified: NotApplicable, InvestmentEvaluated: NotApplicable,
		},
		Evidence: map[Dimension]string{Implemented: "src/scorecard", FixtureVerified: "src/scorecard/scorecard_test.go"},
		Blocker:  "figures are only what you import: nothing fetches or maps SEC frames or European filings into this model yet; banks, insurers, REITs and funds get only the few ratios that mean something, and the scorecard evaluates no investment",
	})

	caps = append(caps, Capability{
		ID: "core-strategy", Name: "Valuation scenarios and the core strategy with a scored checklist, entry ceiling and fund template", Scope: Portfolio, Story: "US-010",
		Levels: map[Dimension]Level{
			Implemented: Yes, FixtureVerified: Yes, SourceVerified: NotApplicable,
			AccountVerified: NotApplicable, InvestmentEvaluated: NotApplicable,
		},
		Evidence: map[Dimension]string{Implemented: "src/strategy", FixtureVerified: "src/strategy/strategy_test.go"},
		Blocker:  "starter thresholds are unvalidated research settings and the configuration starts unreviewed; valuation needs your own documented assumptions; proposals carry no probability and no banks, insurers or REITs template exists",
	})

	caps = append(caps, Capability{
		ID: "allocator", Name: "Portfolio and contribution allocator with sourced fees, caps and a wait result", Scope: Portfolio, Story: "US-011",
		Levels: map[Dimension]Level{
			Implemented: Yes, FixtureVerified: Yes, SourceVerified: NotApplicable,
			AccountVerified: NotApplicable, InvestmentEvaluated: NotApplicable,
		},
		Evidence: map[Dimension]string{Implemented: "src/allocate", FixtureVerified: "src/allocate/allocate_test.go"},
		Blocker:  "fees come from a schedule you enter and are not checked against a broker; the sector and issuer cap interpretations await your confirmation; a fund is not looked through; no sale is ever proposed here",
	})

	caps = append(caps, Capability{
		ID: "plan-snapshots", Name: "Immutable plan snapshots with manifest, lifecycle, revision diff and staleness", Scope: Portfolio, Story: "US-012",
		Levels: map[Dimension]Level{
			Implemented: Yes, FixtureVerified: Yes, SourceVerified: NotApplicable,
			AccountVerified: NotApplicable, InvestmentEvaluated: NotApplicable,
		},
		Evidence: map[Dimension]string{Implemented: "src/plan", FixtureVerified: "src/plan/plan_test.go"},
		Blocker:  "a plan holds no evidence-relationship graph beyond the scorecard notes; price, fees and rates are covered by expiry only; concurrent moves are not serialized; the card is a record, nothing here authorizes an order",
	})

	caps = append(caps, Capability{
		ID: "hold-exit", Name: "Hold, reduce and sell rules with an overdue-aware review lifecycle", Scope: Portfolio, Story: "US-013",
		Levels: map[Dimension]Level{
			Implemented: Yes, FixtureVerified: Yes, SourceVerified: NotApplicable,
			AccountVerified: NotApplicable, InvestmentEvaluated: NotApplicable,
		},
		Evidence: map[Dimension]string{Implemented: "src/review", FixtureVerified: "src/review/review_test.go"},
		Blocker:  "thresholds are unvalidated and start unreviewed; rule 6 (rebalance) has no target weights; no costs or taxes are weighed; no sector or fund look-through; event sleeve does not exist yet",
	})

	caps = append(caps, Capability{
		ID: "reservations-intents", Name: "Shared reservations and order-intent state machine", Scope: Portfolio, Story: "US-014",
		Levels: map[Dimension]Level{
			Implemented: Yes, FixtureVerified: Yes, SourceVerified: NotApplicable,
			AccountVerified: NotApplicable, InvestmentEvaluated: NotApplicable,
		},
		Evidence: map[Dimension]string{Implemented: "src/order/engine.go", FixtureVerified: "src/order/service_test.go"},
		Blocker:  "Fills and statuses come from the user or the local paper engine until a broker adapter exists (US-017..019); the policy tier gate (US-016) and execution-grade quote checks are not wired in",
	})

	caps = append(caps, Capability{
		ID: "paper-engine", Name: "Local paper engine with conservative fills", Scope: Portfolio, Story: "US-015",
		Levels: map[Dimension]Level{
			Implemented: Yes, FixtureVerified: Yes, SourceVerified: NotApplicable,
			AccountVerified: NotApplicable, InvestmentEvaluated: NotApplicable,
		},
		Evidence: map[Dimension]string{Implemented: "src/paper/paper.go", FixtureVerified: "src/paper/paper_test.go"},
		Blocker:  "Fills are a simulation from user-supplied daily bars: no intraday data, spread, tax or corporate actions; the Polygon candle store is not bridged",
	})

	caps = append(caps, Capability{
		ID: "tiers", Name: "Tier progression and suspension", Scope: Portfolio, Story: "US-016",
		Levels: map[Dimension]Level{
			Implemented: Yes, FixtureVerified: Yes, SourceVerified: NotApplicable,
			AccountVerified: NotApplicable, InvestmentEvaluated: NotApplicable,
		},
		Evidence: map[Dimension]string{Implemented: "src/tier/tier.go", FixtureVerified: "src/tier/tier_test.go"},
		Blocker:  "Evidence items are your sourced attestations; broker health signals are set by hand until the IBKR adapters exist",
	})

	caps = append(caps, Capability{
		ID: "ibkr-readonly", Name: "IBKR connection and read-only reconciliation", Scope: Broker, Story: "US-017",
		Levels: map[Dimension]Level{
			Implemented: Yes, FixtureVerified: Yes, SourceVerified: NotApplicable,
			AccountVerified: NotApplicable, InvestmentEvaluated: NotApplicable,
		},
		Evidence: map[Dimension]string{Implemented: "src/broker/recon/recon.go", FixtureVerified: "src/broker/ibkr/ibkr_test.go"},
		Blocker:  "adapter shapes are recalled, not re-verified: run broker status and sync against an IBKR paper account and compare with docs/broker.md; needs the account, entity, permissions and entitlements",
	})

	caps = append(caps, Capability{
		ID: "ibkr-submit", Name: "Confirmed IBKR order preview and submission", Scope: Broker, Story: "US-018",
		Levels: map[Dimension]Level{
			Implemented: Yes, FixtureVerified: Yes, SourceVerified: NotApplicable,
			AccountVerified: NotApplicable, InvestmentEvaluated: NotApplicable,
		},
		Evidence: map[Dimension]string{Implemented: "src/cmd/stoxicity/cmd_brokerorder.go", FixtureVerified: "src/cmd/stoxicity/cmd_brokerorder_test.go"},
		Blocker:  "needs a real paper account: confirm the order, whatif and reply shapes and the collar, quote and funds semantics against the current gateway documentation; limits are unreviewed illustrations until you review them",
	})

	caps = append(caps, Capability{
		ID: "order-monitoring", Name: "Order monitoring, cancel and replace", Scope: Broker, Story: "US-019",
		Levels: map[Dimension]Level{
			Implemented: Yes, FixtureVerified: Yes, SourceVerified: NotApplicable,
			AccountVerified: NotApplicable, InvestmentEvaluated: NotApplicable,
		},
		Evidence: map[Dimension]string{Implemented: "src/broker/recon/recon.go", FixtureVerified: "src/cmd/stoxicity/cmd_brokerorder_test.go"},
		Blocker:  "no in-place replace exists by design (cancel and draft again); real executions, corrections and cancel races not yet seen from a real account",
	})

	caps = append(caps, Capability{
		ID: "event-strategy", Name: "Separate time-bounded event strategy", Scope: Portfolio, Story: "US-020",
		Levels: map[Dimension]Level{
			Implemented: Yes, FixtureVerified: Yes, SourceVerified: NotApplicable,
			AccountVerified: NotApplicable, InvestmentEvaluated: NotApplicable,
		},
		Evidence: map[Dimension]string{Implemented: "src/eventstrat/eventstrat.go", FixtureVerified: "src/eventstrat/eventstrat_test.go"},
		Blocker:  "Experimental and paper-only by design; settings are unvalidated and unreviewed until the walk-forward evaluation (US-023) and your review; no price-timing source verified",
	})

	caps = append(caps, Capability{
		ID: "performance", Name: "Portfolio performance and benchmarks", Scope: Portfolio, Story: "US-021",
		Levels: map[Dimension]Level{
			Implemented: Yes, FixtureVerified: Yes, SourceVerified: NotApplicable,
			AccountVerified: NotApplicable, InvestmentEvaluated: NotApplicable,
		},
		Evidence: map[Dimension]string{Implemented: "src/perf/perf.go", FixtureVerified: "src/perf/perf_test.go"},
		Blocker:  "Marks and rates come from user files, not the stored quote and candle stores; no source-verified benchmark data; benchmark choice and evaluation criteria need your approval",
	})

	caps = append(caps, Capability{
		ID: "heldout-news-eval", Name: "Held-out claims and event precision", Scope: Research, Story: "US-022",
		Levels: map[Dimension]Level{
			Implemented: Yes, FixtureVerified: Yes, SourceVerified: NotApplicable,
			AccountVerified: NotApplicable, InvestmentEvaluated: NotApplicable,
		},
		Evidence: map[Dimension]string{Implemented: "src/heldout/heldout.go", FixtureVerified: "src/heldout/heldout_test.go"},
		Blocker:  "No licensed corpus with independent labels exists yet: every gate reads no_external_labels until you supply one",
	})

	caps = append(caps, Capability{
		ID: "walk-forward", Name: "Strategy replay and walk-forward evaluation", Scope: Research, Story: "US-023",
		Levels: map[Dimension]Level{
			Implemented: Yes, FixtureVerified: Yes, SourceVerified: NotApplicable,
			AccountVerified: NotApplicable, InvestmentEvaluated: NotApplicable,
		},
		Evidence: map[Dimension]string{Implemented: "src/walkfwd/walkfwd.go", FixtureVerified: "src/walkfwd/walkfwd_test.go"},
		Blocker:  "No historical point-in-time data, no approved criteria and no strategy has been evaluated: nothing is validated, and synthetic fixtures cannot validate anything",
	})

	caps = append(caps, Capability{
		ID: "plan-cli", Name: "Plan, Portfolio and Income CLI and exports", Scope: Portfolio, Story: "US-024",
		Levels: map[Dimension]Level{
			Implemented: Yes, FixtureVerified: Yes, SourceVerified: NotApplicable,
			AccountVerified: NotApplicable, InvestmentEvaluated: NotApplicable,
		},
		Evidence: map[Dimension]string{Implemented: "src/report/report.go", FixtureVerified: "src/report/report_test.go"},
		Blocker:  "Exports are built from stored records only and checked on fixtures; no real book has been exported or reconciled with a statement, and the command family was never used against a live account",
	})

	caps = append(caps, Capability{
		ID: "plan-tui", Name: "Plan, Portfolio and Income TUI", Scope: Portfolio, Story: "US-025",
		Levels: map[Dimension]Level{
			Implemented: Yes, FixtureVerified: Yes, SourceVerified: NotApplicable,
			AccountVerified: NotApplicable, InvestmentEvaluated: NotApplicable,
		},
		Evidence: map[Dimension]string{Implemented: "src/tui/workflow.go", FixtureVerified: "src/tui/workflow_test.go"},
		Blocker:  "Checked in a terminal against a scratch fixture workspace only; no real data, no payouts, no open plan; no what-if, no policy editor and no broker-connected confirmation in the interface",
	})

	caps = append(caps,
		planned("alerts-journal", "Plan alerts and decision journal", Portfolio, "US-026", "needs plan revisions and the TUI"),
		planned("ops-recovery", "Multi-database recovery drills and operations", Portfolio, "US-027", "backup.sh covers single databases only"),
		planned("release-acceptance", "Release documentation and end-to-end acceptance", Portfolio, "US-028", "needs every other story"),
	)
	return caps
}

// Gaps returns the known weaknesses that are not simply missing features.
func Gaps() []Gap {
	return []Gap{
		{ID: "keyword-table-unversioned", Story: "US-009", Source: "PROTOCOL.md section 12",
			Summary: "the lens version digests cover boosts and demotions, not the keyword table, so a keyword change does not change a recorded version"},
		{ID: "readme-status-stale", Story: "US-028", Source: "docs/production-plan-mode.md section 2",
			Summary: "README.md carries early-status and navigation descriptions that predate the current behavior"},
		{ID: "backup-not-multi-database", Story: "US-027", Source: "docs/production-plan-mode.md section 9",
			Summary: "backup.sh copies each database online; it is not a consistent snapshot across the base and tenant databases, and its row-count check can disagree with a source that changed afterwards"},
		{ID: "claims-corpus-synthetic", Story: "US-022", Source: "app/src/assertion/challenge",
			Summary: "the claim challenge corpus is 39 invented cases; it guards regressions and says nothing about accuracy on real articles"},
		{ID: "no-fx-feed", Story: "US-005", Source: "app/src/valuation",
			Summary: "FX observations now exist (US-005) but valuation does not read them, so quotes in a foreign currency and ADRs are still rejected rather than converted"},
		{ID: "no-recorded-source-verification", Story: "US-001", Source: "this inventory",
			Summary: "no capability has a recorded check against its real source; source_verified stays unknown until a dated record exists"},
	}
}
