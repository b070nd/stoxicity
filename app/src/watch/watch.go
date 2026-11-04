// Package watch defines a monitoring watch: a saved, named view over the
// shared information base. A watch selects what to follow (stocks, topics,
// sectors), narrows it (domains, event kinds, thresholds, a search query),
// says when to alert and through which channels, and which widgets make up its
// dashboard. Watches never copy articles or re-score them; switching between
// watches only changes the selection, so it is instant.
//
// A watch is one YAML file in the tenant's watches folder. Custom words belong
// in Query (`earnings OR "free cash flow" -"price target"`), which is applied
// on read, rather than in per-watch boost lists, which would need re-scoring.
package watch

import (
	"time"

	"github.com/b070nd/stoxicity/app/src/channel"
	"github.com/b070nd/stoxicity/app/src/dashboard"
	"github.com/b070nd/stoxicity/app/src/model"
)

// Watch is one monitoring watch.
type Watch struct {
	ID          string `yaml:"id"`
	Name        string `yaml:"name"`
	Description string `yaml:"description"`

	Scope Scope  `yaml:"scope"`
	Query string `yaml:"query"` // Google-style, see package filter
	Lens  Lens   `yaml:"lens"`

	Alerts    []Alert            `yaml:"alerts"`
	Digest    Digest             `yaml:"digest"`
	Dashboard []dashboard.Widget `yaml:"dashboard"` // empty means dashboard.Default
}

// Scope says what the watch follows. Tickers, Topics and Sectors are subjects:
// an article must match at least one of those that are set, and if none are
// set every subject qualifies. Domains, ExcludeDomains, Groups and Kinds are
// constraints and all of those that are set must hold. A domain entry matches
// that domain and its subdomains: "sec.gov" matches "www.sec.gov". Groups
// select whole families of events ("integrity"); Kinds select single ones.
type Scope struct {
	Tickers        []string           `yaml:"tickers"`
	Topics         []string           `yaml:"topics"`
	Sectors        []string           `yaml:"sectors"`
	Domains        []string           `yaml:"domains"`
	ExcludeDomains []string           `yaml:"exclude_domains"`
	Groups         []model.EventGroup `yaml:"groups"`
	Kinds          []model.EventKind  `yaml:"kinds"`
}

// Lens tightens the base score for this watch. Zero values mean "use the
// global setting".
type Lens struct {
	Threshold      float64 `yaml:"threshold"`       // relevance, 0..1
	MinMateriality int     `yaml:"min_materiality"` // 1..5
}

// When is the condition of an alert. Every set field must hold.
type When struct {
	MinRelevance   float64           `yaml:"min_relevance"`
	MinMateriality int               `yaml:"min_materiality"`
	Kinds          []model.EventKind `yaml:"kinds"`
}

// Alert sends a notification when a matching article arrives.
type Alert struct {
	Name     string         `yaml:"name"`
	When     When           `yaml:"when"`
	Channels []channel.Kind `yaml:"channels"`
	Quiet    string         `yaml:"quiet_hours"` // "22:00-07:00", in the viewer's time zone
	Cooldown string         `yaml:"cooldown"`    // for example "2h"
}

// Policy turns the alert's quiet hours and cooldown into a channel.Policy. It
// assumes Validate passed.
func (a Alert) Policy() channel.Policy {
	w, _ := channel.ParseWindow(a.Quiet)
	d, _ := time.ParseDuration(a.Cooldown)
	return channel.Policy{Quiet: w, Cooldown: d}
}

// Digest is a periodic summary of the watch.
type Digest struct {
	Cadence  string         `yaml:"cadence"` // off, daily or weekly
	At       string         `yaml:"at"`      // "07:30"
	Channels []channel.Kind `yaml:"channels"`
}

// Hit records that an article matched a watch. A watch and an article have at
// most one hit.
type Hit struct {
	WatchID   string
	ArticleID string
	// At is when the hit was detected, that is, when the ledger found that the
	// article matched. It is not the article's publication time: an article can
	// be fetched hours after it was published, and the read marker (Unread,
	// MarkSeen) is compared with the time the user could first have seen it.
	At     time.Time
	Alerts []string // names of the alerts whose condition also held
}

// Store reads and writes a tenant's watch files.
type Store interface {
	List() ([]Watch, error)
	Get(id string) (Watch, error)
	Save(w Watch) error
	Delete(id string) error
}
