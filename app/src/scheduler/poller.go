package scheduler

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/b070nd/stoxicity/app/src/feeds"
	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/model"
)

// Defaults for the Options a caller leaves at zero.
const (
	defaultInterval   = 15 * time.Minute
	defaultMaxBackoff = 2 * time.Hour
	defaultWorkers    = 16
)

const (
	eventBuffer      = 64
	circuitThreshold = 5               // consecutive failures that open the circuit
	minBackoffBase   = time.Minute     // smallest first retry delay
	blockedWait      = time.Hour       // least wait after a block
	maxSleep         = 5 * time.Minute // longest single sleep of the run loop
	farFuture        = 100 * 365 * 24 * time.Hour
	initialJitter    = 0.10 // of the interval, spreads the first polls
	successJitter    = 0.05 // of the interval, added to the next due time
	backoffJitter    = 0.20 // either way around the backoff delay
	saveTimeout      = 10 * time.Second

	// A host that shows trouble is left alone for a while, whichever of its
	// sources noticed: hostMinCooldown after a 429 or after the second failure
	// in a row, doubling with each further failure up to hostMaxCooldown, and
	// hostBlockedCooldown after a refusal (403).
	hostMinCooldown     = time.Minute
	hostMaxCooldown     = 30 * time.Minute
	hostBlockedCooldown = 10 * time.Minute
)

// Options configures a Poller. Only Sources, Fetchers, Sink and States are
// required.
type Options struct {
	// Sources are the configured sources; only the enabled ones are polled.
	Sources []model.Source
	// Fetchers reads each source kind. Every enabled source needs one.
	Fetchers map[model.Kind]feeds.Fetcher
	// Sink stores what a poll found.
	Sink Sink
	// States keeps State and Health across restarts.
	States StateStore

	// DefaultInterval applies to a source without a valid Interval. Zero
	// means 15 minutes.
	DefaultInterval time.Duration
	// MaxBackoff caps the retry delay and is the pause between probes of an
	// open circuit. Zero means 2 hours.
	MaxBackoff time.Duration
	// Workers is the number of polls that may run at once, over all hosts.
	// Zero means 16.
	Workers int
	// PerHost is the number of polls of one host that may run at once; the
	// sources of different hosts always run side by side, up to Workers. Zero
	// means 1.
	PerHost int
	// HostOf names the host a source is fetched from, so that the sources
	// that share one (or share a rate limit, like the SEC's hosts) are polled
	// politely together. Nil, or an empty answer, uses the host name in the
	// source's URL; a source with no URL is a host of its own.
	HostOf func(src model.Source) string
	// StartTogether makes every source that is overdue at the start due at once,
	// instead of spreading the first polls over a tenth of their intervals. The
	// limits per host and for all polls still apply, so it does not stampede.
	StartTogether bool
	// IntervalFor, when set, overrides the interval of a source at a given
	// time, for example to poll EDGAR faster in US market hours. A result of
	// zero or less falls back to the source's own interval.
	IntervalFor func(src model.Source, now time.Time) time.Duration

	// Now returns the current time. Nil means time.Now.
	Now func() time.Time
	// Sleep waits for d or until ctx ends, whichever is first, and returns
	// ctx's error in the second case. Nil sleeps with a timer.
	Sleep func(ctx context.Context, d time.Duration) error
	// Rand returns a number in [0, 1) for jitter. Nil uses math/rand/v2. The
	// Poller calls it only while holding its own lock, so it needs no locking.
	Rand func() float64
	// Logger receives debug and warning lines. Nil discards them.
	Logger *slog.Logger
}

// entry is the scheduler's record of one enabled source. All fields except src
// are guarded by Poller.mu.
type entry struct {
	src      model.Source
	host     string // see Options.HostOf
	state    feeds.State
	health   Health // health.NextDue is the heap key
	index    int    // position in the queue, -1 while a poll is in flight
	inflight bool
	started  time.Time // when the poll in flight began
	lastNew  int       // articles the last good poll stored
	refresh  bool      // RefreshNow arrived during the poll: poll again right after
}

// Poller implements Scheduler: it keeps a min-heap of enabled sources by
// next-due time and polls them on a bounded set of workers, side by side for
// different hosts and one at a time (PerHost) for one. (The name Scheduler
// belongs to the interface.)
type Poller struct {
	opts   Options
	events chan Event
	wake   chan struct{}

	mu      sync.Mutex
	entries []*entry // enabled sources, sorted by ID
	byID    map[string]*entry
	queue   queue
	hosts   map[string]*hostState
	running int // polls in flight
	started bool
	wg      sync.WaitGroup // in-flight polls
}

var _ Scheduler = (*Poller)(nil)

// hostState is what the scheduler knows about one host, shared by all of its
// sources. Guarded by Poller.mu.
type hostState struct {
	running  int       // polls of this host in flight
	failures int       // failures in a row that point at the host, not at a source
	until    time.Time // no poll of this host starts before this instant
}

// New checks opts and returns a Poller that has not started.
func New(opts Options) (*Poller, error) {
	if opts.Sink == nil {
		return nil, errors.New("scheduler: Sink is required")
	}
	if opts.States == nil {
		return nil, errors.New("scheduler: States is required")
	}
	if opts.DefaultInterval <= 0 {
		opts.DefaultInterval = defaultInterval
	}
	if opts.MaxBackoff <= 0 {
		opts.MaxBackoff = defaultMaxBackoff
	}
	if opts.Workers <= 0 {
		opts.Workers = defaultWorkers
	}
	if opts.PerHost <= 0 {
		opts.PerHost = 1
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Sleep == nil {
		opts.Sleep = sleepCtx
	}
	if opts.Rand == nil {
		opts.Rand = rand.Float64
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}

	p := &Poller{
		opts:   opts,
		events: make(chan Event, eventBuffer),
		wake:   make(chan struct{}, 1),
		byID:   make(map[string]*entry),
		hosts:  make(map[string]*hostState),
	}
	for _, src := range opts.Sources {
		if !src.Enabled {
			continue
		}
		if src.ID == "" {
			return nil, errors.New("scheduler: an enabled source has no id")
		}
		if p.byID[src.ID] != nil {
			return nil, fmt.Errorf("scheduler: source %q is listed twice", src.ID)
		}
		if opts.Fetchers[src.Kind] == nil {
			return nil, fmt.Errorf("scheduler: source %q: no fetcher for kind %q", src.ID, src.Kind)
		}
		e := &entry{src: src, host: p.hostKey(src), index: -1, health: Health{SourceID: src.ID}}
		if p.hosts[e.host] == nil {
			p.hosts[e.host] = &hostState{}
		}
		p.entries = append(p.entries, e)
		p.byID[src.ID] = e
	}
	sort.Slice(p.entries, func(i, j int) bool { return p.entries[i].src.ID < p.entries[j].src.ID })
	return p, nil
}

// Events returns the channel that receives one Event per finished poll. It
// holds 64 events; when it is full new events are dropped.
func (p *Poller) Events() <-chan Event { return p.events }

// Health returns the state of every enabled source, sorted by SourceID.
func (p *Poller) Health() []Health {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.opts.Now()
	out := make([]Health, len(p.entries))
	for i, e := range p.entries {
		h := e.health
		h.Level = level(h, p.interval(e.src, now), now)
		out[i] = h
	}
	return out
}

// RefreshNow makes a source due at once and closes its circuit; an empty id
// means every enabled source and an unknown id is ignored. A source that is
// being polled is polled again as soon as that poll ends.
func (p *Poller) RefreshNow(sourceID string) {
	p.mu.Lock()
	now := p.opts.Now()
	for _, e := range p.entries {
		if sourceID != "" && e.src.ID != sourceID {
			continue
		}
		e.health.CircuitOpen = false
		e.health.Permanent = false
		if e.inflight {
			e.refresh = true
			continue
		}
		e.health.NextDue = now
		if e.index >= 0 {
			heap.Fix(&p.queue, e.index)
		}
	}
	p.mu.Unlock()
	p.wakeUp()
}

// Run polls until ctx ends. It stops starting polls, waits for the ones in
// flight (they receive ctx) and returns nil. It returns an error if the
// persisted state cannot be read or if Run was called before.
func (p *Poller) Run(ctx context.Context) error {
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return errors.New("scheduler: Run called twice")
	}
	p.started = true
	p.mu.Unlock()

	if err := p.load(ctx); err != nil {
		return err
	}
	for ctx.Err() == nil {
		p.sleep(ctx, p.dispatch(ctx))
	}
	p.wg.Wait()
	return nil
}

// load reads each source's persisted State and Health and builds the queue. A
// source whose NextDue is still ahead keeps it; any other is due now plus a
// small jitter, so sources do not all fire together.
func (p *Poller) load(ctx context.Context) error {
	states := make([]feeds.State, len(p.entries))
	healths := make([]Health, len(p.entries))
	for i, e := range p.entries {
		st, h, err := p.opts.States.LoadSource(ctx, e.src.ID)
		if err != nil {
			return fmt.Errorf("scheduler: load source %q: %w", e.src.ID, err)
		}
		states[i], healths[i] = st, h
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.opts.Now()
	for i, e := range p.entries {
		h := healths[i]
		h.SourceID = e.src.ID
		if h.Permanent {
			h.Permanent = false
			h.CircuitOpen = false
			h.NextDue = time.Time{}
		}
		iv := p.interval(e.src, now)
		if !h.NextDue.After(now) {
			h.NextDue = now
			if !p.opts.StartTogether {
				h.NextDue = now.Add(p.jitter(initialJitter, iv))
			}
		}
		h.Level = level(h, iv, now)
		e.state, e.health = states[i], h
		heap.Push(&p.queue, e)
	}
	return nil
}

// dispatch starts every due poll that a worker is free for and returns how
// long the run loop may sleep. A due source whose host is busy (PerHost polls
// in flight) or cooling down waits in its place; the others go on past it, so
// one slow or throttled host never holds up the rest.
func (p *Poller) dispatch(ctx context.Context) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.opts.Now()
	wait := maxSleep // a finished poll or RefreshNow wakes the loop
	var held []*entry
	for p.running < p.opts.Workers && len(p.queue) > 0 {
		if due := p.queue[0].health.NextDue; due.After(now) {
			wait = min(wait, due.Sub(now))
			break
		}
		e := heap.Pop(&p.queue).(*entry)
		hs := p.hosts[e.host]
		if hs.until.After(now) {
			wait = min(wait, hs.until.Sub(now))
			held = append(held, e)
			continue
		}
		if hs.running >= p.opts.PerHost {
			held = append(held, e) // the end of a poll of this host wakes the loop
			continue
		}
		e.inflight = true
		e.started = now
		hs.running++
		p.running++
		p.wg.Add(1)
		go p.poll(ctx, e, e.state)
	}
	for _, e := range held {
		heap.Push(&p.queue, e)
	}
	return wait
}

// hostKey is the host a source is polled with.
func (p *Poller) hostKey(src model.Source) string {
	if p.opts.HostOf != nil {
		if k := p.opts.HostOf(src); k != "" {
			return strings.ToLower(k)
		}
	}
	if u, err := url.Parse(src.URL); err == nil && u.Hostname() != "" {
		return strings.ToLower(u.Hostname())
	}
	return "source:" + src.ID
}

// hostResult tells the host what a poll of one of its sources came to, and
// starts a cooldown when it points at the host: a 429, a refusal, or two
// failures in a row of the network, a timeout or a 5xx. A 404 or a parse error
// says something about one feed and nothing about the host, and an error of
// our own (a full disk) nothing at all. It returns the end of the cooldown, or
// the zero time. The caller holds p.mu.
func (p *Poller) hostResult(host string, err error, now time.Time) time.Time {
	hs := p.hosts[host]
	if err == nil {
		hs.failures = 0
		hs.until = time.Time{}
		return time.Time{}
	}
	var he *httpx.Error
	if !errors.As(err, &he) || he.Class == httpx.Permanent {
		return time.Time{}
	}
	hs.failures++
	var d time.Duration
	switch he.Class {
	case httpx.Throttled:
		d = max(he.RetryAfter, hostMinCooldown)
	case httpx.Blocked:
		d = hostBlockedCooldown
	default:
		if hs.failures < 2 {
			return time.Time{}
		}
		d = hostMinCooldown
		for i := 2; i < hs.failures && d < hostMaxCooldown; i++ {
			d *= 2
		}
		d = min(d, hostMaxCooldown)
	}
	until := now.Add(d)
	if until.After(hs.until) {
		hs.until = until
	}
	// The host's other sources are not due before it is over.
	for _, o := range p.entries {
		if o.host != host || o.inflight || o.index < 0 || !o.health.NextDue.Before(hs.until) {
			continue
		}
		o.health.NextDue = hs.until
		heap.Fix(&p.queue, o.index)
	}
	return hs.until
}

// sleep waits for d, for ctx to end or for a wake-up, whichever is first.
func (p *Poller) sleep(ctx context.Context, d time.Duration) {
	sctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-p.wake:
			cancel()
		case <-sctx.Done():
		}
	}()
	_ = p.opts.Sleep(sctx, d)
	cancel()
	<-done
}

func (p *Poller) wakeUp() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// poll is one fetch of one source on its own goroutine. The entry stays out of
// the queue, so no second poll of it can start, until poll puts it back.
func (p *Poller) poll(ctx context.Context, e *entry, prev feeds.State) {
	defer p.wg.Done()
	next, added, err := p.fetch(ctx, e.src, prev)
	if err != nil && ctx.Err() != nil {
		// Shutting down: the poll was cut short, which says nothing about the source.
		p.finish(e, false)
		return
	}

	p.mu.Lock()
	now := p.opts.Now()
	iv := p.interval(e.src, now)
	h := e.health
	if err == nil {
		p.succeed(&h, iv, now, e.started)
		e.state = next
		e.lastNew = added
	} else {
		p.fail(&h, iv, now, err)
	}
	if cool := p.hostResult(e.host, err, now); cool.After(now) {
		if cool.After(h.NextDue) {
			h.NextDue = cool
		}
		p.opts.Logger.Warn("scheduler: host cooling down", "host", e.host, "until", cool, "cause", err)
	}
	h.Level = level(h, iv, now)
	e.health = h
	st := e.state
	p.mu.Unlock()

	// Saved without ctx's cancellation: articles may already be stored, and
	// the State that goes with them should not be lost to a shutdown.
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), saveTimeout)
	if serr := p.opts.States.SaveSource(sctx, e.src.ID, st, h); serr != nil {
		p.opts.Logger.Warn("scheduler: save source failed", "source", e.src.ID, "err", serr)
	}
	cancel()

	if err != nil {
		p.opts.Logger.Warn("scheduler: poll failed", "source", e.src.ID, "failures", h.Failures, "next_due", h.NextDue, "err", err)
	} else {
		p.opts.Logger.Debug("scheduler: poll done", "source", e.src.ID, "new", added, "next_due", h.NextDue)
	}
	select {
	case p.events <- Event{SourceID: e.src.ID, New: added, Err: err}:
	default:
		p.opts.Logger.Debug("scheduler: event dropped, channel full", "source", e.src.ID)
	}
	p.finish(e, true)
}

// fetch reads the source and hands the articles to the Sink. It returns the
// State to keep: the new one only when everything succeeded.
func (p *Poller) fetch(ctx context.Context, src model.Source, prev feeds.State) (feeds.State, int, error) {
	res, err := p.opts.Fetchers[src.Kind].Fetch(ctx, src, prev)
	if err != nil {
		return prev, 0, err
	}
	if res.NotModified || len(res.Articles) == 0 {
		return res.State, 0, nil
	}
	added, err := p.opts.Sink.Ingest(ctx, src, res.Articles)
	if err != nil {
		return prev, 0, err
	}
	return res.State, added, nil
}

// finish releases the worker and, unless the scheduler is stopping, puts the
// source back in the queue.
func (p *Poller) finish(e *entry, requeue bool) {
	p.mu.Lock()
	e.inflight = false
	p.running--
	p.hosts[e.host].running--
	if requeue {
		if e.refresh {
			e.health.NextDue = p.opts.Now()
		}
		heap.Push(&p.queue, e)
	}
	e.refresh = false
	p.mu.Unlock()
	p.wakeUp()
}

// succeed closes the circuit and schedules the next poll. The timer runs from
// the start of the poll that just ended, so a source keeps its cadence however
// long its polls take. A poll that outlasted its interval is due again at once,
// once, never several times over, but no sooner than a quarter of the interval
// after it ended, so a slow source cannot hammer its host.
func (p *Poller) succeed(h *Health, interval time.Duration, now, started time.Time) {
	h.Failures = 0
	h.LastOK = now
	h.LastError = ""
	h.CircuitOpen = false
	h.Permanent = false
	if started.IsZero() {
		started = now
	}
	next := started.Add(interval + p.jitter(successJitter, interval))
	h.NextDue = next
	if floor := now.Add(interval / 4); floor.After(next) {
		h.NextDue = floor
	}
}

// fail records a failed poll and decides when to try again, by the class of
// the error.
func (p *Poller) fail(h *Health, interval time.Duration, now time.Time, err error) {
	h.Failures++
	h.LastError = err.Error()
	delay := p.backoff(h.Failures, interval)

	var he *httpx.Error
	if errors.As(err, &he) {
		switch he.Class {
		case httpx.Throttled:
			delay = max(he.RetryAfter, delay)
		case httpx.Blocked:
			delay = max(blockedWait, delay)
		case httpx.Permanent:
			h.CircuitOpen = true
			h.Permanent = true
			h.NextDue = now.Add(farFuture)
			return
		}
	}
	if h.Failures >= circuitThreshold {
		h.CircuitOpen = true
		delay = max(delay, p.opts.MaxBackoff) // one probe per MaxBackoff
	}
	h.NextDue = now.Add(delay)
}

// backoff is min(MaxBackoff, base*2^(failures-1)) with 20% jitter either way,
// where base is a quarter of the interval but at least a minute. The result
// never exceeds MaxBackoff.
func (p *Poller) backoff(failures int, interval time.Duration) time.Duration {
	d := max(minBackoffBase, interval/4)
	for i := 1; i < failures && d < p.opts.MaxBackoff; i++ {
		d *= 2
	}
	d = min(d, p.opts.MaxBackoff)
	factor := 1 + backoffJitter*(2*p.opts.Rand()-1)
	return min(time.Duration(float64(d)*factor), p.opts.MaxBackoff)
}

// jitter returns a random duration of up to frac of interval.
func (p *Poller) jitter(frac float64, interval time.Duration) time.Duration {
	return time.Duration(p.opts.Rand() * frac * float64(interval))
}

// interval is how often src is polled at time now.
func (p *Poller) interval(src model.Source, now time.Time) time.Duration {
	if p.opts.IntervalFor != nil {
		if d := p.opts.IntervalFor(src, now); d > 0 {
			return d
		}
	}
	if src.Interval != "" {
		if d, err := time.ParseDuration(src.Interval); err == nil && d > 0 {
			return d
		}
	}
	return p.opts.DefaultInterval
}

// level grades a source's health at time now.
func level(h Health, interval time.Duration, now time.Time) string {
	switch {
	case h.CircuitOpen, h.Failures >= 3, h.LastOK.IsZero() && h.Failures > 0:
		return LevelRed
	case h.Failures > 0, h.LastOK.IsZero(), now.Sub(h.LastOK) > 2*interval:
		return LevelYellow
	}
	return LevelGreen
}

// sleepCtx waits for d or until ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
