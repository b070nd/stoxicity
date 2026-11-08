package httpx_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/quota"
	"github.com/b070nd/stoxicity/app/src/store"
)

type quotaTransport func(*http.Request) (*http.Response, error)

func (f quotaTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func quotaResponse(status int, header http.Header) *http.Response {
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader("fixture"))}
}

func TestQuotaCountsRetriesRedirectsAndConditionalResponses(t *testing.T) {
	for _, scenario := range []string{"retry", "redirect"} {
		t.Run(scenario, func(t *testing.T) {
			db, err := store.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			now := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
			hits := 0
			c, err := httpx.New(httpx.Config{
				UserAgent: "test", DefaultRate: 10000, Burst: 100, Rand: func() float64 { return 0 },
				Now: func() time.Time { return now }, QuotaStore: db,
				Groups: map[string]string{"a.test": "provider", "b.test": "provider"},
				Quotas: map[string]quota.Limit{"provider": {Requests: 2, Period: time.Hour}},
				Transport: quotaTransport(func(r *http.Request) (*http.Response, error) {
					hits++
					if scenario == "retry" {
						return quotaResponse(500, nil), nil
					}
					if hits == 1 {
						return quotaResponse(302, http.Header{"Location": {"https://b.test/target"}}), nil
					}
					return quotaResponse(304, nil), nil
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.Do(context.Background(), httpx.Request{URL: "https://a.test/start?apiKey=never-print-this"})
			if scenario == "redirect" {
				if err != nil {
					t.Fatal(err)
				}
				_, err = c.Do(context.Background(), httpx.Request{URL: "https://a.test/again"})
			}
			var full *quota.Exhausted
			var he *httpx.Error
			if !errors.As(err, &full) || !errors.As(err, &he) || he.Class != httpx.Throttled || hits != 2 {
				t.Fatalf("hits=%d err=%v", hits, err)
			}
			if strings.Contains(err.Error(), "never-print-this") {
				t.Fatal("query credential leaked")
			}
		})
	}
}

func TestQuotaPersistsLongRetryAfterAndIsolatesProviders(t *testing.T) {
	for _, tc := range []struct {
		header string
		status int
	}{
		{"86400", 429}, {"Mon, 09 Nov 2026 10:00:00 GMT", 429}, {"bad-value", 429}, {"86400", 503},
	} {
		header := tc.header
		t.Run(http.StatusText(tc.status)+"/"+header, func(t *testing.T) {
			dir := t.TempDir()
			now := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
			wait := 24 * time.Hour
			if header == "bad-value" {
				wait = time.Minute
			}
			hits := 0
			cfg := httpx.Config{UserAgent: "test", Now: func() time.Time { return now }, DefaultRate: 10000, Burst: 100,
				Transport: quotaTransport(func(r *http.Request) (*http.Response, error) {
					hits++
					if hits == 1 {
						return quotaResponse(tc.status, http.Header{"Retry-After": {header}}), nil
					}
					return quotaResponse(200, nil), nil
				})}
			db, err := store.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			cfg.QuotaStore = db
			c, err := httpx.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.Do(context.Background(), httpx.Request{URL: "https://a.test/"})
			var full *quota.Exhausted
			if !errors.As(err, &full) || !full.Until.Equal(now.Add(wait)) {
				t.Fatalf("retry: %v", err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = store.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			cfg.QuotaStore = db
			c, err = httpx.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.Do(context.Background(), httpx.Request{URL: "https://a.test/other"})
			if !errors.As(err, &full) || hits != 1 {
				t.Fatalf("cooldown bypassed after restart: hits=%d err=%v", hits, err)
			}
			if _, err := c.Do(context.Background(), httpx.Request{URL: "https://b.test/"}); err != nil {
				t.Fatal(err)
			}
			now = now.Add(wait)
			if _, err := c.Do(context.Background(), httpx.Request{URL: "https://a.test/"}); err != nil {
				t.Fatal(err)
			}
			if hits != 3 {
				t.Fatalf("got %d requests", hits)
			}
		})
	}
}

func TestQuotaAccountingFailureNeverSendsARequest(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	hits := 0
	c, err := httpx.New(httpx.Config{UserAgent: "test", QuotaStore: db, Transport: quotaTransport(func(r *http.Request) (*http.Response, error) {
		hits++
		return quotaResponse(200, nil), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Do(context.Background(), httpx.Request{URL: "https://a.test/"})
	if err == nil || hits != 0 || !strings.Contains(err.Error(), "quota accounting unavailable") {
		t.Fatalf("hits=%d err=%v", hits, err)
	}
}

func TestQuotaRechecksThrottleAfterRateLimitWait(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	other, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	now := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	hits, sleeps := 0, 0
	c, err := httpx.New(httpx.Config{
		UserAgent: "test", DefaultRate: 1, Burst: 1, Now: func() time.Time { return now }, QuotaStore: db,
		Quotas: map[string]quota.Limit{"a.test": {Requests: 3, Period: time.Hour}},
		Sleep: func(ctx context.Context, d time.Duration) error {
			sleeps++
			// Another worker observes a throttle while our request is queued.
			return other.BlockQuota(ctx, "a.test", now.Add(24*time.Hour), 429)
		},
		Transport: quotaTransport(func(r *http.Request) (*http.Response, error) { hits++; return quotaResponse(200, nil), nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Do(context.Background(), httpx.Request{URL: "https://a.test/"}); err != nil {
		t.Fatal(err)
	}
	_, err = c.Do(context.Background(), httpx.Request{URL: "https://a.test/"})
	var full *quota.Exhausted
	if !errors.As(err, &full) || hits != 1 || sleeps != 1 {
		t.Fatalf("queued request bypassed cooldown: hits=%d sleeps=%d err=%v", hits, sleeps, err)
	}
	states, err := db.QuotaStates(context.Background())
	if err != nil || len(states) != 1 || states[0].Used != 2 {
		t.Fatalf("recheck charged twice: %+v %v", states, err)
	}
}

func TestQuotaClockDoesNotReplaceRateLimiterClock(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	clock := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	quotaClock := clock.Add(-24 * time.Hour)
	var waited time.Duration
	c, err := httpx.New(httpx.Config{
		UserAgent: "test", DefaultRate: 1, Burst: 1, Now: func() time.Time { return clock },
		Sleep:     func(ctx context.Context, d time.Duration) error { clock = clock.Add(d); waited += d; return nil },
		Transport: quotaTransport(func(r *http.Request) (*http.Response, error) { return quotaResponse(200, nil), nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	c, err = c.WithQuotas(db, map[string]quota.Limit{"a.test": {Requests: 3, Period: time.Hour}}, func() time.Time { return quotaClock })
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := c.Do(context.Background(), httpx.Request{URL: "https://a.test/"}); err != nil {
			t.Fatal(err)
		}
	}
	if waited != 2*time.Second {
		t.Fatalf("rate limiter clock replaced: waited %s, want 2s", waited)
	}
	states, err := db.QuotaStates(context.Background())
	if err != nil || len(states) != 1 || !states[0].Reset.Equal(quotaClock.Add(time.Hour)) {
		t.Fatalf("quota clock lost: %+v %v", states, err)
	}
}
