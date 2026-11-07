package tui

import (
	"strings"
	"testing"

	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/nav"
)

func TestStockPageListsTheInsiderTradesWithSizePriceAndWho(t *testing.T) {
	h := newHarness(t)
	h.be.insiders = map[string][]model.InsiderTrade{"NVDA": {
		{Accession: "0000000001-26-000007", CIK: "0001045810", Owner: "Doe Jane", Role: "Chief Executive Officer, director", Date: "2026-11-05",
			Security: "Common Stock", Period: "2026-11-05", Code: "P", Acquired: true, Shares: 10000, Price: 12.5, After: 60000, Direct: true},
		{Accession: "0000000001-26-000006", CIK: "0001045810", Owner: "Roe Richard", Role: "director", Date: "2026-11-03",
			Security: "Common Stock", Period: "2026-11-03", Code: "S", Shares: 4000, Price: 193.8, After: 1147, Direct: false, Plan: true},
		{Accession: "0000000001-26-000005", CIK: "0001045810", Owner: "Doe Jane", Date: "2026-11-01", Code: "A", Acquired: true, Shares: 5000},
	}}
	h.numbersFor(map[string]map[string]float64{
		"buys": {"NVDA": 125000}, "buyers": {"NVDA": 1}, "sells": {"NVDA": 775200}, "sellers": {"NVDA": 1},
	})
	h.m.goTo(nav.Route{Kind: nav.Stock, Arg: "NVDA"})
	h.run(h.m.loadCmd(false))
	h.mustContain("Insiders",
		"last 90 days, common stock: bought 125K (1 person), sold 775K (1 person)",
		"2026-11-05", "purchase", "10,000 shares at 12.50 = 125K", "Doe Jane, Chief Executive Officer, director", "holds 60,000 after",
		"sale", "4,000 shares at 193.80", "10b5-1 plan", "held indirectly",
		"granted or awarded", "5,000 shares")

	var good, bad, dim int
	for _, r := range h.m.scr.Rows {
		switch {
		case strings.Contains(r.Text, "purchase") && r.URL != "":
			good += map[bool]int{true: 1}[r.Style == Good]
			if !strings.HasPrefix(r.URL, "https://www.sec.gov/Archives/edgar/data/1045810/") {
				t.Errorf("a trade opens its filing: %q", r.URL)
			}
		case strings.Contains(r.Text, "sale"):
			bad += map[bool]int{true: 1}[r.Style == Bad]
		case strings.Contains(r.Text, "granted or awarded"):
			dim += map[bool]int{true: 1}[r.Style == Dim]
		}
	}
	if good != 1 || bad != 1 || dim != 1 {
		t.Errorf("a purchase is green, a sale red, an award dim: %d %d %d", good, bad, dim)
	}

	h.m.goTo(nav.Route{Kind: nav.Stock, Arg: "KO"})
	h.run(h.m.loadCmd(false))
	if strings.Contains(h.view(), "Insiders") {
		t.Error("a company with no stored trade has no insiders block")
	}
}

func TestCompaniesShowWhatInsidersBoughtAndSoldAndSortAndFilterByIt(t *testing.T) {
	h := newHarness(t)
	h.numbersFor(map[string]map[string]float64{
		"buys": {"NVDA": 125000, "KO": 0}, "sells": {"NVDA": 0, "KO": 2.4e6}, "buyers": {"NVDA": 2, "KO": 0}, "sellers": {"NVDA": 0, "KO": 3},
	})
	h.m.goTo(nav.Route{Kind: nav.Companies, Arg: "group:none sort:-buys"})
	h.run(h.m.loadCmd(false))
	h.mustContain("BUYS", "SELLS", "125K", "2.40M")
	h.m.goTo(nav.Route{Kind: nav.Companies, Arg: "group:none buys:>=100k"})
	h.run(h.m.loadCmd(false))
	if v := h.view(); !strings.Contains(v, "NVDA") || strings.Contains(v, "Coca") {
		t.Errorf("buys:>=100k keeps NVDA and not Coca-Cola:\n%s", v)
	}
	h.m.goTo(nav.Route{Kind: nav.Companies, Arg: "group:none buyers:>=2"})
	h.run(h.m.loadCmd(false))
	if v := h.view(); !strings.Contains(v, "NVDA") || strings.Contains(v, "Coca") {
		t.Errorf("buyers:>=2 keeps NVDA:\n%s", v)
	}
}
