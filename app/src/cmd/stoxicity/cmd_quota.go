package main

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/b070nd/stoxicity/app/src/quota"
)

// cmdQuota reads configured budgets and recorded usage without making requests.
func cmdQuota(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 || args[0] != "status" {
		return usageErrorf("expected: quota status [-json]")
	}
	fs := newFlags("quota status")
	asJSON := fs.Bool("json", false, "print recorded provider budgets and cooldowns as JSON")
	rest, err := parseFlags(fs, args[1:])
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	states, err := db.QuotaStates(ctx)
	if err != nil {
		return err
	}
	byKey := map[string]quota.State{}
	for _, s := range states {
		byKey[s.Provider] = s
	}
	for key := range a.cfg.Fetch.Quotas {
		if _, ok := byKey[key]; !ok {
			byKey[key] = quota.State{Provider: key}
		}
	}
	type row struct {
		Provider string    `json:"provider"`
		State    string    `json:"state"`
		Reason   string    `json:"reason"`
		Used     int       `json:"used"`
		Requests int       `json:"configured_requests"`
		Period   string    `json:"configured_period,omitempty"`
		Reset    time.Time `json:"reset_at,omitempty"`
		RetryAt  time.Time `json:"retry_at,omitempty"`
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := []row{}
	for _, key := range keys {
		s := byKey[key]
		q := a.cfg.Fetch.Quotas[key]
		s = s.WithLimit(q.Limit(), a.now())
		r := row{Provider: key, State: "unknown", Reason: "no configured budget; provider allowance is unknown", Used: s.Used, Requests: q.Requests, Period: q.Period, Reset: s.Reset}
		if q.Requests > 0 {
			r.State = "available"
			r.Reason = "configured local budget; not a provider entitlement"
		}
		if err := s.Check(a.now()); err != nil {
			r.State, r.Reason = "quota_exhausted", err.Error()
			r.RetryAt = err.(*quota.Exhausted).Until
		}
		out = append(out, r)
	}
	if *asJSON {
		return writeJSON(a.stdout, out)
	}
	if len(out) == 0 {
		fmt.Fprintln(a.stdout, "no recorded provider quotas; allowances unknown (configure fetch.quotas to set local budgets)")
		return nil
	}
	for _, r := range out {
		fmt.Fprintf(a.stdout, "%s: %s — %s", r.Provider, r.State, r.Reason)
		if r.Requests > 0 {
			fmt.Fprintf(a.stdout, " (%d/%d attempts per %s)", r.Used, r.Requests, r.Period)
		}
		fmt.Fprintln(a.stdout)
	}
	return nil
}
