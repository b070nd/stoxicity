package alerts

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/channel"
	"github.com/b070nd/stoxicity/app/src/store"
)

type fake struct {
	kind channel.Kind
	err  error
	got  []channel.Notification
}

func (f *fake) Kind() channel.Kind { return f.kind }
func (f *fake) Send(_ context.Context, n channel.Notification) error {
	if f.err != nil {
		return f.err
	}
	f.got = append(f.got, n)
	return nil
}

type env struct {
	t   *testing.T
	db  *store.DB
	now time.Time
	o   Outbox
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{t: t, db: db, now: time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)}
	e.o = e.outbox("acme")
	return e
}

func (e *env) outbox(tenant string) Outbox {
	return Outbox{RS: e.db.Records(tenant), Tenant: tenant, Now: func() time.Time { return e.now }}
}

func alert(rule Rule, subject, rev string) Alert {
	return Alert{Rule: rule, Subject: subject, Revision: rev, Title: "Title " + subject + " " + rev, Body: "Body of " + subject + " " + rev}
}

func scan(rules []Rule, as ...Alert) Scan {
	m := map[Rule]bool{}
	for _, r := range rules {
		m[r] = true
	}
	return Scan{Alerts: as, Scanned: m}
}

func senders(fs ...*fake) map[channel.Kind]channel.Channel {
	m := map[channel.Kind]channel.Channel{}
	for _, f := range fs {
		m[f.kind] = f
	}
	return m
}

func (e *env) raise(cfg Config, sc Scan) Raised {
	e.t.Helper()
	r, err := e.o.Raise(context.Background(), cfg, sc)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *env) deliver(cfg Config, s map[channel.Kind]channel.Channel, retry bool) Outcome {
	e.t.Helper()
	out, err := e.o.Deliver(context.Background(), cfg, s, DeliverOptions{Retry: retry})
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *env) state(a Alert, ch channel.Kind) Delivery {
	e.t.Helper()
	dls, err := e.o.deliveries(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	d, ok := dls[a.Key()+"|"+string(ch)]
	if !ok {
		e.t.Fatalf("no delivery of %s on %s", a.Key(), ch)
	}
	return d
}

func TestConfigDefaultsAndValidation(t *testing.T) {
	c := DefaultConfig()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	got := c.ChannelsFor(PlanRevision)
	if len(got) != 2 || got[0] != channel.Feed || got[1] != channel.File {
		t.Fatalf("default channels must be the local ones, got %v", got)
	}
	c.Channels["income_missing"] = []channel.Kind{channel.TUI, channel.Email, channel.Email}
	if got := c.ChannelsFor(IncomeMissing); len(got) != 1 || got[0] != channel.Email {
		t.Errorf("tui is not a delivery and a channel is listed once: %v", got)
	}
	for name, mod := range map[string]func(*Config){
		"one end":      func(c *Config) { c.QuietStart = "22:00" },
		"same ends":    func(c *Config) { c.QuietStart, c.QuietEnd = "22:00", "22:00" },
		"bad time":     func(c *Config) { c.QuietStart, c.QuietEnd = "25:00", "07:00" },
		"bad zone":     func(c *Config) { c.Zone = "Mars/Base" },
		"bad rule":     func(c *Config) { c.Critical = []Rule{"nope"} },
		"bad channel":  func(c *Config) { c.Channels["*"] = []channel.Kind{"pigeon"} },
		"rule name":    func(c *Config) { c.Channels["nope"] = []channel.Kind{channel.File} },
		"attempts":     func(c *Config) { c.MaxAttempts = 0 },
		"review days":  func(c *Config) { c.ReviewDays = 3 },
		"retry minute": func(c *Config) { c.RetryMinutes = 0 },
	} {
		c := DefaultConfig()
		mod(&c)
		if c.Validate() == nil {
			t.Errorf("%s: want a refusal", name)
		}
	}
}

func TestQuietWindow(t *testing.T) {
	c := DefaultConfig()
	c.QuietStart, c.QuietEnd = "22:00", "07:00"
	at := func(h, m int) time.Time { return time.Date(2026, 11, 4, h, m, 0, 0, time.UTC) }
	cases := []struct {
		now   time.Time
		quiet bool
		until time.Time
	}{
		{at(21, 59), false, time.Time{}},
		{at(22, 0), true, time.Date(2026, 11, 5, 7, 0, 0, 0, time.UTC)},
		{at(23, 30), true, time.Date(2026, 11, 5, 7, 0, 0, 0, time.UTC)},
		{at(3, 0), true, at(7, 0)},
		{at(7, 0), false, time.Time{}},
	}
	for _, x := range cases {
		until, q := c.Quiet(x.now)
		if q != x.quiet || !until.Equal(x.until) {
			t.Errorf("%s: quiet %v until %s, want %v %s", x.now.Format("15:04"), q, until, x.quiet, x.until)
		}
	}
	// a zone moves the window
	c.Zone = "Europe/Budapest" // UTC+1 in November
	if _, q := c.Quiet(at(21, 30)); !q {
		t.Error("21:30 UTC is 22:30 in Budapest: quiet")
	}
	if _, q := c.Quiet(at(6, 30)); q {
		t.Error("06:30 UTC is 07:30 in Budapest: not quiet")
	}
	// a daytime window
	d := DefaultConfig()
	d.QuietStart, d.QuietEnd = "09:00", "17:00"
	if _, q := d.Quiet(at(12, 0)); !q {
		t.Error("noon is inside 09:00-17:00")
	}
	if _, q := DefaultConfig().Quiet(at(12, 0)); q {
		t.Error("no window, never quiet")
	}
}

func TestRaiseOnceAndDeliverToLocalChannels(t *testing.T) {
	e := newEnv(t)
	cfg := DefaultConfig()
	a := alert(PlanRevision, "pl_1", "r1")
	r := e.raise(cfg, scan([]Rule{PlanRevision}, a, a)) // twice in one scan
	if len(r.New) != 1 {
		t.Fatalf("one alert, got %d", len(r.New))
	}
	r = e.raise(cfg, scan([]Rule{PlanRevision}, a)) // and again later
	if len(r.New) != 0 || r.Known != 1 {
		t.Fatalf("a known alert is not raised again: %+v", r)
	}
	file, feed := &fake{kind: channel.File}, &fake{kind: channel.Feed}
	out := e.deliver(cfg, senders(file, feed), false)
	if len(out.Sent) != 2 || len(file.got) != 1 || len(feed.got) != 1 {
		t.Fatalf("sent once on each channel: %+v", out)
	}
	n := file.got[0]
	if n.TenantID != "acme" || n.Alert != a.Key() || len(n.Articles) != 0 || !strings.Contains(n.Body, "Body of pl_1") {
		t.Errorf("notification: %+v", n)
	}
	e.deliver(cfg, senders(file, feed), false)
	e.raise(cfg, scan([]Rule{PlanRevision}, a))
	if len(file.got) != 1 {
		t.Errorf("a delivered alert is never delivered again, got %d", len(file.got))
	}
	if d := e.state(a, channel.File); d.State != Sent || d.Attempts != 1 {
		t.Errorf("state %+v", d)
	}
}

func TestFailedSendIsNeverMarkedSent(t *testing.T) {
	e := newEnv(t)
	cfg := DefaultConfig()
	cfg.Channels = map[string][]channel.Kind{"*": {channel.File}}
	cfg.MaxAttempts, cfg.RetryMinutes = 3, 10
	a := alert(OrderUnknown, "or_1", "unknown")
	e.raise(cfg, scan([]Rule{OrderUnknown}, a))
	f := &fake{kind: channel.File, err: errors.New("disk full")}
	out := e.deliver(cfg, senders(f), false)
	if len(out.Failed) != 1 || len(out.Sent) != 0 {
		t.Fatalf("failed: %+v", out)
	}
	d := e.state(a, channel.File)
	if d.State != Failed || d.Attempts != 1 || !d.Next.Equal(e.now.Add(10*time.Minute)) || d.Reason != "disk full" {
		t.Fatalf("first failure: %+v", d)
	}
	// not due yet: no second try
	out = e.deliver(cfg, senders(f), false)
	if len(out.Waiting) != 1 || e.state(a, channel.File).Attempts != 1 {
		t.Fatalf("waits for its time: %+v", out)
	}
	// the wait doubles
	e.now = e.now.Add(11 * time.Minute)
	e.deliver(cfg, senders(f), false)
	d = e.state(a, channel.File)
	if d.Attempts != 2 || !d.Next.Equal(e.now.Add(20*time.Minute)) {
		t.Fatalf("second failure: %+v", d)
	}
	// the last attempt gives up and says so
	e.now = e.now.Add(21 * time.Minute)
	e.deliver(cfg, senders(f), false)
	d = e.state(a, channel.File)
	if d.State != Failed || d.Attempts != 3 || !d.Next.IsZero() {
		t.Fatalf("gave up: %+v", d)
	}
	e.now = e.now.Add(48 * time.Hour)
	out = e.deliver(cfg, senders(f), false)
	if len(out.Gone) != 1 || e.state(a, channel.File).Attempts != 3 {
		t.Fatalf("a given-up delivery waits for the user: %+v", out)
	}
	// the channel works again and the user asks
	ok := &fake{kind: channel.File}
	out = e.deliver(cfg, senders(ok), true)
	if len(out.Sent) != 1 || e.state(a, channel.File).State != Sent || e.state(a, channel.File).Attempts != 4 {
		t.Fatalf("retry: %+v %+v", out, e.state(a, channel.File))
	}
}

func TestUnconfiguredChannelWaitsAndIsNotSent(t *testing.T) {
	e := newEnv(t)
	cfg := DefaultConfig()
	cfg.Channels = map[string][]channel.Kind{"*": {channel.Webhook}}
	a := alert(ReviewDue, "b1/KO", "2026-11-01")
	e.raise(cfg, scan([]Rule{ReviewDue}, a))
	out := e.deliver(cfg, senders(), false)
	if len(out.Waiting) != 1 || len(out.Sent) != 0 {
		t.Fatalf("%+v", out)
	}
	d := e.state(a, channel.Webhook)
	if d.State != Pending || !strings.Contains(d.Reason, "not configured") {
		t.Fatalf("%+v", d)
	}
	e.deliver(cfg, senders(), false)
	rows, _ := e.o.Status(context.Background(), false)
	if len(rows) != 1 || !rows[0].Open() {
		t.Errorf("an alert nobody received stays open: %+v", rows)
	}
	w := &fake{kind: channel.Webhook}
	e.deliver(cfg, senders(w), false)
	if len(w.got) != 1 || e.state(a, channel.Webhook).State != Sent {
		t.Errorf("sent once the channel exists")
	}
}

func TestRestartTurnsASendInFlightIntoUncertain(t *testing.T) {
	e := newEnv(t)
	cfg := DefaultConfig()
	cfg.Channels = map[string][]channel.Kind{"*": {channel.File}}
	a := alert(IncomeMissing, "b1/dv_1", "2026-11-03")
	e.raise(cfg, scan([]Rule{IncomeMissing}, a))
	// the program stopped after recording the send and before the result
	d := e.state(a, channel.File)
	d.State, d.Attempts = Sending, 1
	if err := e.o.put(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	// a new process: a fresh outbox over the same store
	e.o = e.outbox("acme")
	f := &fake{kind: channel.File}
	out := e.deliver(cfg, senders(f), false)
	if len(out.Recovered) != 1 || len(out.Sent) != 1 {
		t.Fatalf("%+v", out)
	}
	if !strings.HasPrefix(f.got[0].Title, "May repeat an earlier message: ") || !strings.Contains(f.got[0].Body, "may already have reached you") {
		t.Errorf("the repeat is flagged: %q", f.got[0].Title)
	}
	got := e.state(a, channel.File)
	if got.State != Sent || got.Attempts != 2 || !strings.Contains(got.Reason, "unknown result") {
		t.Errorf("%+v", got)
	}
	// the notification time is the alert's, so a file channel overwrites its own file
	if !f.got[0].At.Equal(e.now) {
		t.Errorf("At %s", f.got[0].At)
	}
}

func TestUncertainStaysUncertainWhenTheRetryFails(t *testing.T) {
	e := newEnv(t)
	cfg := DefaultConfig()
	cfg.Channels = map[string][]channel.Kind{"*": {channel.File}}
	a := alert(OrderUnknown, "or_9", "unknown")
	e.raise(cfg, scan([]Rule{OrderUnknown}, a))
	d := e.state(a, channel.File)
	d.State = Sending
	e.o.put(context.Background(), d)
	e.deliver(cfg, senders(&fake{kind: channel.File, err: errors.New("down")}), false)
	if got := e.state(a, channel.File); got.State == Sent {
		t.Fatalf("never marked sent without proof: %+v", got)
	}
}

func TestQuietHoursHoldAndCriticalGoesThrough(t *testing.T) {
	e := newEnv(t)
	cfg := DefaultConfig()
	cfg.Channels = map[string][]channel.Kind{"*": {channel.File}}
	cfg.QuietStart, cfg.QuietEnd = "22:00", "07:00"
	cfg.Critical = []Rule{OrderUnknown}
	e.now = time.Date(2026, 11, 4, 23, 0, 0, 0, time.UTC)
	plan1 := alert(PlanRevision, "pl_1", "r1")
	crit := alert(OrderUnknown, "or_1", "unknown")
	crit.Critical = true
	e.raise(cfg, scan([]Rule{PlanRevision, OrderUnknown}, plan1, crit))
	f := &fake{kind: channel.File}
	out := e.deliver(cfg, senders(f), false)
	if len(out.Held) != 1 || len(out.Sent) != 1 || len(f.got) != 1 || f.got[0].Alert != crit.Key() {
		t.Fatalf("only the critical alert goes through quiet hours: %+v", out)
	}
	h := e.state(plan1, channel.File)
	if h.State != Held || !h.Next.Equal(time.Date(2026, 11, 5, 7, 0, 0, 0, time.UTC)) {
		t.Fatalf("%+v", h)
	}
	// still quiet: nothing more, and no needless records
	e.now = e.now.Add(2 * time.Hour)
	hist1, _ := e.o.RS.History(context.Background(), KindDelivery, plan1.Key()+"|file")
	e.deliver(cfg, senders(f), false)
	hist2, _ := e.o.RS.History(context.Background(), KindDelivery, plan1.Key()+"|file")
	if len(f.got) != 1 || len(hist2) != len(hist1) {
		t.Errorf("held alerts stay as they are: %d sent, %d -> %d records", len(f.got), len(hist1), len(hist2))
	}
	// quiet hours end
	e.now = time.Date(2026, 11, 5, 7, 0, 0, 0, time.UTC)
	out = e.deliver(cfg, senders(f), false)
	if len(out.Sent) != 1 || len(f.got) != 2 || e.state(plan1, channel.File).State != Sent {
		t.Fatalf("released: %+v", out)
	}
}

func TestNewRevisionReplacesAnUndeliveredOne(t *testing.T) {
	e := newEnv(t)
	cfg := DefaultConfig()
	cfg.Channels = map[string][]channel.Kind{"*": {channel.File}}
	r1, r2 := alert(PlanRevision, "pl_1", "r1"), alert(PlanRevision, "pl_1", "r2")
	e.raise(cfg, scan([]Rule{PlanRevision}, r1))
	e.now = e.now.Add(time.Hour)
	res := e.raise(cfg, scan([]Rule{PlanRevision}, r1, r2))
	if len(res.New) != 1 || res.New[0].Corrects != r1.Key() || len(res.Superseded) != 1 {
		t.Fatalf("%+v", res)
	}
	f := &fake{kind: channel.File}
	e.deliver(cfg, senders(f), false)
	if len(f.got) != 1 || f.got[0].Alert != r2.Key() || strings.HasPrefix(f.got[0].Title, "Correction") {
		t.Fatalf("only the newest goes out, and it is no correction of something nobody saw: %+v", f.got)
	}
	if e.state(r1, channel.File).State != Superseded {
		t.Errorf("%+v", e.state(r1, channel.File))
	}
}

func TestCorrectionAfterDeliveryIsSentAsACorrection(t *testing.T) {
	e := newEnv(t)
	cfg := DefaultConfig()
	cfg.Channels = map[string][]channel.Kind{"*": {channel.File}}
	a1, a2, a3 := alert(IncomeDifference, "b1/dv_1", "e1"), alert(IncomeDifference, "b1/dv_1", "e2"), alert(IncomeDifference, "b1/dv_1", "e3")
	f := &fake{kind: channel.File}
	e.raise(cfg, scan([]Rule{IncomeDifference}, a1))
	e.deliver(cfg, senders(f), false)
	e.now = e.now.Add(time.Hour)
	e.raise(cfg, scan([]Rule{IncomeDifference}, a2))
	e.now = e.now.Add(time.Hour)
	e.raise(cfg, scan([]Rule{IncomeDifference}, a3)) // a2 never left: a3 replaces it
	e.deliver(cfg, senders(f), false)
	if len(f.got) != 2 {
		t.Fatalf("%d", len(f.got))
	}
	n := f.got[1]
	if n.Alert != a3.Key() || !strings.HasPrefix(n.Title, "Correction: ") || !strings.Contains(n.Body, a1.ID()) {
		t.Errorf("a3 corrects what the reader saw, a1: %q %q", n.Title, n.Body)
	}
}

func TestEndedSituationWithdrawsOnlyWhenTheProducerFinished(t *testing.T) {
	e := newEnv(t)
	cfg := DefaultConfig()
	cfg.Channels = map[string][]channel.Kind{"*": {channel.File}}
	a := alert(BrokerException, "b1/cash", "1")
	e.raise(cfg, scan([]Rule{BrokerException}, a))
	// a producer that did not finish says nothing about the alert
	res := e.raise(cfg, Scan{Scanned: map[Rule]bool{}})
	if len(res.Withdrawn) != 0 || e.state(a, channel.File).State != Pending {
		t.Fatalf("%+v", res)
	}
	// a producer that finished and did not find it
	res = e.raise(cfg, scan([]Rule{BrokerException}))
	if len(res.Withdrawn) != 1 || e.state(a, channel.File).State != Withdrawn {
		t.Fatalf("%+v", res)
	}
	f := &fake{kind: channel.File}
	e.deliver(cfg, senders(f), false)
	if len(f.got) != 0 {
		t.Error("a situation that ended before delivery is not announced")
	}
	rows, _ := e.o.Status(context.Background(), false)
	if len(rows) != 0 {
		t.Errorf("an ended alert is not open: %+v", rows)
	}
	rows, _ = e.o.Status(context.Background(), true)
	if len(rows) != 1 || !rows[0].Ended {
		t.Errorf("but it is still on record: %+v", rows)
	}
	// it comes back before anyone was told
	res = e.raise(cfg, scan([]Rule{BrokerException}, a))
	if len(res.Reopened) != 1 || e.state(a, channel.File).State != Pending {
		t.Fatalf("%+v", res)
	}
	// an event that is a fact stays a fact
	p := alert(PlanRevision, "pl_1", "r1")
	e.raise(cfg, scan([]Rule{PlanRevision, BrokerException}, p, a))
	e.raise(cfg, scan([]Rule{PlanRevision, BrokerException}, a))
	if e.state(p, channel.File).State != Pending {
		t.Error("a plan revision is not withdrawn because it was not listed")
	}
}

func TestSentAlertsAreNotWithdrawn(t *testing.T) {
	e := newEnv(t)
	cfg := DefaultConfig()
	cfg.Channels = map[string][]channel.Kind{"*": {channel.File}}
	a := alert(IncomeMissing, "b1/dv_2", "2026-11-03")
	e.raise(cfg, scan([]Rule{IncomeMissing}, a))
	e.deliver(cfg, senders(&fake{kind: channel.File}), false)
	e.raise(cfg, scan([]Rule{IncomeMissing}))
	if e.state(a, channel.File).State != Sent {
		t.Error("what was sent was sent")
	}
}

func TestAnswerIsAppendedAndNeverRewritesTheAlert(t *testing.T) {
	e := newEnv(t)
	cfg := DefaultConfig()
	cfg.Channels = map[string][]channel.Kind{"*": {channel.File}}
	a := alert(PlanRevision, "pl_1", "r1")
	e.raise(cfg, scan([]Rule{PlanRevision}, a))
	ctx := context.Background()
	if _, err := e.o.Respond(ctx, a.ID(), "rejected", "", "me"); err == nil {
		t.Error("a rejection needs its reason")
	}
	if _, err := e.o.Respond(ctx, a.ID(), "noted", "looks fine", ""); err == nil {
		t.Error("an answer needs a name")
	}
	if _, err := e.o.Respond(ctx, "al_nothing", "accepted", "", "me"); err == nil {
		t.Error("an unknown alert")
	}
	if _, err := e.o.Respond(ctx, a.ID(), "noted", "waiting for the dividend", "me"); err != nil {
		t.Fatal(err)
	}
	if e.state(a, channel.File).State != Pending {
		t.Error("a note does not stop the delivery")
	}
	if _, err := e.o.Respond(ctx, a.Key(), "rejected", "no thanks", "me"); err != nil {
		t.Fatal(err)
	}
	d := e.state(a, channel.File)
	if d.State != Withdrawn || !strings.Contains(d.Reason, "answered in the program") {
		t.Errorf("%+v", d)
	}
	f := &fake{kind: channel.File}
	e.deliver(cfg, senders(f), false)
	if len(f.got) != 0 {
		t.Error("an answered alert is not sent")
	}
	hist, _ := e.o.RS.History(ctx, KindEvent, a.Key())
	if len(hist) != 1 {
		t.Errorf("the alert is never rewritten: %d records", len(hist))
	}
	rows, _ := e.o.Status(ctx, true)
	if len(rows) != 1 || len(rows[0].Responses) != 2 || !rows[0].Answered() {
		t.Errorf("%+v", rows)
	}
	if open, _ := e.o.Status(ctx, false); len(open) != 0 {
		t.Error("an answered alert is not open")
	}
}

func TestTenantsDoNotShareAlertsOrDeliveries(t *testing.T) {
	e := newEnv(t)
	cfg := DefaultConfig()
	cfg.Channels = map[string][]channel.Kind{"*": {channel.File}}
	other := e.outbox("globex")
	a := alert(PlanRevision, "pl_1", "r1")
	e.raise(cfg, scan([]Rule{PlanRevision}, a))
	if _, err := other.Raise(context.Background(), cfg, scan([]Rule{PlanRevision})); err != nil {
		t.Fatal(err)
	}
	f := &fake{kind: channel.File}
	out, err := other.Deliver(context.Background(), cfg, senders(f), DeliverOptions{})
	if err != nil || len(out.Sent) != 0 || len(f.got) != 0 {
		t.Fatalf("another tenant sees nothing: %+v %v", out, err)
	}
	e.deliver(cfg, senders(f), false)
	if len(f.got) != 1 || f.got[0].TenantID != "acme" {
		t.Errorf("%+v", f.got)
	}
}

func TestConfigRoundTripAndRefusal(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c, stored, err := e.o.Config(ctx)
	if err != nil || stored || c.Zone != "UTC" {
		t.Fatalf("%+v %v %v", c, stored, err)
	}
	c.QuietStart, c.QuietEnd, c.Zone = "22:00", "07:00", "Europe/Budapest"
	c.Critical = []Rule{OrderUnknown, BrokerException}
	if err := e.o.SetConfig(ctx, c); err != nil {
		t.Fatal(err)
	}
	got, stored, err := e.o.Config(ctx)
	if err != nil || !stored || got.QuietStart != "22:00" || len(got.Critical) != 2 {
		t.Fatalf("%+v %v %v", got, stored, err)
	}
	c.MaxAttempts = 0
	if e.o.SetConfig(ctx, c) == nil {
		t.Error("an invalid configuration is not stored")
	}
}

func TestAlertValidation(t *testing.T) {
	for _, a := range []Alert{
		{Rule: "x", Subject: "s", Revision: "r", Title: "t"},
		{Rule: PlanRevision, Subject: "", Revision: "r", Title: "t"},
		{Rule: PlanRevision, Subject: "a|b", Revision: "r", Title: "t"},
		{Rule: PlanRevision, Subject: "s", Revision: "r"},
		{Rule: PlanRevision, Subject: strings.Repeat("x", 250), Revision: "r", Title: "t"},
	} {
		if a.Validate() == nil {
			t.Errorf("%+v should be refused", a)
		}
	}
	if err := alert(PlanRevision, "s", "r").Validate(); err != nil {
		t.Error(err)
	}
}

// The store leaves a record alone whose kind, key and content it already holds.
// A state that comes back (withdrawn, reopened, withdrawn again, all at one
// instant) must still be recorded as the latest.
func TestAStateThatComesBackIsRecordedAgain(t *testing.T) {
	e := newEnv(t)
	cfg := DefaultConfig()
	cfg.Channels = map[string][]channel.Kind{"*": {channel.File}}
	a := alert(BrokerException, "b1/x", "e0")
	for i := 0; i < 3; i++ {
		e.raise(cfg, scan([]Rule{BrokerException}, a))
		if got := e.state(a, channel.File).State; got != Pending {
			t.Fatalf("round %d: %s, want pending", i, got)
		}
		e.raise(cfg, scan([]Rule{BrokerException}))
		if got := e.state(a, channel.File).State; got != Withdrawn {
			t.Fatalf("round %d: %s, want withdrawn", i, got)
		}
	}
	rows, _ := e.o.Status(context.Background(), true)
	if len(rows) != 1 || !rows[0].Ended {
		t.Errorf("the last scan did not find it: %+v", rows)
	}
}

// A rule listed as critical is not enough: the alert itself must be about
// unresolved live exposure. An order of a paper book, or a difference still
// explained by pending activity, waits for the quiet hours to end.
func TestAListedRuleGoesThroughQuietHoursOnlyWhenTheAlertIsCritical(t *testing.T) {
	e := newEnv(t)
	cfg := DefaultConfig()
	cfg.Channels = map[string][]channel.Kind{"*": {channel.File}}
	cfg.QuietStart, cfg.QuietEnd = "22:00", "07:00"
	e.now = time.Date(2026, 11, 4, 23, 0, 0, 0, time.UTC)
	paper := alert(BrokerException, "paper/x", "e0")
	live := alert(BrokerException, "live/y", "e0.urgent")
	live.Critical = true
	e.raise(cfg, scan([]Rule{BrokerException}, paper, live))
	f := &fake{kind: channel.File}
	out := e.deliver(cfg, senders(f), false)
	if len(out.Sent) != 1 || len(out.Held) != 1 || f.got[0].Alert != live.Key() {
		t.Fatalf("%+v", out)
	}
}

// A critical alert is not repeated without end and not at all once answered: it
// is sent again only after the configured wait, and each time says it is a reminder.
func TestACriticalAlertIsRemindedOnTheScheduleUntilAnswered(t *testing.T) {
	e := newEnv(t)
	cfg := DefaultConfig()
	cfg.Channels = map[string][]channel.Kind{"*": {channel.File}}
	cfg.RepeatHours = 24
	a := alert(OrderUnknown, "or_7", "unknown")
	a.Critical = true
	sc := scan([]Rule{OrderUnknown}, a)
	e.raise(cfg, sc)
	f := &fake{kind: channel.File}
	if out := e.deliver(cfg, senders(f), false); len(out.Sent) != 1 {
		t.Fatalf("%+v", out)
	}
	// the same scan again, a day less a minute later: nothing
	e.now = e.now.Add(24*time.Hour - time.Minute)
	if r := e.raise(cfg, sc); len(r.Reminded) != 0 {
		t.Fatalf("too early: %+v", r)
	}
	e.now = e.now.Add(2 * time.Minute)
	if r := e.raise(cfg, sc); len(r.Reminded) != 1 {
		t.Fatalf("%+v", r)
	}
	e.deliver(cfg, senders(f), false)
	if len(f.got) != 2 || !strings.HasPrefix(f.got[1].Title, "Reminder 1, not yet answered:") {
		t.Fatalf("%+v", f.got)
	}
	// the same scan within the next day does not remind again
	e.now = e.now.Add(time.Hour)
	if r := e.raise(cfg, sc); len(r.Reminded) != 0 {
		t.Fatalf("reminded twice in a day: %+v", r)
	}
	// an answer ends the reminders
	if _, err := e.o.Respond(context.Background(), a.ID(), "accepted", "", "me"); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(48 * time.Hour)
	if r := e.raise(cfg, sc); len(r.Reminded) != 0 {
		t.Fatalf("reminded after an answer: %+v", r)
	}
	// no schedule, no reminder
	cfg.RepeatHours = 0
	b := alert(OrderUnknown, "or_8", "unknown")
	b.Critical = true
	e.raise(cfg, scan([]Rule{OrderUnknown}, b))
	e.deliver(cfg, senders(f), false)
	e.now = e.now.Add(100 * time.Hour)
	if r := e.raise(cfg, scan([]Rule{OrderUnknown}, b)); len(r.Reminded) != 0 {
		t.Fatalf("%+v", r)
	}
}

// When a difference that was only watched becomes critical, the new revision
// says it is now urgent and that nothing else about it is new.
func TestAnEscalatedRevisionSaysItIsNowUrgent(t *testing.T) {
	e := newEnv(t)
	cfg := DefaultConfig()
	cfg.Channels = map[string][]channel.Kind{"*": {channel.File}}
	watched := alert(BrokerException, "live/y", "e0")
	e.raise(cfg, scan([]Rule{BrokerException}, watched))
	f := &fake{kind: channel.File}
	e.deliver(cfg, senders(f), false)
	urgent := alert(BrokerException, "live/y", "e0.urgent")
	urgent.Critical = true
	e.raise(cfg, scan([]Rule{BrokerException}, urgent))
	e.deliver(cfg, senders(f), false)
	if len(f.got) != 2 || !strings.HasPrefix(f.got[1].Title, "Now urgent:") || !strings.Contains(f.got[1].Body, "which was not urgent") {
		t.Fatalf("%+v", f.got)
	}
}

func TestEscalationSettingsHaveDefaultsAndLimits(t *testing.T) {
	c := DefaultConfig()
	if c.Syncs() != 2 || c.Minutes() != 30 || c.Tolerance().String() != "1" || c.RepeatHours != 24 || !c.IsCritical(OrderUnknown) || !c.IsCritical(BrokerException) {
		t.Fatalf("%+v", c)
	}
	for _, bad := range []func(*Config){
		func(c *Config) { c.RepeatHours = -1 },
		func(c *Config) { c.BrokerSyncs = 21 },
		func(c *Config) { c.BrokerMinutes = 2000 },
		func(c *Config) { c.CashTolerance = "abc" },
		func(c *Config) { c.CashTolerance = "-1" },
	} {
		x := DefaultConfig()
		bad(&x)
		if x.Validate() == nil {
			t.Errorf("accepted %+v", x)
		}
	}
	// a configuration stored before these settings existed still validates and means the defaults
	old := Config{Zone: "UTC", Critical: []Rule{OrderUnknown}, Channels: map[string][]channel.Kind{"*": {channel.File}}, MaxAttempts: 5, RetryMinutes: 15, DividendDays: 7, ReviewDays: 91}
	if err := old.Validate(); err != nil || old.Syncs() != 2 || old.RepeatHours != 0 {
		t.Fatalf("%v %+v", err, old)
	}
}
