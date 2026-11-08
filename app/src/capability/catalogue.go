package capability

// Default is the catalogue that ships with the product. It lists what the
// product's own code or its plan names, and writes "unknown" for everything
// nobody has checked. No entry has a verification: the first one is recorded
// when someone runs a check against the real endpoint and writes down the date.
// No provider in it costs money.
func Default() *Registry {
	r, err := New(defaultProviders())
	if err != nil {
		panic(err) // the catalogue is code; a test builds it
	}
	return r
}

func defaultProviders() []Provider {
	return []Provider{
		{
			ID: "sec-edgar", Name: "SEC EDGAR (filings, company facts, indexes)",
			DocsURL: "https://www.sec.gov/os/accessing-edgar-data",
			Cost:    CostFree,
			Rights:  "public filings; the SEC's fair-access rules require a declared contact in the User-Agent",
			Quota:   Quota{Requests: 10, Per: "second", Basis: "documented", Note: "per client across the SEC's hosts, as the HTTP client already enforces"},
			Entitlements: []Entitlement{
				{Data: Filings, Markets: []string{"US"}, Instruments: []string{"stock"}, History: Unknown, Delay: "as filed; the daily index is polled", Currencies: "as stated in each filing", Wired: true},
				{Data: Fundamentals, Markets: []string{"US"}, Instruments: []string{"stock"}, History: Unknown, Delay: "as filed", Currencies: "as stated per fact; never assumed", Wired: true},
			},
		},
		{
			ID: "polygon", Name: "Polygon.io (now Massive) daily bars and splits",
			DocsURL: "https://polygon.io/docs",
			Cost:    CostFreeKey, KeyEnv: "MASSIVE_API_KEY",
			Rights: Unknown,
			Quota:  Quota{Basis: Unknown, Note: "whether the user's free entitlement allows these requests is not verified"},
			Entitlements: []Entitlement{
				{Data: PricesEOD, Markets: []string{"US"}, Instruments: []string{"stock"}, History: Unknown, Delay: "end of day (grouped daily bars)", Currencies: "as the source states per candle; empty when it does not say", Wired: true},
				{Data: CorporateAction, Markets: []string{"US"}, Instruments: []string{"stock"}, History: Unknown, Delay: Unknown, Currencies: Unknown, Wired: true, Note: "stock splits only"},
			},
		},
		{
			ID: "ecb-fx", Name: "ECB euro foreign exchange reference rates",
			DocsURL: "https://www.ecb.europa.eu/stats/policy_and_exchange_rates/euro_reference_exchange_rates/html/index.en.html",
			Cost:    CostFree,
			Rights:  "published for information; the ECB discourages use for transactions, so these are reporting rates, not executable quotes",
			Quota:   Quota{Basis: Unknown},
			Entitlements: []Entitlement{
				{Data: FXReference, Markets: []string{"*"}, Instruments: []string{"*"}, History: "daily, 90-day and full-history files (documented, not checked)", Delay: "daily reference", Currencies: "units of foreign currency per 1 EUR", Wired: true, Note: "stoxicity fx fetch; currencies without a minor-unit table here are skipped and counted"},
			},
		},
		{
			ID: "ibkr-web-api", Name: "Interactive Brokers Web API (account, positions, orders, entitled market data)",
			DocsURL: "https://www.interactivebrokers.com/campus/ibkr-api-page/web-api-trading/",
			Cost:    CostAccount,
			Rights:  "account terms; market data needs separate entitlements that can cost extra",
			Quota:   Quota{Basis: Unknown, Note: "pacing limits exist and are not recorded here"},
			Entitlements: []Entitlement{
				{Data: PricesLive, Markets: nil, Instruments: nil, History: Unknown, Delay: Unknown, Currencies: Unknown, Wired: false, Note: "markets depend on the user's entitlements; adapter arrives with US-017"},
			},
		},
		{
			ID: "structured-import", Name: "Structured import (your own files, with source and time kept)",
			Cost:       CostFree,
			Rights:     "your own data; the source and the time it was observed are kept with every record",
			Quota:      Quota{Basis: Unknown},
			ImportOnly: true,
			Entitlements: []Entitlement{
				{Data: Instruments, Markets: []string{"*"}, Instruments: []string{"*"}, History: "as imported", Delay: "as imported", Currencies: "as imported, with the quote unit stated", Wired: true, Note: "stoxicity instruments import"},
				{Data: PricesEOD, Markets: []string{"*"}, Instruments: []string{"*"}, History: "as imported", Delay: "as imported", Currencies: "as imported, with the quote unit stated", Wired: true, Note: "stoxicity prices import; a row without a unit takes the listing's and is marked"},
				{Data: FXReference, Markets: []string{"*"}, Instruments: []string{"*"}, History: "as imported", Delay: "as imported", Currencies: "as imported", Wired: true, Note: "stoxicity fx import; a rate with no basis is stored as a user assumption"},
				{Data: Dividends, Markets: []string{"*"}, Instruments: []string{"*"}, History: "as imported", Delay: "as imported", Currencies: "as imported, with the payout currency stated", Wired: true, Note: "stoxicity dividends import; declared, estimated and cancelled kept apart"},
				{Data: Fundamentals, Markets: []string{"*"}, Instruments: []string{"*"}, History: "as imported", Delay: "as imported", Currencies: "as imported", Wired: false, Note: "import arrives with US-009"},
				{Data: ETFMetadata, Markets: []string{"*"}, Instruments: []string{"*"}, History: "as imported", Delay: "as imported", Currencies: "as imported", Wired: false, Note: "import arrives with US-009"},
				{Data: ETFHoldings, Markets: []string{"*"}, Instruments: []string{"*"}, History: "as imported", Delay: "as imported", Currencies: "as imported", Wired: false, Note: "import arrives with US-009"},
			},
		},
	}
}
