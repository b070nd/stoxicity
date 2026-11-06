package tui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/b070nd/stoxicity/app/src/nav"
)

// Action is something a key does. The shell maps actions to behaviour; keeping
// the key map as data lets a test catch two actions bound to one key and lets
// the help screen list the same bindings the shell uses.
type Action string

const (
	NextWatch    Action = "next-watch" // the whole point: one key to the next watch
	PrevWatch    Action = "prev-watch"
	JumpWatch    Action = "jump-watch"    // 1-9: the n-th watch
	SwitchWatch  Action = "switch-watch"  // fuzzy switcher over watches, with unread badges
	SwitchTenant Action = "switch-tenant" // only offered when there is more than one tenant
	Open         Action = "open"
	Back         Action = "back"
	Forward      Action = "forward"
	GoHome       Action = "home"
	GoWatches    Action = "watches"
	GoStocks     Action = "stocks"
	GoCompanies  Action = "companies" // every known company, filterable
	TagStock     Action = "tag"       // tag the stock under the cursor
	ListStock    Action = "list"      // put the stock under the cursor in a list
	GoTopics     Action = "topics"
	GoDomains    Action = "domains"
	GoSources    Action = "sources"
	OpenURL      Action = "open-url" // the selected article in the browser
	Up           Action = "up"
	Down         Action = "down"
	PageUp       Action = "page-up"
	PageDown     Action = "page-down"
	Top          Action = "top"
	Bottom       Action = "bottom"
	FocusFilter  Action = "filter"      // the Google-style filter bar
	Jump         Action = "jump"        // move to the first company that starts with what you type
	SortNext     Action = "sort-next"   // sort by the next column
	SortPrev     Action = "sort-prev"   // sort by the previous column
	SortInvert   Action = "sort-invert" // turn the order round
	Mark         Action = "mark"        // mark the stock under the cursor and move down
	Unmark       Action = "unmark"      // clear every mark
	Keep         Action = "keep"        // label the selected article relevant
	Drop         Action = "drop"        // label it noise
	Refresh      Action = "refresh"
	NewWatch     Action = "new-watch"
	EditWatch    Action = "edit-watch"
	MuteWatch    Action = "mute-watch"
	Help         Action = "help"
	Quit         Action = "quit"
)

// Need is what a binding wants of the place it is used.
type Need int

const (
	// Anywhere: no condition.
	Anywhere Need = iota
	// OnRow: the cursor is on a row that opens something.
	OnRow
	// OnArticle: the cursor is on an article, or one is open.
	OnArticle
	// OnStock: the cursor is on a stock, or a stock's page is open.
	OnStock
	// OnLink: there is a web address under the cursor.
	OnLink
	// OnColumns: the screen is a table with columns to sort by.
	OnColumns
)

// Binding is one key assignment.
type Binding struct {
	Keys   []string
	Action Action
	Help   string
	// Bar is the short label the key bar shows for it; empty keeps it off the
	// bar (the help screen lists it all the same).
	Bar string
	// Needs and Screens say where the key works: with the cursor on the right
	// kind of row, on these screens only (none listed means any). Detail says it
	// also works while an article is open.
	Needs   Need
	Screens []nav.Kind
	Detail  bool
}

// Context is where a key is pressed: the screen, whether an article is open,
// the row under the cursor and whether the screen has columns.
type Context struct {
	Kind   nav.Kind
	Detail bool
	Row    Row
	HasRow bool
	Table  bool
	// Article is the article under the cursor or open.
	Article bool
}

// Applies reports whether the binding does anything in the context.
func (b Binding) Applies(c Context) bool {
	if c.Detail && !b.Detail {
		return false
	}
	if len(b.Screens) > 0 && !slices.Contains(b.Screens, c.Kind) {
		return false
	}
	switch b.Needs {
	case OnRow:
		return c.HasRow && (c.Row.Article != nil || c.Row.Go != nil || c.Row.HasWatch)
	case OnArticle:
		return c.Article
	case OnStock:
		return c.Kind == nav.Stock || (c.HasRow && markable(c.Row) != "")
	case OnLink:
		return c.Article || (c.HasRow && c.Row.URL != "")
	case OnColumns:
		return c.Table
	}
	return true
}

// Refusal says why a binding does nothing in the context, for the status line.
func (b Binding) Refusal(c Context) string {
	if len(b.Screens) > 0 && !slices.Contains(b.Screens, c.Kind) {
		names := make([]string, len(b.Screens))
		for i, k := range b.Screens {
			names[i] = string(k)
		}
		return fmt.Sprintf("%s works on the %s screen only", b.Keys[0], strings.Join(names, " or "))
	}
	switch b.Needs {
	case OnRow:
		return "nothing to open on this row"
	case OnArticle:
		return "put the cursor on an article first"
	case OnStock:
		return "put the cursor on a stock first"
	case OnLink:
		return "nothing here has a web address"
	case OnColumns:
		return "this screen has no columns to sort"
	}
	return b.Keys[0] + " does nothing here"
}

// DefaultKeys is the key map, in the order the key bar shows it. Watch
// switching sits on the easiest keys (tab and the digits) because it is the
// most frequent move.
var DefaultKeys = []Binding{
	{Keys: []string{"tab"}, Action: NextWatch, Help: "next watch", Bar: "watch"},
	{Keys: []string{"shift+tab"}, Action: PrevWatch, Help: "previous watch"},
	{Keys: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}, Action: JumpWatch, Help: "jump to watch n"},
	{Keys: []string{"w", "ctrl+k"}, Action: SwitchWatch, Help: "find a watch"},
	{Keys: []string{"T"}, Action: SwitchTenant, Help: "switch tenant"},
	{Keys: []string{"enter"}, Action: Open, Help: "open", Bar: "open", Needs: OnRow, Detail: true},
	{Keys: []string{"esc", "backspace"}, Action: Back, Help: "back", Bar: "back", Detail: true},
	{Keys: []string{"]"}, Action: Forward, Help: "forward"},
	{Keys: []string{"H"}, Action: GoHome, Help: "dashboard"},
	{Keys: []string{"W"}, Action: GoWatches, Help: "manage watches"},
	{Keys: []string{"s"}, Action: GoStocks, Help: "all stocks, news first"},
	{Keys: []string{"c"}, Action: GoCompanies, Help: "all companies (/ narrows)"},
	{Keys: []string{"/"}, Action: FocusFilter, Help: "filter", Bar: "filter"},
	{Keys: []string{"g"}, Action: Jump, Help: "catalogue: jump to a ticker or name", Bar: "jump", Screens: []nav.Kind{nav.Companies}},
	{Keys: []string{">"}, Action: SortNext, Help: "sort by the next column", Bar: "sort", Needs: OnColumns},
	{Keys: []string{"<"}, Action: SortPrev, Help: "sort by the previous column", Needs: OnColumns},
	{Keys: []string{"I"}, Action: SortInvert, Help: "turn the sort order round", Bar: "invert", Needs: OnColumns},
	{Keys: []string{"space"}, Action: Mark, Help: "mark the stock, move down", Bar: "mark", Needs: OnStock},
	{Keys: []string{"U"}, Action: Unmark, Help: "clear all marks"},
	{Keys: []string{"#"}, Action: TagStock, Help: "tag stock or marked (-tag removes)", Bar: "tag", Needs: OnStock},
	{Keys: []string{"L"}, Action: ListStock, Help: "list stock or marked (-list removes)", Bar: "list", Needs: OnStock},
	{Keys: []string{"k"}, Action: Keep, Help: "keep (relevant)", Bar: "keep", Needs: OnArticle, Detail: true},
	{Keys: []string{"d"}, Action: Drop, Help: "drop (noise)", Bar: "drop", Needs: OnArticle, Detail: true},
	{Keys: []string{"o"}, Action: OpenURL, Help: "open article, site or filing", Bar: "browser", Needs: OnLink, Detail: true},
	{Keys: []string{"t"}, Action: GoTopics, Help: "topics"},
	{Keys: []string{"D"}, Action: GoDomains, Help: "domains"},
	{Keys: []string{"S"}, Action: GoSources, Help: "source health"},
	{Keys: []string{"up", "ctrl+p"}, Action: Up, Help: "move up"},
	{Keys: []string{"down", "ctrl+n"}, Action: Down, Help: "move down"},
	{Keys: []string{"pgup"}, Action: PageUp, Help: "page up"},
	{Keys: []string{"pgdown"}, Action: PageDown, Help: "page down"},
	{Keys: []string{"home"}, Action: Top, Help: "first row"},
	{Keys: []string{"end"}, Action: Bottom, Help: "last row"},
	{Keys: []string{"r"}, Action: Refresh, Help: "refresh now", Bar: "refresh"},
	{Keys: []string{"n"}, Action: NewWatch, Help: "new watch"},
	{Keys: []string{"e"}, Action: EditWatch, Help: "edit watch"},
	{Keys: []string{"m"}, Action: MuteWatch, Help: "mute watch"},
	{Keys: []string{"?"}, Action: Help, Help: "help", Bar: "help", Detail: true},
	{Keys: []string{"q", "ctrl+c"}, Action: Quit, Help: "quit", Bar: "quit", Detail: true},
}

// Lookup finds the binding for a key.
func Lookup(key string) (Binding, bool) {
	for _, b := range DefaultKeys {
		if slices.Contains(b.Keys, key) {
			return b, true
		}
	}
	return Binding{}, false
}

// keyName is how the key bar writes a key.
func keyName(k string) string {
	switch k {
	case "enter":
		return "Enter"
	case "esc":
		return "Esc"
	case "space":
		return "Space"
	case "tab":
		return "Tab"
	}
	return k
}
