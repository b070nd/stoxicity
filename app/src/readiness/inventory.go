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

	caps = append(caps,
		planned("core-strategy", "Valuation scenarios and the core strategy", Portfolio, "US-010", "needs scorecards"),
		planned("allocator", "Portfolio and contribution allocator", Portfolio, "US-011", "needs strategy proposals and the ledger"),
		planned("plan-snapshots", "Immutable plan snapshots and explanations", Portfolio, "US-012", "needs allocator, income and valuation"),
		planned("hold-exit", "Hold, reduce and sell rules with review lifecycle", Portfolio, "US-013", "needs plan snapshots"),
		planned("reservations-intents", "Shared reservations and order-intent state machine", Portfolio, "US-014", "needs ledger and plans"),
		planned("paper-engine", "Local paper engine with conservative fills", Portfolio, "US-015", "needs intents, income and exit rules"),
		planned("tiers", "Tier progression and suspension", Portfolio, "US-016", "needs policy, exit rules and the paper engine"),
		planned("ibkr-readonly", "IBKR connection and read-only reconciliation", Broker, "US-017", "needs an IBKR account and the supported retail session flow, reverified"),
		planned("ibkr-submit", "Confirmed IBKR order preview and submission", Broker, "US-018", "needs read-only sync, tiers and current execution-grade quotes"),
		planned("order-monitoring", "Order monitoring, cancel and replace", Broker, "US-019", "needs confirmed submission"),
		planned("event-strategy", "Separate time-bounded event strategy", Portfolio, "US-020", "needs scorecards, paper engine and tiers"),
		planned("performance", "Performance, income and benchmark reporting", Portfolio, "US-021", "needs the ledger, income accounting and marks"),
		planned("heldout-news-eval", "Held-out real-news evaluation", Research, "US-022", "needs an independently labelled real corpus"),
		planned("walk-forward", "Walk-forward strategy evaluation", Portfolio, "US-023", "needs years of point-in-time history; the dataset starts around November 2026"),
		planned("plan-cli", "Plan, Portfolio and Income CLI and exports", Portfolio, "US-024", "needs the plan, ledger and income modules"),
		planned("plan-tui", "Plan, Portfolio and Income TUI", Portfolio, "US-025", "needs the CLI layer"),
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
