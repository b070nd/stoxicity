// Package feeds turns a model.Source into model.Articles. One subpackage per
// source kind implements Fetcher: rss (RSS and Atom feeds), edgar (SEC
// filings) and api (licensed JSON APIs). The design is in docs/fetchers.md.
//
// A fetcher does no scheduling, rate limiting or storage. The scheduler decides
// when to poll and hands the fetcher the State it returned last time; all HTTP
// goes through package httpx; articles go through package pipeline.
package feeds

import (
	"context"

	"github.com/b070nd/stoxicity/app/src/model"
)

// State is what a fetcher remembers about one source between polls. The
// scheduler stores it; the fetcher only reads it and returns the next one.
type State struct {
	ETag         string // validators for conditional GET, where the source sends them
	LastModified string
	Seen         []string // newest article IDs already stored; a paged read stops at the first of them
	Cursor       string   // source-specific position, for example a "start" offset or a date
}

// Result is one poll of one source.
type Result struct {
	Articles    []model.Article // new articles only; already sanitized
	State       State           // pass back on the next poll
	NotModified bool            // the source answered 304; Articles is empty
	// Partial, when not empty, says why the read is incomplete although it
	// succeeded: a part of the source failed, or more is waiting than one poll
	// reads. The poller reports it; it is not an error.
	Partial string
	// Commit, when set, records progress the fetcher keeps outside State (for a
	// pool, the validators and seen items of each feed). The caller runs it only
	// after Articles are durably stored; if storing fails it is not run, so the
	// next poll reads the same items again instead of finding them "seen".
	Commit func(ctx context.Context) error
}

// Fetcher reads one source.
type Fetcher interface {
	// Fetch polls src. It returns an error only when nothing useful was read;
	// a partial read returns what it has and the State to resume from.
	Fetch(ctx context.Context, src model.Source, prev State) (Result, error)
}
