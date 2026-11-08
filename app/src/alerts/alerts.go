// Package alerts keeps the things the user should hear about, once, and keeps
// honest track of whether they were heard.
//
// An Alert is an immutable event: a plan revision that needs a decision, a
// review that is due, an ex-date, an income that did not arrive or differs, a
// broker exception, an order whose state is unknown. Its identity is the rule,
// the subject and the revision of the fact (Key), so the same fact is raised
// once however often it is looked for, and a corrected fact is a new revision
// that says which one it replaces.
//
// Delivery is a separate record per alert and channel, an outbox. The state
// machine is written for the failures that make an alerting system lie:
//
//   - A send is recorded as "sending" BEFORE it is attempted and as "sent" only
//     after the channel returned no error. A send that was started and whose
//     result was never recorded (the program stopped) becomes "uncertain" on
//     the next run and is repeated with a note that it may be a repeat. It is
//     never marked sent without proof.
//   - A failed send keeps the alert pending with the attempts and the next try;
//     after the last attempt it is shown as failed until the user retries.
//   - A channel that is not configured leaves the alert waiting, never sent.
//   - Quiet hours hold an alert until they end; a rule the user configured as
//     critical goes through them.
//   - A newer revision of the same fact replaces an undelivered older one
//     (superseded) and, when the older one had been delivered, is sent as a
//     correction. A situation that ended before delivery withdraws the alert.
//
// The user's answer (accept, reject, note) is a record of its own, kept beside
// the alert. It never rewrites the alert, the plan or the evidence behind it.
//
// Alerts carry the facts of the user's own plans, books and orders and never
// the text of an article. They go only to the channels the tenant lists for the
// rule; the default is local files and the feed, and a shared channel such as
// email or a webhook is used only when the tenant asks for it.
package alerts

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/channel"
)

// The record kinds.
const (
	KindEvent    = "alert.event"
	KindDelivery = "alert.delivery"
	KindConfig   = "alert.config"
	KindResponse = "alert.response"
)

// Rule names what an alert is about.
type Rule string

// The rules.
const (
	PlanRevision     Rule = "plan_revision"     // a plan revision waits for a decision
	ReviewDue        Rule = "review_due"        // a holding or an accepted plan is due for review
	DividendExDate   Rule = "dividend_ex_date"  // an ex-date is near, or just passed, for a held security
	IncomeMissing    Rule = "income_missing"    // the pay day has passed and nothing is booked
	IncomeDifference Rule = "income_difference" // the ledger differs from what the payout implies
	BrokerException  Rule = "broker_exception"  // the broker and the books differ
	OrderUnknown     Rule = "order_unknown"     // an order's state is unknown
)

// Rules lists every rule in a fixed order.
func Rules() []Rule {
	return []Rule{PlanRevision, ReviewDue, DividendExDate, IncomeMissing, IncomeDifference, BrokerException, OrderUnknown}
}

// Valid reports whether r is a known rule.
func (r Rule) Valid() bool {
	for _, x := range Rules() {
		if x == r {
			return true
		}
	}
	return false
}

// Condition reports whether the rule describes a situation that can end. When
// it ends before the alert was delivered, the alert is withdrawn. A plan
// revision is an event: it stays true.
func (r Rule) Condition() bool { return r != PlanRevision }

// Alert is one fact the user should hear about. It is never edited.
type Alert struct {
	Rule     Rule      `json:"rule"`
	Subject  string    `json:"subject"`  // what it is about: a plan, a payout, an order, a book and security
	Revision string    `json:"revision"` // the version of the fact; a change of the fact is a new revision
	Title    string    `json:"title"`
	Body     string    `json:"body"` // Markdown, facts only, no article text
	Link     string    `json:"link,omitempty"`
	Raised   time.Time `json:"raised"`
	// Corrects is the key of the earlier revision of the same fact this one
	// replaces, set when it is raised.
	Corrects string `json:"corrects,omitempty"`
}

// Key identifies the fact and its revision.
func (a Alert) Key() string { return string(a.Rule) + "|" + a.Subject + "|" + a.Revision }

// Group identifies the fact over its revisions.
func (a Alert) Group() string { return string(a.Rule) + "|" + a.Subject }

// ID is a short name for the command line.
func (a Alert) ID() string { return idOf(a.Key()) }

func idOf(key string) string {
	sum := sha256.Sum256([]byte(key))
	return "al_" + hex.EncodeToString(sum[:])[:10]
}

// Validate says what makes the alert unusable.
func (a Alert) Validate() error {
	var bad []string
	if !a.Rule.Valid() {
		bad = append(bad, fmt.Sprintf("unknown rule %q", a.Rule))
	}
	if strings.TrimSpace(a.Subject) == "" || strings.TrimSpace(a.Revision) == "" {
		bad = append(bad, "a subject and a revision are required")
	}
	if strings.Contains(a.Subject, "|") || strings.Contains(a.Revision, "|") {
		bad = append(bad, "the subject and the revision must not contain |")
	}
	if len(a.Key()) > 200 {
		bad = append(bad, "the key is longer than 200 characters")
	}
	if strings.TrimSpace(a.Title) == "" {
		bad = append(bad, "a title is required")
	}
	if len(bad) > 0 {
		return errors.New("alert: " + strings.Join(bad, "; "))
	}
	return nil
}

// State of one delivery of one alert on one channel.
type State string

// The delivery states.
const (
	Pending    State = "pending"    // waiting to be sent
	Sending    State = "sending"    // a send was started; the result is not recorded
	Sent       State = "sent"       // the channel accepted it: the only proof of delivery
	Failed     State = "failed"     // the last send failed; Next is the next try, empty after the last attempt
	Held       State = "held"       // held by quiet hours until Next
	Uncertain  State = "uncertain"  // a send was started and its result was lost; it is repeated and flagged
	Superseded State = "superseded" // a newer revision replaced it before it was sent
	Withdrawn  State = "withdrawn"  // the situation ended, or the user answered, before it was sent
)

// Final reports whether the state ends the delivery.
func (s State) Final() bool { return s == Sent || s == Superseded || s == Withdrawn }

// Waiting reports whether the delivery still has to be sent.
func (s State) Waiting() bool { return !s.Final() }

// Delivery is the state of one alert on one channel. Each change is a new
// record; the latest is the state.
type Delivery struct {
	Alert    string       `json:"alert"` // the alert key
	Channel  channel.Kind `json:"channel"`
	Rule     Rule         `json:"rule"`
	Group    string       `json:"group"`
	State    State        `json:"state"`
	Attempts int          `json:"attempts"`
	Next     time.Time    `json:"next,omitempty"`
	Reason   string       `json:"reason,omitempty"`
	At       time.Time    `json:"at"`
	// Seq counts the changes of this delivery, so two records of it are never
	// the same content: the store leaves a record alone whose kind, key and
	// content it already holds, and a state that comes back must still be new.
	Seq int `json:"seq"`
}

// Key identifies the delivery.
func (d Delivery) Key() string { return d.Alert + "|" + string(d.Channel) }

// Config is how a tenant wants to be told.
type Config struct {
	// QuietStart and QuietEnd are HH:MM in Zone; both empty means no quiet hours.
	// The window may run past midnight.
	QuietStart string `json:"quiet_start,omitempty"`
	QuietEnd   string `json:"quiet_end,omitempty"`
	Zone       string `json:"zone"`
	// Critical lists the rules that go through quiet hours.
	Critical []Rule `json:"critical"`
	// Channels lists the channels of a rule; the key "*" is every other rule.
	Channels map[string][]channel.Kind `json:"channels"`
	// MaxAttempts and RetryMinutes govern a failed send: the wait doubles after
	// each failure.
	MaxAttempts  int `json:"max_attempts"`
	RetryMinutes int `json:"retry_minutes"`
	// DividendDays is how far ahead an ex-date is announced.
	DividendDays int `json:"dividend_days"`
	// ReviewDays is how long a holding goes without a review before one is due.
	ReviewDays int `json:"review_days"`
}

// DefaultConfig is the starter configuration: no quiet hours, an unknown order
// is critical, and alerts go to local files and the feed only.
func DefaultConfig() Config {
	return Config{
		Zone: "UTC", Critical: []Rule{OrderUnknown},
		Channels:    map[string][]channel.Kind{"*": {channel.File, channel.Feed}},
		MaxAttempts: 5, RetryMinutes: 15, DividendDays: 7, ReviewDays: 91,
	}
}

// ChannelsFor lists the channels to send an alert of the rule to. The TUI is
// not one of them: it shows alerts itself from the stored events.
func (c Config) ChannelsFor(r Rule) []channel.Kind {
	ks, ok := c.Channels[string(r)]
	if !ok {
		ks = c.Channels["*"]
	}
	var out []channel.Kind
	seen := map[channel.Kind]bool{}
	for _, k := range ks {
		if k != channel.TUI && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// IsCritical reports whether the rule goes through quiet hours.
func (c Config) IsCritical(r Rule) bool {
	for _, x := range c.Critical {
		if x == r {
			return true
		}
	}
	return false
}

func clock(s string) (int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, fmt.Errorf("%q is not a time as HH:MM", s)
	}
	return t.Hour()*60 + t.Minute(), nil
}

func (c Config) location() (*time.Location, error) {
	if c.Zone == "" || c.Zone == "UTC" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(c.Zone)
	if err != nil {
		return nil, fmt.Errorf("unknown time zone %q", c.Zone)
	}
	return loc, nil
}

// Validate says why the configuration cannot be used.
func (c Config) Validate() error {
	if (c.QuietStart == "") != (c.QuietEnd == "") {
		return errors.New("alerts: set both ends of the quiet hours or neither")
	}
	if c.QuietStart != "" {
		s, err := clock(c.QuietStart)
		if err != nil {
			return fmt.Errorf("alerts: quiet start: %w", err)
		}
		e, err := clock(c.QuietEnd)
		if err != nil {
			return fmt.Errorf("alerts: quiet end: %w", err)
		}
		if s == e {
			return errors.New("alerts: the quiet hours start and end at the same time")
		}
	}
	if _, err := c.location(); err != nil {
		return fmt.Errorf("alerts: %w", err)
	}
	for _, r := range c.Critical {
		if !r.Valid() {
			return fmt.Errorf("alerts: unknown critical rule %q", r)
		}
	}
	for k, ks := range c.Channels {
		if k != "*" && !Rule(k).Valid() {
			return fmt.Errorf("alerts: channels name unknown rule %q", k)
		}
		for _, x := range ks {
			if !x.Valid() {
				return fmt.Errorf("alerts: unknown channel %q", x)
			}
		}
	}
	switch {
	case c.MaxAttempts < 1 || c.MaxAttempts > 20:
		return errors.New("alerts: attempts must be 1 to 20")
	case c.RetryMinutes < 1 || c.RetryMinutes > 1440:
		return errors.New("alerts: the retry wait must be 1 to 1440 minutes")
	case c.DividendDays < 0 || c.DividendDays > 60:
		return errors.New("alerts: the ex-date notice must be 0 to 60 days")
	case c.ReviewDays < 7 || c.ReviewDays > 400:
		return errors.New("alerts: the review interval must be 7 to 400 days")
	}
	return nil
}

// Quiet says whether now is inside the quiet hours and, if so, when they end.
func (c Config) Quiet(now time.Time) (until time.Time, quiet bool) {
	if c.QuietStart == "" {
		return time.Time{}, false
	}
	loc, err := c.location()
	if err != nil {
		return time.Time{}, false
	}
	s, err1 := clock(c.QuietStart)
	e, err2 := clock(c.QuietEnd)
	if err1 != nil || err2 != nil {
		return time.Time{}, false
	}
	t := now.In(loc)
	m := t.Hour()*60 + t.Minute()
	in := m >= s && m < e
	if s > e {
		in = m >= s || m < e
	}
	if !in {
		return time.Time{}, false
	}
	end := time.Date(t.Year(), t.Month(), t.Day(), e/60, e%60, 0, 0, loc)
	if !end.After(t) {
		end = end.AddDate(0, 0, 1)
	}
	return end.UTC(), true
}

// Response is the user's answer to an alert.
type Response struct {
	Alert string    `json:"alert"`
	Kind  string    `json:"kind"` // accepted, rejected or noted
	Note  string    `json:"note,omitempty"`
	By    string    `json:"by"`
	At    time.Time `json:"at"`
}
