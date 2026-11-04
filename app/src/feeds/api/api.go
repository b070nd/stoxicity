// Package api is the place for licensed JSON news or market-data APIs. It is
// optional: the first release runs on primary sources only.
//
// docs/fetchers.md names the kind (model.KindAPI) and its constraints but does
// not describe a response format, a provider, a cursor or a request budget, so
// no provider is implemented. The Fetcher answers every poll with a permanent
// "not supported" error, which stops the scheduler polling the source until
// the configuration or the code changes. It never reads the environment
// variable named by the source's auth_env, so no credential can leak from it.
//
// A real provider, when one is added, must read its token from the variable
// named by Source.AuthEnv, send it in a header and never in the URL, a log
// line or an error message. Daily request budgets are enforced by the
// scheduler.
package api

import (
	"context"
	"errors"
	"strings"

	"github.com/b070nd/stoxicity/app/src/feeds"
	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/model"
)

// Fetcher is the fetcher for model.KindAPI sources. It implements feeds.Fetcher.
type Fetcher struct {
	client httpx.Client
}

// New returns a Fetcher. The client is kept for the providers to come.
func New(client httpx.Client) *Fetcher { return &Fetcher{client: client} }

// Fetch always fails with a Permanent *httpx.Error saying that no JSON API
// provider is supported yet. It makes no request.
func (f *Fetcher) Fetch(_ context.Context, src model.Source, _ feeds.State) (feeds.Result, error) {
	u := src.URL
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	return feeds.Result{}, &httpx.Error{
		Class: httpx.Permanent,
		URL:   u,
		Err:   errors.New("api: JSON API sources are not supported yet (source " + src.ID + ")"),
	}
}
