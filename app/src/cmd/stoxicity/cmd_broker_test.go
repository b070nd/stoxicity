package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stoxicity/app/src/broker"
	"github.com/b070nd/stoxicity/app/src/broker/fixture"
	"github.com/b070nd/stoxicity/app/src/money"
)

// brokerFixture writes a fixture broker with a paper account and returns its path.
func writeBrokerFixture(t *testing.T, e *testEnv, edit func(*fixture.State)) string {
	t.Helper()
	s := fixture.State{
		Session:    broker.Session{Authenticated: true, Connected: true},
		Accounts:   []broker.Account{{ID: "DU1234567", Type: "INDIVIDUAL", Title: "Paper", Paper: true}, {ID: "U7654321", Type: "INDIVIDUAL", Title: "Live"}},
		Balances:   map[string][]broker.Balance{"DU1234567": {{Currency: "USD", Cash: 100000, Settled: 100000}}, "U7654321": {}},
		Positions:  map[string][]broker.Position{},
		Orders:     map[string][]broker.OpenOrder{},
		Executions: map[string][]broker.Execution{},
		Contracts:  []broker.Contract{{Conid: 265598, Symbol: "AAPL", Name: "APPLE INC", Exchange: "NASDAQ", Currency: "USD", Type: "STK"}, {Conid: 9, Symbol: "AAPL", Name: "APPLE INC", Exchange: "MEXI", Currency: "MXN", Type: "STK"}},
	}
	if edit != nil {
		edit(&s)
	}
	p := filepath.Join(e.dir, "broker.json")
	if _, err := fixture.Create(p, s); err != nil {
		t.Fatal(err)
	}
	return p
}

func brokerEnv(t *testing.T) (*testEnv, string) {
	t.Helper()
	e := newTestEnv(t)
	inst := writeInstruments(t, e)
	raw := e.run(t, "instruments", "example").out
	with := strings.Replace(raw, "    from: \"1980-12-12\"\n    primary: \"yes\"\n", "    from: \"1980-12-12\"\n    primary: \"yes\"\n    contracts:\n      - broker: ibkr\n        conid: \"265598\"\n", 1)
	if with == raw {
		t.Fatal("the example instruments changed shape")
	}
	writeFile(t, e, "instruments.yaml", with)
	e.run(t, "instruments", "import", "-file", inst, "-source", "fixture").wantCode(t, exitOK)
	e.run(t, "portfolio", "book", "create", "-account", "main", "-name", "pb", "-env", "paper_broker", "-base", "USD").wantCode(t, exitOK)
	e.run(t, "portfolio", "book", "create", "-account", "main", "-name", "live", "-env", "real", "-base", "USD").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "main/pb", "deposit", "-date", "2026-10-01", "-amount", "1000.00 USD").wantCode(t, exitOK)
	return e, writeBrokerFixture(t, e, nil)
}

func TestBrokerIsOfflineUntilConfigured(t *testing.T) {
	e := newTestEnv(t)
	e.run(t, "broker").wantCode(t, exitUsage)
	e.run(t, "broker", "frobnicate").wantCode(t, exitUsage)
	e.run(t, "broker", "status").wantOut(t, "OFFLINE", "nothing is sent", "not verified")
	e.run(t, "broker", "config", "show").wantOut(t, "offline")
	r := e.run(t, "broker", "sync", "main/pb")
	r.wantCode(t, exitError)
	e.run(t, "broker", "config", "set", "-transport", "ibkr", "-url", "http://localhost:5000", "-by", "me").wantCode(t, exitUsage)
	e.run(t, "broker", "config", "set", "-transport", "ibkr", "-url", "https://broker.example.com", "-by", "me").wantCode(t, exitUsage)
	e.run(t, "broker", "config", "set", "-transport", "ibkr", "-url", "https://localhost:5000", "-by", "me").wantCode(t, exitOK)
	e.run(t, "broker", "config", "show").wantOut(t, "transport ibkr", "mode read_only", "https://localhost:5000")
	e.run(t, "broker", "fixture", "example").wantOut(t, "minor units", `"accounts"`)
}

func TestBrokerBindKeepsPaperAndLiveApart(t *testing.T) {
	e, fx := brokerEnv(t)
	e.run(t, "broker", "config", "set", "-transport", "fixture", "-fixture", fx, "-by", "me").wantCode(t, exitOK)
	st := e.run(t, "broker", "status")
	st.wantOut(t, "authenticated", "read_only", "****567", "(not bound)", "real-account verification: not verified")
	if strings.Contains(st.out, "1234567") || strings.Contains(st.out, "7654321") {
		t.Fatalf("an account number must be redacted:\n%s", st.out)
	}

	r := e.run(t, "broker", "bind", "main/live", "-account", "DU1234567", "-by", "me")
	r.wantCode(t, exitError)
	r.wantErr(t, "real book", "paper account")
	r = e.run(t, "broker", "bind", "main/pb", "-account", "U7654321", "-by", "me")
	r.wantCode(t, exitError)
	r.wantErr(t, "paper_broker book", "not a paper account")
	e.run(t, "broker", "bind", "main/pb", "-account", "NOPE", "-by", "me").wantCode(t, exitError)
	e.run(t, "broker", "bind", "main/pb", "-account", "DU1234567").wantCode(t, exitUsage)
	e.run(t, "broker", "bind", "main/pb", "-account", "DU1234567", "-by", "me").wantOut(t, "****567", "paper")
	e.run(t, "broker", "bind", "main/live", "-account", "U7654321", "-by", "me").wantOut(t, "live")
	e.run(t, "broker", "status").wantOut(t, "main/pb", "main/live")

	// a local paper book has no broker account
	e.run(t, "portfolio", "book", "create", "-account", "main", "-name", "sim", "-env", "paper_local", "-base", "USD").wantCode(t, exitOK)
	e.run(t, "broker", "bind", "main/sim", "-account", "DU1234567", "-by", "me").wantCode(t, exitError)

	// the environment is checked again on every use: the broker changed what the account is
	writeBrokerFixture(t, e, func(s *fixture.State) { s.Accounts[0].Paper = false })
	r = e.run(t, "broker", "sync", "main/pb")
	r.wantCode(t, exitError)
	r.wantErr(t, "refusing to continue")
}

func TestBrokerSyncRecordsDifferencesAndSuspendsNewRisk(t *testing.T) {
	e, fx := brokerEnv(t)
	e.run(t, "broker", "sync", "main/pb").wantCode(t, exitError)
	e.run(t, "broker", "config", "set", "-transport", "fixture", "-fixture", fx, "-by", "me").wantCode(t, exitOK)
	e.run(t, "broker", "sync", "main/pb").wantCode(t, exitError) // not bound
	e.run(t, "broker", "bind", "main/pb", "-account", "DU1234567", "-by", "me").wantCode(t, exitOK)

	e.run(t, "broker", "sync", "main/pb").wantOut(t, "both sides agree", "****567")
	e.run(t, "broker", "differences").wantOut(t, "no open differences")
	e.run(t, "tier", "signal", "show").wantOut(t, "no health signal was ever recorded")

	// the broker shows less cash, and a holding nobody here bought
	writeBrokerFixture(t, e, func(s *fixture.State) {
		s.Balances["DU1234567"] = []broker.Balance{{Currency: "USD", Cash: 99000, Settled: 99000}}
		s.Positions["DU1234567"] = []broker.Position{{Conid: 265598, Symbol: "AAPL", Qty: money.MustDecimal("3"), Currency: "USD"}, {Conid: 777, Symbol: "ZZZ", Qty: money.MustDecimal("1"), Currency: "USD"}}
	})
	r := e.run(t, "broker", "sync", "main/pb")
	r.wantCode(t, exitOK)
	r.wantOut(t, "cash_mismatch", "1000.00 USD", "990.00 USD", "position_mismatch", "sec_apple", "unmapped_position", "conid 777", "NEW RISK IS SUSPENDED", "Nothing at the broker or in the ledger was overwritten")
	e.run(t, "tier", "signal", "show").wantOut(t, "reconciliation_failed", "ACTIVE")
	e.run(t, "portfolio", "show", "main/pb").wantOut(t, "1000.00")
	e.run(t, "portfolio", "verify").wantCode(t, exitOK)

	// the same differences are not written twice
	e.run(t, "broker", "sync", "main/pb").wantOut(t, "cash_mismatch")
	r = e.run(t, "broker", "differences", "-json")
	if n := strings.Count(r.out, `"state": "open"`); n != 3 {
		t.Fatalf("three open differences, once each:\n%s", r.out)
	}
	e.run(t, "broker", "differences", "main/pb").wantOut(t, "OPEN", "cash_mismatch", "unmapped_position")

	// explained: the differences clear, but resuming new risk is a person's act
	writeBrokerFixture(t, e, nil)
	e.run(t, "broker", "sync", "main/pb").wantOut(t, "both sides agree")
	e.run(t, "broker", "differences").wantOut(t, "no open differences")
	e.run(t, "broker", "differences", "-all").wantOut(t, "cleared", "cash_mismatch")
	e.run(t, "tier", "signal", "show").wantOut(t, "ACTIVE")
	e.run(t, "tier", "signal", "clear", "reconciliation_failed", "-reason", "explained", "-by", "me").wantCode(t, exitOK)

	// a difference that comes back is open again
	writeBrokerFixture(t, e, func(s *fixture.State) {
		s.Balances["DU1234567"][0].Cash = 99000
		s.Balances["DU1234567"][0].Settled = 99000
	})
	e.run(t, "broker", "sync", "main/pb").wantOut(t, "cash_mismatch")
	e.run(t, "broker", "differences").wantOut(t, "OPEN")
}

func TestBrokerFailuresAreReportedAsTheyAre(t *testing.T) {
	e, fx := brokerEnv(t)
	e.run(t, "broker", "config", "set", "-transport", "fixture", "-fixture", fx, "-by", "me").wantCode(t, exitOK)
	e.run(t, "broker", "bind", "main/pb", "-account", "DU1234567", "-by", "me").wantCode(t, exitOK)

	writeBrokerFixture(t, e, func(s *fixture.State) { s.Session.Authenticated = false })
	r := e.run(t, "broker", "sync", "main/pb")
	r.wantCode(t, exitError)
	r.wantErr(t, "log in", "never logs in")
	e.run(t, "broker", "status").wantOut(t, "NOT LOGGED IN")

	writeBrokerFixture(t, e, func(s *fixture.State) { s.Session.Competing = true })
	r = e.run(t, "broker", "sync", "main/pb")
	r.wantCode(t, exitError)
	r.wantErr(t, "another session")
	e.run(t, "broker", "status").wantOut(t, "COMPETING")

	writeBrokerFixture(t, e, func(s *fixture.State) {
		s.Failures = map[string]fixture.Failure{"Balances": {Kind: broker.Pacing, Message: "slow down", RetryAfterMS: 3000}}
	})
	r = e.run(t, "broker", "sync", "main/pb")
	r.wantCode(t, exitError)
	r.wantErr(t, "try again after 3s", "Nothing was retried")
	// a rate limit is not a reason to stop new risk
	e.run(t, "tier", "signal", "show").wantOut(t, "no health signal was ever recorded")

	writeBrokerFixture(t, e, func(s *fixture.State) {
		s.Failures = map[string]fixture.Failure{"Balances": {Kind: broker.Maintenance, Message: "daily maintenance"}}
	})
	r = e.run(t, "broker", "sync", "main/pb")
	r.wantCode(t, exitError)
	r.wantErr(t, "maintenance")
	e.run(t, "tier", "signal", "show").wantOut(t, "broker_unhealthy", "ACTIVE")

	writeBrokerFixture(t, e, func(s *fixture.State) {
		s.Failures = map[string]fixture.Failure{"Quote": {Kind: broker.Entitlement, Message: "no subscription"}}
	})
	e.run(t, "broker", "sync", "main/pb").wantCode(t, exitOK)
}

func TestBrokerContractsSayWhatTheRegisterKnows(t *testing.T) {
	e, fx := brokerEnv(t)
	e.run(t, "broker", "contracts", "AAPL").wantCode(t, exitError)
	e.run(t, "broker", "config", "set", "-transport", "fixture", "-fixture", fx, "-by", "me").wantCode(t, exitOK)
	r := e.run(t, "broker", "contracts", "AAPL")
	r.wantOut(t, "265598", "yes: sec_apple", "MEXI", "no: add contracts", "Nothing is matched by symbol alone")
	e.run(t, "broker", "contracts", "NOSUCH").wantOut(t, "knows no contract")
	e.run(t, "broker", "contracts", "AAPL", "-json").wantOut(t, `"conid": 265598`)
}
