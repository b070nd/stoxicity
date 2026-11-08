// Package quota describes durable, installation-wide request budgets. A budget
// is configured, not a claim about a provider's subscription or remaining credit.
package quota

import (
	"context"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"time"
)

// Limit counts request attempts in a window beginning with its first attempt.
// Zero means unknown/unconfigured, not unlimited provider entitlement.
type Limit struct {
	Requests int           `json:"requests"`
	Period   time.Duration `json:"period"`
}

func (l Limit) Validate() error {
	if l == (Limit{}) {
		return nil
	}
	if l.Requests <= 0 || l.Period < time.Millisecond || l.Period > 366*24*time.Hour {
		return fmt.Errorf("quota: requests must be positive and period between 1ms and 8784h")
	}
	return nil
}

var keyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,252}$`)

func ValidKey(key string) bool {
	if keyPattern.MatchString(key) {
		return true
	}
	addr, err := netip.ParseAddr(key)
	return err == nil && addr.Zone() == "" && key == strings.ToLower(key)
}

// State contains no URL, credential or response body. Until is a server's
// cooldown; Reset is the configured budget's window end, independently.
type State struct {
	Provider string    `json:"provider"`
	Used     int       `json:"used"`
	Limit    Limit     `json:"limit"`
	Reset    time.Time `json:"reset,omitempty"`
	Until    time.Time `json:"blocked_until,omitempty"`
	Status   int       `json:"last_throttle_status,omitempty"`
}

// WithLimit projects current configuration without reserving an attempt or
// starting a window. Enforcement and status use the same transition rules.
func (s State) WithLimit(limit Limit, now time.Time) State {
	if !now.Before(s.Reset) {
		s.Used, s.Reset = 0, time.Time{}
	} else if limit.Requests > 0 && limit.Period > s.Limit.Period {
		end := now.Add(limit.Period).Add(time.Millisecond - time.Nanosecond).Truncate(time.Millisecond)
		if end.After(s.Reset) {
			s.Reset = end
		}
	}
	s.Limit = limit
	return s
}

// Exhausted is a refusal before network IO, or a recorded server throttle.
type Exhausted struct {
	Provider string
	Until    time.Time
	Reason   string
}

func (e *Exhausted) Error() string {
	return fmt.Sprintf("provider quota %s: %s; retry at %s", e.Provider, e.Reason, e.Until.UTC().Format(time.RFC3339Nano))
}

// Check evaluates without writing. A clock moving backwards cannot refill a
// budget: only reaching its recorded reset ends the window.
func (s State) Check(now time.Time) error {
	until, reason := s.Until, "provider throttled requests"
	if s.Limit.Requests > 0 && s.Used >= s.Limit.Requests && now.Before(s.Reset) && !s.Reset.Before(until) {
		until, reason = s.Reset, "configured request budget exhausted"
	}
	if now.Before(until) {
		return &Exhausted{Provider: s.Provider, Until: until, Reason: reason}
	}
	return nil
}

// Store serializes reservations across clients/processes. Requests that fail,
// redirect, retry or return 304 still consume attempts; no refund is guessed.
type Store interface {
	TakeQuota(context.Context, string, Limit, time.Time) error
	CheckQuotaCooldown(context.Context, string, time.Time) error
	BlockQuota(context.Context, string, time.Time, int) error
	QuotaStates(context.Context) ([]State, error)
}
