package scheduler

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/feeds"
	"github.com/b070nd/stoxicity/app/src/model"
)

var t0 = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

// fakeClock is a clock that moves only when a test calls Advance.
//
// A Sleep that starts after an Advance, with a duration worked out before it,
// must not oversleep, so Sleep counts from the last time Now handed out rather
// than from the clock's current reading.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	seen    time.Time // the latest value Now returned
	waiters []*clockWaiter
}

type clockWaiter struct {
	at time.Time
	ch chan struct{}
}

func newFakeClock() *fakeClock { return &fakeClock{now: t0, seen: t0} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = c.now
	return c.now
}

func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	w := &clockWaiter{at: c.seen.Add(d), ch: make(chan struct{})}
	if !w.at.After(c.now) {
		c.mu.Unlock()
		return nil
	}
	c.waiters = append(c.waiters, w)
	c.mu.Unlock()
	select {
	case <-w.ch:
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		for i, x := range c.waiters {
			if x == w {
				c.waiters = append(c.waiters[:i], c.waiters[i+1:]...)
				break
			}
		}
		c.mu.Unlock()
		return ctx.Err()
	}
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	kept := c.waiters[:0]
	for _, w := range c.waiters {
		if w.at.After(c.now) {
			kept = append(kept, w)
		} else {
			close(w.ch)
		}
	}
	c.waiters = kept
}

// scriptRand returns queued values first, then def.
type scriptRand struct {
	mu     sync.Mutex
	queued []float64
	def    float64
}

func (r *scriptRand) next() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.queued) > 0 {
		v := r.queued[0]
		r.queued = r.queued[1:]
		return v
	}
	return r.def
}

// fakeFetcher records calls and tracks concurrency. fn decides each result.
type fakeFetcher struct {
	mu        sync.Mutex
	fn        func(ctx context.Context, src model.Source, prev feeds.State) (feeds.Result, error)
	order     []string
	prevs     []feeds.State
	active    int
	maxActive int
	perSource map[string]int
	maxPer    int
}

func (f *fakeFetcher) Fetch(ctx context.Context, src model.Source, prev feeds.State) (feeds.Result, error) {
	f.mu.Lock()
	f.order = append(f.order, src.ID)
	f.prevs = append(f.prevs, prev)
	f.active++
	f.maxActive = max(f.maxActive, f.active)
	if f.perSource == nil {
		f.perSource = map[string]int{}
	}
	f.perSource[src.ID]++
	f.maxPer = max(f.maxPer, f.perSource[src.ID])
	fn := f.fn
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.active--
		f.perSource[src.ID]--
		f.mu.Unlock()
	}()
	if fn == nil {
		return feeds.Result{}, nil
	}
	return fn(ctx, src, prev)
}

func (f *fakeFetcher) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.order)
}

func (f *fakeFetcher) inflight() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active
}

func (f *fakeFetcher) setFn(fn func(ctx context.Context, src model.Source, prev feeds.State) (feeds.Result, error)) {
	f.mu.Lock()
	f.fn = fn
	f.mu.Unlock()
}

func (f *fakeFetcher) callOrder() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.order...)
}

type fakeSink struct {
	mu    sync.Mutex
	fn    func(ctx context.Context, src model.Source, articles []model.Article) (int, error)
	count int
}

func (s *fakeSink) Ingest(ctx context.Context, src model.Source, articles []model.Article) (int, error) {
	s.mu.Lock()
	s.count++
	fn := s.fn
	s.mu.Unlock()
	if fn == nil {
		return len(articles), nil
	}
	return fn(ctx, src, articles)
}

func (s *fakeSink) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count
}

type saved struct {
	state  feeds.State
	health Health
}

type fakeStore struct {
	mu      sync.Mutex
	data    map[string]saved
	saves   int
	err     error // returned by LoadSource
	saveErr error // returned by SaveSource
}

func newFakeStore() *fakeStore { return &fakeStore{data: map[string]saved{}} }

func (s *fakeStore) LoadSource(_ context.Context, id string) (feeds.State, Health, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.data[id]
	return d.state, d.health, s.err
}

func (s *fakeStore) SaveSource(_ context.Context, id string, st feeds.State, h Health) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saves++
	if s.saveErr != nil {
		return s.saveErr
	}
	s.data[id] = saved{st, h}
	return nil
}

func (s *fakeStore) get(id string) saved {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data[id]
}

func (s *fakeStore) put(id string, st feeds.State, h Health) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[id] = saved{st, h}
}

func (s *fakeStore) saveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saves
}

func source(id string) model.Source {
	return model.Source{ID: id, Kind: model.KindRSS, Enabled: true, Interval: "10m"}
}

func sources(ids ...string) []model.Source {
	out := make([]model.Source, len(ids))
	for i, id := range ids {
		out[i] = source(id)
	}
	return out
}

// harness wires a Poller to fakes. start may be called again after stop to
// simulate a restart on the same store and clock.
type harness struct {
	t       *testing.T
	clock   *fakeClock
	fetch   *fakeFetcher
	sink    *fakeSink
	store   *fakeStore
	rnd     *scriptRand
	srcs    []model.Source
	tweak   func(*Options)
	sched   *Poller
	cancel  context.CancelFunc
	done    chan error
	stopped bool
}

func newHarness(t *testing.T, srcs []model.Source, tweak func(*Options)) *harness {
	t.Helper()
	baseline := runtime.NumGoroutine()
	h := &harness{
		t:     t,
		clock: newFakeClock(),
		fetch: &fakeFetcher{},
		sink:  &fakeSink{},
		store: newFakeStore(),
		rnd:   &scriptRand{},
		srcs:  srcs,
		tweak: tweak,
	}
	t.Cleanup(func() {
		waitFor(t, "goroutines to end", func() bool { return runtime.NumGoroutine() <= baseline })
	})
	t.Cleanup(func() {
		if h.cancel != nil && !h.stopped {
			h.stop()
		}
	})
	return h
}

func (h *harness) options() Options {
	o := Options{
		Sources:         h.srcs,
		Fetchers:        map[model.Kind]feeds.Fetcher{model.KindRSS: h.fetch},
		Sink:            h.sink,
		States:          h.store,
		DefaultInterval: 10 * time.Minute,
		MaxBackoff:      2 * time.Hour,
		Workers:         1,
		Now:             h.clock.Now,
		Sleep:           h.clock.Sleep,
		Rand:            h.rnd.next,
	}
	if h.tweak != nil {
		h.tweak(&o)
	}
	return o
}

// start builds a fresh Poller and runs it, and waits until it has loaded.
func (h *harness) start() {
	h.t.Helper()
	p, err := New(h.options())
	if err != nil {
		h.t.Fatalf("New: %v", err)
	}
	h.sched = p
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.stopped = false
	h.done = make(chan error, 1)
	go func() { h.done <- p.Run(ctx) }()
	waitFor(h.t, "scheduler to load", func() bool {
		for _, hh := range p.Health() {
			if hh.NextDue.IsZero() {
				return false
			}
		}
		return true
	})
}

// stop cancels Run and checks that it returns nil.
func (h *harness) stop() {
	h.t.Helper()
	h.cancel()
	select {
	case err := <-h.done:
		if err != nil {
			h.t.Errorf("Run returned %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		h.t.Fatal("Run did not return after cancel")
	}
	h.stopped = true
}

func (h *harness) event() Event {
	h.t.Helper()
	select {
	case ev := <-h.sched.Events():
		return ev
	case <-time.After(5 * time.Second):
		h.t.Fatal("no event within 5s")
		return Event{}
	}
}

func (h *harness) health(id string) Health {
	h.t.Helper()
	for _, x := range h.sched.Health() {
		if x.SourceID == id {
			return x
		}
	}
	h.t.Fatalf("no health for %q", id)
	return Health{}
}

// untilNextDue is how far ahead of the fake clock the source is due.
func (h *harness) untilNextDue(id string) time.Duration {
	return h.health(id).NextDue.Sub(h.clock.Now())
}

// settle gives the run loop a moment, for checks that nothing happens.
func settle() { time.Sleep(30 * time.Millisecond) }

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func ids(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("s%03d", i)
	}
	return out
}
