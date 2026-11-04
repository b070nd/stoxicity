package channel

import (
	"errors"
	"fmt"
	"net/http"
)

// Settings holds the values the channels need, one field per environment
// variable named in the field comment. The caller reads the environment; this
// package does not.
type Settings struct {
	FileDir    string // STOXICITY_FILE_DIR
	FeedPath   string // STOXICITY_FEED_PATH
	FeedTitle  string // STOXICITY_FEED_TITLE
	FeedURL    string // STOXICITY_FEED_URL
	WebhookURL string // STOXICITY_WEBHOOK_URL
}

// setting pairs a variable name with its value for the empty-value check.
type setting struct {
	name, value string
}

// Build returns the channel for each kind in kinds, keyed by kind, together
// with one error for each kind that could not be built. Other kinds still
// build when one fails. The TUI is skipped without error because it shows hits
// itself. Email is not available yet. A setting that is empty is reported by
// its variable name, never by its value. client is used by the webhook channel
// and may be nil.
//
// The caller fills Settings from these environment variables:
//
//	STOXICITY_FILE_DIR     folder for the file channel
//	STOXICITY_FEED_PATH    Atom file for the feed channel
//	STOXICITY_FEED_TITLE   title of the feed
//	STOXICITY_FEED_URL     public address the feed is read from
//	STOXICITY_WEBHOOK_URL  https address for the webhook channel (a secret)
func Build(kinds []Kind, s Settings, client *http.Client) (map[Kind]Channel, []error) {
	out := map[Kind]Channel{}
	var errs []error
	for _, k := range kinds {
		switch k {
		case TUI:
			// The TUI shows hits itself; nothing to build.
		case Email:
			errs = append(errs, errors.New("email: not available yet: the Gmail route is not decided"))
		case File:
			if e := unset(File, setting{"STOXICITY_FILE_DIR", s.FileDir}); len(e) > 0 {
				errs = append(errs, e...)
				continue
			}
			ch, err := NewFile(s.FileDir)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			out[File] = ch
		case Feed:
			if e := unset(Feed,
				setting{"STOXICITY_FEED_PATH", s.FeedPath},
				setting{"STOXICITY_FEED_TITLE", s.FeedTitle},
				setting{"STOXICITY_FEED_URL", s.FeedURL}); len(e) > 0 {
				errs = append(errs, e...)
				continue
			}
			ch, err := NewFeed(s.FeedPath, s.FeedTitle, s.FeedURL, 0)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			out[Feed] = ch
		case Webhook:
			if e := unset(Webhook, setting{"STOXICITY_WEBHOOK_URL", s.WebhookURL}); len(e) > 0 {
				errs = append(errs, e...)
				continue
			}
			ch, err := NewWebhook(s.WebhookURL, client)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			out[Webhook] = ch
		default:
			errs = append(errs, fmt.Errorf("channel: unknown kind %q", string(k)))
		}
	}
	return out, errs
}

// unset returns one error per setting whose value is empty, naming the
// variable and never its value.
func unset(k Kind, settings ...setting) []error {
	var errs []error
	for _, s := range settings {
		if s.value == "" {
			errs = append(errs, fmt.Errorf("channel: %s: setting not configured: %s", k, s.name))
		}
	}
	return errs
}
