// Package nav is the navigation model of the TUI: every screen has a route,
// written like a URL, and a Stack remembers the trail the user followed so the
// breadcrumb bar and back/forward work the same on every screen.
//
// Most screens show the data of one watch. Their route may carry the watch as
// a prefix; without it the current watch is meant.
//
//	/                      home: the watch's dashboard
//	/stocks                all stocks seen in relevant news
//	/companies             every known company, with a filter: /companies?q=exchange:nyse
//	/stock/NVDA            one stock
//	/exchanges             the markets stocks are listed on
//	/exchange/NYSE         one exchange: facts, hours, statistics, its stocks
//	/topics                all topics
//	/topic/ai-infra        one topic
//	/domains               relevant news per domain
//	/domain/sec.gov        one domain
//	/day/2026-11-03        one day
//	/search?q=...          Google-style query results
//	/w/buffett-core/stock/KO   one stock, in the buffett-core watch
//
// Three screens belong to no watch, because they describe the shared base or the
// tenant: /sources (source health), /watches (list, create, edit watches) and
// /label (the articles to judge, from all the tenant's news).
package nav

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
)

// Kind is the screen a route opens.
type Kind string

const (
	Home   Kind = "home"
	Stocks Kind = "stocks"
	// Companies is the reference list of every known company. Arg is the
	// filter, written as in universe.ParseQuery.
	Companies Kind = "companies"
	Stock     Kind = "stock"
	// Exchanges lists the markets stocks are listed on; Exchange is one of them
	// (Arg is its name as the universe writes it).
	Exchanges Kind = "exchanges"
	Exchange  Kind = "exchange"
	Topics    Kind = "topics"
	Topic     Kind = "topic"
	Domains   Kind = "domains"
	Domain    Kind = "domain"
	Day       Kind = "day"
	Search    Kind = "search"
	Sources   Kind = "sources"
	Watches   Kind = "watches"
	// Label is the queue of articles to judge; it belongs to no watch.
	Label Kind = "label"
	// The daily workflow. None of these belongs to a watch: they describe the
	// tenant's plans, books, income, orders and policy. Arg of a list is a
	// filter, as for Companies; Arg of a detail is the plan (pl_x or pl_x.rN),
	// the book (account/name) or the order.
	Plans     Kind = "plans"
	Plan      Kind = "plan"
	Portfolio Kind = "portfolio"
	Book      Kind = "book"
	Income    Kind = "income"
	Orders    Kind = "orders"
	Order     Kind = "order"
	Policy    Kind = "policy"
)

// Route is a parsed location. Arg is the ticker, topic ID, domain or day for
// the detail kinds, and the query for Search. Watch is the watch whose data the
// screen shows; empty means the current one.
type Route struct {
	Kind  Kind
	Arg   string
	Watch string
}

// Scoped reports whether the screen shows one watch's data. Sources and
// Watches do not.
func (r Route) Scoped() bool {
	switch r.Kind {
	case Sources, Watches, Label, Plans, Plan, Portfolio, Book, Income, Orders, Order, Policy:
		return false
	}
	return true
}

// Workflow reports whether the screen is one of the daily workflow: plans,
// books, income, orders and policy.
func (r Route) Workflow() bool {
	switch r.Kind {
	case Plans, Plan, Portfolio, Book, Income, Orders, Order, Policy:
		return true
	}
	return false
}

// IsWorkflowList reports whether the kind is a list of the daily workflow, whose
// route argument is a filter.
func IsWorkflowList(k Kind) bool {
	switch k {
	case Plans, Portfolio, Income, Orders:
		return true
	}
	return false
}

// Section reports whether the route is a top-level list that starts a new trail.
func (r Route) Section() bool {
	switch r.Kind {
	case Home, Stocks, Companies, Exchanges, Topics, Domains, Sources, Watches, Label, Search, Plans, Portfolio, Income, Orders, Policy:
		return true
	}
	return false
}

// Parent returns the list a detail route belongs to, if it has one.
func (r Route) Parent() (Route, bool) {
	switch r.Kind {
	case Stock:
		return Route{Kind: Stocks, Watch: r.Watch}, true
	case Topic:
		return Route{Kind: Topics, Watch: r.Watch}, true
	case Domain:
		return Route{Kind: Domains, Watch: r.Watch}, true
	case Exchange:
		return Route{Kind: Exchanges, Watch: r.Watch}, true
	case Plan:
		return Route{Kind: Plans}, true
	case Book:
		return Route{Kind: Portfolio}, true
	case Order:
		return Route{Kind: Orders}, true
	}
	return Route{}, false
}

// Label is the short text shown in the breadcrumb bar.
func (r Route) Label() string {
	switch r.Kind {
	case Home:
		return "Home"
	case Stocks:
		return "Stocks"
	case Topics:
		return "Topics"
	case Domains:
		return "Domains"
	case Exchanges:
		return "Exchanges"
	case Sources:
		return "Sources"
	case Watches:
		return "Watches"
	case Label:
		return "Label"
	case Plans:
		return withFilter("Plan", r.Arg)
	case Portfolio:
		return withFilter("Portfolio", r.Arg)
	case Income:
		return withFilter("Income", r.Arg)
	case Orders:
		return withFilter("Orders", r.Arg)
	case Policy:
		return "Policy"
	case Search:
		return "Search: " + r.Arg
	case Companies:
		if r.Arg != "" {
			return "Companies: " + r.Arg
		}
		return "Companies"
	default:
		return r.Arg
	}
}

func withFilter(name, q string) string {
	if q == "" {
		return name
	}
	return name + ": " + q
}

// String writes the route as a path, with the /w/<watch> prefix when it has one.
func (r Route) String() string {
	prefix := ""
	if r.Watch != "" {
		prefix = "/w/" + r.Watch
	}
	switch r.Kind {
	case Home:
		if prefix == "" {
			return "/"
		}
		return prefix
	case Stocks, Topics, Domains, Exchanges, Sources, Watches, Label:
		return prefix + "/" + string(r.Kind)
	case Search:
		return prefix + "/search?q=" + url.QueryEscape(r.Arg)
	case Companies:
		if r.Arg == "" {
			return prefix + "/companies"
		}
		return prefix + "/companies?q=" + url.QueryEscape(r.Arg)
	case Plans, Portfolio, Income, Orders:
		if r.Arg == "" {
			return "/" + string(r.Kind)
		}
		return "/" + string(r.Kind) + "?q=" + url.QueryEscape(r.Arg)
	case Policy:
		return "/policy"
	default:
		return prefix + "/" + string(r.Kind) + "/" + r.Arg
	}
}

// Parse reads a route written by String.
func Parse(s string) (Route, error) {
	path, query, _ := strings.Cut(s, "?")
	path = strings.Trim(path, "/")
	watch := ""
	if path == "w" {
		return Route{}, fmt.Errorf("nav: %q: missing watch ID", s)
	}
	if rest, ok := strings.CutPrefix(path, "w/"); ok {
		watch, path, _ = strings.Cut(rest, "/")
		if !model.ValidID(watch) {
			return Route{}, fmt.Errorf("nav: %q: bad watch ID", s)
		}
	}
	r, err := parsePath(s, path, query)
	if err != nil {
		return Route{}, err
	}
	if watch != "" && !r.Scoped() {
		return Route{}, fmt.Errorf("nav: %q: this screen does not belong to a watch", s)
	}
	r.Watch = watch
	return r, nil
}

func parsePath(s, path, query string) (Route, error) {
	if path == "" {
		return Route{Kind: Home}, nil
	}
	head, arg, hasArg := strings.Cut(path, "/")
	switch Kind(head) {
	case Stocks, Topics, Domains, Exchanges, Sources, Watches, Label:
		if hasArg {
			return Route{}, fmt.Errorf("nav: %q takes no argument", s)
		}
		return Route{Kind: Kind(head)}, nil
	case Policy:
		if hasArg {
			return Route{}, fmt.Errorf("nav: %q takes no argument", s)
		}
		return Route{Kind: Policy}, nil
	case Plan, Book, Order:
		if !hasArg || arg == "" {
			return Route{}, fmt.Errorf("nav: %q needs an argument", s)
		}
		return Route{Kind: Kind(head), Arg: arg}, nil
	case Stock, Topic, Domain, Exchange:
		if !hasArg || arg == "" {
			return Route{}, fmt.Errorf("nav: %q needs an argument", s)
		}
		return Route{Kind: Kind(head), Arg: arg}, nil
	case Day:
		if _, err := time.Parse("2006-01-02", arg); err != nil {
			return Route{}, fmt.Errorf("nav: %q: day must be YYYY-MM-DD", s)
		}
		return Route{Kind: Day, Arg: arg}, nil
	case Search, Companies, Plans, Portfolio, Income, Orders:
		if hasArg {
			return Route{}, fmt.Errorf("nav: %q takes no path argument", s)
		}
		q, err := url.ParseQuery(query)
		if err != nil {
			return Route{}, fmt.Errorf("nav: %q: %w", s, err)
		}
		return Route{Kind: Kind(head), Arg: q.Get("q")}, nil
	}
	return Route{}, fmt.Errorf("nav: unknown route %q", s)
}
