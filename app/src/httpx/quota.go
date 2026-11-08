package httpx

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/quota"
)

// WithQuotas returns a new client with the same transport/settings and durable
// accounting. It does not copy mutexes or mutate a client already used elsewhere.
func (h *HTTP) WithQuotas(store quota.Store, limits map[string]quota.Limit, now func() time.Time) (*HTTP, error) {
	cfg := h.cfg
	cfg.QuotaStore, cfg.Quotas = store, limits
	if now != nil {
		cfg.QuotaNow = now
	}
	return New(cfg)
}

func (h *HTTP) quotaNow() time.Time {
	if h.cfg.QuotaNow != nil {
		return h.cfg.QuotaNow()
	}
	return h.cfg.Now()
}

func (h *HTTP) groupKey(host string) string {
	key := strings.ToLower(host)
	if group, ok := h.cfg.Groups[key]; ok {
		return group
	}
	return key
}

func (h *HTTP) persistThrottle(ctx context.Context, u *url.URL, status int, retryAfter string) error {
	until := quotaRetryAt(retryAfter, h.quotaNow())
	key := h.groupKey(u.Hostname())
	err := h.cfg.QuotaStore.BlockQuota(ctx, key, until, status)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	cause := &quota.Exhausted{Provider: key, Until: until, Reason: "provider throttled requests"}
	return newError(Throttled, status, max(0, until.Sub(h.quotaNow())), u, errors.Join(cause, err))
}

func (h *HTTP) quotaError(ctx context.Context, u *url.URL, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		return nil
	}
	var exhausted *quota.Exhausted
	if errors.As(err, &exhausted) {
		return newError(Throttled, 0, max(0, exhausted.Until.Sub(h.quotaNow())), u, err)
	}
	return newError(Blocked, 0, 0, u, fmt.Errorf("quota accounting unavailable: %w", err))
}

// Unlike the short in-memory sleep cap, a durable provider cooldown must not
// shorten a valid long Retry-After (for example a daily quota). Missing or
// malformed values use the existing one-minute fallback, not a guessed reset.
func quotaRetryAt(value string, now time.Time) time.Time {
	if at, ok := retryAfterAt(value, now); ok && at.After(now) {
		return at
	}
	return now.Add(defaultThrottle)
}

func retryAfterAt(value string, now time.Time) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value != "" && strings.Trim(value, "0123456789") == "" {
		n, err := strconv.ParseUint(value, 10, 64)
		if err != nil || n > uint64((1<<63-1)/time.Second) {
			return now.Add(1<<63 - 1), true
		}
		return now.Add(time.Duration(n) * time.Second), true
	}
	if at, err := http.ParseTime(value); err == nil && !at.Before(now) {
		return at, true
	}
	return time.Time{}, false
}
