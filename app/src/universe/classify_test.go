package universe

import (
	"slices"
	"testing"
)

func TestTypeReadsTheNameThenTheTicker(t *testing.T) {
	for _, c := range []struct {
		p    Profile
		want string
	}{
		{Profile{Ticker: "KO", Name: "Coca Cola Co", Kind: KindStock}, TypeStock},
		{Profile{Ticker: "QQQ", Name: "Invesco QQQ Trust", Kind: KindETF}, TypeETF},
		{Profile{Ticker: "ZVZZT", Kind: KindTest}, TypeTest},
		{Profile{Ticker: "MFA-C", Name: "MFA Financial 6.50% Series C Cumulative Redeemable Preferred Stock", Kind: KindStock}, TypePreferred},
		{Profile{Ticker: "JPM-D", Name: "J P Morgan Chase & Co Depositary Shares, each representing a 1/400th interest in a share of 5.75% Non-Cumulative Preferred Stock", Kind: KindStock}, TypePreferred},
		{Profile{Ticker: "PCG-PB", Name: "PG&E Corp", Kind: KindStock}, TypePreferred},
		{Profile{Ticker: "AAC-W", Name: "Ares Acquisition Corporation III Redeemable warrants, each whole warrant", Kind: KindStock}, TypeWarrant},
		{Profile{Ticker: "BEBE-WT", Name: "TGE Value Creative Solutions Corp"}, TypeWarrant},
		{Profile{Ticker: "AAC-U", Name: "Ares Acquisition Corporation III Units, each consisting of one Class A share", Kind: KindStock}, TypeUnit},
		{Profile{Ticker: "DGAC-R", Name: "Disciplined Growth Acquisition Corporation Rights, each right entitling the holder to one share", Kind: KindStock}, TypeRight},
		{Profile{Ticker: "TSM", Name: "Taiwan Semiconductor American Depositary Shares", Kind: KindStock}, TypeADR},
		{Profile{Ticker: "BRK-B", Name: "Berkshire Hathaway Inc", Kind: KindStock}, TypeStock}, // a class letter alone says nothing
		{Profile{Ticker: "ACME", Name: "Acme Corp", EntityType: "operating"}, TypeStock},
		{Profile{Ticker: "ODD", Name: "Odd Co"}, ""}, // the sources say nothing
	} {
		if got := c.p.Type(); got != c.want {
			t.Errorf("%s (%q): type %q, want %q", c.p.Ticker, c.p.Name, got, c.want)
		}
	}
}

func TestDomainIsTheSICDivision(t *testing.T) {
	for sic, want := range map[string]string{
		"3571": "Manufacturing", "7372": "Services", "6512": "Finance & Real Estate", "0100": "Agriculture",
		"1311": "Mining", "5411": "Retail", "4911": "Infrastructure", "": "", "9": "", "ab12": "", "9999": "Public Administration", "1900": "",
	} {
		if got := Domain(sic); got != want {
			t.Errorf("Domain(%q) = %q, want %q", sic, got, want)
		}
	}
	if len(Domains()) != 10 {
		t.Errorf("Domains() = %v", Domains())
	}
}

func TestTypeDomainAndPriceOperators(t *testing.T) {
	u := build(t)
	f := Facts{
		Close:  map[string]float64{"AAPL": 190, "NVDA": 900, "KO": 60},
		Change: map[string]float64{"AAPL": -0.02, "NVDA": 0.05, "KO": 0.001},
	}
	if got := find(t, u, "domain:manufacturing", f); !slices.Contains(got, "AAPL") || !slices.Contains(got, "NVDA") || slices.Contains(got, "ACME") {
		t.Errorf("domain:manufacturing = %v", got)
	}
	if got := find(t, u, "domain:finance", f); slices.Contains(got, "ACME") {
		t.Errorf("domain:finance = %v", got)
	}
	if got := find(t, u, "domain:services", f); !slices.Equal(got, []string{"ACME"}) {
		t.Errorf("domain:services = %v", got)
	}
	if got := find(t, u, "type:etf", f); !slices.Contains(got, "QQQ") || slices.Contains(got, "KO") {
		t.Errorf("type:etf = %v", got)
	}
	if got := find(t, u, "sort:-change", f); !slices.Equal(got[:3], []string{"NVDA", "KO", "AAPL"}) {
		t.Errorf("sort:-change = %v", got)
	}
	up := find(t, u, "sort:change", f)
	if !slices.Equal(up[:3], []string{"AAPL", "KO", "NVDA"}) {
		t.Errorf("sort:change = %v", up)
	}
	down := find(t, u, "sort:-close", f)
	if !slices.Equal(down[:3], []string{"NVDA", "AAPL", "KO"}) {
		t.Errorf("sort:-close = %v", down)
	}
	// Without a candle a stock comes last whichever way round the order goes.
	for _, q := range []string{"sort:close", "sort:-close", "sort:change", "sort:-change"} {
		got := find(t, u, q, f)
		for _, last := range got[3:] {
			if _, ok := f.Close[last]; ok {
				t.Errorf("%s: %s, which has a candle, is among the unpriced %v", q, last, got[3:])
			}
		}
	}
	if _, err := ParseQuery("type:bond"); err == nil {
		t.Error("type:bond should be an error that lists the types")
	}
}
