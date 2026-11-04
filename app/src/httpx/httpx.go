// Package httpx is the one HTTP client every fetcher uses, so timeouts,
// the User-Agent, retries and per-host rate limits live in one place.
//
// The client follows the pattern of cvewatch's httpx gate: one rate limiter
// and cooldown per host group, Retry-After honoured, a cooldown after a 429,
// private and loopback addresses refused (SSRF) at dial time, response bodies
// capped, and no secrets in errors. Reading robots.txt is left to the callers
// that need it.
package httpx

import (
	"errors"
	"fmt"
	"strings"
)

// UserAgent builds the User-Agent header. Contact is an email address or an
// https URL of the person running the program. The SEC asks automated clients
// to identify themselves with contact details, limits each to 10 requests per
// second, and blocks those that do not comply, so there is no default: a
// program that cannot say who runs it must not start fetching.
func UserAgent(app, version, contact string) (string, error) {
	contact = strings.TrimSpace(contact)
	switch {
	case contact == "":
		return "", errors.New("httpx: fetch.contact is empty; set an email address or URL so sources can reach you")
	case strings.ContainsAny(contact, "\r\n\x00() "):
		return "", fmt.Errorf("httpx: contact %q must not contain spaces, parentheses or control characters", contact)
	case !strings.Contains(contact, "@") && !strings.HasPrefix(contact, "https://"):
		return "", fmt.Errorf("httpx: contact %q must be an email address or an https URL", contact)
	}
	return fmt.Sprintf("%s/%s (%s)", app, version, contact), nil
}
