package scheduler

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/feeds"
	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/model"
)

var errBoom = errors.New("boom")

// articles returns a result with one article and the given ETag as new State.
func articles(etag string) func(context.Context, model.Source, feeds.State) (feeds.Result, error) {
	return func(context.Context, model.Source, feeds.State) (feeds.Result, error) {
		return feeds.Result{Articles: []model.Article{{}}, State: feeds.State{ETag: etag}}, nil
	}
}

func failWith(err error) func(context.Context, model.Source, feeds.State) (feeds.Result, error) {
	return func(context.Context, model.Source, feeds.State) (feeds.Result, error) {
		return feeds.Result{}, err
	}
}

func TestNewValidates(t *testing.T) {
	good := Options{
		Sources:  []model.Source{source("a")},
		Fetchers: map[model.Kind]feeds.Fetcher{model.KindRSS: &fakeFetcher{}},
		Sink:     &fakeSink{},
		States:   newFakeStore(),
	}
	disabled := source("off")
	disabled.Enabled = false
	disabled.Kind = model.KindAPI

	tests := []struct {
		name    string
		mod     func(*Options)
		wantErr string
	}{
		{"valid", func(*Options) {}, ""},
		{"no sources", func(o *Options) { o.Sources = nil; o.Fetchers = nil }, ""},
		{"disabled source needs no fetcher", func(o *Options) { o.Sources = append(o.Sources, disabled) }, ""},
		{"missing fetcher names source", func(o *Options) {
			s := source("edgar")
			s.Kind = model.KindFilings
			o.Sources = append(o.Sources, s)
		}, `source "edgar"`},
		{"nil fetcher value", func(o *Options) { o.Fetchers = map[model.Kind]feeds.Fetcher{model.KindRSS: nil} }, `source "a"`},
		{"no sink", func(o *Options) { o.Sink = nil }, "Sink"},
		{"no store", func(o *Options) { o.States = nil }, "States"},
		{"duplicate id", func(o *Options) { o.Sources = append(o.Sources, source("a")) }, "twice"},
		{"empty id", func(o *Options) { o.Sources = append(o.Sources, source("")) }, "no id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := good
			tt.mod(&o)
			p, err := New(o)
			if tt.wantErr == "" {
				if err != nil || p == nil {
					t.Fatalf("New = %v, %v; want success", p, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) || !strings.HasPrefix(err.Error(), "scheduler: ") {
				t.Fatalf("New error = %v; want scheduler error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestRunWithNoSources(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.start()
	h.sched.RefreshNow("")
	h.stop()
	if got := h.sched.Health(); len(got) != 0 {
		t.Fatalf("Health = %v, want none", got)
	}
}

func TestRunTwiceFails(t *testing.T) {
	h := newHarness(t, sources("a"), nil)
	h.start()
	if err := h.sched.Run(context.Background()); err == nil {
		t.Fatal("second Run succeeded")
	}
	h.stop()
}

func TestFirstPollOrderAndJitter(t *testing.T) {
	h := newHarness(t, sources("a", "b", "c"), nil)
	// Jitter draws are made in ID order: a gets 0.9, b 0.1, c 0.5 of 10% of 10m.
	h.rnd.queued = []float64{0.9, 0.1, 0.5}
	h.start()

	want := map[string]time.Duration{"a": 54 * time.Second, "b": 6 * time.Second, "c": 30 * time.Second}
	for id, d := range want {
		if got := h.untilNextDue(id); got != d {
			t.Errorf("%s first due in %v, want %v", id, got, d)
		}
		if got := h.untilNextDue(id); got >= time.Minute {
			t.Errorf("%s jitter %v is not below 10%% of the interval", id, got)
		}
	}
	if h.fetch.calls() != 0 {
		t.Fatal("polled before any source was due")
	}

	h.clock.Advance(time.Minute)
	waitFor(t, "three polls", func() bool { return h.fetch.calls() == 3 })
	if got, want := fmt.Sprint(h.fetch.callOrder()), "[b c a]"; got != want {
		t.Fatalf("poll order = %s, want %s", got, want)
	}
}

func TestStateSavedOnlyAfterIngest(t *testing.T) {
	h := newHarness(t, sources("a"), nil)
	h.fetch.fn = articles("v1")
	gate := make(chan struct{})
	entered := make(chan struct{})
	h.sink.fn = func(context.Context, model.Source, []model.Article) (int, error) {
		close(entered)
		<-gate
		return 2, nil
	}
	h.start()
	<-entered
	settle()
	if n := h.store.saveCount(); n != 0 {
		t.Fatalf("state saved %d times while Ingest was still running", n)
	}
	close(gate)
	ev := h.event()
	if ev.SourceID != "a" || ev.New != 2 || ev.Err != nil {
		t.Fatalf("event = %+v, want a with 2 new", ev)
	}
	got := h.store.get("a")
	if got.state.ETag != "v1" {
		t.Fatalf("saved ETag = %q, want v1", got.state.ETag)
	}
	if !got.health.LastOK.Equal(h.clock.Now()) || got.health.Level != LevelGreen {
		t.Fatalf("saved health = %+v, want LastOK now and GREEN", got.health)
	}
}

func TestIngestErrorKeepsOldState(t *testing.T) {
	h := newHarness(t, sources("a"), nil)
	h.store.put("a", feeds.State{ETag: "old"}, Health{})
	h.fetch.fn = articles("new")
	h.sink.fn = func(context.Context, model.Source, []model.Article) (int, error) { return 0, errBoom }
	h.start()
	ev := h.event()
	if !errors.Is(ev.Err, errBoom) || ev.New != 0 {
		t.Fatalf("event = %+v, want errBoom and 0 new", ev)
	}
	if got := h.store.get("a"); got.state.ETag != "old" || got.health.Failures != 1 {
		t.Fatalf("saved = %+v, want old state and one failure", got)
	}

	// The retry gets the old State again.
	h.clock.Advance(h.untilNextDue("a"))
	h.event()
	if prevs := h.fetch.prevs; len(prevs) != 2 || prevs[1].ETag != "old" {
		t.Fatalf("fetch states = %+v, want old twice", prevs)
	}
}

func TestNotModifiedAndEmptyAreHealthy(t *testing.T) {
	tests := []struct {
		name string
		res  feeds.Result
	}{
		{"not modified", feeds.Result{NotModified: true, State: feeds.State{ETag: "e"}}},
		{"no articles", feeds.Result{State: feeds.State{ETag: "e"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, sources("a"), nil)
			h.fetch.fn = func(context.Context, model.Source, feeds.State) (feeds.Result, error) { return tt.res, nil }
			h.start()
			if ev := h.event(); ev.Err != nil || ev.New != 0 {
				t.Fatalf("event = %+v", ev)
			}
			if h.sink.calls() != 0 {
				t.Fatal("Ingest called without articles")
			}
			got := h.store.get("a")
			if got.state.ETag != "e" || got.health.Failures != 0 || got.health.Level != LevelGreen {
				t.Fatalf("saved = %+v, want ETag kept and healthy", got)
			}
		})
	}
}

func TestSuccessSchedulesNextPoll(t *testing.T) {
	h := newHarness(t, sources("a"), nil)
	h.rnd.queued = []float64{0, 0.5} // no first-poll jitter, then half of the 5% success jitter
	h.start()
	h.event()
	want := 10*time.Minute + 15*time.Second
	if got := h.untilNextDue("a"); got != want {
		t.Fatalf("next due in %v, want %v", got, want)
	}
}

func TestBackoffGrowthAndCap(t *testing.T) {
	h := newHarness(t, []model.Source{{ID: "a", Kind: model.KindRSS, Enabled: true, Interval: "4m"}},
		func(o *Options) { o.MaxBackoff = 6 * time.Minute })
	h.rnd.queued = []float64{0}
	h.rnd.def = 0.5 // no backoff jitter
	h.fetch.fn = failWith(errBoom)
	h.start()

	// Base is max(1m, 4m/4) = 1m: 1, 2, 4, then capped at 6; the fifth failure
	// opens the circuit, which probes once per MaxBackoff.
	want := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 6 * time.Minute, 6 * time.Minute}
	for i, w := range want {
		if ev := h.event(); !errors.Is(ev.Err, errBoom) {
			t.Fatalf("poll %d event = %+v", i+1, ev)
		}
		got := h.untilNextDue("a")
		if got != w {
			t.Fatalf("after failure %d next due in %v, want %v", i+1, got, w)
		}
		if f := h.health("a").Failures; f != i+1 {
			t.Fatalf("failures = %d, want %d", f, i+1)
		}
		h.clock.Advance(got)
	}
}

func TestBackoffMath(t *testing.T) {
	p := &Poller{opts: Options{MaxBackoff: time.Hour}}
	tests := []struct {
		name     string
		failures int
		interval time.Duration
		rand     float64
		want     time.Duration
	}{
		{"first, no jitter", 1, 40 * time.Minute, 0.5, 10 * time.Minute},
		{"minimum base is a minute", 1, time.Minute, 0.5, time.Minute},
		{"doubles", 3, 40 * time.Minute, 0.5, 40 * time.Minute},
		{"low jitter is minus 20%", 1, 40 * time.Minute, 0, 8 * time.Minute},
		{"high jitter is plus 20%", 1, 40 * time.Minute, 1, 12 * time.Minute},
		{"capped", 10, 40 * time.Minute, 0.5, time.Hour},
		{"jitter never exceeds the cap", 10, 40 * time.Minute, 1, time.Hour},
		{"huge failure count does not overflow", 500, 40 * time.Minute, 0.5, time.Hour},
		{"base above cap", 1, 10 * time.Hour, 0.5, time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p.opts.Rand = func() float64 { return tt.rand }
			if got := p.backoff(tt.failures, tt.interval); got != tt.want {
				t.Fatalf("backoff = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFailureClasses(t *testing.T) {
	// Interval 10m: base 2.5m; rand 0.5 means no jitter.
	tests := []struct {
		name string
		err  error
		want time.Duration
	}{
		{"retryable", &httpx.Error{Class: httpx.Retryable}, 150 * time.Second},
		{"plain error", errBoom, 150 * time.Second},
		{"throttled, long retry-after", &httpx.Error{Class: httpx.Throttled, RetryAfter: 30 * time.Minute}, 30 * time.Minute},
		{"throttled, short retry-after", &httpx.Error{Class: httpx.Throttled, RetryAfter: 10 * time.Second}, 150 * time.Second},
		{"throttled, wrapped", fmt.Errorf("fetch: %w", &httpx.Error{Class: httpx.Throttled, RetryAfter: 20 * time.Minute}), 20 * time.Minute},
		{"blocked waits an hour", &httpx.Error{Class: httpx.Blocked}, time.Hour},
		{"blocked, wrapped", fmt.Errorf("fetch: %w", &httpx.Error{Class: httpx.Blocked}), time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, sources("a"), nil)
			h.rnd.queued = []float64{0}
			h.rnd.def = 0.5
			h.fetch.fn = failWith(tt.err)
			h.start()
			h.event()
			if got := h.untilNextDue("a"); got != tt.want {
				t.Fatalf("next due in %v, want %v", got, tt.want)
			}
			if hh := h.health("a"); hh.Failures != 1 || hh.CircuitOpen || hh.LastError == "" {
				t.Fatalf("health = %+v, want one failure, circuit closed, error text", hh)
			}
		})
	}
}

func TestBlockedWaitsAtLeastAnHourEvenWithSmallMaxBackoff(t *testing.T) {
	h := newHarness(t, sources("a"), func(o *Options) { o.MaxBackoff = 10 * time.Minute })
	h.fetch.fn = failWith(&httpx.Error{Class: httpx.Blocked})
	h.start()
	h.event()
	if got := h.untilNextDue("a"); got < time.Hour {
		t.Fatalf("next due in %v, want at least 1h", got)
	}
}

func TestPermanentOpensCircuitUntilRefresh(t *testing.T) {
	h := newHarness(t, sources("a"), nil)
	h.fetch.fn = failWith(&httpx.Error{Class: httpx.Permanent, Status: 404})
	h.start()
	h.event()

	hh := h.health("a")
	if !hh.CircuitOpen || !hh.Permanent || hh.Level != LevelRed || hh.Failures != 1 {
		t.Fatalf("health = %+v, want open permanent circuit, RED", hh)
	}
	if h.untilNextDue("a") < 365*24*time.Hour {
		t.Fatalf("next due in %v, want far in the future", h.untilNextDue("a"))
	}
	h.clock.Advance(1000 * 24 * time.Hour)
	settle()
	if h.fetch.calls() != 1 {
		t.Fatalf("%d polls, want 1: a permanent failure must not be retried", h.fetch.calls())
	}

	h.fetch.setFn(nil)
	h.sched.RefreshNow("a")
	if hh := h.health("a"); hh.CircuitOpen || hh.Permanent {
		t.Fatalf("health after RefreshNow = %+v, want circuit closed", hh)
	}
	if ev := h.event(); ev.Err != nil {
		t.Fatalf("event after refresh = %+v", ev)
	}
	if hh := h.health("a"); hh.Failures != 0 || hh.Level != LevelGreen || hh.LastError != "" {
		t.Fatalf("health after good poll = %+v, want healthy", hh)
	}
}

func TestPermanentCircuitClearedByRestart(t *testing.T) {
	h := newHarness(t, sources("a"), nil)
	h.fetch.fn = failWith(&httpx.Error{Class: httpx.Permanent})
	h.start()
	h.event()
	h.stop()
	if !h.store.get("a").health.Permanent {
		t.Fatal("permanent circuit was not persisted")
	}

	h.fetch.setFn(nil)
	h.start()
	if hh := h.health("a"); hh.CircuitOpen || hh.Permanent {
		t.Fatalf("health after restart = %+v, want circuit closed", hh)
	}
	if ev := h.event(); ev.Err != nil {
		t.Fatalf("event after restart = %+v", ev)
	}
}

func TestCircuitHalfOpenProbeCadence(t *testing.T) {
	h := newHarness(t, []model.Source{{ID: "a", Kind: model.KindRSS, Enabled: true, Interval: "4m"}},
		func(o *Options) { o.MaxBackoff = time.Hour })
	h.rnd.queued = []float64{0}
	h.rnd.def = 0.5
	h.fetch.fn = failWith(&httpx.Error{Class: httpx.Retryable})
	h.start()

	for i := 1; i <= 5; i++ {
		h.event()
		if i < 5 {
			h.clock.Advance(h.untilNextDue("a"))
		}
	}
	if hh := h.health("a"); !hh.CircuitOpen || hh.Permanent || hh.Level != LevelRed {
		t.Fatalf("health = %+v, want circuit open after 5 failures", hh)
	}
	if got := h.untilNextDue("a"); got != time.Hour {
		t.Fatalf("probe due in %v, want MaxBackoff (the plain backoff would be 16m)", got)
	}

	// Just short of the probe time nothing happens; at it, one probe runs and fails.
	h.clock.Advance(time.Hour - time.Second)
	settle()
	if h.fetch.calls() != 5 {
		t.Fatalf("%d polls, want 5 before the probe is due", h.fetch.calls())
	}
	h.clock.Advance(time.Second)
	h.event()
	if h.fetch.calls() != 6 || !h.health("a").CircuitOpen || h.untilNextDue("a") != time.Hour {
		t.Fatalf("after a failed probe: polls=%d health=%+v", h.fetch.calls(), h.health("a"))
	}

	// A good probe closes the circuit.
	h.fetch.setFn(nil)
	h.clock.Advance(time.Hour)
	if ev := h.event(); ev.Err != nil {
		t.Fatalf("probe event = %+v", ev)
	}
	if hh := h.health("a"); hh.CircuitOpen || hh.Failures != 0 || hh.Level != LevelGreen {
		t.Fatalf("health after good probe = %+v, want closed and healthy", hh)
	}
}

func TestRefreshNowClearsFailureCircuit(t *testing.T) {
	h := newHarness(t, sources("a"), nil)
	h.store.put("a", feeds.State{}, Health{Failures: 7, CircuitOpen: true, LastError: "x", NextDue: t0.Add(2 * time.Hour)})
	h.start()
	if !h.health("a").CircuitOpen {
		t.Fatal("persisted open circuit was lost")
	}
	h.sched.RefreshNow("a")
	if h.health("a").CircuitOpen {
		t.Fatal("RefreshNow left the circuit open")
	}
	if ev := h.event(); ev.Err != nil {
		t.Fatalf("event = %+v", ev)
	}
}

func TestRefreshNowTargets(t *testing.T) {
	h := newHarness(t, sources("a", "b"), nil)
	h.store.put("a", feeds.State{}, Health{NextDue: t0.Add(time.Hour)})
	h.store.put("b", feeds.State{}, Health{NextDue: t0.Add(time.Hour)})
	h.start()

	h.sched.RefreshNow("nope")
	settle()
	if h.fetch.calls() != 0 {
		t.Fatal("unknown id caused a poll")
	}
	h.sched.RefreshNow("b")
	h.event()
	if got := h.fetch.callOrder(); fmt.Sprint(got) != "[b]" {
		t.Fatalf("polled %v, want only b", got)
	}
	h.sched.RefreshNow("")
	h.event()
	h.event()
	if h.fetch.calls() != 3 {
		t.Fatalf("%d polls, want 3 after refreshing everything", h.fetch.calls())
	}
}

func TestOneInFlightPollPerSource(t *testing.T) {
	h := newHarness(t, []model.Source{{ID: "a", Kind: model.KindRSS, Enabled: true, Interval: "1m"}},
		func(o *Options) { o.Workers = 4 })
	gate := make(chan struct{})
	h.fetch.fn = func(ctx context.Context, _ model.Source, _ feeds.State) (feeds.Result, error) {
		select {
		case <-gate:
		case <-ctx.Done():
		}
		return feeds.Result{}, nil
	}
	h.start()
	waitFor(t, "first poll", func() bool { return h.fetch.inflight() == 1 })

	for range 3 {
		h.sched.RefreshNow("a")
		h.clock.Advance(time.Hour)
	}
	settle()
	if h.fetch.calls() != 1 {
		t.Fatalf("%d polls while the first was running, want 1", h.fetch.calls())
	}

	// The refresh that came during the poll is honoured right after it.
	gate <- struct{}{}
	waitFor(t, "poll after refresh", func() bool { return h.fetch.calls() == 2 })
	gate <- struct{}{}
	h.event()
	h.event()
	h.fetch.mu.Lock()
	defer h.fetch.mu.Unlock()
	if h.fetch.maxPer != 1 {
		t.Fatalf("up to %d concurrent polls of one source, want 1", h.fetch.maxPer)
	}
}

func TestWorkerLimit(t *testing.T) {
	h := newHarness(t, sources(ids(6)...), func(o *Options) { o.Workers = 2 })
	gate := make(chan struct{})
	h.fetch.fn = func(ctx context.Context, _ model.Source, _ feeds.State) (feeds.Result, error) {
		select {
		case <-gate:
		case <-ctx.Done():
		}
		return feeds.Result{}, nil
	}
	h.start()
	waitFor(t, "two polls in flight", func() bool { return h.fetch.inflight() == 2 })
	settle()
	if n := h.fetch.inflight(); n != 2 {
		t.Fatalf("%d polls in flight, want exactly 2", n)
	}
	for range 6 {
		gate <- struct{}{}
	}
	waitFor(t, "all six polls", func() bool { return h.fetch.calls() == 6 })
	h.fetch.mu.Lock()
	defer h.fetch.mu.Unlock()
	if h.fetch.maxActive != 2 {
		t.Fatalf("max concurrent polls = %d, want 2", h.fetch.maxActive)
	}
}

func TestEventsDroppedWhenFullWithoutBlocking(t *testing.T) {
	const n = 70
	h := newHarness(t, sources(ids(n)...), func(o *Options) { o.Workers = 4 })
	h.start()
	waitFor(t, "every source polled", func() bool {
		for _, hh := range h.sched.Health() {
			if hh.LastOK.IsZero() {
				return false
			}
		}
		return true
	})
	if got := len(h.sched.Events()); got != 64 {
		t.Fatalf("%d events buffered, want 64 (the rest dropped)", got)
	}
	h.stop()
}

func TestEventCarriesNewCountAndError(t *testing.T) {
	h := newHarness(t, sources("a", "b"), nil)
	h.fetch.fn = func(_ context.Context, src model.Source, _ feeds.State) (feeds.Result, error) {
		if src.ID == "b" {
			return feeds.Result{}, errBoom
		}
		return feeds.Result{Articles: make([]model.Article, 5)}, nil
	}
	h.sink.fn = func(context.Context, model.Source, []model.Article) (int, error) { return 3, nil }
	h.start()
	got := map[string]Event{}
	for range 2 {
		ev := h.event()
		got[ev.SourceID] = ev
	}
	if a := got["a"]; a.New != 3 || a.Err != nil {
		t.Errorf("event a = %+v, want 3 new, no error", a)
	}
	if b := got["b"]; b.New != 0 || !errors.Is(b.Err, errBoom) {
		t.Errorf("event b = %+v, want errBoom", b)
	}
}

func TestPersistedNextDueRespectedAfterRestart(t *testing.T) {
	h := newHarness(t, sources("a"), nil)
	h.store.put("a", feeds.State{ETag: "s"}, Health{LastOK: t0.Add(-5 * time.Minute), NextDue: t0.Add(30 * time.Minute)})
	h.start()
	if got := h.untilNextDue("a"); got != 30*time.Minute {
		t.Fatalf("next due in %v, want the persisted 30m", got)
	}
	h.clock.Advance(29 * time.Minute)
	settle()
	if h.fetch.calls() != 0 {
		t.Fatal("polled before the persisted due time")
	}
	h.clock.Advance(time.Minute)
	h.event()
	if prevs := h.fetch.prevs; len(prevs) != 1 || prevs[0].ETag != "s" {
		t.Fatalf("polled with %+v, want the persisted state", prevs)
	}
	h.stop()

	// A second restart sees the NextDue that the poll saved.
	h.start()
	want := h.store.get("a").health.NextDue.Sub(h.clock.Now())
	if got := h.untilNextDue("a"); got != want || want <= 0 {
		t.Fatalf("after restart next due in %v, want %v", got, want)
	}
	before := h.fetch.calls()
	h.clock.Advance(want - time.Second)
	settle()
	if h.fetch.calls() != before {
		t.Fatal("polled early after restart")
	}
}

func TestOverduePersistedSourceGetsJitter(t *testing.T) {
	h := newHarness(t, sources("a"), nil)
	h.store.put("a", feeds.State{}, Health{LastOK: t0.Add(-time.Hour), NextDue: t0.Add(-time.Minute)})
	h.rnd.queued = []float64{0.5}
	h.start()
	if got := h.untilNextDue("a"); got != 30*time.Second {
		t.Fatalf("overdue source due in %v, want 30s of jitter", got)
	}
}

func TestLoadErrorStopsRun(t *testing.T) {
	h := newHarness(t, sources("a"), nil)
	h.store.err = errBoom
	p, err := New(h.options())
	if err != nil {
		t.Fatal(err)
	}
	err = p.Run(context.Background())
	if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), `"a"`) {
		t.Fatalf("Run = %v, want an error naming the source and wrapping the cause", err)
	}
}

func TestIntervalSelection(t *testing.T) {
	src := func(iv string) model.Source { return model.Source{ID: "a", Interval: iv} }
	p := &Poller{opts: Options{DefaultInterval: 15 * time.Minute}}
	if got := p.interval(src("5m"), t0); got != 5*time.Minute {
		t.Errorf("source interval = %v, want 5m", got)
	}
	for _, bad := range []string{"", "soon", "-1m", "0s"} {
		if got := p.interval(src(bad), t0); got != 15*time.Minute {
			t.Errorf("interval %q = %v, want the default", bad, got)
		}
	}
	p.opts.IntervalFor = func(_ model.Source, now time.Time) time.Duration {
		if now.Equal(t0) {
			return time.Minute
		}
		return 0
	}
	if got := p.interval(src("5m"), t0); got != time.Minute {
		t.Errorf("IntervalFor result = %v, want 1m", got)
	}
	if got := p.interval(src("5m"), t0.Add(time.Hour)); got != 5*time.Minute {
		t.Errorf("IntervalFor zero fell back to %v, want 5m", got)
	}
}

func TestIntervalForDrivesSchedule(t *testing.T) {
	h := newHarness(t, sources("a"), func(o *Options) {
		o.IntervalFor = func(model.Source, time.Time) time.Duration { return 2 * time.Minute }
	})
	h.start()
	h.event()
	if got := h.untilNextDue("a"); got != 2*time.Minute {
		t.Fatalf("next due in %v, want 2m", got)
	}
}

func TestHealthLevels(t *testing.T) {
	iv := 10 * time.Minute
	tests := []struct {
		name string
		h    Health
		want string
	}{
		{"fresh success", Health{LastOK: t0.Add(-5 * time.Minute)}, LevelGreen},
		{"success at exactly 2x interval", Health{LastOK: t0.Add(-2 * iv)}, LevelGreen},
		{"stale", Health{LastOK: t0.Add(-2*iv - time.Second)}, LevelYellow},
		{"never polled", Health{}, LevelYellow},
		{"one failure", Health{LastOK: t0, Failures: 1}, LevelYellow},
		{"two failures", Health{LastOK: t0, Failures: 2}, LevelYellow},
		{"three failures", Health{LastOK: t0, Failures: 3}, LevelRed},
		{"open circuit", Health{LastOK: t0, CircuitOpen: true}, LevelRed},
		{"never succeeded with failures", Health{Failures: 1}, LevelRed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := level(tt.h, iv, t0); got != tt.want {
				t.Fatalf("level = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestHealthSortedAndGoesStaleWithTime(t *testing.T) {
	h := newHarness(t, sources("c", "a", "b"), nil)
	h.start()
	hs := h.sched.Health()
	if len(hs) != 3 || hs[0].SourceID != "a" || hs[1].SourceID != "b" || hs[2].SourceID != "c" {
		t.Fatalf("Health = %+v, want a, b, c", hs)
	}
	for range 3 {
		h.event()
	}
	if lv := h.health("a").Level; lv != LevelGreen {
		t.Fatalf("level after success = %s, want GREEN", lv)
	}
	// Level is computed when asked: with nothing polling, it ages to YELLOW.
	h.fetch.fn = failWith(&httpx.Error{Class: httpx.Throttled, RetryAfter: 1000 * time.Hour})
	h.clock.Advance(h.untilNextDue("a"))
	for range 3 {
		h.event()
	}
	if lv := h.health("a").Level; lv != LevelYellow {
		t.Fatalf("level after one failure = %s, want YELLOW", lv)
	}
}

func TestShutdownWaitsForInFlightPolls(t *testing.T) {
	h := newHarness(t, sources("a"), nil)
	gate := make(chan struct{})
	started := make(chan struct{})
	h.fetch.fn = func(context.Context, model.Source, feeds.State) (feeds.Result, error) {
		close(started)
		<-gate // ignores ctx on purpose
		return feeds.Result{Articles: []model.Article{{}}, State: feeds.State{ETag: "late"}}, nil
	}
	h.start()
	<-started
	h.cancel()
	select {
	case err := <-h.done:
		t.Fatalf("Run returned (%v) while a poll was still in flight", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(gate)
	select {
	case err := <-h.done:
		if err != nil {
			t.Fatalf("Run = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the poll ended")
	}
	h.stopped = true
	if got := h.store.get("a").state.ETag; got != "late" {
		t.Fatalf("state of the finished poll was %q, want it saved despite the shutdown", got)
	}
}

func TestShutdownCancelsPollsAndRecordsNoFailure(t *testing.T) {
	h := newHarness(t, sources("a", "b"), func(o *Options) { o.Workers = 2 })
	h.fetch.fn = func(ctx context.Context, _ model.Source, _ feeds.State) (feeds.Result, error) {
		<-ctx.Done()
		return feeds.Result{}, ctx.Err()
	}
	h.start()
	waitFor(t, "both polls running", func() bool { return h.fetch.inflight() == 2 })
	h.stop()
	if h.fetch.inflight() != 0 {
		t.Fatal("polls still running after Run returned")
	}
	if n := h.store.saveCount(); n != 0 {
		t.Fatalf("%d saves; a cancelled poll must not count as a failure", n)
	}
	if n := len(h.sched.Events()); n != 0 {
		t.Fatalf("%d events for cancelled polls, want none", n)
	}
	for _, hh := range h.sched.Health() {
		if hh.Failures != 0 {
			t.Fatalf("health %+v: cancellation counted as a failure", hh)
		}
	}
}

func TestSaveFailureDoesNotStopPolling(t *testing.T) {
	h := newHarness(t, sources("a"), nil)
	h.store.saveErr = errBoom
	h.start()
	h.event()
	h.clock.Advance(h.untilNextDue("a"))
	h.event()
	if h.fetch.calls() != 2 {
		t.Fatalf("%d polls, want 2", h.fetch.calls())
	}
}

func TestDisabledSourcesAreNotPolled(t *testing.T) {
	off := source("off")
	off.Enabled = false
	h := newHarness(t, []model.Source{source("on"), off}, nil)
	h.start()
	h.event()
	settle()
	if got := fmt.Sprint(h.fetch.callOrder()); got != "[on]" {
		t.Fatalf("polled %s, want only on", got)
	}
	if hs := h.sched.Health(); len(hs) != 1 || hs[0].SourceID != "on" {
		t.Fatalf("Health = %+v, want only the enabled source", hs)
	}
}

func TestDefaultSleepStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sleepCtx(ctx, time.Hour) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("sleepCtx = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sleepCtx ignored cancellation")
	}
	if err := sleepCtx(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("sleepCtx = %v, want nil after the wait", err)
	}
}

func TestRealClockDefaults(t *testing.T) {
	p, err := New(Options{
		Sources:  []model.Source{source("a")},
		Fetchers: map[model.Kind]feeds.Fetcher{model.KindRSS: &fakeFetcher{}},
		Sink:     &fakeSink{},
		States:   newFakeStore(),
		Rand:     func() float64 { return 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	select {
	case ev := <-p.Events():
		if ev.SourceID != "a" || ev.Err != nil {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no poll with the real clock")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run = %v", err)
	}
}

func hosted(id, rawURL string) model.Source {
	s := source(id)
	s.URL = rawURL
	return s
}

// gateFetch holds every poll until the test lets it go.
func gateFetch(h *harness) chan struct{} {
	gate := make(chan struct{})
	h.fetch.fn = func(ctx context.Context, _ model.Source, _ feeds.State) (feeds.Result, error) {
		select {
		case <-gate:
		case <-ctx.Done():
		}
		return feeds.Result{}, nil
	}
	return gate
}

func TestDifferentHostsRunTogetherAndOneHostOneAtATime(t *testing.T) {
	srcs := []model.Source{
		hosted("a1", "https://a.example/1"), hosted("a2", "https://a.example/2"), hosted("a3", "https://a.example/3"),
		hosted("b1", "https://b.example/1"), hosted("c1", "https://c.example/1"),
	}
	h := newHarness(t, srcs, func(o *Options) { o.Workers = 8 })
	gate := gateFetch(h)
	h.start()
	// One poll for each of the three hosts, not three on the first.
	waitFor(t, "a poll per host", func() bool { return h.fetch.inflight() == 3 })
	settle()
	if n := h.fetch.inflight(); n != 3 {
		t.Fatalf("%d polls in flight, want 3 (one per host)", n)
	}
	got := map[string]int{}
	for _, id := range h.fetch.callOrder() {
		got[id[:1]]++
	}
	if got["a"] != 1 || got["b"] != 1 || got["c"] != 1 {
		t.Fatalf("polls by host = %v, want one each", got)
	}
	// The end of one poll of host a lets the next source of host a start.
	for range 5 {
		gate <- struct{}{}
	}
	waitFor(t, "all five polls", func() bool { return h.fetch.calls() == 5 })
}

func TestBusyHostDoesNotHoldUpOtherHosts(t *testing.T) {
	// Three sources of one host are first in line; with two workers the other
	// host must still get one of them.
	srcs := []model.Source{
		hosted("a1", "https://a.example/1"), hosted("a2", "https://a.example/2"), hosted("a3", "https://a.example/3"),
		hosted("z1", "https://z.example/1"),
	}
	h := newHarness(t, srcs, func(o *Options) { o.Workers = 2 })
	gateFetch(h)
	h.start()
	waitFor(t, "two polls", func() bool { return h.fetch.inflight() == 2 })
	order := h.fetch.callOrder() // the two polls record themselves in either order
	slices.Sort(order)
	if !slices.Equal(order, []string{"a1", "z1"}) {
		t.Fatalf("polled %v first, want a1 and z1", order)
	}
}

func TestHostGroupsShareOneHost(t *testing.T) {
	srcs := []model.Source{hosted("x", "https://www.one.example/x"), hosted("y", "https://data.one.example/y")}
	h := newHarness(t, srcs, func(o *Options) {
		o.Workers = 4
		o.HostOf = func(model.Source) string { return "one" }
	})
	gate := gateFetch(h)
	h.start()
	waitFor(t, "first poll", func() bool { return h.fetch.inflight() == 1 })
	settle()
	if n := h.fetch.inflight(); n != 1 {
		t.Fatalf("%d polls of one host group at once, want 1", n)
	}
	gate <- struct{}{}
	gate <- struct{}{}
	waitFor(t, "both polled", func() bool { return h.fetch.calls() == 2 })
}

func TestPerHostAllowsMoreThanOne(t *testing.T) {
	srcs := []model.Source{hosted("x", "https://a.example/x"), hosted("y", "https://a.example/y"), hosted("z", "https://a.example/z")}
	h := newHarness(t, srcs, func(o *Options) { o.Workers = 4; o.PerHost = 2 })
	gateFetch(h)
	h.start()
	waitFor(t, "two polls", func() bool { return h.fetch.inflight() == 2 })
	settle()
	if n := h.fetch.inflight(); n != 2 {
		t.Fatalf("%d polls of one host at once, want 2", n)
	}
}

func TestThrottledHostCoolsDownWhileOthersGoOn(t *testing.T) {
	srcs := []model.Source{
		hosted("a1", "https://a.example/1"), hosted("a2", "https://a.example/2"), hosted("b1", "https://b.example/1"),
	}
	h := newHarness(t, srcs, func(o *Options) { o.Workers = 4 })
	var throttled atomic.Bool
	h.fetch.fn = func(_ context.Context, src model.Source, _ feeds.State) (feeds.Result, error) {
		if src.ID == "a1" && !throttled.Swap(true) {
			return feeds.Result{}, &httpx.Error{Class: httpx.Throttled, RetryAfter: 20 * time.Minute}
		}
		return feeds.Result{}, nil
	}
	h.start()
	h.event()
	h.event()
	waitFor(t, "a1 and b1 polled", func() bool { return h.fetch.calls() >= 2 })
	// a2 was waiting behind a1 (one poll per host) and is now held until the
	// 20 minutes are over; b1 is not held at all.
	settle()
	for _, id := range h.fetch.callOrder() {
		if id == "a2" {
			t.Fatalf("a2 was polled while its host was cooling down: %v", h.fetch.callOrder())
		}
	}
	if d := h.untilNextDue("a2"); d < 19*time.Minute {
		t.Fatalf("a2 due in %v, want after the host's 20 minute cooldown", d)
	}
	h.clock.Advance(21 * time.Minute)
	waitFor(t, "a2 polled after the cooldown", func() bool {
		return slices.Contains(h.fetch.callOrder(), "a2")
	})
}

func TestTwoFailuresInARowCoolTheHost(t *testing.T) {
	srcs := []model.Source{hosted("a1", "https://a.example/1"), hosted("a2", "https://a.example/2"), hosted("a3", "https://a.example/3")}
	h := newHarness(t, srcs, nil)
	h.rnd.queued = []float64{0, 0, 0} // due at once; later, no jitter
	h.rnd.def = 0.5
	h.fetch.fn = failWith(&httpx.Error{Class: httpx.Retryable, Status: 503})
	h.start()
	h.event()
	// One failure is the source's own business: the next source goes at once.
	h.event()
	// The second makes the host rest for a minute, whatever the source's own
	// (longer) backoff, so the third is held back by it.
	settle()
	if n := h.fetch.calls(); n != 2 {
		t.Fatalf("%d polls, want 2 before the host rests", n)
	}
	if d := h.untilNextDue("a3"); d < 50*time.Second {
		t.Fatalf("a3 due in %v, want after the host's cooldown", d)
	}
	h.clock.Advance(2 * time.Minute)
	h.event()
	if n := h.fetch.calls(); n != 3 {
		t.Fatalf("%d polls after the cooldown, want 3", n)
	}
}

func TestErrorsOfOneSourceDoNotCoolTheHost(t *testing.T) {
	for name, err := range map[string]error{
		"not found": &httpx.Error{Class: httpx.Permanent, Status: 404},
		"our own":   errBoom,
	} {
		t.Run(name, func(t *testing.T) {
			srcs := []model.Source{hosted("a1", "https://a.example/1"), hosted("a2", "https://a.example/2"), hosted("a3", "https://a.example/3")}
			h := newHarness(t, srcs, nil)
			h.fetch.fn = failWith(err)
			h.start()
			h.event()
			h.event()
			h.event()
			if n := h.fetch.calls(); n != 3 {
				t.Fatalf("%d polls, want 3: the host should not rest", n)
			}
		})
	}
}

func TestHostRecoversAfterASuccess(t *testing.T) {
	srcs := []model.Source{hosted("a1", "https://a.example/1"), hosted("a2", "https://a.example/2")}
	h := newHarness(t, srcs, nil)
	fail := true
	h.fetch.fn = func(_ context.Context, _ model.Source, _ feeds.State) (feeds.Result, error) {
		if fail {
			fail = false
			return feeds.Result{}, &httpx.Error{Class: httpx.Retryable}
		}
		return feeds.Result{}, nil
	}
	h.start()
	h.event()
	h.event()
	// One failure and then a success: nothing rests.
	if d := h.untilNextDue("a2"); d < 5*time.Minute {
		t.Fatalf("a2 due in %v, want its normal interval", d)
	}
}
