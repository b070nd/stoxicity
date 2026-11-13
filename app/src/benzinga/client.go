package benzinga

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
)

// RatingsURL is Massive's endpoint for Benzinga's analyst ratings. Massive was
// called Polygon.io and its API still answers on the old host, which is also the
// one the request quota of the price fetchers is kept under.
const RatingsURL = "https://api.polygon.io/benzinga/v1/ratings"

// The most records one answer holds, and the most pages of one request that are
// followed through the next-page address.
const (
	ratingsLimit = 50000
	ratingsPages = 20
)

// Client asks Massive for Benzinga's ratings.
type Client struct {
	HTTP httpx.Client
	// Key is the Massive key. It is added to the request only when it is made,
	// and is never part of an address that is printed or put in an error.
	Key string
	// URL replaces RatingsURL; tests point it at a server.
	URL string
}

func (c Client) base() string {
	if c.URL != "" {
		return c.URL
	}
	return RatingsURL
}

// RequestURL is the address of the request for the ratings issued on one day
// (YYYY-MM-DD), without the key, so that it can be shown. ticker may be empty
// for every stock. Massive spells a class share BRK.B where the program may hold
// BRK-B.
func (c Client) RequestURL(day, ticker string) string {
	q := url.Values{"date": {day}, "limit": {strconv.Itoa(ratingsLimit)}}
	if t := strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(ticker)), "-", "."); t != "" {
		q.Set("ticker", t)
	}
	return c.base() + "?" + q.Encode()
}

// Ratings returns the records issued on one day (YYYY-MM-DD, the date Benzinga
// states), of one ticker or, when ticker is empty, of every stock, ordered by
// date, time and record number so that the result does not depend on the order
// Massive answers in. It follows the next-page address, which must stay on the
// host of the first request: the key goes with every request.
func (c Client) Ratings(ctx context.Context, day, ticker string) ([]Rating, error) {
	if c.Key == "" {
		return nil, errors.New("benzinga: Massive needs an API key (MASSIVE_API_KEY or STOXICITY_POLYGON_KEY)")
	}
	if _, err := time.Parse(time.DateOnly, day); err != nil {
		return nil, fmt.Errorf("benzinga: %q is not a day in the form YYYY-MM-DD", day)
	}
	first, err := url.Parse(c.RequestURL(day, ticker))
	if err != nil {
		return nil, fmt.Errorf("benzinga: the request address is not valid: %w", err)
	}
	var out []Rating
	next := first.String()
	for page := 0; next != ""; page++ {
		if page == ratingsPages {
			return out, fmt.Errorf("benzinga: %s has more ratings than %d pages hold", day, ratingsPages)
		}
		resp, err := c.HTTP.Do(ctx, httpx.Request{URL: withKey(next, c.Key)})
		if err != nil {
			return out, err
		}
		var ans struct {
			Results []Rating `json:"results"`
			Next    string   `json:"next_url"`
		}
		if err := json.Unmarshal(resp.Body, &ans); err != nil {
			return out, fmt.Errorf("benzinga: read the ratings answer for %s: %w", day, err)
		}
		out = append(out, ans.Results...)
		next = ans.Next
		if next == "" {
			break
		}
		u, err := url.Parse(next)
		if err != nil || u.Host != first.Host || u.Scheme != first.Scheme {
			return out, fmt.Errorf("benzinga: the next page of the ratings for %s is on another host, not followed", day)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Date != b.Date {
			return a.Date < b.Date
		}
		if a.Time != b.Time {
			return a.Time < b.Time
		}
		return a.ID < b.ID
	})
	return out, nil
}

// withKey adds the key to an address that does not carry it; the next-page
// address Massive gives does not.
func withKey(address, key string) string {
	u, err := url.Parse(address)
	if err != nil {
		return address
	}
	q := u.Query()
	if q.Get("apiKey") == "" {
		q.Set("apiKey", key)
		u.RawQuery = q.Encode()
	}
	return u.String()
}
