package nav

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestParseRoundTrip(t *testing.T) {
	for _, s := range []string{
		"/", "/stocks", "/stock/NVDA", "/topics", "/topic/ai-infra",
		"/domains", "/domain/sec.gov", "/exchanges", "/exchange/NYSE American", "/day/2026-11-03", "/sources",
		"/search?q=%22free+cash+flow%22+-rumor",
	} {
		r, err := Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q): %v", s, err)
		}
		if got := r.String(); got != s {
			t.Errorf("round trip: %q -> %q", s, got)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, s := range []string{"/nope", "/stock", "/stocks/AAPL", "/day/yesterday"} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q): expected an error", s)
		}
	}
}

func TestWatchRoutes(t *testing.T) {
	for _, s := range []string{
		"/w/buffett-core", "/w/buffett-core/stocks", "/w/buffett-core/stock/KO",
		"/w/macro/search?q=rates", "/watches",
	} {
		r, err := Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q): %v", s, err)
		}
		if got := r.String(); got != s {
			t.Errorf("round trip: %q -> %q", s, got)
		}
	}
	r, _ := Parse("/w/buffett-core/stock/KO")
	if r.Watch != "buffett-core" || r.Kind != Stock || r.Arg != "KO" {
		t.Errorf("parsed %+v", r)
	}
	for _, s := range []string{"/w/Bad Id/stocks", "/w//stocks", "/w/a/sources", "/w/a/watches", "/w"} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q): expected an error", s)
		}
	}
}

func TestSwitchWatchKeepsTheScreen(t *testing.T) {
	s := New()
	s.SwitchWatch("a")
	s.Go(Route{Kind: Topics})
	s.Go(Route{Kind: Topic, Arg: "ai"})
	if got := s.Current(); got != (Route{Kind: Topic, Arg: "ai", Watch: "a"}) {
		t.Fatalf("current = %+v, want the topic stamped with watch a", got)
	}

	s.SwitchWatch("b")
	if got, want := labels(s), "Home > Topics > ai"; got != want {
		t.Fatalf("crumbs = %q, want %q", got, want)
	}
	for _, c := range s.Crumbs() {
		if c.Route.Watch != "b" {
			t.Errorf("crumb %q is still on watch %q", c.Label, c.Route.Watch)
		}
	}
	if s.Watch() != "b" {
		t.Errorf("Watch() = %q", s.Watch())
	}

	// Going back and forward stays in the new watch.
	s.Back()
	s.SwitchWatch("c")
	s.Forward()
	if got := s.Current().Watch; got != "c" {
		t.Errorf("forward screen is on watch %q, want c", got)
	}
}

func TestSwitchWatchLeavesGlobalScreens(t *testing.T) {
	s := New()
	s.SwitchWatch("a")
	s.Go(Route{Kind: Sources})
	s.SwitchWatch("b")
	if got := s.Current(); got != (Route{Kind: Sources}) {
		t.Fatalf("current = %+v; Sources belongs to no watch", got)
	}
	if home := s.Crumbs()[0].Route; home.Watch != "b" {
		t.Errorf("Home crumb is on watch %q, want b", home.Watch)
	}
}

func TestDeepLinkSwitchesWatch(t *testing.T) {
	s := New()
	s.SwitchWatch("a")
	s.Go(Route{Kind: Stock, Arg: "KO", Watch: "b"})
	if s.Watch() != "b" || s.Current().Watch != "b" {
		t.Fatalf("watch = %q, current = %+v", s.Watch(), s.Current())
	}
	if got, want := labels(s), "Home > Stocks > KO"; got != want {
		t.Errorf("crumbs = %q, want %q", got, want)
	}
}

func TestNextAndNth(t *testing.T) {
	ids := []string{"a", "b", "c"}
	cases := []struct {
		cur  string
		dir  int
		want string
	}{
		{"a", 1, "b"}, {"c", 1, "a"}, {"a", -1, "c"}, {"b", -1, "a"},
		{"gone", 1, "a"}, {"gone", -1, "c"},
	}
	for _, c := range cases {
		if got := Next(ids, c.cur, c.dir); got != c.want {
			t.Errorf("Next(%q, %d) = %q, want %q", c.cur, c.dir, got, c.want)
		}
	}
	if Next(nil, "a", 1) != "" {
		t.Error("Next on no watches should be empty")
	}
	if id, ok := Nth(ids, 2); !ok || id != "b" {
		t.Errorf("Nth(2) = %q, %v", id, ok)
	}
	for _, n := range []int{0, 4, -1} {
		if _, ok := Nth(ids, n); ok {
			t.Errorf("Nth(%d) should fail", n)
		}
	}
}

func labels(s *Stack) string {
	var out []string
	for _, c := range s.Crumbs() {
		out = append(out, c.Label)
	}
	return strings.Join(out, " > ")
}

func TestBreadcrumbs(t *testing.T) {
	s := New()
	s.Go(Route{Kind: Topics})
	s.Go(Route{Kind: Topic, Arg: "AI infrastructure"})
	s.Go(Route{Kind: Stock, Arg: "NVDA"})
	if got, want := labels(s), "Home > Topics > AI infrastructure > NVDA"; got != want {
		t.Fatalf("crumbs = %q, want %q", got, want)
	}
	// A detail opened straight from Home gets its list inserted.
	s = New()
	s.Go(Route{Kind: Stock, Arg: "KO"})
	if got, want := labels(s), "Home > Stocks > KO"; got != want {
		t.Fatalf("crumbs = %q, want %q", got, want)
	}
	// A section resets the trail.
	s.Go(Route{Kind: Domains})
	if got, want := labels(s), "Home > Domains"; got != want {
		t.Fatalf("crumbs = %q, want %q", got, want)
	}
}

func TestBackForwardJump(t *testing.T) {
	s := New()
	s.Go(Route{Kind: Topics})
	s.Go(Route{Kind: Topic, Arg: "ai"})
	if !s.Back() || s.Current().Kind != Topics {
		t.Fatalf("Back: current = %v", s.Current())
	}
	if !s.Forward() || s.Current() != (Route{Kind: Topic, Arg: "ai"}) {
		t.Fatalf("Forward: current = %v", s.Current())
	}
	if !s.JumpTo(0) || !reflect.DeepEqual(s.Current(), Route{Kind: Home}) {
		t.Fatalf("JumpTo(0): current = %v", s.Current())
	}
	if s.Back() {
		t.Fatal("Back at Home should report false")
	}
	if s.JumpTo(5) {
		t.Fatal("JumpTo out of range should report false")
	}
}

func TestCompaniesRouteRoundTrips(t *testing.T) {
	for _, r := range []Route{
		{Kind: Companies},
		{Kind: Companies, Arg: "exchange:nyse tag:moat"},
		{Kind: Companies, Arg: `name:"berkshire hathaway" -etf`, Watch: "buffett"},
	} {
		got, err := Parse(r.String())
		if err != nil || got != r {
			t.Errorf("Parse(%q) = %+v, %v; want %+v", r.String(), got, err, r)
		}
	}
	if _, err := Parse("/companies/extra"); err == nil {
		t.Error("companies takes no path argument")
	}
	if got := (Route{Kind: Companies, Arg: "tag:moat"}).Label(); got != "Companies: tag:moat" {
		t.Errorf("label = %q", got)
	}
	if !(Route{Kind: Companies}).Section() {
		t.Error("companies is a section")
	}
}

func TestStockOpenedFromCompaniesOrSearchSitsUnderThem(t *testing.T) {
	for _, list := range []Route{{Kind: Companies, Arg: "tag:moat"}, {Kind: Search, Arg: "nvidia"}} {
		s := New()
		s.Go(list)
		s.Go(Route{Kind: Stock, Arg: "NVDA"})
		var labels []string
		for _, c := range s.Crumbs() {
			labels = append(labels, c.Label)
		}
		want := []string{"Home", list.Label(), "NVDA"}
		if !slices.Equal(labels, want) {
			t.Errorf("from %v: crumbs = %v, want %v", list.Kind, labels, want)
		}
	}
}

func TestExchangeSitsUnderExchanges(t *testing.T) {
	s := New()
	s.Go(Route{Kind: Exchange, Arg: "NYSE"})
	if got, want := labels(s), "Home > Exchanges > NYSE"; got != want {
		t.Errorf("crumbs = %q, want %q", got, want)
	}
	if !(Route{Kind: Exchanges}).Section() || (Route{Kind: Exchange}).Section() {
		t.Error("the list of exchanges is a section, one exchange is not")
	}
	if _, err := Parse("/exchanges/NYSE"); err == nil {
		t.Error("the list takes no argument")
	}
}

func TestWorkflowRoutesRoundTripAndBelongToNoWatch(t *testing.T) {
	for _, s := range []string{"/plans", "/plans?q=sleeve%3Aevent", "/plan/pl_abc.r2", "/portfolio", "/book/main/core", "/income", "/income?q=status%3Aentitled", "/orders", "/order/ord_1", "/policy"} {
		r, err := Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q): %v", s, err)
		}
		if got := r.String(); got != s {
			t.Errorf("round trip: %q -> %q", s, got)
		}
		if r.Scoped() || !r.Workflow() {
			t.Errorf("%q must belong to no watch and be a workflow screen", s)
		}
	}
	for _, s := range []string{"/plan", "/book", "/order", "/policy/x", "/w/a/plans", "/plans/x"} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q): expected an error", s)
		}
	}
	if p, ok := (Route{Kind: Order, Arg: "o"}).Parent(); !ok || p.Kind != Orders {
		t.Errorf("parent of an order: %+v", p)
	}
}
