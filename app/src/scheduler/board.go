package scheduler

import (
	"sort"
	"time"
)

// State of a source on the Board.
type State string

const (
	// Running: a poll is in flight. Since is when it began.
	Running State = "running"
	// Waiting: the source is due but waits for a free worker or for another poll
	// of its host to end. Since is when it became due.
	Waiting State = "waiting"
	// Resting: the source is due but its host is cooling down after trouble.
	// Until is when the host may be tried again.
	Resting State = "resting"
	// Idle: nothing to do until the source's timer runs out. Until is when.
	Idle State = "idle"
	// Late: the source's timer ran out while its poll was still running. It
	// starts again as soon as that poll ends (never two at once). Since is when
	// the timer ran out.
	Late State = "late"
)

// Activity is one row of the Board: a source that has work to do or is doing it.
type Activity struct {
	SourceID string
	Host     string
	State    State
	Since    time.Time
	// Until is when a resting host may be tried again (Resting), or when an
	// idle source is due (Idle).
	Until time.Time

	// What the last polls came to; set on every row.
	LastOK    time.Time // zero before the first good poll
	LastNew   int       // articles stored by that poll
	Failures  int       // failed polls in a row
	LastError string
	// Parked: nothing will poll the source until it is refreshed (a 404, a parse
	// failure).
	Parked bool
}

// Board is every source at one instant: first those with work (running, then
// the rest in the order they became due), then the idle ones in the order their
// timers run out.
type Board struct {
	At   time.Time
	Rows []Activity
	Idle int // how many of the rows are idle
	// Workers is the most polls that may run at once (Options.Workers).
	Workers int
	// Next and NextDue are the idle source that is due soonest.
	Next    string
	NextDue time.Time
}

// Board returns every source with what it is doing now. A source that has
// finished is idle, with the time its next timer runs out.
func (p *Poller) Board() Board {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.opts.Now()
	b := Board{At: now, Workers: p.opts.Workers}
	for _, e := range p.entries {
		hs := p.hosts[e.host]
		row := func(state State, since time.Time) Activity {
			return Activity{
				SourceID: e.src.ID, Host: e.host, State: state, Since: since,
				LastOK: e.health.LastOK, LastNew: e.lastNew, Failures: e.health.Failures,
				LastError: e.health.LastError, Parked: e.health.Permanent,
			}
		}
		switch {
		case e.inflight:
			b.Rows = append(b.Rows, row(Running, e.started))
			if due := e.started.Add(p.interval(e.src, now)); !due.After(now) {
				b.Rows = append(b.Rows, row(Late, due))
			}
		case e.index >= 0 && !e.health.NextDue.After(now):
			a := row(Waiting, e.health.NextDue)
			if hs.until.After(now) {
				a.State, a.Until = Resting, hs.until
			}
			b.Rows = append(b.Rows, a)
		default:
			a := row(Idle, e.health.NextDue)
			a.Until = e.health.NextDue
			b.Rows = append(b.Rows, a)
			b.Idle++
			if !e.health.CircuitOpen && (b.Next == "" || e.health.NextDue.Before(b.NextDue)) {
				b.Next, b.NextDue = e.src.ID, e.health.NextDue
			}
		}
	}
	rank := func(a Activity) int {
		switch a.State {
		case Running:
			return 0
		case Idle:
			return 2
		}
		return 1
	}
	sort.SliceStable(b.Rows, func(i, j int) bool {
		a, c := b.Rows[i], b.Rows[j]
		if rank(a) != rank(c) {
			return rank(a) < rank(c)
		}
		if !a.Since.Equal(c.Since) {
			return a.Since.Before(c.Since)
		}
		return a.SourceID < c.SourceID
	})
	return b
}
