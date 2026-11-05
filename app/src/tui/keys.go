package tui

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
	FocusFilter  Action = "filter" // the Google-style filter bar
	Keep         Action = "keep"   // label the selected article relevant
	Drop         Action = "drop"   // label it noise
	Refresh      Action = "refresh"
	NewWatch     Action = "new-watch"
	EditWatch    Action = "edit-watch"
	MuteWatch    Action = "mute-watch"
	Help         Action = "help"
	Quit         Action = "quit"
)

// Binding is one key assignment.
type Binding struct {
	Keys   []string
	Action Action
	Help   string
}

// DefaultKeys is the key map. Watch switching sits on the easiest keys (tab and
// the digits) because it is the most frequent move.
var DefaultKeys = []Binding{
	{[]string{"tab"}, NextWatch, "next watch"},
	{[]string{"shift+tab"}, PrevWatch, "previous watch"},
	{[]string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}, JumpWatch, "jump to watch n"},
	{[]string{"w", "ctrl+k"}, SwitchWatch, "find a watch"},
	{[]string{"T"}, SwitchTenant, "switch tenant"},
	{[]string{"enter"}, Open, "open"},
	{[]string{"esc", "backspace"}, Back, "back"},
	{[]string{"]"}, Forward, "forward"},
	{[]string{"H"}, GoHome, "dashboard"},
	{[]string{"W"}, GoWatches, "manage watches"},
	{[]string{"s"}, GoStocks, "stocks"},
	{[]string{"c"}, GoCompanies, "all companies (/ narrows them as you type)"},
	{[]string{"#"}, TagStock, "tag the stock (-tag removes)"},
	{[]string{"L"}, ListStock, "put the stock in a list (-list removes)"},
	{[]string{"t"}, GoTopics, "topics"},
	{[]string{"D"}, GoDomains, "domains"},
	{[]string{"S"}, GoSources, "source health"},
	{[]string{"o"}, OpenURL, "open the article, website or filing in the browser"},
	{[]string{"up", "ctrl+p"}, Up, "move up"},
	{[]string{"down", "ctrl+n"}, Down, "move down"},
	{[]string{"pgup"}, PageUp, "page up"},
	{[]string{"pgdown"}, PageDown, "page down"},
	{[]string{"home"}, Top, "first row"},
	{[]string{"end"}, Bottom, "last row"},
	{[]string{"/"}, FocusFilter, "filter"},
	{[]string{"k"}, Keep, "keep (relevant)"},
	{[]string{"d"}, Drop, "drop (noise)"},
	{[]string{"r"}, Refresh, "refresh now"},
	{[]string{"n"}, NewWatch, "new watch"},
	{[]string{"e"}, EditWatch, "edit watch"},
	{[]string{"m"}, MuteWatch, "mute watch"},
	{[]string{"?"}, Help, "help"},
	{[]string{"q", "ctrl+c"}, Quit, "quit"},
}

// Lookup finds the binding for a key.
func Lookup(key string) (Binding, bool) {
	for _, b := range DefaultKeys {
		for _, k := range b.Keys {
			if k == key {
				return b, true
			}
		}
	}
	return Binding{}, false
}
