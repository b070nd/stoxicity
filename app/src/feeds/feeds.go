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
}

// Fetcher reads one source.
type Fetcher interface {
	// Fetch polls src. It returns an error only when nothing useful was read;
	// a partial read returns what it has and the State to resume from.
	Fetch(ctx context.Context, src model.Source, prev State) (Result, error)
}
