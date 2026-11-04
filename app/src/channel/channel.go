// Package channel delivers what a watch finds. A watch names the channels it
// uses; the same Notification goes to each, so a user can follow a watch in
// the TUI, by email, in a chat webhook, as a file or as a feed, with identical
// content. Each kind lives in its own file (email.go, webhook.go, ...) and
// registers a Channel.
package channel

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
)

// Kind names a delivery channel.
type Kind string

const (
	TUI     Kind = "tui"     // the in-app alerts widget and unread badges
	Email   Kind = "email"   // the Gmail API with OAuth, see gmail.go
	Webhook Kind = "webhook" // JSON POST; Slack and Discord adapters, Teams via Workflows (its Office 365 connectors were retired in May 2026)
	File    Kind = "file"    // Markdown written to a folder
	Feed    Kind = "feed"    // Atom feed a reader can subscribe to
)

// Valid reports whether k is a known channel.
func (k Kind) Valid() bool {
	switch k {
	case TUI, Email, Webhook, File, Feed:
		return true
	}
	return false
}

// Notification is one delivery: an alert hit or a digest. Before sending,
// callers must drop articles whose source is not visible to TenantID
// (sources.Registry.VisibleTo): a personal or internal licence does not allow
// passing the content to another tenant by any channel.
type Notification struct {
	TenantID string
	WatchID  string
	Alert    string // alert name, or "digest"
	Title    string
	Body     string // Markdown
	Link     string // the route to open, for example /w/buffett-core/stock/KO
	At       time.Time
	Articles []model.Article
}

// Key identifies the event the notification is about, so the same event is
// delivered once even when several sources syndicate it: tenant, watch, alert
// and the stories involved. Stories are identified by cluster, falling back to
// the article.
func (n Notification) Key() string {
	ids := make([]string, 0, len(n.Articles))
	for _, a := range n.Articles {
		id := a.ClusterID
		if id == "" {
			id = a.ID
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return strings.Join([]string{n.TenantID, n.WatchID, n.Alert, strings.Join(ids, ",")}, "|")
}

// Channel delivers notifications of one kind.
type Channel interface {
	Kind() Kind
	Send(ctx context.Context, n Notification) error
}
