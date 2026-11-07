package tui

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"

	"github.com/b070nd/stoxicity/app/src/nav"
)

// Action is something a key does. The shell maps actions to behaviour; keeping
// the key map as data lets a test catch two actions bound to one key and lets
// the help screen list the same bindings the shell uses.
type Action string

const (
	NextSection    Action = "next-section"
	PrevSection    Action = "previous-section"
	FocusNav       Action = "focus-navigation"
	ToggleRelevant Action = "relevant"
	ToggleFacets   Action = "facets"
	NextWatch      Action = "next-watch" // the whole point: one key to the next watch
	PrevWatch      Action = "prev-watch"
	JumpWatch      Action = "jump-watch" // 1-9: the n-th watch; in an open article, its n-th stock
	Open           Action = "open"
	Back           Action = "back"
	Forward        Action = "forward"
	GoHome         Action = "home"
	GoWatches      Action = "watches"
	GoStocks       Action = "stocks"
	GoCompanies    Action = "companies" // every known company, filterable
	TagStock       Action = "tag"       // tag the stock under the cursor
	ListStock      Action = "list"      // put the stock under the cursor in a list
	GoTopics       Action = "topics"
	GoExchanges    Action = "exchanges"     // the markets stocks are listed on
	NextExchange   Action = "next-exchange" // the exchange every screen is narrowed to: the next one, ending with all
	PrevExchange   Action = "prev-exchange"
	UseExchange    Action = "use-exchange" // narrow every screen to the exchange on this screen
	GoDomains      Action = "domains"
	GoSources      Action = "sources"
	GoLabel        Action = "label"    // the queue of articles to judge
	OpenURL        Action = "open-url" // the selected article in the browser
	Up             Action = "up"
	Down           Action = "down"
	PageUp         Action = "page-up"
	PageDown       Action = "page-down"
	Top            Action = "top"
	Bottom         Action = "bottom"
	FocusFilter    Action = "filter"      // the Google-style filter bar
	Jump           Action = "jump"        // move to the first company that starts with what you type
	SortNext       Action = "sort-next"   // sort by the next column
	SortPrev       Action = "sort-prev"   // sort by the previous column
	SortInvert     Action = "sort-invert" // turn the order round
	GroupNext      Action = "group"       // group the catalogue by exchange, kind, industry, or not
	Mark           Action = "mark"        // mark the stock under the cursor and move down
	Unmark         Action = "unmark"      // clear every mark
	Keep           Action = "keep"        // label the selected article relevant
	Drop           Action = "drop"        // label it noise
	Refresh        Action = "refresh"
	Help           Action = "help"
	Quit           Action = "quit"
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
	// Marked: some stocks are marked, so # and L have something to apply to
	// wherever the cursor is.
	Marked bool
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
		return c.Marked || c.Kind == nav.Stock || (c.HasRow && markable(c.Row) != "")
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

// DefaultKeys is the key map, in the order the key bar shows it.
//
// The rules the map keeps (keys_test.go checks them):
//   - no key is bound twice, ignoring case, so a letter never means one thing
//     capitalised and another in lower case;
//   - every screen is reached by a letter that is in its own name, so the tab
//     bar can underline it in the word (Sources is "o": Stocks has the s);
//   - every key that is a letter of its label is underlined there; only keys
//     that are not (arrows, Space, +, -, /, #, ?) are written in front;
//   - the arrows move through the hierarchy: left is back, right is open, up and
//     down move the cursor, and on a row of chips left and right move along it.
//
// Tab cycles sections; watch switching is separate from navigation.
var DefaultKeys = []Binding{
	{Keys: []string{"tab"}, Action: NextSection, Help: "next section", Bar: "sections"},
	{Keys: []string{"shift+tab"}, Action: PrevSection, Help: "previous section"},
	{Keys: []string{"f6"}, Action: FocusNav, Help: "focus navigation; arrows select a section"},
	{Keys: []string{"ctrl+tab"}, Action: NextWatch, Help: "next watch"},
	{Keys: []string{"ctrl+shift+tab"}, Action: PrevWatch, Help: "previous watch"},
	{Keys: []string{"v"}, Action: ToggleRelevant, Help: "toggle relevant-only news", Bar: "relevant"},
	{Keys: []string{"f"}, Action: ToggleFacets, Help: "expand or collapse catalogue facets", Bar: "facets", Screens: []nav.Kind{nav.Companies}},
	{Keys: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}, Action: JumpWatch, Help: "watch n (in an article: its stock n)", Detail: true},
	{Keys: []string{"right", "enter"}, Action: Open, Help: "open the row", Bar: "open", Needs: OnRow, Detail: true},
	{Keys: []string{"left", "esc", "backspace"}, Action: Back, Help: "back up the hierarchy", Bar: "back", Detail: true},
	{Keys: []string{"]"}, Action: Forward, Help: "forward again"},
	{Keys: []string{"n", "h"}, Action: GoHome, Help: "News: chronological feed"},
	{Keys: []string{"s"}, Action: GoStocks, Help: "Stocks: all, news first"},
	{Keys: []string{"c"}, Action: GoCompanies, Help: "Companies (/ narrows)"},
	{Keys: []string{"t"}, Action: GoTopics, Help: "Topics"},
	{Keys: []string{"d"}, Action: GoDomains, Help: "Domains"},
	{Keys: []string{"e"}, Action: GoExchanges, Help: "Exchanges: hours, statistics, news"},
	{Keys: []string{"o"}, Action: GoSources, Help: "Sources: their health"},
	{Keys: []string{"w"}, Action: GoWatches, Help: "Watches: manage them"},
	{Keys: []string{"a"}, Action: GoLabel, Help: "Label: judge what the lens doubts"},
	{Keys: []string{"/"}, Action: FocusFilter, Help: "filter", Bar: "filter"},
	{Keys: []string{"j"}, Action: Jump, Help: "catalogue: jump to a ticker or name", Bar: "jump", Screens: []nav.Kind{nav.Companies}},
	{Keys: []string{">"}, Action: SortNext, Help: "sort by the next column", Bar: "sort", Needs: OnColumns},
	{Keys: []string{"<"}, Action: SortPrev, Help: "sort by the previous column", Needs: OnColumns},
	{Keys: []string{"i"}, Action: SortInvert, Help: "turn the sort order round", Bar: "invert", Needs: OnColumns},
	{Keys: []string{"g"}, Action: GroupNext, Help: "group by exchange, kind, industry, none", Bar: "group", Screens: []nav.Kind{nav.Companies}},
	{Keys: []string{"space"}, Action: Mark, Help: "mark stock, move down (chip: pick)", Bar: "mark", Needs: OnStock},
	{Keys: []string{"ctrl+u"}, Action: Unmark, Help: "clear all marks"},
	{Keys: []string{"#"}, Action: TagStock, Help: "tag stock or marked (-tag removes)", Bar: "tag", Needs: OnStock},
	{Keys: []string{"l"}, Action: ListStock, Help: "list stock or marked (-list removes)", Bar: "list", Needs: OnStock},
	{Keys: []string{"+"}, Action: Keep, Help: "keep (relevant)", Bar: "keep", Needs: OnArticle, Detail: true},
	{Keys: []string{"-"}, Action: Drop, Help: "drop (noise)", Bar: "drop", Needs: OnArticle, Detail: true},
	{Keys: []string{"b"}, Action: OpenURL, Help: "open article, site or filing", Bar: "browser", Needs: OnLink, Detail: true},
	{Keys: []string{"x"}, Action: NextExchange, Help: "next exchange (narrows every screen)", Bar: "exchange"},
	{Keys: []string{"ctrl+x"}, Action: PrevExchange, Help: "previous exchange (all comes between)"},
	{Keys: []string{"u"}, Action: UseExchange, Help: "narrow every screen to this exchange", Screens: []nav.Kind{nav.Exchanges, nav.Exchange}},
	{Keys: []string{"up", "ctrl+p"}, Action: Up, Help: "move up"},
	{Keys: []string{"down", "ctrl+n"}, Action: Down, Help: "move down"},
	{Keys: []string{"pgup"}, Action: PageUp, Help: "page up"},
	{Keys: []string{"pgdown"}, Action: PageDown, Help: "page down"},
	{Keys: []string{"home"}, Action: Top, Help: "first row"},
	{Keys: []string{"end"}, Action: Bottom, Help: "last row"},
	{Keys: []string{"r"}, Action: Refresh, Help: "refresh now", Bar: "refresh"},
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
	case "left":
		return "←"
	case "right":
		return "→"
	case "up":
		return "↑"
	case "down":
		return "↓"
	}
	return k
}

// hotKey reports whether the key is a single letter or digit that the label
// contains, so it can be underlined in the word instead of written in front.
func hotKey(key, label string) (at int, ok bool) {
	if len(key) != 1 || !(key[0] >= 'a' && key[0] <= 'z' || key[0] >= '0' && key[0] <= '9') {
		return 0, false
	}
	for i := 0; i < len(label); i++ { // bytes: a multi-byte rune never equals an ASCII key
		if c := label[i]; c|0x20 == key[0] && (c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z') || c == key[0] {
			return i, true
		}
	}
	return 0, false
}

// hotLabel writes a label with its key: the key's letter underlined inside the
// word when it is one, else the key's name in front. rest styles the label, hot
// styles the key.
func hotLabel(key, label string, rest, hot lipgloss.Style) string {
	if key == "" {
		return rest.Render(label)
	}
	if at, ok := hotKey(key, label); ok {
		return rest.Render(label[:at]) + hot.Render(label[at:at+1]) + rest.Render(label[at+1:])
	}
	return hot.Render(keyName(key)) + rest.Render(" "+label)
}

// hotWidth is the cells hotLabel takes.
func hotWidth(key, label string) int {
	if key == "" {
		return utf8.RuneCountInString(label)
	}
	if _, ok := hotKey(key, label); ok {
		return utf8.RuneCountInString(label)
	}
	return utf8.RuneCountInString(keyName(key)) + 1 + utf8.RuneCountInString(label)
}
