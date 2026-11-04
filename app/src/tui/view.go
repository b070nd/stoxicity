package tui

import "github.com/b070nd/stoxicity/app/src/nav"

// View is one screen. The shell owns the breadcrumb bar, the filter bar, the
// status line (last refresh, sources failing) and the key map; a View draws
// only its body.
type View interface {
	// Route is the location the view shows.
	Route() nav.Route
	// Render draws the body in a width × height cell area.
	Render(width, height int) string
	// Open returns the route to follow when the user presses enter on the
	// selected row, or false if the row opens nothing.
	Open() (nav.Route, bool)
	// Label records the user's verdict on the selected article (keep/drop).
	// Views without articles ignore it.
	Label(keep bool)
}

// Factory builds the view for a route.
type Factory func(r nav.Route) (View, error)

// The shell draws, above every view, a watch bar: one tab per watch of the
// tenant with its unread badge, the current one highlighted. tab, shift+tab
// and the digits switch watch and keep the current screen (see
// nav.Stack.SwitchWatch), so comparing a topic across watches is one keypress.
// Opening a watch's screen marks its hits as seen (store.TenantStore.MarkSeen).
//
// Views lists what each route shows. Every detail view has the same layout
// rules: a summary on top, the timeline (bars per day, then events), the tag
// cloud, and tables whose rows open other routes.
//
//	nav.Home     today's digest: top relevant articles, domain table, rising
//	             topics, new stock candidates, tag cloud, 14-day timeline
//	nav.Stocks   stocks seen in relevant news, sortable by mentions, materiality, tone
//	nav.Stock    summary, price and fundamentals, tone and materiality trend,
//	             timeline, related topics, top domains, articles
//	nav.Topics   all topics as a tag cloud and a trend table
//	nav.Topic    its stocks, domains, timeline, tag cloud, top articles
//	nav.Domains  relevant against total per domain today and over time
//	nav.Domain   one domain's output, precision against your labels, timeline
//	nav.Day      one day's digest
//	nav.Search   results of a Google-style query, with facet counts
//	nav.Sources  source health: last success, failures, next poll, circuit state
//	nav.Watches  the tenant's watches: unread, last hit, channels; create, edit, mute
//
// Views is filled in as each view is written.
var Views = map[nav.Kind]Factory{}
