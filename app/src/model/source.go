package model

import (
	"errors"
	"fmt"
	"net/url"
	"time"
)

// Licence says what the terms of a source allow. It is enforced where
// articles are shown or sent to a tenant.
type Licence string

const (
	// LicenceRedistributable: the source's terms let anyone copy and pass on
	// the content. Terms must cite where that is granted.
	LicenceRedistributable Licence = "redistributable"
	// LicenceInternal: for the owner's own use inside one organisation; it may
	// not be passed to other tenants.
	LicenceInternal Licence = "internal"
	// LicencePersonal: for one person's own use; not for other tenants.
	LicencePersonal Licence = "personal"
)

// Source is one place news comes from.
type Source struct {
	ID      string   `yaml:"id"`
	Name    string   `yaml:"name"`
	Domain  string   `yaml:"domain"`
	Kind    Kind     `yaml:"kind"`
	URL     string   `yaml:"url"`
	Tier    Tier     `yaml:"tier"`
	Tags    []string `yaml:"tags"`
	Enabled bool     `yaml:"enabled"`

	// Licence and Terms: what the source's terms allow and where they say so.
	// Terms is required for redistributable sources, so the grant is on record.
	Licence Licence `yaml:"licence"`
	Terms   string  `yaml:"terms"`
	// Owner is the tenant that holds a personal or internal licence, and the
	// only tenant that may see the source's articles.
	Owner string `yaml:"owner"`

	// AuthEnv names the environment variable that holds this source's API key
	// or token (set it in .env.local). Only the name is ever logged or shown.
	AuthEnv string `yaml:"auth_env"`
	// Forms lists the SEC form types a filings source follows, such as "8-K".
	Forms []string `yaml:"forms"`

	// Interval overrides fetch.interval for this source, for example "5m".
	Interval string `yaml:"interval"`
	// Paged marks a rolling-window feed that sends no ETag or Last-Modified
	// (EDGAR's current-filings feed). The fetcher must read it page by page
	// until it reaches an article it already has, or entries are lost.
	Paged bool `yaml:"paged"`
}

// VisibleTo reports whether a tenant may see this source's articles.
func (s Source) VisibleTo(tenantID string) bool {
	switch s.Licence {
	case LicenceRedistributable:
		return true
	case LicenceInternal, LicencePersonal:
		return s.Owner != "" && s.Owner == tenantID
	}
	return false
}

// Validate reports every problem with the source definition.
func (s Source) Validate() error {
	var errs []error
	bad := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("source %q: "+format, append([]any{s.ID}, args...)...))
	}
	if !ValidID(s.ID) {
		bad("id must be 1-40 lower-case letters, digits or hyphens")
	}
	if u, err := url.Parse(s.URL); err != nil || u.Scheme != "https" || u.Host == "" {
		bad("url %q must be an https URL", s.URL)
	}
	switch s.Kind {
	case KindRSS, KindFilings, KindAPI:
	default:
		bad("unknown kind %q", s.Kind)
	}
	if s.Tier < TierPrimary || s.Tier > TierCommentary {
		bad("tier %d must be 1, 2 or 3", s.Tier)
	}
	switch s.Licence {
	case LicenceRedistributable:
		if s.Terms == "" {
			bad("a redistributable licence needs terms: the page that grants it")
		}
	case LicenceInternal, LicencePersonal:
		if !ValidID(s.Owner) {
			bad("a %s licence needs an owner tenant", s.Licence)
		}
	default:
		bad("licence %q must be redistributable, internal or personal", s.Licence)
	}
	if s.Interval != "" {
		if d, err := time.ParseDuration(s.Interval); err != nil || d < time.Minute {
			bad("interval %q must be a duration of at least 1m", s.Interval)
		}
	}
	return errors.Join(errs...)
}
