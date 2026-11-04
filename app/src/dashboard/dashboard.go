// Package dashboard names the widgets a watch's home screen can show. A watch
// lists the ones it wants, in order; the TUI lays them out to fit the terminal.
package dashboard

import "fmt"

// Widget is one panel of the dashboard.
type Widget string

const (
	Alerts     Widget = "alerts"     // recent alert hits with unread markers
	Digest     Widget = "digest"     // top relevant articles
	Timeline   Widget = "timeline"   // bars per day, then events
	TagCloud   Widget = "tagcloud"   // topics and stocks as a cloud
	Stocks     Widget = "stocks"     // stocks in this watch, by materiality and tone
	Topics     Widget = "topics"     // rising topics
	Domains    Widget = "domains"    // relevant news per domain
	Candidates Widget = "candidates" // stocks not on any list yet
)

var known = map[Widget]bool{
	Alerts: true, Digest: true, Timeline: true, TagCloud: true,
	Stocks: true, Topics: true, Domains: true, Candidates: true,
}

// Default is the layout used when a watch lists no widgets.
func Default() []Widget {
	return []Widget{Alerts, Digest, Timeline, TagCloud, Stocks, Domains}
}

// Validate checks that every widget is known and none repeats.
func Validate(ws []Widget) error {
	seen := map[Widget]bool{}
	for _, w := range ws {
		if !known[w] {
			return fmt.Errorf("dashboard: unknown widget %q", w)
		}
		if seen[w] {
			return fmt.Errorf("dashboard: widget %q listed twice", w)
		}
		seen[w] = true
	}
	return nil
}
