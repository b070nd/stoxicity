package api

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/b070nd/stoxicity/app/src/feeds"
	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/model"
)

// failClient fails the test if the fetcher makes a request.
type failClient struct{ t *testing.T }

func (c failClient) Do(context.Context, httpx.Request) (*httpx.Response, error) {
	c.t.Error("api fetcher made a request")
	return nil, errors.New("unexpected request")
}

func TestNotSupported(t *testing.T) {
	const secret = "s3cr3t-token-value"
	t.Setenv("TEST_API_KEY", secret)
	src := model.Source{ID: "paid-news", Kind: model.KindAPI, URL: "https://api.example.com/v1/news?token=" + secret, AuthEnv: "TEST_API_KEY"}
	_, err := New(failClient{t}).Fetch(context.Background(), src, feeds.State{})
	var he *httpx.Error
	if !errors.As(err, &he) || he.Class != httpx.Permanent {
		t.Fatalf("err = %v, want a Permanent *httpx.Error", err)
	}
	if !strings.Contains(err.Error(), "api: ") || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("message %q", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error leaks the credential: %q", err)
	}
}
