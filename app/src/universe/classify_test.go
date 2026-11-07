package universe

import (
	"slices"
	"testing"

	"github.com/b070nd/stoxicity/app/src/company"
	"github.com/b070nd/stoxicity/app/src/model"
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
		// The Nasdaq directory's own name for the listing beats the company's name.
		{Profile{Ticker: "VFSWW", Name: "VinFast Auto Ltd", Kind: KindStock, Security: "VinFast Auto Ltd. - Warrant"}, TypeWarrant},
		{Profile{Ticker: "AMPGZ", Name: "Amplitech Group, Inc.", Kind: KindStock, Security: "Amplitech Group, Inc. - Series B Right"}, TypeRight},
		{Profile{Ticker: "AACQU", Name: "Armada Acquisition Corp III", Kind: KindStock, Security: "Armada Acquisition Corp. III - Units"}, TypeUnit},
		{Profile{Ticker: "ATRZ", Name: "Adamas Trust, Inc.", Kind: KindStock, Security: "Adamas Trust, Inc. - 9.125% Senior Notes Due 2030"}, TypeNote},
		{Profile{Ticker: "MSFT", Name: "Microsoft Corp", Kind: KindStock, Security: "Microsoft Corporation - Common Stock"}, TypeStock},
		{Profile{Ticker: "TSM", Name: "Taiwan Semiconductor", Kind: KindStock, Security: "Taiwan Semiconductor Manufacturing - American Depositary Shares"}, TypeADR},
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

func TestNumericOperatorsFilterAndSortAndUnknownNumbersMatchNothing(t *testing.T) {
	u := build(t)
	f := Facts{
		Close:  map[string]float64{"AAPL": 190, "NVDA": 900, "KO": 60},
		Change: map[string]float64{"AAPL": -0.02, "NVDA": 0.05},
		Num: map[string]map[string]float64{
			"marketcap": {"AAPL": 3.0e12, "NVDA": 2.2e12, "KO": 2.6e11},
			"margin":    {"AAPL": 0.25, "NVDA": 0.5},
			"revenue":   {"AAPL": 4.0e11, "KO": 4.6e10},
			"buys":      {"NVDA": 250000, "KO": 5000},
			"buyers":    {"NVDA": 3, "KO": 1},
		},
	}
	for expr, want := range map[string][]string{
		"buys:>=100k":                           {"NVDA"}, // what the insiders bought on the open market
		"buyers:>=1 buys:<10k":                  {"KO"},
		"marketcap:>1t":                         {"AAPL", "NVDA"},
		"marketcap:>=260b":                      {"AAPL", "KO", "NVDA"},
		"revenue:1b..100b":                      {"KO"},
		"margin:>30%":                           {"NVDA"},
		"margin:>30":                            {"NVDA"}, // a ratio is a percentage with or without the sign
		"margin:<=25%":                          {"AAPL"},
		"change:<0":                             {"AAPL"},
		"close:>=190 close:<500":                {"AAPL"},
		"-margin:>30% type:stock marketcap:>1t": {"AAPL"},
	} {
		got := find(t, u, expr, f)
		if len(got) == 0 && len(want) > 0 || !slices.Equal(sortedCopy(got), want) {
			t.Errorf("%s = %v, want %v", expr, got, want)
		}
	}
	// Sorting puts the stocks the number is unknown for last, either way round.
	for expr, want := range map[string][]string{
		"marketcap:>0 sort:-marketcap": {"AAPL", "NVDA", "KO"},
		"marketcap:>0 sort:marketcap":  {"KO", "NVDA", "AAPL"},
		"buys:>0 sort:-buys":           {"NVDA", "KO"},
	} {
		if got := find(t, u, expr, f); !slices.Equal(got, want) {
			t.Errorf("%s = %v, want %v", expr, got, want)
		}
	}
	got := find(t, u, "type:stock sort:-margin", f)
	if len(got) < 3 || got[0] != "NVDA" || got[1] != "AAPL" {
		t.Errorf("sort:-margin = %v", got)
	}
	for _, bad := range []string{"marketcap:big", "margin:", "revenue:5..1", "growth:>x%", "sort:pe"} {
		if _, err := ParseQuery(bad); err == nil {
			t.Errorf("%q should not parse", bad)
		}
	}
}

func sortedCopy(s []string) []string { c := slices.Clone(s); slices.Sort(c); return c }

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

func TestAttachedSecuritiesAreReadFromTheSiblingsTicker(t *testing.T) {
	u := New(Inputs{Master: company.Master{Entities: []model.Entity{
		{Ticker: "AACI", Name: "Armada Acquisition Corp. III", CIK: "0002092897", Exchange: "Nasdaq"},
		{Ticker: "AACIU", Name: "Armada Acquisition Corp. III", CIK: "0002092897", Exchange: "Nasdaq"},
		{Ticker: "AACIW", Name: "Armada Acquisition Corp. III", CIK: "0002092897", Exchange: "Nasdaq"},
		{Ticker: "AACIR", Name: "Armada Acquisition Corp. III", CIK: "0002092897", Exchange: "Nasdaq"},
		{Ticker: "ARW", Name: "Arrow Electronics", CIK: "0000007536", Exchange: "NYSE"},
		{Ticker: "AR", Name: "Antero Resources", CIK: "0001433270", Exchange: "NYSE"},
	}}})
	for tk, want := range map[string]string{"AACIU": TypeUnit, "AACIW": TypeWarrant, "AACIR": TypeRight} {
		if p, _ := u.Get(tk); p.Type() != want {
			t.Errorf("%s: %q, want %q", tk, p.Type(), want)
		}
	}
	// A ticker that only ends in the same letter, or whose base is not a sibling, is no such thing.
	for _, tk := range []string{"AACI", "ARW", "AR"} {
		if p, _ := u.Get(tk); p.Type() == TypeUnit || p.Type() == TypeWarrant || p.Type() == TypeRight {
			t.Errorf("%s read as %q", tk, p.Type())
		}
	}
}
