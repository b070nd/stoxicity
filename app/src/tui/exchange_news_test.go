package tui

import (
	"strings"
	"testing"

	"github.com/b070nd/stoxicity/app/src/nav"
)

func TestExchangePageAggregatesItsNewsAndLinksEveryStockInIt(t *testing.T) {
	h := newHarness(t)
	h.run(h.m.goTo(nav.Route{Kind: nav.Exchange, Arg: "Nasdaq"}))
	rows := h.rows()
	for _, want := range []string{"Latest news about its stocks ›", "Nvidia beats estimates", "Stocks in the news ›", "NVDA"} {
		if !strings.Contains(rows, want) {
			t.Errorf("the Nasdaq page lacks %q", want)
		}
	}
	if strings.Contains(rows, "Coca-Cola raises dividend") {
		t.Error("the NYSE news is not Nasdaq's")
	}
	// Each stock of the news is a row that opens its page.
	found := false
	for _, r := range h.m.scr.Rows {
		if r.Go != nil && r.Go.Kind == nav.Stock && r.Go.Arg == "NVDA" && strings.Contains(r.Text, "NVDA") {
			found = true
		}
	}
	if !found {
		t.Error("no row of the Nasdaq page links to the NVDA page")
	}
	// The heading opens the list of those stocks, narrowed to the exchange.
	for i, r := range h.m.scr.Rows {
		if strings.HasPrefix(r.Text, "Stocks in the news") {
			if r.Go == nil || r.Go.Kind != nav.Companies || !strings.Contains(r.Go.Arg, `exchange:"Nasdaq"`) {
				t.Fatalf("heading %d goes to %+v", i, r.Go)
			}
			h.m.vp.Select(h.m.vp.IndexOf(i))
			h.press("right")
			if got := h.rows(); strings.Contains(got, "Coca-Cola") || !strings.Contains(got, "Nvidia") {
				t.Errorf("the linked list is not Nasdaq's:\n%s", got)
			}
			return
		}
	}
	t.Fatal("no Stocks in the news heading")
}

func TestStockPageLinksToItsExchange(t *testing.T) {
	h := newHarness(t)
	h.run(h.m.goTo(nav.Route{Kind: nav.Stock, Arg: "KO"}))
	for i, r := range h.m.scr.Rows {
		if strings.Contains(r.Text, "listed") && strings.Contains(r.Text, "NYSE") {
			if r.Go == nil || r.Go.Kind != nav.Exchange || r.Go.Arg != "NYSE" {
				t.Fatalf("the listed row goes to %+v", r.Go)
			}
			h.m.vp.Select(h.m.vp.IndexOf(i))
			h.press("right")
			if cur := h.m.stack.Current(); cur.Kind != nav.Exchange {
				t.Errorf("→ on the listed row opened %v", cur)
			}
			return
		}
	}
	t.Fatal("the stock page has no listed row")
}

func TestOpenArticleNumbersItsStocks(t *testing.T) {
	h := newHarness(t)
	h.openStocks()
	h.press("enter")
	h.selectArticle("Nvidia beats estimates")
	h.press("enter")
	h.mustContain("stocks: 1 NVDA", "1-9 open a stock", "← back")
}
