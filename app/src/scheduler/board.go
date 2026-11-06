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
	Until    time.Time // Resting only
}

// Board is the work in progress at one instant: what is running, what is
// waiting, and how many sources are simply idle until their timers run out.
type Board struct {
	At   time.Time
	Rows []Activity // running first, then the rest in the order they became due
	Idle int
	// Next and NextDue are the idle source that is due soonest.
	Next    string
	NextDue time.Time
}

// Board returns the work in progress now. A source that has finished leaves
// the board and is idle until its next timer runs out.
func (p *Poller) Board() Board {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.opts.Now()
	b := Board{At: now}
	for _, e := range p.entries {
		hs := p.hosts[e.host]
		switch {
		case e.inflight:
			b.Rows = append(b.Rows, Activity{SourceID: e.src.ID, Host: e.host, State: Running, Since: e.started})
			if due := e.started.Add(p.interval(e.src, now)); !due.After(now) {
				b.Rows = append(b.Rows, Activity{SourceID: e.src.ID, Host: e.host, State: Late, Since: due})
			}
		case e.index >= 0 && !e.health.NextDue.After(now):
			a := Activity{SourceID: e.src.ID, Host: e.host, State: Waiting, Since: e.health.NextDue}
			if hs.until.After(now) {
				a.State, a.Until = Resting, hs.until
			}
			b.Rows = append(b.Rows, a)
		default:
			b.Idle++
			if !e.health.CircuitOpen && (b.Next == "" || e.health.NextDue.Before(b.NextDue)) {
				b.Next, b.NextDue = e.src.ID, e.health.NextDue
			}
		}
	}
	sort.SliceStable(b.Rows, func(i, j int) bool {
		a, c := b.Rows[i], b.Rows[j]
		if (a.State == Running) != (c.State == Running) {
			return a.State == Running
		}
		if !a.Since.Equal(c.Since) {
			return a.Since.Before(c.Since)
		}
		return a.SourceID < c.SourceID
	})
	return b
}
