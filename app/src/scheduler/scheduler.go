// Package scheduler polls sources in the background. Each source keeps its own
// next-due time, ETag and Last-Modified values, failure count and backoff (with
// jitter), and a circuit breaker; requests to one host are rate limited by
// package httpx. Every change is announced on Events so the TUI can refresh without polling.
package scheduler

import (
	"context"
	"time"

	"github.com/b070nd/stoxicity/app/src/feeds"
	"github.com/b070nd/stoxicity/app/src/model"
)

// StateStore keeps what the scheduler remembers about each source across
// restarts. package store implements it. An unknown source loads as the zero
// State and zero Health with a nil error.
type StateStore interface {
	LoadSource(ctx context.Context, sourceID string) (feeds.State, Health, error)
	SaveSource(ctx context.Context, sourceID string, st feeds.State, h Health) error
}

// Sink receives what a poll found. The ingest pipeline implements it. It
// returns how many of the articles were new. The scheduler saves a source's
// State only after Ingest returned without error.
type Sink interface {
	Ingest(ctx context.Context, src model.Source, articles []model.Article) (int, error)
}

// Health levels, as in Health.Level.
const (
	LevelGreen  = "GREEN"
	LevelYellow = "YELLOW"
	LevelRed    = "RED"
)

// Health is the state of one source, shown in the sources view.
type Health struct {
	SourceID    string
	LastOK      time.Time
	LastError   string
	Failures    int
	NextDue     time.Time
	CircuitOpen bool

	// Permanent marks a circuit opened by a permanent error (404, 410, parse
	// failure). Nothing retries it; RefreshNow or a restart clears it.
	Permanent bool
	// Level is LevelGreen, LevelYellow or LevelRed. Green: the last poll
	// succeeded and LastOK is within twice the interval. Yellow: one or two
	// failures, or no success yet, or no success for more than twice the
	// interval while nothing is failing. Red: three or more failures, an open
	// circuit, or failures and never a success.
	Level string
}

// Event says what changed after a poll.
type Event struct {
	SourceID string
	New      int // articles stored for the first time
	Err      error
}

// Scheduler runs until its context ends.
type Scheduler interface {
	Run(ctx context.Context) error
	// Events receives one Event per finished poll.
	Events() <-chan Event
	Health() []Health
	// RefreshNow polls a source ahead of schedule; empty means all of them.
	RefreshNow(sourceID string)
}
