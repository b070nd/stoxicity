package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/b070nd/stoxicity/app/src/nav"
)

func (h *harness) chipRow() string {
	for _, r := range h.m.scr.Rows {
		if len(r.Chips) > 0 {
			return r.Text
		}
	}
	return ""
}

func TestExchangeCloudIsOnHomeCompaniesAndExchanges(t *testing.T) {
	h := newHarness(t)
	for _, key := range []string{"h", "c", "e"} {
		h.press(key)
		row := h.chipRow()
		for _, want := range []string{"All 4", "Nasdaq 3", "NYSE 1"} {
			if !strings.Contains(row, want) {
				t.Errorf("after %s the exchange cloud %q lacks %q", key, row, want)
			}
		}
		if strings.Index(row, "Nasdaq") > strings.Index(row, "NYSE") {
			t.Errorf("the biggest exchange comes first: %q", row)
		}
	}
	h.press("t")
	if h.chipRow() != "" {
		t.Error("the topics screen has no exchange cloud")
	}
}

func TestExchangeCloudWeighsByCount(t *testing.T) {
	h := newHarness(t)
	var nasdaq, nyse Style
	for _, c := range h.m.scr.Rows[1].Chips {
		switch c.Value {
		case "Nasdaq":
			nasdaq = c.Style
		case "NYSE":
			nyse = c.Style
		}
	}
	if nasdaq <= nyse {
		t.Errorf("Nasdaq (3 companies) is drawn %v, NYSE (1) %v: the bigger must be heavier", nasdaq, nyse)
	}
}

func TestUpFromTheFirstRowStepsIntoTheCloudAndTheArrowsPick(t *testing.T) {
	h := newHarness(t)
	h.press("c")
	if h.m.chipFocus {
		t.Fatal("the cloud has the focus only when asked")
	}
	// The list opens on the first company, below the heading of its group.
	h.press("up")
	h.mustContain("↑ exchanges")
	h.press("down", "up") // still in the list: not stepped into the cloud
	if h.m.chipFocus {
		t.Fatal("up moves the cursor while there is a row above")
	}
	h.press("up")
	if !h.m.chipFocus {
		t.Fatal("up from the first row steps into the cloud")
	}
	h.mustContain("← →", "pick", "back to the list")
	if bar := h.barLine(); strings.Contains(bar, "filter") {
		t.Errorf("the list's keys are out of the way while choosing: %q", bar)
	}
	h.press("right") // All -> Nasdaq
	h.press("enter")
	if h.m.exchange != "Nasdaq" {
		t.Fatalf("exchange = %q after → enter", h.m.exchange)
	}
	h.mustContain("●Nasdaq 3", "exchange Nasdaq")
	h.press("right", "space") // NYSE
	if h.m.exchange != "NYSE" || !h.m.chipFocus {
		t.Fatalf("exchange = %q, focus %v: space picks too, and the cloud keeps the focus", h.m.exchange, h.m.chipFocus)
	}
	if strings.Contains(h.rows(), "NVDA") {
		t.Error("narrowed to NYSE: Nvidia is gone")
	}
	h.press("right", "right") // the end stays the end
	h.press("home", "enter")
	if h.m.exchange != "" {
		t.Errorf("home is the All chip: exchange %q", h.m.exchange)
	}
	h.press("down")
	if h.m.chipFocus {
		t.Error("down goes back to the list")
	}
	h.press("home", "up", "esc")
	if h.m.chipFocus {
		t.Error("esc leaves the cloud")
	}
	if h.m.stack.Current().Kind != nav.Companies {
		t.Error("and stays on the screen")
	}
}

func TestAKeyOutsideTheCloudLeavesItAndDoesItsWork(t *testing.T) {
	h := newHarness(t)
	h.press("c", "up", "up")
	if !h.m.chipFocus {
		t.Fatal("setup")
	}
	h.press("t")
	if h.m.chipFocus || h.m.stack.Current().Kind != nav.Topics {
		t.Errorf("t: focus %v, screen %v", h.m.chipFocus, h.m.stack.Current().Kind)
	}
}

func TestTheCloudStartsOnTheChosenExchange(t *testing.T) {
	h := newHarness(t)
	h.press("x", "x") // NYSE
	h.press("c", "up", "up")
	chips, _ := h.m.chipList()
	if chips[h.m.chip].Value != "NYSE" {
		t.Errorf("the focus starts on %q, want the chosen NYSE", chips[h.m.chip].Value)
	}
}

func TestRightOpensAndLeftGoesBack(t *testing.T) {
	h := newHarness(t)
	h.openStocks()
	h.press("right") // the first stock's page
	if k := h.m.stack.Current().Kind; k != nav.Stock {
		t.Fatalf("→ on a stock opens it, got %v", k)
	}
	h.selectArticle("Nvidia beats estimates")
	h.press("right")
	h.mustContain("Why this score")
	h.press("left")
	if h.m.detail != nil {
		t.Error("← closes the article")
	}
	h.press("left")
	if k := h.m.stack.Current().Kind; k == nav.Stock {
		t.Error("← goes up from the stock page")
	}
}

func TestDigitsInAnArticleOpenItsStocks(t *testing.T) {
	h := newHarness(t)
	h.openStocks()
	h.press("enter")
	h.selectArticle("Nvidia beats estimates")
	h.press("enter")
	h.press("9")
	if h.m.detail == nil || !h.m.failed {
		t.Errorf("no ninth stock: detail %v, failed %v, status %q", h.m.detail != nil, h.m.failed, h.m.status)
	}
	h.press("1")
	if h.m.detail != nil || h.m.stack.Current().Kind != nav.Stock {
		t.Fatalf("1 opens the first stock: detail %v, screen %v", h.m.detail != nil, h.m.stack.Current())
	}
	if got := h.m.stack.Current().Arg; got != "NVDA" {
		t.Errorf("opened %q", got)
	}
}

// The same data always draws the same screens: the cloud, the tiers, the
// groups and the order of the rows come from sorted data, never from a map.
func TestScreensAreDeterministic(t *testing.T) {
	texts := func(h *harness) string {
		var b strings.Builder
		for _, r := range h.m.scr.Rows {
			b.WriteString(r.Text + "\n")
		}
		return b.String()
	}
	first := map[string]string{}
	for round := 0; round < 5; round++ {
		h := newHarness(t)
		for _, key := range []string{"h", "s", "c", "e", "t", "d", "o"} {
			h.press(key)
			got := texts(h)
			if round == 0 {
				first[key] = got
			} else if got != first[key] {
				t.Fatalf("key %s: round %d drew different rows", key, round)
			}
		}
		if view := ansi.Strip(h.view()); round == 0 {
			first["view"] = view
		} else if view != first["view"] {
			t.Fatalf("round %d drew a different view", round)
		}
	}
	a, b := newHarness(t), newHarness(t)
	if !reflect.DeepEqual(a.m.scr.Rows[1].Chips, b.m.scr.Rows[1].Chips) {
		t.Error("the chips differ between two builds")
	}
}
