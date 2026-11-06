// Package exchange is what the program knows about the markets stocks are
// listed on: who runs them, what they list and when they trade, with the
// calendar of US market holidays so it can say whether a market is open now.
//
// The names are those of universe.Profile.Exchange ("Nasdaq", "NYSE",
// "NYSE American", "NYSE Arca", "Cboe BZX", "IEX", "OTC", "TXSE"). The facts
// here are the published, slow-moving ones (operator, core hours, kind of
// issuer); what changes (how many companies, how much news) is counted from
// the universe and the articles by the screens.
package exchange

import (
	"strings"
	"time"
	_ "time/tzdata" // the market's clock must not depend on the host having zone files
)

// Session is a stretch of the trading day, in the market's own clock.
type Session struct {
	Name     string
	From, To string // "15:04"
	// Core marks the main session; the others are extended hours.
	Core bool
}

// Info describes one exchange.
type Info struct {
	// Name is the word the universe uses for it.
	Name string
	// Full is its proper name; Operator the company that runs it.
	Full, Operator string
	Country, City  string
	// MIC is the ISO 10383 market identifier, empty when not sure of it.
	MIC string
	// Zone is the market's time zone; Currency what it trades in.
	Zone, Currency string
	Website        string
	// About says what the market is; Lists what kind of companies and
	// securities trade on it.
	About, Lists string
	// Sessions are the trading hours of a normal weekday, in Zone.
	Sessions []Session
	// Note is a caveat about the hours.
	Note string
}

var usSessions = func(early, late string) []Session {
	return []Session{
		{Name: "Pre-market", From: early, To: "09:30"},
		{Name: "Regular", From: "09:30", To: "16:00", Core: true},
		{Name: "After-hours", From: "16:00", To: late},
	}
}

const extendedNote = "Extended sessions are the exchange's; brokers open them for their customers at their own times and limits. Holidays and early closes (13:00) follow the US market calendar."

var infos = []Info{
	{
		Name: "Nasdaq", Full: "Nasdaq Stock Market", Operator: "Nasdaq, Inc.", Country: "United States", City: "New York",
		MIC: "XNAS", Zone: "America/New_York", Currency: "USD", Website: "https://www.nasdaq.com",
		About:    "Fully electronic exchange founded in 1971, among the largest in the world by the value of its listed companies. Home of the Nasdaq-100.",
		Lists:    "Technology, biotech and other growth companies, and many large established ones. Three listing tiers: Global Select (strictest), Global and Capital Market (smaller companies). Also ETFs.",
		Sessions: usSessions("04:00", "20:00"), Note: extendedNote,
	},
	{
		Name: "NYSE", Full: "New York Stock Exchange", Operator: "Intercontinental Exchange (ICE)", Country: "United States", City: "New York",
		MIC: "XNYS", Zone: "America/New_York", Currency: "USD", Website: "https://www.nyse.com",
		About:    "The oldest US exchange (1792) and the largest by the value of its listed companies; a hybrid of a trading floor in Wall Street and electronic matching.",
		Lists:    "Many of the largest and longest-established companies, in every industry; also preferred shares, closed-end funds and some ETFs.",
		Sessions: usSessions("04:00", "20:00"), Note: extendedNote,
	},
	{
		Name: "NYSE American", Full: "NYSE American", Operator: "Intercontinental Exchange (ICE)", Country: "United States", City: "New York",
		MIC: "XASE", Zone: "America/New_York", Currency: "USD", Website: "https://www.nyse.com/markets/nyse-american",
		About:    "The former American Stock Exchange, run by ICE since the NYSE took it over.",
		Lists:    "Small and mid-size companies, with lighter listing standards than the NYSE; also some ETFs and options.",
		Sessions: usSessions("04:00", "20:00"), Note: extendedNote,
	},
	{
		Name: "NYSE Arca", Full: "NYSE Arca", Operator: "Intercontinental Exchange (ICE)", Country: "United States", City: "New York",
		MIC: "ARCX", Zone: "America/New_York", Currency: "USD", Website: "https://www.nyse.com/markets/nyse-arca",
		About:    "All-electronic exchange, the main listing venue of US exchange-traded funds.",
		Lists:    "Mostly ETFs and other exchange-traded products; a few companies.",
		Sessions: usSessions("04:00", "20:00"), Note: extendedNote,
	},
	{
		Name: "Cboe BZX", Full: "Cboe BZX Exchange", Operator: "Cboe Global Markets", Country: "United States", City: "Chicago",
		MIC: "BATS", Zone: "America/New_York", Currency: "USD", Website: "https://www.cboe.com",
		About:    "Electronic exchange that began as BATS and is run by Cboe; one of the busiest US trading venues.",
		Lists:    "ETFs and exchange-traded products above all, and some companies that moved their listing from others.",
		Sessions: usSessions("04:00", "20:00"), Note: extendedNote,
	},
	{
		Name: "IEX", Full: "Investors Exchange", Operator: "IEX Group", Country: "United States", City: "New York",
		MIC: "IEXG", Zone: "America/New_York", Currency: "USD", Website: "https://www.iex.io",
		About: "Electronic exchange approved as a national securities exchange in 2016, known for the speed bump that slows every order by about 350 microseconds.",
		Lists: "Mostly a trading venue for stocks listed elsewhere; very few companies list here.",
		Sessions: []Session{
			{Name: "Pre-market", From: "08:00", To: "09:30"},
			{Name: "Regular", From: "09:30", To: "16:00", Core: true},
			{Name: "Post-market", From: "16:00", To: "17:00"},
		},
		Note: extendedNote,
	},
	{
		Name: "OTC", Full: "Over-the-counter markets", Operator: "OTC Markets Group", Country: "United States", City: "New York",
		Zone: "America/New_York", Currency: "USD", Website: "https://www.otcmarkets.com",
		About:    "Not an exchange: networks of broker-dealers who quote shares directly (OTCQX, OTCQB, Pink).",
		Lists:    "Small, young, foreign and thinly traded companies, and former exchange-listed ones. Disclosure is lighter and prices can be wide and thin.",
		Sessions: []Session{{Name: "Regular", From: "09:30", To: "16:00", Core: true}},
		Note:     "Quotes also run in extended hours, but the times vary by broker and security. Holidays follow the US market calendar.",
	},
	{
		Name: "TXSE", Full: "Texas Stock Exchange", Operator: "TXSE Group", Country: "United States", City: "Dallas",
		Zone: "America/Chicago", Currency: "USD", Website: "https://www.txse.com",
		About:    "A new US national securities exchange in Dallas, approved by the SEC in 2025. Check its website for the state of listings and trading.",
		Lists:    "Companies that chose Texas for their listing; few so far.",
		Sessions: []Session{{Name: "Regular", From: "08:30", To: "15:00", Core: true}},
		Note:     "Regular hours are shown in Central time (the same 09:30–16:00 Eastern); extended hours are not modelled. Holidays follow the US market calendar.",
	},
}

// aliases are other spellings of an exchange name.
var aliases = map[string]string{"cboe": "Cboe BZX", "bats": "Cboe BZX", "nasdaq stock market": "Nasdaq", "nyse arca": "NYSE Arca"}

// Lookup finds an exchange by its name, ignoring case.
func Lookup(name string) (Info, bool) {
	name = strings.TrimSpace(name)
	if a, ok := aliases[strings.ToLower(name)]; ok {
		name = a
	}
	for _, i := range infos {
		if strings.EqualFold(i.Name, name) {
			return i, true
		}
	}
	return Info{}, false
}

// Known lists every exchange there is reference data for, in the order it
// matters to a US reader.
func Known() []Info { return append([]Info(nil), infos...) }

// Location is the market's time zone.
func (i Info) Location() *time.Location {
	loc, err := time.LoadLocation(i.Zone)
	if err != nil {
		return time.UTC
	}
	return loc
}
