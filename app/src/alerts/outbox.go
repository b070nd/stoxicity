package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/channel"
	"github.com/b070nd/stoxicity/app/src/store"
)

// Outbox is the durable state of a tenant's alerts: the events, the delivery of
// each on each channel, the configuration and the user's answers. Everything
// is an append to the tenant's record store, so a restart loses nothing and
// forgets nothing.
type Outbox struct {
	RS     store.Records
	Tenant string
	Now    func() time.Time
}

const (
	reasonEnded = "the situation ended before this was sent"
	scanKey     = "last"
	kindScan    = "alert.scan"
)

func (o Outbox) now() time.Time {
	if o.Now != nil {
		return o.Now().UTC()
	}
	return time.Now().UTC()
}

// Config returns the stored configuration, or the default when none was stored.
func (o Outbox) Config(ctx context.Context) (Config, bool, error) {
	rec, err := o.RS.Latest(ctx, KindConfig, "default")
	if errors.Is(err, store.ErrNoRecord) {
		return DefaultConfig(), false, nil
	}
	if err != nil {
		return Config{}, false, err
	}
	var c Config
	if err := json.Unmarshal(rec.Body, &c); err != nil {
		return Config{}, false, fmt.Errorf("alerts: the stored configuration does not decode: %w", err)
	}
	if err := c.Validate(); err != nil {
		return Config{}, false, fmt.Errorf("the stored configuration is not valid: %w", err)
	}
	return c, true, nil
}

// SetConfig stores a configuration after checking it.
func (o Outbox) SetConfig(ctx context.Context, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	body, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, _, err = o.RS.Append(ctx, store.RecordInput{Kind: KindConfig, Key: "default", Body: body})
	return err
}

func (o Outbox) events(ctx context.Context) (map[string]Alert, error) {
	recs, err := o.RS.Current(ctx, KindEvent, "")
	if err != nil {
		return nil, err
	}
	out := make(map[string]Alert, len(recs))
	for _, r := range recs {
		var a Alert
		if err := json.Unmarshal(r.Body, &a); err != nil {
			return nil, fmt.Errorf("alert %s does not decode: %w", r.Key, err)
		}
		out[a.Key()] = a
	}
	return out, nil
}

func (o Outbox) deliveries(ctx context.Context) (map[string]Delivery, error) {
	recs, err := o.RS.Current(ctx, KindDelivery, "")
	if err != nil {
		return nil, err
	}
	out := make(map[string]Delivery, len(recs))
	for _, r := range recs {
		var d Delivery
		if err := json.Unmarshal(r.Body, &d); err != nil {
			return nil, fmt.Errorf("delivery %s does not decode: %w", r.Key, err)
		}
		out[d.Key()] = d
	}
	return out, nil
}

func (o Outbox) put(ctx context.Context, d Delivery) error {
	d.At = o.now()
	d.Seq++
	body, err := json.Marshal(d)
	if err != nil {
		return err
	}
	_, _, err = o.RS.Append(ctx, store.RecordInput{Kind: KindDelivery, Key: d.Key(), Body: body})
	return err
}

// Scan is what the producers found: the alerts that hold now, and the rules
// whose producer looked at everything it should. Only for a rule that was
// scanned to the end may an alert that is no longer found be withdrawn, so a
// producer that failed halfway cannot make alerts disappear.
type Scan struct {
	Alerts  []Alert
	Scanned map[Rule]bool
}

type scanRec struct {
	Seq     int       `json:"seq"`
	At      time.Time `json:"at"`
	Scanned []Rule    `json:"scanned"`
	Present []string  `json:"present"`
}

// Raised says what a scan changed.
type Raised struct {
	New        []Alert  // alerts raised for the first time
	Known      int      // alerts already raised, left alone
	Reopened   []string // keys of alerts whose situation came back before they were sent
	Superseded []string // keys of earlier revisions replaced
	Withdrawn  []string // keys of alerts whose situation ended before they were sent
}

func ruleSet(m map[Rule]bool) []Rule {
	var out []Rule
	for r, ok := range m {
		if ok {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Raise records the alerts of a scan that were not raised before, with their
// deliveries, in one transaction: an alert and its outbox entries exist
// together or not at all.
func (o Outbox) Raise(ctx context.Context, cfg Config, sc Scan) (Raised, error) {
	var res Raised
	evs, err := o.events(ctx)
	if err != nil {
		return res, err
	}
	dls, err := o.deliveries(ctx)
	if err != nil {
		return res, err
	}
	now := o.now()
	dirty := map[string]Delivery{}
	set := func(d Delivery) { d.At = now; d.Seq++; dls[d.Key()] = d; dirty[d.Key()] = d }
	var events []store.RecordInput

	cand := append([]Alert(nil), sc.Alerts...)
	sort.Slice(cand, func(i, j int) bool { return cand[i].Key() < cand[j].Key() })
	present := map[string]bool{}
	for _, a := range cand {
		if err := a.Validate(); err != nil {
			return res, err
		}
		if present[a.Key()] {
			continue
		}
		present[a.Key()] = true
		if _, ok := evs[a.Key()]; ok {
			res.Known++
			reopened := false
			for _, d := range sortedDeliveries(dls, a.Key()) {
				if d.State == Withdrawn && d.Reason == reasonEnded {
					d.State, d.Reason, d.Next = Pending, "the situation came back", time.Time{}
					set(d)
					reopened = true
				}
			}
			if reopened {
				res.Reopened = append(res.Reopened, a.Key())
			}
			continue
		}
		if prev, ok := latestOfGroup(evs, a.Group()); ok {
			a.Corrects = prev.Key()
			for _, d := range sortedDeliveries(dls, prev.Key()) {
				if d.State.Waiting() && d.State != Sending {
					d.State, d.Reason, d.Next = Superseded, "replaced by "+a.ID(), time.Time{}
					set(d)
				}
			}
			res.Superseded = append(res.Superseded, prev.Key())
		}
		a.Raised = now
		body, err := json.Marshal(a)
		if err != nil {
			return res, err
		}
		events = append(events, store.RecordInput{Kind: KindEvent, Key: a.Key(), Body: body})
		evs[a.Key()] = a
		res.New = append(res.New, a)
		for _, ch := range cfg.ChannelsFor(a.Rule) {
			set(Delivery{Alert: a.Key(), Channel: ch, Rule: a.Rule, Group: a.Group(), State: Pending})
		}
	}

	keys := make([]string, 0, len(evs))
	for k := range evs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		e := evs[k]
		if !e.Rule.Condition() || !sc.Scanned[e.Rule] || present[k] {
			continue
		}
		withdrawn := false
		for _, d := range sortedDeliveries(dls, k) {
			switch d.State {
			case Pending, Held, Failed, Uncertain:
				d.State, d.Reason, d.Next = Withdrawn, reasonEnded, time.Time{}
				set(d)
				withdrawn = true
			}
		}
		if withdrawn {
			res.Withdrawn = append(res.Withdrawn, k)
		}
	}

	in := events
	dk := make([]string, 0, len(dirty))
	for k := range dirty {
		dk = append(dk, k)
	}
	sort.Strings(dk)
	for _, k := range dk {
		body, err := json.Marshal(dirty[k])
		if err != nil {
			return res, err
		}
		in = append(in, store.RecordInput{Kind: KindDelivery, Key: k, Body: body})
	}
	// what the last scan saw, so the alerts of a situation that has ended can be
	// told from those that still hold
	sr := scanRec{At: now, Scanned: ruleSet(sc.Scanned)}
	for k := range present {
		sr.Present = append(sr.Present, k)
	}
	sort.Strings(sr.Present)
	if last, ok := o.lastScan(ctx); !ok || !sameScan(last, sr) {
		sr.Seq = last.Seq + 1
		body, _ := json.Marshal(sr)
		in = append(in, store.RecordInput{Kind: kindScan, Key: scanKey, Body: body})
	}
	if len(in) > 0 {
		if _, _, err := o.RS.Append(ctx, in...); err != nil {
			return res, err
		}
	}
	return res, nil
}

func sameScan(a, b scanRec) bool {
	if len(a.Scanned) != len(b.Scanned) || len(a.Present) != len(b.Present) {
		return false
	}
	for i := range a.Scanned {
		if a.Scanned[i] != b.Scanned[i] {
			return false
		}
	}
	for i := range a.Present {
		if a.Present[i] != b.Present[i] {
			return false
		}
	}
	return true
}

func (o Outbox) lastScan(ctx context.Context) (scanRec, bool) {
	rec, err := o.RS.Latest(ctx, kindScan, scanKey)
	if err != nil {
		return scanRec{}, false
	}
	var s scanRec
	if json.Unmarshal(rec.Body, &s) != nil {
		return scanRec{}, false
	}
	return s, true
}

func sortedDeliveries(dls map[string]Delivery, alert string) []Delivery {
	var out []Delivery
	for _, d := range dls {
		if d.Alert == alert {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Channel < out[j].Channel })
	return out
}

func latestOfGroup(evs map[string]Alert, group string) (Alert, bool) {
	var best Alert
	found := false
	for _, e := range evs {
		if e.Group() != group {
			continue
		}
		if !found || e.Raised.After(best.Raised) || (e.Raised.Equal(best.Raised) && e.Key() > best.Key()) {
			best, found = e, true
		}
	}
	return best, found
}

// Line is one delivery in a report.
type Line struct {
	Alert   string `json:"alert"`
	ID      string `json:"id"`
	Title   string `json:"title"`
	Channel string `json:"channel"`
	State   string `json:"state"`
	Note    string `json:"note,omitempty"`
}

// Outcome is what a delivery run did.
type Outcome struct {
	Sent      []Line
	Failed    []Line // sent without success this run
	Held      []Line // held by quiet hours
	Waiting   []Line // not due yet, or the channel is not configured
	Gone      []Line // given up after the last attempt; retried only on request
	Recovered []Line // sends of unknown result found at the start, now uncertain
}

// DeliverOptions change a run.
type DeliverOptions struct {
	// Retry sends the deliveries that gave up or are waiting for their next try.
	Retry bool
}

// Deliver sends what is waiting through the channels given. A delivery is
// recorded as sending before the channel is called and as sent only when the
// channel returned no error; whatever stops the program in between leaves it
// "sending", which the next run turns into "uncertain" and repeats with a note.
func (o Outbox) Deliver(ctx context.Context, cfg Config, senders map[channel.Kind]channel.Channel, opt DeliverOptions) (Outcome, error) {
	var out Outcome
	evs, err := o.events(ctx)
	if err != nil {
		return out, err
	}
	dls, err := o.deliveries(ctx)
	if err != nil {
		return out, err
	}
	line := func(d Delivery, note string) Line {
		e := evs[d.Alert]
		return Line{Alert: d.Alert, ID: e.ID(), Title: e.Title, Channel: string(d.Channel), State: string(d.State), Note: note}
	}
	all := make([]Delivery, 0, len(dls))
	for _, d := range dls {
		all = append(all, d)
	}
	sort.Slice(all, func(i, j int) bool {
		a, b := evs[all[i].Alert], evs[all[j].Alert]
		if !a.Raised.Equal(b.Raised) {
			return a.Raised.Before(b.Raised)
		}
		return all[i].Key() < all[j].Key()
	})

	// a send that was started and never finished
	for i, d := range all {
		if d.State != Sending {
			continue
		}
		d.State = Uncertain
		d.Reason = "a send was started and the program stopped before the result was known; it may or may not have arrived"
		if err := o.put(ctx, d); err != nil {
			return out, err
		}
		all[i] = d
		dls[d.Key()] = d
		out.Recovered = append(out.Recovered, line(d, d.Reason))
	}

	for _, d := range all {
		if d.State.Final() {
			continue
		}
		ev, ok := evs[d.Alert]
		if !ok {
			continue
		}
		now := o.now()
		switch d.State {
		case Failed:
			if !opt.Retry {
				if d.Next.IsZero() {
					out.Gone = append(out.Gone, line(d, "gave up after "+fmt.Sprint(d.Attempts)+" attempts; deliver -retry sends it again"))
					continue
				}
				if d.Next.After(now) {
					out.Waiting = append(out.Waiting, line(d, "next try "+d.Next.Format(time.RFC3339)))
					continue
				}
			}
		case Held:
			if !opt.Retry && d.Next.After(now) {
				out.Held = append(out.Held, line(d, "held until "+d.Next.Format(time.RFC3339)))
				continue
			}
		}
		send := senders[d.Channel]
		if send == nil {
			note := "the " + string(d.Channel) + " channel is not configured; this waits and is not counted as sent"
			if d.Reason != note {
				d.Reason = note
				if err := o.put(ctx, d); err != nil {
					return out, err
				}
			}
			out.Waiting = append(out.Waiting, line(d, note))
			continue
		}
		if until, quiet := cfg.Quiet(now); quiet && !cfg.IsCritical(d.Rule) && !opt.Retry {
			if d.State != Held || !d.Next.Equal(until) {
				d.State, d.Next = Held, until
				d.Reason = "quiet hours until " + until.Format(time.RFC3339)
				if err := o.put(ctx, d); err != nil {
					return out, err
				}
			}
			out.Held = append(out.Held, line(d, d.Reason))
			continue
		}

		repeat := d.State == Uncertain
		n := o.notification(ev, correctionOf(evs, dls, ev, d.Channel), repeat)
		d.Attempts++
		d.State, d.Reason, d.Next = Sending, "", time.Time{}
		if err := o.put(ctx, d); err != nil {
			return out, fmt.Errorf("the send was not started, because it could not be recorded first: %w", err)
		}
		serr := send.Send(ctx, n)
		done := context.WithoutCancel(ctx)
		if serr == nil {
			d.State = Sent
			if repeat {
				d.Reason = "sent again after an earlier attempt of unknown result"
			}
			if err := o.put(done, d); err != nil {
				return out, err
			}
			out.Sent = append(out.Sent, line(d, d.Reason))
			continue
		}
		d.State, d.Reason = Failed, short(serr)
		if d.Attempts < cfg.MaxAttempts {
			wait := time.Duration(cfg.RetryMinutes) * time.Minute
			for i := 1; i < d.Attempts && wait < 24*time.Hour; i++ {
				wait *= 2
			}
			if wait > 24*time.Hour {
				wait = 24 * time.Hour
			}
			d.Next = o.now().Add(wait)
		}
		if err := o.put(done, d); err != nil {
			return out, err
		}
		out.Failed = append(out.Failed, line(d, d.Reason))
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
	}
	return out, nil
}

// correctionOf returns the earlier alert this one corrects, when that earlier
// alert, or one before it, reached the channel: only then does the reader have
// something to correct.
func correctionOf(evs map[string]Alert, dls map[string]Delivery, a Alert, ch channel.Kind) *Alert {
	key := a.Corrects
	for i := 0; key != "" && i < 20; i++ {
		prev, ok := evs[key]
		if !ok {
			return nil
		}
		if d, ok := dls[key+"|"+string(ch)]; ok && d.State == Sent {
			return &prev
		}
		key = prev.Corrects
	}
	return nil
}

func (o Outbox) notification(a Alert, corrects *Alert, repeat bool) channel.Notification {
	title, body := a.Title, a.Body
	if corrects != nil {
		title = "Correction: " + title
		body += "\n\nThis replaces the earlier alert " + corrects.ID() + " (" + corrects.Revision + ")."
	}
	if repeat {
		title = "May repeat an earlier message: " + title
		body += "\n\nAn earlier attempt to send this ended without a recorded result, so it may already have reached you."
	}
	return channel.Notification{TenantID: o.Tenant, WatchID: "alerts", Alert: a.Key(), Title: title, Body: body, Link: a.Link, At: a.Raised}
}

func short(err error) string {
	s := strings.TrimSpace(strings.SplitN(err.Error(), "\n", 2)[0])
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// Row is one alert with where it stands.
type Row struct {
	Alert      Alert
	Deliveries []Delivery
	Responses  []Response
	Ended      bool // a condition the last scan did not find
}

// Answered reports whether the user accepted or rejected the alert.
func (r Row) Answered() bool {
	for _, x := range r.Responses {
		if x.Kind == "accepted" || x.Kind == "rejected" {
			return true
		}
	}
	return false
}

// Open reports whether the alert still needs the user: not ended and not answered.
func (r Row) Open() bool { return !r.Ended && !r.Answered() }

// Status lists the alerts, newest first; open ones only unless all is set.
func (o Outbox) Status(ctx context.Context, all bool) ([]Row, error) {
	evs, err := o.events(ctx)
	if err != nil {
		return nil, err
	}
	dls, err := o.deliveries(ctx)
	if err != nil {
		return nil, err
	}
	rr, err := o.RS.All(ctx, KindResponse, "")
	if err != nil {
		return nil, err
	}
	resp := map[string][]Response{}
	for _, r := range rr {
		var x Response
		if err := json.Unmarshal(r.Body, &x); err != nil {
			return nil, fmt.Errorf("answer %s does not decode: %w", r.Key, err)
		}
		resp[x.Alert] = append(resp[x.Alert], x)
	}
	last, haveScan := o.lastScan(ctx)
	present := map[string]bool{}
	scanned := map[Rule]bool{}
	for _, k := range last.Present {
		present[k] = true
	}
	for _, r := range last.Scanned {
		scanned[r] = true
	}
	var rows []Row
	for k, a := range evs {
		row := Row{Alert: a, Deliveries: sortedDeliveries(dls, k), Responses: resp[k]}
		row.Ended = haveScan && a.Rule.Condition() && scanned[a.Rule] && !present[k]
		if all || row.Open() {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].Alert.Raised.Equal(rows[j].Alert.Raised) {
			return rows[i].Alert.Raised.After(rows[j].Alert.Raised)
		}
		return rows[i].Alert.Key() < rows[j].Alert.Key()
	})
	return rows, nil
}

// Find returns the alert named by its short ID or its full key.
func (o Outbox) Find(ctx context.Context, ref string) (Alert, error) {
	evs, err := o.events(ctx)
	if err != nil {
		return Alert{}, err
	}
	if a, ok := evs[ref]; ok {
		return a, nil
	}
	for _, a := range evs {
		if a.ID() == ref {
			return a, nil
		}
	}
	return Alert{}, fmt.Errorf("no alert %q (alerts status -all lists them)", ref)
}

// Respond records the user's answer. Accepting or rejecting also withdraws the
// copies not yet sent: the user has already seen it. Nothing already recorded,
// the alert least of all, is changed.
func (o Outbox) Respond(ctx context.Context, ref, kind, note, by string) (Alert, error) {
	switch kind {
	case "accepted", "rejected", "noted":
	default:
		return Alert{}, errors.New("an answer is accepted, rejected or noted")
	}
	if strings.TrimSpace(by) == "" {
		return Alert{}, errors.New("say who is answering (-by)")
	}
	if kind != "accepted" && strings.TrimSpace(note) == "" {
		return Alert{}, errors.New("a rejection or a note needs its reason (-note)")
	}
	a, err := o.Find(ctx, ref)
	if err != nil {
		return a, err
	}
	now := o.now()
	body, _ := json.Marshal(Response{Alert: a.Key(), Kind: kind, Note: strings.TrimSpace(note), By: strings.TrimSpace(by), At: now})
	in := []store.RecordInput{{Kind: KindResponse, Key: a.Key(), Body: body}}
	if kind != "noted" {
		dls, err := o.deliveries(ctx)
		if err != nil {
			return a, err
		}
		for _, d := range sortedDeliveries(dls, a.Key()) {
			switch d.State {
			case Pending, Held, Failed, Uncertain:
				d.State, d.Next, d.Reason, d.At = Withdrawn, time.Time{}, "answered in the program ("+kind+" by "+strings.TrimSpace(by)+") before it was sent", now
				d.Seq++
				b, _ := json.Marshal(d)
				in = append(in, store.RecordInput{Kind: KindDelivery, Key: d.Key(), Body: b})
			}
		}
	}
	_, _, err = o.RS.Append(ctx, in...)
	return a, err
}
