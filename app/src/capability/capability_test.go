package capability

import (
	"strings"
	"testing"
)

func keys(have ...string) func(string) bool {
	return func(env string) bool {
		for _, h := range have {
			if h == env {
				return true
			}
		}
		return false
	}
}

func find(as []Answer, id string) Answer {
	for _, a := range as {
		if a.Provider.ID == id {
			return a
		}
	}
	return Answer{}
}

func TestDefaultCatalogueIsWellFormedAndPromisesNothing(t *testing.T) {
	r := Default()
	for _, p := range r.Providers() {
		if p.Verified.At != "" {
			t.Errorf("%s claims a verification with no check on record", p.ID)
		}
		if p.Cost == CostPaid {
			t.Errorf("%s: no provider in the default catalogue costs money", p.ID)
		}
		if p.Rights == "" {
			t.Errorf("%s: rights", p.ID)
		}
	}
}

func TestNothingUnwiredIsEverUsable(t *testing.T) {
	r := Default()
	o := Options{AccountReady: map[string]bool{"ibkr-web-api": true}, HasKey: func(string) bool { return true }}
	for _, p := range r.Providers() {
		for _, e := range p.Entitlements {
			a := find(r.Coverage(Request{Data: e.Data}, o), p.ID)
			if !e.Wired && (a.State == Usable || a.State == ImportOnly) {
				t.Errorf("%s/%s has no code but is %s", p.ID, e.Data, a.State)
			}
		}
	}
}

func TestFreeKeyNeedsTheKey(t *testing.T) {
	r := Default()
	req := Request{Data: PricesEOD, Market: "US", Kind: "stock"}
	if a := find(r.Coverage(req, Options{}), "polygon"); a.State != NeedsKey || !strings.Contains(a.Reason, "MASSIVE_API_KEY") {
		t.Errorf("%+v", a)
	}
	if a := find(r.Coverage(req, Options{HasKey: keys("MASSIVE_API_KEY")}), "polygon"); a.State != Usable {
		t.Errorf("%+v", a)
	}
	if c := r.Choose(req, Options{HasKey: keys("MASSIVE_API_KEY")}); !c.Automatic || c.Provider.ID != "polygon" {
		t.Errorf("%+v", c)
	}
}

func TestEuropeanPricesHaveNoAutomaticCoverage(t *testing.T) {
	r := Default()
	req := Request{Data: PricesEOD, Market: "DE", Kind: "stock"}
	o := Options{HasKey: keys("MASSIVE_API_KEY")}
	if a := find(r.Coverage(req, o), "polygon"); a.State != NoEntitlement || !strings.Contains(a.Reason, "covers US, not DE") {
		t.Errorf("a US-only source must not claim Germany: %+v", a)
	}
	c := r.Choose(req, o)
	if c.Automatic {
		t.Errorf("%+v", c)
	}
	// The only way in is the structured import, and the answer says so: no
	// provider is chosen as if it were automatic.
	if !c.ImportOnly || c.Provider == nil || c.Provider.ID != "structured-import" {
		t.Errorf("%+v", c)
	}
	if ans := find(r.Coverage(req, o), "structured-import"); ans.State != ImportOnly {
		t.Errorf("%+v", ans)
	}
	// Fundamentals have an import path in the plan but no code yet: not wired, not import-only.
	if ans := find(r.Coverage(Request{Data: Fundamentals, Market: "DE"}, o), "structured-import"); ans.State != NotWired {
		t.Errorf("%+v", ans)
	}
	// Instruments do have a working import path today, and it says so.
	ci := r.Choose(Request{Data: Instruments, Market: "DE"}, Options{})
	if !ci.ImportOnly || ci.Provider.ID != "structured-import" || ci.Automatic {
		t.Errorf("%+v", ci)
	}
}

func TestPaidAndAccountProvidersStayOffUntilAllowed(t *testing.T) {
	ent := func(markets, kinds []string, d DataType) []Entitlement {
		return []Entitlement{{Data: d, Markets: markets, Instruments: kinds, History: Unknown, Delay: Unknown, Currencies: Unknown, Wired: true}}
	}
	paid := Provider{ID: "vendor", Name: "A paid vendor", Cost: CostPaid, Rights: Unknown, Quota: Quota{Basis: Unknown},
		Entitlements: ent([]string{"*"}, []string{"*"}, PricesEOD)}
	free := Provider{ID: "free", Name: "Free", Cost: CostFree, Rights: Unknown, Quota: Quota{Basis: Unknown},
		Entitlements: ent([]string{"US"}, []string{"stock"}, PricesEOD)}
	r, err := New([]Provider{paid, free})
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Data: PricesEOD, Market: "DE", Kind: "etf"}
	// Only the paid vendor covers Germany; with no permission there is no fallback to it.
	if a := find(r.Coverage(req, Options{}), "vendor"); a.State != PaidNotAllowed {
		t.Errorf("%+v", a)
	}
	if c := r.Choose(req, Options{}); c.Automatic || c.Provider != nil {
		t.Errorf("a paid provider was chosen without permission: %+v", c)
	}
	if c := r.Choose(req, Options{AllowPaid: map[string]bool{"vendor": true}}); !c.Automatic || c.Provider.ID != "vendor" {
		t.Errorf("%+v", c)
	}
	// Where a free one covers it, the free one is chosen even if the paid one is allowed.
	usReq := Request{Data: PricesEOD, Market: "US", Kind: "stock"}
	if c := r.Choose(usReq, Options{AllowPaid: map[string]bool{"vendor": true}}); c.Provider.ID != "free" {
		t.Errorf("free must come before paid: %+v", c)
	}
	// An account provider needs its account set up.
	acct := Provider{ID: "broker", Name: "Broker", Cost: CostAccount, Rights: Unknown, Quota: Quota{Basis: Unknown},
		Entitlements: ent([]string{"*"}, []string{"*"}, PricesLive)}
	r2, _ := New([]Provider{acct})
	if a := find(r2.Coverage(Request{Data: PricesLive}, Options{}), "broker"); a.State != NeedsAccount {
		t.Errorf("%+v", a)
	}
	if a := find(r2.Coverage(Request{Data: PricesLive}, Options{AccountReady: map[string]bool{"broker": true}}), "broker"); a.State != Usable {
		t.Errorf("%+v", a)
	}
}

func TestUnknownCoverageIsNotCoverage(t *testing.T) {
	r := Default()
	// IBKR's markets are unknown, so a market question gets no yes.
	o := Options{AccountReady: map[string]bool{"ibkr-web-api": true}}
	a := find(r.Coverage(Request{Data: PricesLive, Market: "DE"}, o), "ibkr-web-api")
	if a.State != NoEntitlement || !strings.Contains(a.Reason, "not known") {
		t.Errorf("%+v", a)
	}
}

func TestRegistryRejectsMalformedProviders(t *testing.T) {
	good := Provider{ID: "x", Name: "X", Cost: CostFree, Rights: Unknown, Quota: Quota{Basis: Unknown}}
	bad := map[string]func(*Provider){
		"no id":              func(p *Provider) { p.ID = "" },
		"bad cost":           func(p *Provider) { p.Cost = "cheap" },
		"key without env":    func(p *Provider) { p.Cost = CostFreeKey },
		"no rights":          func(p *Provider) { p.Rights = "" },
		"bad quota basis":    func(p *Provider) { p.Quota.Basis = "hope" },
		"quota with no rate": func(p *Provider) { p.Quota.Basis = "documented" },
		"unknown data type": func(p *Provider) {
			p.Entitlements = []Entitlement{{Data: "tips", History: Unknown, Delay: Unknown, Currencies: Unknown}}
		},
		"unstated delay": func(p *Provider) {
			p.Entitlements = []Entitlement{{Data: PricesEOD, History: Unknown, Currencies: Unknown}}
		},
		"verification half": func(p *Provider) { p.Verified = Verification{At: "2026-11-08"} },
	}
	for name, edit := range bad {
		p := good
		edit(&p)
		if _, err := New([]Provider{p}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := New([]Provider{good, good}); err == nil {
		t.Error("duplicate id")
	}
	if _, err := New([]Provider{good}); err != nil {
		t.Error(err)
	}
}
