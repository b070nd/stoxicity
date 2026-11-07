package ledger

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/channel"
	"github.com/b070nd/stoxicity/app/src/digest"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/store"
	"github.com/b070nd/stoxicity/app/src/watch"
)

// digestAlert is the Notification.Alert of a digest.
const digestAlert = "digest"

// digestBaselineDays is how many days before a digest the trends are measured
// against.
const digestBaselineDays = 14

// Delivery counts what one call to Deliver did with the notifications it
// built, one per watch and alert.
type Delivery struct {
	// Sent is the number of notifications that went out through at least one
	// channel and are recorded as delivered.
	Sent int
	// Deferred is the number held back by quiet hours or a cooldown. They stay
	// pending and are sent by a later call.
	Deferred int
	// Failed is the number whose sending failed. They stay pending and are
	// tried again by the next call.
	Failed int
	// Blocked is the number that name a channel kind that is not configured.
	// They stay pending, and the channels that are configured are not sent the
	// same notification again; configuring the kind sends the rest.
	Blocked int
}

// group is the pending alerts of one watch and alert name.
type group struct {
	watchID string
	alert   string
	items   []store.PendingAlert
}

// Deliver sends the alerts of the tenant's hits that were not delivered yet.
// For each watch and alert it builds one Notification that holds all the
// articles pending for it, titled "<watch name>: <alert name> (N)", and sends
// it to every channel the alert names. What each channel took is recorded, so a
// retry sends only to the channels that have not: a notification is recorded as
// delivered, and so never sent again, only when every channel it names has it.
//
// The alert's policy decides when: inside its quiet hours (in the Ledger's
// zone) or within its cooldown of the previous delivery of the same alert the
// notification is deferred, not dropped; the articles stay pending and go out
// in the next call that is allowed. The tui channel needs nothing sent. A kind
// with no configured channel is logged once per call and counted in
// Delivery.Blocked; the alert stays pending, so configuring the kind later sends
// what was missed (to that kind only, if the others have it). A pending alert whose watch or alert no longer exists stays
// pending, and one whose article the tenant may not see, or that is gone, is
// recorded as delivered without being sent.
//
// A failing send does not stop the others: it is counted in Delivery.Failed
// and reported in the error, together with store failures.
func (l *Ledger) Deliver(ctx context.Context, tenantID string) (Delivery, error) {
	var d Delivery
	watches, err := l.watches(tenantID)
	if err != nil {
		return d, err
	}
	byID := make(map[string]watch.Watch, len(watches))
	for _, w := range watches {
		byID[w.ID] = w
	}
	ts := l.cfg.Store.Tenant(tenantID)
	pending, err := ts.PendingAlerts(ctx)
	if err != nil {
		return d, fmt.Errorf("ledger: %w", err)
	}

	rd := l.reader(tenantID)
	now := l.cfg.Now()
	warned := map[channel.Kind]bool{}
	var errs []error
	for _, g := range groups(pending) {
		w, ok := byID[g.watchID]
		alert, found := findAlert(w, g.alert)
		if !ok || !found {
			l.cfg.Logger.Debug("ledger: pending alert has no watch or alert definition", "tenant", tenantID, "watch", g.watchID, "alert", g.alert)
			continue
		}

		var gone []store.PendingAlert
		var items []store.PendingAlert
		var articles []model.Article
		var lookup error
		for _, it := range g.items {
			a, err := rd.Get(ctx, it.ArticleID)
			if errors.Is(err, store.ErrNotFound) {
				gone = append(gone, it)
				continue
			}
			if err != nil {
				lookup = fmt.Errorf("ledger: watch %q alert %q: %w", g.watchID, g.alert, err)
				break
			}
			items = append(items, it)
			articles = append(articles, a)
		}
		if lookup != nil {
			d.Failed++
			errs = append(errs, lookup)
			continue
		}
		if len(gone) > 0 {
			if err := ts.MarkDelivered(ctx, gone, now); err != nil {
				errs = append(errs, fmt.Errorf("ledger: %w", err))
				continue
			}
		}
		if len(items) == 0 {
			continue
		}

		required := requiredKinds(alert.Channels)
		if len(required) == 0 { // the TUI shows its hits itself: nothing to send
			if err := ts.MarkDelivered(ctx, items, now); err != nil {
				errs = append(errs, fmt.Errorf("ledger: %w", err))
			}
			continue
		}
		senders, missing := l.route(required, warned, fmt.Sprintf("watch %q alert %q", w.ID, alert.Name))
		if len(senders) > 0 {
			deferred, err := l.held(ctx, ts, w, alert, now)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if deferred {
				d.Deferred++
				continue
			}
		}
		done, err := ts.DeliveredChannels(ctx, w.ID, alert.Name)
		if err != nil {
			errs = append(errs, fmt.Errorf("ledger: %w", err))
			continue
		}
		failed := false
		for _, ch := range senders {
			kind := string(ch.Kind())
			var todo []store.PendingAlert
			var todoArticles []model.Article
			for i, it := range items {
				if !done[it.ArticleID][kind] {
					todo = append(todo, it)
					todoArticles = append(todoArticles, articles[i])
				}
			}
			if len(todo) == 0 {
				continue
			}
			n := alertNotification(tenantID, w, alert, todo, todoArticles)
			if err := ch.Send(ctx, n); err != nil {
				failed = true
				errs = append(errs, fmt.Errorf("ledger: watch %q alert %q: send to %s: %w", w.ID, alert.Name, ch.Kind(), err))
				continue
			}
			if err := ts.MarkSentTo(ctx, todo, kind, now); err != nil {
				failed = true
				errs = append(errs, fmt.Errorf("ledger: %w", err))
				continue
			}
			for _, it := range todo {
				if done[it.ArticleID] == nil {
					done[it.ArticleID] = map[string]bool{}
				}
				done[it.ArticleID][kind] = true
			}
		}
		switch {
		case failed:
			d.Failed++
		case len(missing) > 0:
			d.Blocked++
		}
		if failed || len(missing) > 0 {
			continue
		}
		if err := ts.MarkDelivered(ctx, items, now); err != nil {
			errs = append(errs, fmt.Errorf("ledger: %w", err))
			continue
		}
		d.Sent++
	}
	return d, errors.Join(errs...)
}

// requiredKinds are the channel kinds an alert or digest names that need a
// sender: each once, without the TUI.
func requiredKinds(kinds []channel.Kind) []channel.Kind {
	var out []channel.Kind
	for _, k := range kinds {
		if k != channel.TUI && !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	return out
}

// groups collects the pending alerts by watch and alert name, in the order of
// the oldest hit of each group.
func groups(pending []store.PendingAlert) []group {
	var out []group
	at := map[[2]string]int{}
	for _, p := range pending {
		key := [2]string{p.WatchID, p.Alert}
		i, ok := at[key]
		if !ok {
			i = len(out)
			at[key] = i
			out = append(out, group{watchID: p.WatchID, alert: p.Alert})
		}
		out[i].items = append(out[i].items, p)
	}
	return out
}

// findAlert returns the watch's alert with the name.
func findAlert(w watch.Watch, name string) (watch.Alert, bool) {
	for _, a := range w.Alerts {
		if a.Name == name {
			return a, true
		}
	}
	return watch.Alert{}, false
}

// held reports whether the alert's quiet hours or cooldown keep it from firing
// at now.
func (l *Ledger) held(ctx context.Context, ts store.TenantStore, w watch.Watch, a watch.Alert, now time.Time) (bool, error) {
	p := a.Policy()
	if p.Quiet.Contains(now.In(l.cfg.Zone)) {
		return true, nil
	}
	if p.Cooldown <= 0 {
		return false, nil
	}
	last, ok, err := ts.LastDelivery(ctx, w.ID, a.Name)
	if err != nil {
		return false, fmt.Errorf("ledger: %w", err)
	}
	return ok && now.Sub(last) < p.Cooldown, nil
}

// route returns the channels to send to for the kinds, and the kinds that have
// no configured channel. A missing kind is logged, once per call (warned
// remembers which); what is meant for it stays pending.
func (l *Ledger) route(kinds []channel.Kind, warned map[channel.Kind]bool, what string) (senders []channel.Channel, missing []channel.Kind) {
	for _, k := range kinds {
		ch := l.cfg.Channels[k]
		if ch == nil {
			missing = append(missing, k)
			if !warned[k] {
				warned[k] = true
				l.cfg.Logger.Warn("ledger: no channel configured, the alert stays pending until it is", "channel", string(k), "first_for", what)
			}
			continue
		}
		senders = append(senders, ch)
	}
	return senders, missing
}

// alertNotification builds the one notification for the pending alerts of a
// watch and alert. Its time is that of the newest hit in it, not the clock, so
// a repeated attempt names the same file in the file channel.
func alertNotification(tenantID string, w watch.Watch, a watch.Alert, items []store.PendingAlert, articles []model.Article) channel.Notification {
	var at time.Time
	var body strings.Builder
	for i, it := range items {
		if it.At.After(at) {
			at = it.At
		}
		art := articles[i]
		kind := "-"
		if art.Score.Kind != "" {
			kind = string(art.Score.Kind)
		}
		fmt.Fprintf(&body, "- %s (%s, %s, materiality %d)\n", oneLine(art.Title), art.Domain, kind, art.Score.Materiality)
	}
	return channel.Notification{
		TenantID: tenantID,
		WatchID:  w.ID,
		Alert:    a.Name,
		Title:    fmt.Sprintf("%s: %s (%d)", oneLine(w.Name), oneLine(a.Name), len(articles)),
		Body:     body.String(),
		Link:     "/w/" + w.ID,
		At:       at,
		Articles: articles,
	}
}

// oneLine collapses all white space, so a name or title stays on its line.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// Digests sends the digests that are due and returns how many it sent. A
// watch with a digest cadence of daily or weekly (Mondays) is due when the time
// now, in the Ledger's zone, is at or after the digest time (Digest.At) of a
// day for which no digest of that period was sent: the period is the day
// (YYYY-MM-DD) for daily and the ISO week (YYYY-Www) for weekly. Any other
// cadence sends nothing.
//
// The digest covers the last 24 hours (daily) or 7 days (weekly) up to now,
// drawn from what the watch matches among the articles the tenant may see. It
// is rendered by digest.Text and goes to the channels Digest.Channels names,
// under the same rules as an alert's: tui needs nothing sent, and a kind with
// no configured channel is logged once per call and the period counted as done.
// The period is recorded only when every send succeeded, so a failure is tried
// again by the next call. The time of the notification is the scheduled time of
// the period, so the retry of a file names the same file.
func (l *Ledger) Digests(ctx context.Context, tenantID string) (int, error) {
	watches, err := l.watches(tenantID)
	if err != nil {
		return 0, err
	}
	ts := l.cfg.Store.Tenant(tenantID)
	now := l.cfg.Now()
	local := now.In(l.cfg.Zone)
	warned := map[channel.Kind]bool{}
	sent := 0
	var errs []error
	for _, w := range watches {
		span, ok := digestSpan(w.Digest.Cadence)
		if !ok {
			continue
		}
		scheduled, key, due := digestDue(w.Digest, local, l.cfg.Zone)
		if !due {
			continue
		}
		done, err := ts.DigestSent(ctx, w.ID, key)
		if err != nil {
			errs = append(errs, fmt.Errorf("ledger: %w", err))
			continue
		}
		if done {
			continue
		}

		required := requiredKinds(w.Digest.Channels)
		if len(required) == 0 {
			if err := ts.MarkDigestSent(ctx, w.ID, key, now); err != nil {
				errs = append(errs, fmt.Errorf("ledger: %w", err))
			}
			continue
		}
		senders, missing := l.route(required, warned, fmt.Sprintf("watch %q digest", w.ID))
		part := store.PendingAlert{WatchID: w.ID, ArticleID: "digest:" + key, Alert: digestAlert}
		sentTo, err := ts.DeliveredChannels(ctx, w.ID, digestAlert)
		if err != nil {
			errs = append(errs, fmt.Errorf("ledger: %w", err))
			continue
		}
		var n channel.Notification
		built := false
		failed := false
		for _, ch := range senders {
			kind := string(ch.Kind())
			if sentTo[part.ArticleID][kind] {
				continue
			}
			if !built {
				n, err = l.digestNotification(ctx, tenantID, w, key, scheduled, now, span)
				if err != nil {
					errs = append(errs, err)
					failed = true
					break
				}
				built = true
			}
			if err := ch.Send(ctx, n); err != nil {
				failed = true
				errs = append(errs, fmt.Errorf("ledger: watch %q digest %s: send to %s: %w", w.ID, key, ch.Kind(), err))
				continue
			}
			if err := ts.MarkSentTo(ctx, []store.PendingAlert{part}, kind, now); err != nil {
				failed = true
				errs = append(errs, fmt.Errorf("ledger: %w", err))
			}
		}
		if failed || len(missing) > 0 {
			continue
		}
		if err := ts.MarkDigestSent(ctx, w.ID, key, now); err != nil {
			errs = append(errs, fmt.Errorf("ledger: %w", err))
			continue
		}
		sent++
	}
	return sent, errors.Join(errs...)
}

// digestSpan is how far back a digest of the cadence reaches. The second result
// is false for a cadence that sends no digest.
func digestSpan(cadence string) (time.Duration, bool) {
	switch cadence {
	case "daily":
		return 24 * time.Hour, true
	case "weekly":
		return 7 * 24 * time.Hour, true
	}
	return 0, false
}

// digestDue says whether the digest of d is due at local, which is in zone: the
// scheduled time of its period, the period key, and whether the time has come
// (weekly digests only on Mondays). A digest time that is not HH:MM is never
// due.
func digestDue(d watch.Digest, local time.Time, zone *time.Location) (scheduled time.Time, key string, due bool) {
	t, err := time.Parse("15:04", d.At)
	if err != nil {
		return time.Time{}, "", false
	}
	y, m, day := local.Date()
	scheduled = time.Date(y, m, day, t.Hour(), t.Minute(), 0, 0, zone)
	switch d.Cadence {
	case "daily":
		key = local.Format(time.DateOnly)
	case "weekly":
		if local.Weekday() != time.Monday {
			return time.Time{}, "", false
		}
		isoYear, week := local.ISOWeek()
		key = fmt.Sprintf("%04d-W%02d", isoYear, week)
	default:
		return time.Time{}, "", false
	}
	return scheduled, key, !local.Before(scheduled)
}

// digestNotification builds the digest of w for the span before now.
func (l *Ledger) digestNotification(ctx context.Context, tenantID string, w watch.Watch, key string, scheduled, now time.Time, span time.Duration) (channel.Notification, error) {
	q, err := l.parseQuery(tenantID, w.Query)
	if err != nil {
		return channel.Notification{}, fmt.Errorf("ledger: watch %q: query: %w", w.ID, err)
	}
	rd := l.reader(tenantID)
	from := now.Add(-span)
	window, err := rd.Range(ctx, from, now)
	if err != nil {
		return channel.Notification{}, fmt.Errorf("ledger: watch %q digest: %w", w.ID, err)
	}
	baseline, err := rd.Range(ctx, from.AddDate(0, 0, -digestBaselineDays), from)
	if err != nil {
		return channel.Notification{}, fmt.Errorf("ledger: watch %q digest: %w", w.ID, err)
	}
	opts := l.matchOptions(q)
	window, baseline = matching(w, window, opts), matching(w, baseline, opts)

	threshold := l.cfg.Threshold
	if w.Lens.Threshold > 0 {
		threshold = w.Lens.Threshold
	}
	dg := digest.BuildWindow(from, now, l.cfg.Zone, window, baseline, w.Scope.Tickers, digest.Options{
		Threshold:    threshold,
		BaselineDays: digestBaselineDays,
	})
	var body strings.Builder
	digest.Text(&body, dg, tenantID, l.cfg.Zone, len(window), threshold)
	return channel.Notification{
		TenantID: tenantID,
		WatchID:  w.ID,
		Alert:    digestAlert,
		Title:    fmt.Sprintf("%s: %s digest %s", oneLine(w.Name), w.Digest.Cadence, key),
		Body:     body.String(),
		Link:     "/w/" + w.ID,
		At:       scheduled,
		Articles: dg.Top,
	}, nil
}

// matching returns the articles w matches.
func matching(w watch.Watch, articles []model.Article, o watch.Options) []model.Article {
	var out []model.Article
	for _, a := range articles {
		if w.Match(a, o) {
			out = append(out, a)
		}
	}
	return out
}
