package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/feeds"
	"github.com/b070nd/stoxicity/app/src/feeds/registry"
	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/ledger"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/pipeline"
	"github.com/b070nd/stoxicity/app/src/scheduler"
)

// deliverEvery is how often the run command delivers the alerts that quiet
// hours or a cooldown held back, and the digests that have come due.
const deliverEvery = time.Minute

// newFlags returns a flag set whose errors are usage errors.
func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// parseFlags parses args, flags first, and returns the rest.
func parseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	if err := fs.Parse(args); err != nil {
		return nil, usageErrorf("%v", err)
	}
	return fs.Args(), nil
}

// parseMixed parses args where flags may come before or after the positional
// arguments, and returns the positional ones.
func parseMixed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		rest, err := parseFlags(fs, args)
		if err != nil {
			return nil, err
		}
		if len(rest) == 0 {
			return pos, nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

// freshStates forgets when each source is next due and any open circuit, so a
// poll that the user asked for happens now. The saved fetch state stays.
type freshStates struct{ scheduler.StateStore }

func (f freshStates) LoadSource(ctx context.Context, id string) (feeds.State, scheduler.Health, error) {
	st, h, err := f.StateStore.LoadSource(ctx, id)
	h.NextDue = time.Time{}
	h.CircuitOpen = false
	h.Permanent = false
	return st, h, err
}

// poller builds the scheduler over srcs, with the real fetchers and the
// pipeline as its sink. now makes every source due at once.
func (a *app) poller(srcs []model.Source, pipe *pipeline.Pipeline, now bool) (*scheduler.Poller, error) {
	db, err := a.store()
	if err != nil {
		return nil, err
	}
	client, err := a.httpClient()
	if err != nil {
		return nil, err
	}
	var states scheduler.StateStore = db
	opts := scheduler.Options{
		Sources:         srcs,
		Fetchers:        registry.New(client, registry.Options{Now: a.opts.now}),
		Sink:            pipe,
		DefaultInterval: a.cfg.FetchInterval(),
		MaxBackoff:      a.cfg.MaxBackoff(),
		Now:             a.opts.now,
		Rand:            a.opts.rand,
		Logger:          a.log,
		IntervalFor:     a.cfg.IntervalFor,
	}
	if now {
		states = freshStates{db}
		opts.Rand = func() float64 { return 0 } // no start-up jitter
	}
	opts.States = states
	p, err := scheduler.New(opts)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// sourcesToPoll returns the sources a command line names, or all enabled ones.
// A source that is named is polled even if it is disabled.
func (a *app) sourcesToPoll(ids []string) ([]model.Source, error) {
	if len(ids) == 0 {
		return a.reg.Enabled(), nil
	}
	var out []model.Source
	for _, id := range ids {
		src, ok := a.reg[id]
		if !ok {
			return nil, usageErrorf("unknown source %q (see: stoxicity sources list)", id)
		}
		if slices.ContainsFunc(out, func(s model.Source) bool { return s.ID == id }) {
			continue
		}
		src.Enabled = true
		out = append(out, src)
	}
	return out, nil
}

// cmdFetch polls the sources once, prints what each one found and exits.
func cmdFetch(ctx context.Context, a *app, args []string) error {
	srcs, err := a.sourcesToPoll(args)
	if err != nil {
		return err
	}
	if len(srcs) == 0 {
		fmt.Fprintln(a.stdout, "no enabled sources: set enabled: true in sources.yaml, or name a source")
		return nil
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	p, err := a.poller(srcs, a.pipeline(db), true)
	if err != nil {
		return err
	}
	led := a.ledger(db, a.channels())

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.Run(runCtx) }()

	results := make(map[string]scheduler.Event, len(srcs))
	for len(results) < len(srcs) {
		select {
		case ev := <-p.Events():
			if _, seen := results[ev.SourceID]; !seen {
				results[ev.SourceID] = ev
			}
		case err := <-done:
			if err == nil {
				err = errors.New("interrupted")
			}
			return err
		}
	}
	cancel()
	if err := <-done; err != nil {
		return err
	}

	failed := 0
	for _, src := range srcs {
		ev := results[src.ID]
		if ev.Err != nil {
			failed++
			fmt.Fprintf(a.stdout, "%-24s error (%s): %s\n", src.ID, errorClass(ev.Err), oneLine(ev.Err.Error()))
			continue
		}
		fmt.Fprintf(a.stdout, "%-24s %d new\n", src.ID, ev.New)
	}
	a.evaluateAll(ctx, led)
	if failed > 0 {
		return fmt.Errorf("%d of %d sources failed", failed, len(srcs))
	}
	return nil
}

// cmdRun polls the enabled sources until the context ends, printing one line
// for every poll.
func cmdRun(ctx context.Context, a *app, args []string) error {
	if len(args) > 0 {
		return usageErrorf("takes no arguments")
	}
	if !a.cfg.Refresh.Enabled {
		return errors.New("refresh.enabled is false in config.yaml")
	}
	srcs := a.reg.Enabled()
	if len(srcs) == 0 {
		return errors.New("no enabled sources: set enabled: true in sources.yaml")
	}
	db, err := a.store()
	if err != nil {
		return err
	}
	p, err := a.poller(srcs, a.pipeline(db), false)
	if err != nil {
		return err
	}
	led := a.ledger(db, a.channels())

	every := a.opts.tick
	if every <= 0 {
		every = deliverEvery
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	fmt.Fprintf(a.stdout, "polling %d sources; interrupt to stop\n", len(srcs))
	for {
		select {
		case ev := <-p.Events():
			a.printEvent(ev)
			if ev.Err == nil && ev.New > 0 {
				a.evaluateAll(ctx, led)
			}
		case <-tick.C:
			a.deliverDue(ctx, led)
		case err := <-done:
			for {
				select {
				case ev := <-p.Events():
					a.printEvent(ev)
					continue
				default:
				}
				break
			}
			if err == nil {
				fmt.Fprintln(a.stdout, "stopped")
			}
			return err
		}
	}
}

// printEvent prints the result of one poll on one line.
func (a *app) printEvent(ev scheduler.Event) {
	at := a.now().In(a.location()).Format(time.TimeOnly)
	if ev.Err != nil {
		fmt.Fprintf(a.stdout, "%s %-24s error (%s): %s\n", at, ev.SourceID, errorClass(ev.Err), oneLine(ev.Err.Error()))
		return
	}
	fmt.Fprintf(a.stdout, "%s %-24s %d new\n", at, ev.SourceID, ev.New)
}

// evaluateAll records the new watch hits of every tenant and delivers their
// alerts. A tenant or a step that fails is reported on stderr and does not stop
// the others.
func (a *app) evaluateAll(ctx context.Context, led *ledger.Ledger) {
	tenants, err := a.tenants().List()
	if err != nil {
		fmt.Fprintf(a.stderr, "stoxicity: evaluate watches: %v\n", err)
		return
	}
	for _, t := range tenants {
		if _, err := led.Evaluate(ctx, t.ID); err != nil {
			fmt.Fprintf(a.stderr, "stoxicity: evaluate watches of %s: %v\n", t.ID, err)
		}
		a.deliver(ctx, led, t.ID)
	}
}

// deliver sends the pending alerts of one tenant and reports a failure on
// stderr.
func (a *app) deliver(ctx context.Context, led *ledger.Ledger, tenantID string) {
	if _, err := led.Deliver(ctx, tenantID); err != nil {
		fmt.Fprintf(a.stderr, "stoxicity: deliver alerts of %s: %v\n", tenantID, err)
	}
}

// deliverDue sends the digests that have come due and the alerts that quiet
// hours or a cooldown held back, for every tenant.
func (a *app) deliverDue(ctx context.Context, led *ledger.Ledger) {
	tenants, err := a.tenants().List()
	if err != nil {
		fmt.Fprintf(a.stderr, "stoxicity: deliver: %v\n", err)
		return
	}
	for _, t := range tenants {
		if _, err := led.Digests(ctx, t.ID); err != nil {
			fmt.Fprintf(a.stderr, "stoxicity: send digests of %s: %v\n", t.ID, err)
		}
		a.deliver(ctx, led, t.ID)
	}
}

// errorClass names the kind of a fetch error.
func errorClass(err error) string {
	var he *httpx.Error
	if errors.As(err, &he) {
		return he.Class.String()
	}
	return "error"
}

// oneLine replaces line breaks and tabs with spaces, so a value fits one line
// of output.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
