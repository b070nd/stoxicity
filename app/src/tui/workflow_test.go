package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/b070nd/stoxicity/app/src/nav"
	"github.com/b070nd/stoxicity/app/src/plan"
	"github.com/b070nd/stoxicity/app/src/report"
)

// wfBackend is the news fake with a workflow on top, built from canned data.
// It counts every read, so a test can show that drawing reads nothing.
type wfBackend struct {
	*fake
	reads     atomic.Int32
	confirmed []string
	confirmOK bool
	plans     []report.PlanRow
	orders    []report.OrderRow
	income    report.IncomeView
	policy    report.PolicyStatus
	books     []report.BookRef
}

const longName = "Überlängen-Gesellschaft für außergewöhnlich ausführliche Firmenbezeichnungen 日本語の会社名 🚀 AG"

func newWF() *wfBackend {
	b := &wfBackend{fake: newFake(), confirmOK: true}
	b.plans = []report.PlanRow{
		{Revision: "pl_aaaaaaaaaaaa.r2", Security: "sec_apple", Name: "Apple Inc", Book: "main/core", Sleeve: "core", Action: "BUY", State: "draft", Recorded: "draft", Qty: "3", Expiry: "2026-11-09", Data: "ok", Strategy: "reviewed", Execution: "eligible", Currency: "EUR", Unknowns: 1},
		{Revision: "pl_bbbbbbbbbbbb.r1", Security: "sec_long", Name: longName, Book: "main/core", Sleeve: "core", Action: "SELL", State: "proposed", Recorded: "proposed", Qty: "10", Expiry: "2026-11-04", Data: "stale", Strategy: "reviewed", Execution: "blocked", Currency: "EUR"},
		{Revision: "pl_cccccccccccc.r1", Security: "sec_sap", Name: "SAP SE", Book: "main/core", Sleeve: "event", Action: "ABSTAIN", State: "draft", Recorded: "draft", Expiry: "2026-11-12", Data: "ok", Strategy: "experimental", Execution: "paper only", Currency: "EUR"},
		{Revision: "pl_dddddddddddd.r1", Security: "sec_old", Name: "Old Corp", Book: "main/core", Sleeve: "core", Action: "BUY", State: "expired", Recorded: "draft", Expiry: "2026-11-01", Data: "ok", Strategy: "reviewed", Execution: "eligible", Currency: "EUR"},
	}
	b.orders = []report.OrderRow{
		{ID: "ord_1111", Book: "bk_paper", Env: "paper_local", State: "draft", Side: "buy", Security: "sec_apple", Qty: "3", Filled: "0", Remaining: "3", Limit: "180.00", Currency: "USD", Type: "limit", TIF: "DAY", FeeEstimate: "1.25 USD", TermsHash: "abc123", Plan: "pl_aaaaaaaaaaaa.r2", Warnings: []string{"price not checked against a quote"}, Why: []string{}, Late: []string{}, Created: "2026-11-03T10:00:00Z", Confirmable: true},
		{ID: "ord_2222", Book: "bk_real", Env: "real", State: "draft", Side: "sell", Security: "sec_long", Qty: "10", Filled: "0", Remaining: "10", Limit: "50.00", Currency: "EUR", Type: "limit", TIF: "DAY", FeeEstimate: "2.00 EUR", TermsHash: "def456", Warnings: []string{}, Why: []string{}, Late: []string{}, Created: "2026-11-03T11:00:00Z", NotBecause: "this book is bound to a broker account: confirm with the order confirm command, which checks the broker's preview first"},
		{ID: "ord_3333", Book: "bk_paper", Env: "paper_local", State: "unknown", Side: "buy", Security: "sec_sap", Qty: "1", Filled: "0", Remaining: "1", Limit: "100.00", Currency: "EUR", Type: "limit", TIF: "DAY", FeeEstimate: "1.00 EUR", TermsHash: "999", Warnings: []string{}, Why: []string{"the answer to the send was lost"}, Late: []string{}, Created: "2026-11-03T12:00:00Z", Unknown: true, NotBecause: "only a draft or an expired confirmation can be confirmed"},
	}
	b.income = report.IncomeView{Day: "2026-11-08", Rows: []report.IncomeRow{
		{Book: "main/core", Payout: "sec_apple|cash|2026Q4", Security: "sec_apple", ExDay: "2026-11-10", PayDay: "2026-11-14", Status: "entitled", Shares: "3", PerShare: "0.25 USD", Gross: "0.75 USD", Booked: "-"},
		{Book: "main/core", Payout: "sec_sap|cash|2026", Security: "sec_sap", ExDay: "2026-12-01", Status: "pending", Reason: "declared, not yet paid", Shares: "1", PerShare: "2.00 EUR", Gross: "2.00 EUR", Booked: "-"},
	}, Findings: []string{"main/core, sec_x|cash|2025: accrual differs (expected 1.00 EUR, ledger 2.00 EUR)"}}
	b.policy = report.PolicyStatus{Active: true, Epoch: 3, Hash: "polhash", Tier: 1, At: "2026-11-01T00:00:00Z", Changes: []report.PolicyChange{{Path: "constraints.max_issuer", Old: "0.10", New: "0.30"}}, Effects: []string{"1 plan is stale"}, RiskNotes: []string{}}
	b.books = []report.BookRef{{ID: "bk_paper", Account: "main", Name: "paper", Env: "paper_local", Base: "EUR", Status: "open"}, {ID: "bk_real", Account: "main", Name: "core", Env: "real", Base: "EUR", Status: "open"}}
	return b
}

func (b *wfBackend) PolicyStatus(context.Context) (report.PolicyStatus, error) {
	b.reads.Add(1)
	return b.policy, nil
}
func (b *wfBackend) PlanRows(context.Context) ([]report.PlanRow, error) {
	b.reads.Add(1)
	return b.plans, nil
}
func (b *wfBackend) PlanDoc(_ context.Context, ref string) (report.PlanDoc, []int, error) {
	b.reads.Add(1)
	id, _, _ := plan.ParseRef(ref)
	card := plan.Card{AsOf: "2026-11-03", Expiry: "2026-11-09", Security: "sec_apple", Name: longName, Book: "bk_real", Sleeve: "core", Strategy: "core-v1", Action: "BUY",
		DataState: "ok", StrategyState: "reviewed", ExecutionEligibility: "eligible", Reasons: []string{"quality passes"}, Unknowns: []string{"spread unavailable"}, Score: 72, MaxScore: 100,
		Sizing: &plan.Sizing{Qty: "3", Base: "EUR", Notional: 50000, Fee: 125, Reserve: "ok"}, Counter: []string{longName + " " + longName}}
	return report.PlanDoc{Revision: plan.Revision{PlanID: id, Rev: 2, Card: card, ManifestHash: "mh", Manifest: plan.Manifest{Ledger: "lh"}}, State: "draft", Effective: "draft", Intact: false}, []int{1, 2}, nil
}
func (b *wfBackend) Books(context.Context) ([]report.BookRef, error) {
	b.reads.Add(1)
	return b.books, nil
}
func (b *wfBackend) BookReport(_ context.Context, book string) (report.Report, error) {
	b.reads.Add(1)
	env := "real"
	if strings.Contains(book, "paper") {
		env = "paper_local"
	}
	return report.Report{Book: book, Env: env, Base: "EUR", AsOf: "2026-11-08", Ledger: "fp", Policy: "epoch 3",
		Cash:     []report.Cash{{Currency: "EUR", Settled: "1000.00 EUR", Unsettled: "0.00 EUR", Reserved: "10.00 EUR", Available: "990.00 EUR"}},
		Holdings: []report.Holding{{Security: "sec_apple", Qty: "3", Cost: "540.00 USD", Lots: 1}},
		Orders:   []report.Order{{ID: "ord_1111", State: "draft", Side: "buy", Security: "sec_apple", Qty: "3", Limit: "180.00", Currency: "USD"}},
		Plans:    []report.Plan{{Revision: "pl_aaaaaaaaaaaa.r2", Security: "sec_apple", Action: "BUY", State: "draft", Expiry: "2026-11-09"}},
		Income:   report.Income{Findings: []string{}, Payouts: []report.Payout{}}}, nil
}
func (b *wfBackend) Income(context.Context) (report.IncomeView, error) {
	b.reads.Add(1)
	return b.income, nil
}
func (b *wfBackend) OrderRows(context.Context) ([]report.OrderRow, error) {
	b.reads.Add(1)
	return b.orders, nil
}
func (b *wfBackend) ConfirmOrder(_ context.Context, id, typed string) error {
	b.reads.Add(1)
	if typed != id {
		return fmt.Errorf("to confirm, type the order ID %s exactly", id)
	}
	if !b.confirmOK {
		return errors.New("refused")
	}
	b.confirmed = append(b.confirmed, id)
	for i := range b.orders {
		if b.orders[i].ID == id {
			b.orders[i].State, b.orders[i].Confirmable = "confirmed", false
			b.orders[i].NotBecause = "only a draft or an expired confirmation can be confirmed"
		}
	}
	return nil
}

func newWFHarness(t *testing.T, w, hgt int) (*harness, *wfBackend) {
	t.Helper()
	be := newWF()
	h := &harness{t: t, be: be.fake}
	h.m = New(context.Background(), Options{Backend: be, Refresh: time.Millisecond, Redraw: time.Millisecond, OpenURL: func(string) error { return nil }})
	h.m.Update(tea.WindowSizeMsg{Width: w, Height: hgt})
	h.run(h.m.Init())
	return h, be
}

func (h *harness) go_(r nav.Route) {
	h.t.Helper()
	h.run(h.m.goTo(r))
}

// ---- tests ---------------------------------------------------------------------

func TestSectionKeysOpenPlanPortfolioAndIncome(t *testing.T) {
	h, _ := newWFHarness(t, 120, 40)
	for key, want := range map[string]string{"p": "Plans", "f": "Portfolio", "m": "Income as of 2026-11-08"} {
		h.press("n")
		h.press(key)
		if got := h.m.scr.Title; got != want {
			t.Errorf("%s opened %q, want %q", key, got, want)
		}
	}
	h.press("p")
	if h.m.litTab() != "Plan" {
		t.Errorf("tab lit: %q", h.m.litTab())
	}
	h.go_(nav.Route{Kind: nav.Orders})
	if h.m.litTab() != "Portfolio" {
		t.Errorf("orders belong under Portfolio, lit %q", h.m.litTab())
	}
}

func TestPlansGroupDueFirstAndNeverColourABuyOrASale(t *testing.T) {
	h, _ := newWFHarness(t, 120, 40)
	h.press("p")
	rows := h.rows()
	due := strings.Index(rows, "Due now")
	buy := strings.Index(rows, "Buy candidates")
	watch := strings.Index(rows, "Watch, wait and blocked")
	settled := strings.Index(rows, "Settled")
	if !(due >= 0 && due < buy && buy < watch && watch < settled) {
		t.Fatalf("groups out of order:\n%s", rows)
	}
	for _, r := range h.m.scr.Rows {
		if strings.Contains(r.Text, "BUY") || strings.Contains(r.Text, "SELL") {
			if r.Style == Good || r.Style == Bad {
				t.Errorf("an action must not be green or red: %q", r.Text)
			}
		}
	}
	for _, want := range []string{"pl_aaaaaaaaaaaa.r2", "sec_apple", "pl_bbbbbbbbbbbb.r1", "recorded as draft: past its expiry day", "tier 1", "epoch 3"} {
		if !strings.Contains(rows, want) {
			t.Errorf("plans lack %q", want)
		}
	}
}

func TestPlansFilterNarrowsAndSaysSo(t *testing.T) {
	h, _ := newWFHarness(t, 120, 40)
	h.go_(nav.Route{Kind: nav.Plans, Arg: "sleeve:event"})
	rows := h.rows()
	if strings.Contains(rows, "pl_aaaaaaaaaaaa") || !strings.Contains(rows, "pl_cccccccccccc") {
		t.Fatalf("filter not applied:\n%s", rows)
	}
	if !strings.Contains(rows, `filter "sleeve:event": 1 of 4 shown`) {
		t.Errorf("the filter is not announced:\n%s", rows)
	}
	// typing a filter on the screen keeps it as the route
	h.go_(nav.Route{Kind: nav.Plans})
	h.press("/")
	h.typeText("action:sell")
	h.press("enter")
	if got := h.m.stack.Current(); got.Kind != nav.Plans || got.Arg != "action:sell" {
		t.Fatalf("route %+v", got)
	}
	if !strings.Contains(h.rows(), "pl_bbbbbbbbbbbb") {
		t.Error("the sale is missing")
	}
}

func TestPlanDetailShowsTheWholeCardVersionsAndLinkedOrders(t *testing.T) {
	h, _ := newWFHarness(t, 100, 50)
	h.go_(nav.Route{Kind: nav.Plan, Arg: "pl_aaaaaaaaaaaa.r2"})
	rows := strings.Join(strings.Fields(h.rows()), " ")
	for _, want := range []string{"pl_aaaaaaaaaaaa.r2", "BUY", "sec_apple", "score 72 of 100", "notional 500.00 EUR", "fee 1.25 EUR", "THE REVISION DOES NOT MATCH ITS MANIFEST HASH", "Versions", "pl_aaaaaaaaaaaa.r1", "(this one)",
		"quality passes", "spread unavailable", "Evidence and the exact inputs", "manifest mh", "ledger lh", "holds no article text", "What-if"} {
		if !strings.Contains(rows, want) {
			t.Errorf("plan detail lacks %q", want)
		}
	}
	// the version row opens the earlier revision
	h.selectKey("ver:pl_aaaaaaaaaaaa.r1")
	h.press("enter")
	if got := h.m.stack.Current(); got.Kind != nav.Plan || got.Arg != "pl_aaaaaaaaaaaa.r1" {
		t.Fatalf("version did not open: %+v", got)
	}
}

func TestPaperAndRealAreSaidInWordsOnEveryScreenThatHoldsMoney(t *testing.T) {
	h, _ := newWFHarness(t, 120, 40)
	h.go_(nav.Route{Kind: nav.Book, Arg: "main/paper"})
	if !strings.Contains(h.rows(), "PAPER (local engine): not real money") {
		t.Errorf("paper book not marked:\n%s", h.rows())
	}
	h.go_(nav.Route{Kind: nav.Book, Arg: "main/core"})
	if !strings.Contains(h.rows(), "REAL MONEY") {
		t.Errorf("real book not marked:\n%s", h.rows())
	}
	h.go_(nav.Route{Kind: nav.Order, Arg: "ord_1111"})
	if !strings.Contains(h.rows(), "PAPER (local engine): not real money") {
		t.Errorf("paper order not marked")
	}
	h.go_(nav.Route{Kind: nav.Order, Arg: "ord_2222"})
	if !strings.Contains(h.rows(), "REAL MONEY") {
		t.Errorf("real order not marked")
	}
	h.go_(nav.Route{Kind: nav.Orders})
	rows := h.rows()
	for _, want := range []string{"OUTCOME UNKNOWN", "PAPER (local engine)", "REAL MONEY"} {
		if !strings.Contains(rows, want) {
			t.Errorf("orders lack %q", want)
		}
	}
}

func TestIncomeShowsEntitledFirstWithTheCheckFindings(t *testing.T) {
	h, _ := newWFHarness(t, 120, 40)
	h.press("m")
	rows := h.rows()
	if strings.Index(rows, "Status: entitled") > strings.Index(rows, "Status: pending") || !strings.Contains(rows, "Status: entitled") {
		t.Errorf("entitled must come first:\n%s", rows)
	}
	for _, want := range []string{"Income check", "accrual differs", "declared, not yet paid", "gross 0.75 USD", "before any tax withheld"} {
		if !strings.Contains(rows, want) {
			t.Errorf("income lacks %q", want)
		}
	}
}

func TestPolicyShowsBeforeAndAfter(t *testing.T) {
	h, _ := newWFHarness(t, 120, 40)
	h.go_(nav.Route{Kind: nav.Policy})
	rows := h.rows()
	for _, want := range []string{"epoch 3", "constraints.max_issuer: 0.10 -> 0.30", "1 plan is stale", "policy preview -file", "never activates a higher risk by itself"} {
		if !strings.Contains(rows, want) {
			t.Errorf("policy lacks %q:\n%s", want, rows)
		}
	}
}

func TestConfirmNeedsTheOrderIDTypedAndSendsNothing(t *testing.T) {
	h, be := newWFHarness(t, 120, 40)
	h.go_(nav.Route{Kind: nav.Order, Arg: "ord_1111"})
	h.selectKey("confirm:ord_1111")
	h.press("enter")
	if h.m.mode != modeConfirm || !strings.Contains(h.view(), "Type ord_1111 to confirm it (nothing is sent)") {
		t.Fatalf("no prompt: mode %v\n%s", h.m.mode, h.view())
	}
	h.press("esc")
	// enter on the lines under the confirm row asks the same
	h.selectKey("confirm:ord_1111#1")
	h.press("enter")
	if h.m.mode != modeConfirm || h.m.target != "ord_1111" {
		t.Fatalf("the line under the row did not ask: mode %v target %q", h.m.mode, h.m.target)
	}
	// a wrong ID is refused, and nothing is recorded
	h.typeText("ord_1")
	h.press("enter")
	if len(be.confirmed) != 0 || !h.m.failed || !strings.Contains(h.m.status, "type the order ID ord_1111 exactly") {
		t.Fatalf("a wrong ID was accepted: %v %q", be.confirmed, h.m.status)
	}
	// escape cancels
	h.press("enter")
	h.typeText("ord_1111")
	h.press("esc")
	if len(be.confirmed) != 0 || h.m.mode != modeList {
		t.Fatal("escape confirmed")
	}
	// the right ID confirms, and the screen reloads showing the new state
	h.selectKey("confirm:ord_1111")
	h.press("enter")
	h.typeText("ord_1111")
	h.press("enter")
	if len(be.confirmed) != 1 || be.confirmed[0] != "ord_1111" {
		t.Fatalf("not confirmed: %v", be.confirmed)
	}
	if !strings.Contains(h.m.status, "Nothing was sent") {
		t.Errorf("status: %q", h.m.status)
	}
	rows := h.rows()
	if !strings.Contains(rows, "state confirmed") || strings.Contains(rows, "▶ Confirm this order") {
		t.Errorf("the screen did not reload:\n%s", rows)
	}
}

func TestAnOrderThatCannotBeConfirmedHereSaysWhy(t *testing.T) {
	h, be := newWFHarness(t, 120, 40)
	for _, id := range []string{"ord_2222", "ord_3333"} {
		h.go_(nav.Route{Kind: nav.Order, Arg: id})
		for _, r := range h.m.scr.Rows {
			if strings.HasPrefix(r.Key, "confirm:") {
				t.Errorf("%s offers a confirm row", id)
			}
		}
		if !strings.Contains(h.rows(), "Not confirmable here") {
			t.Errorf("%s does not say why:\n%s", id, h.rows())
		}
	}
	h.go_(nav.Route{Kind: nav.Order, Arg: "ord_2222"})
	if !strings.Contains(h.rows(), "bound to a broker account") {
		t.Error("the broker reason is missing")
	}
	if len(be.confirmed) != 0 {
		t.Fatal("something was confirmed")
	}
}

func TestDrawingReadsNothing(t *testing.T) {
	h, be := newWFHarness(t, 120, 40)
	for _, r := range []nav.Route{{Kind: nav.Plans}, {Kind: nav.Plan, Arg: "pl_aaaaaaaaaaaa.r2"}, {Kind: nav.Portfolio}, {Kind: nav.Book, Arg: "main/core"}, {Kind: nav.Income}, {Kind: nav.Orders}, {Kind: nav.Order, Arg: "ord_1111"}, {Kind: nav.Policy}} {
		h.go_(r)
		before := be.reads.Load()
		for range 5 {
			_ = h.view()
			h.m.Update(clockMsg(time.Now()))
		}
		if got := be.reads.Load(); got != before {
			t.Errorf("%s: drawing read the backend %d time(s)", r.String(), got-before)
		}
	}
}

func TestABackendWithoutAWorkflowSaysSo(t *testing.T) {
	h := newHarness(t)
	h.press("p")
	if !strings.Contains(h.view(), "does not read plans, books, income or orders") {
		t.Errorf("no explanation:\n%s", h.view())
	}
}

// Every line of every workflow screen fits the width it was built for, at narrow
// and wide terminals, with wide characters in the names, and the essential values
// are all still there: nothing is cut without being said.
func TestNarrowAndWideScreensKeepEveryEssentialValue(t *testing.T) {
	routes := []struct {
		r    nav.Route
		want []string
	}{
		{nav.Route{Kind: nav.Plans}, []string{"pl_aaaaaaaaaaaa.r2", "sec_apple", "pl_bbbbbbbbbbbb.r1", "sec_long", "2026-11-09", "2026-11-04"}},
		{nav.Route{Kind: nav.Plan, Arg: "pl_aaaaaaaaaaaa.r2"}, []string{"pl_aaaaaaaaaaaa.r2", "sec_apple", "score 72 of 100", "manifest mh", "ledger lh", "spread unavailable"}},
		{nav.Route{Kind: nav.Portfolio}, []string{"main/paper", "main/core", "REAL MONEY"}},
		{nav.Route{Kind: nav.Book, Arg: "main/core"}, []string{"1000.00 EUR", "990.00 EUR", "sec_apple", "540.00 USD", "ord_1111", "ledger fp"}},
		{nav.Route{Kind: nav.Income}, []string{"sec_apple|cash|2026Q4", "2026-11-10", "2026-11-14", "0.75 USD", "sec_sap|cash|2026"}},
		{nav.Route{Kind: nav.Orders}, []string{"ord_1111", "ord_2222", "ord_3333", "OUTCOME UNKNOWN"}},
		{nav.Route{Kind: nav.Order, Arg: "ord_2222"}, []string{"ord_2222", "sell", "sec_long", "50.00 EUR", "def456", "REAL MONEY", "bound to a broker account"}},
		{nav.Route{Kind: nav.Policy}, []string{"constraints.max_issuer", "0.10 -> 0.30", "polhash"}},
	}
	for _, width := range []int{40, 60, 100, 160} {
		h, _ := newWFHarness(t, width, 60)
		for _, c := range routes {
			h.go_(c.r)
			var text []string
			for _, row := range h.m.scr.Rows {
				if w := ansi.StringWidth(row.Text); w > width-2 {
					t.Errorf("width %d, %s: a row is %d cells wide: %q", width, c.r, w, row.Text)
				}
				text = append(text, row.Text)
			}
			// a value split across lines is still all there: compare without spaces
			flat := strings.Join(strings.Fields(strings.Join(text, " ")), "")
			for _, want := range c.want {
				if !strings.Contains(flat, strings.Join(strings.Fields(want), "")) {
					t.Errorf("width %d, %s: %q is missing", width, c.r, want)
				}
			}
		}
	}
}

func TestLongUnicodeNamesAreWrappedNotDropped(t *testing.T) {
	for _, width := range []int{30, 45, 80} {
		lines := wrapText(longName, width)
		if got := strings.Join(strings.Fields(strings.Join(lines, " ")), ""); got != strings.Join(strings.Fields(longName), "") {
			t.Errorf("width %d: the name changed: %q", width, lines)
		}
		for _, l := range lines {
			if ansi.StringWidth(l) > max(width, minWrap) {
				t.Errorf("width %d: %q is %d cells", width, l, ansi.StringWidth(l))
			}
		}
	}
	// a word wider than the line is broken, not cut
	long := strings.Repeat("x", 90)
	if got := strings.Join(wrapText(long, 30), ""); got != long {
		t.Errorf("a long word lost characters: %q", got)
	}
}

func TestTerminalControlsInWorkflowTextAreRemoved(t *testing.T) {
	h, be := newWFHarness(t, 120, 40)
	be.plans[0].Name = "Evil\x1b[31m red \x1b]0;title\x07 Corp"
	be.plans[0].Security = "sec_\x1b[2Jx"
	h.go_(nav.Route{Kind: nav.Plans})
	for _, r := range h.m.scr.Rows {
		if strings.ContainsRune(r.Text, 0x1b) || strings.ContainsRune(r.Text, 0x07) {
			t.Errorf("a control character reached the screen: %q", r.Text)
		}
	}
}

func TestResizingKeepsTheCursorOnTheSamePlan(t *testing.T) {
	h, _ := newWFHarness(t, 120, 40)
	h.press("p")
	h.selectKey("plan:pl_bbbbbbbbbbbb.r1")
	h.m.Update(tea.WindowSizeMsg{Width: 40, Height: 20})
	h.run(h.m.loadCmd(true))
	row, ok := h.m.row()
	if !ok || row.Key != "plan:pl_bbbbbbbbbbbb.r1" {
		t.Fatalf("the cursor moved: %+v", row)
	}
	for _, line := range strings.Split(h.view(), "\n") {
		if w := ansi.StringWidth(line); w > 40 {
			t.Errorf("a drawn line is %d cells at width 40: %q", w, line)
		}
	}
}

// selectKey puts the cursor on the row with the key.
func (h *harness) selectKey(key string) {
	h.t.Helper()
	i := h.m.find("k:"+key, 0)
	if i < 0 {
		h.t.Fatalf("no row %q on %s:\n%s", key, h.m.stack.Current(), h.rows())
	}
	h.m.vp.Select(i)
}

// A card longer than the window can be read to its last line with the keys: a
// viewport scrolls to follow its cursor, so every line must be able to hold it.
func TestALongCardCanBeReadToItsEnd(t *testing.T) {
	h, _ := newWFHarness(t, 60, 16)
	h.go_(nav.Route{Kind: nav.Plan, Arg: "pl_aaaaaaaaaaaa.r2"})
	if strings.Contains(h.view(), "What-if") {
		t.Fatal("the end is already in view: make the window smaller")
	}
	h.press("end")
	if !strings.Contains(h.view(), "What-if") {
		t.Errorf("End did not reach the last lines:\n%s", h.view())
	}
	h.press("home")
	if !strings.Contains(h.view(), "pl_aaaaaaaaaaaa.r2") {
		t.Error("Home did not return to the top")
	}
	// page keys move through it too
	h.press("pgdown")
	if strings.Contains(h.view(), "Apple") && h.m.vp.Top() == 0 {
		t.Error("PgDn did not scroll")
	}
}
