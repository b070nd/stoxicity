package channel

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildAllConfigured(t *testing.T) {
	dir := t.TempDir()
	s := Settings{
		FileDir:    filepath.Join(dir, "notes"),
		FeedPath:   filepath.Join(dir, "feed.xml"),
		FeedTitle:  "Acme news",
		FeedURL:    "https://feeds.example.com/acme.xml",
		WebhookURL: "https://hooks.example.com/" + secretPart,
	}
	chans, errs := Build([]Kind{TUI, File, Feed, Webhook}, s, nil)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(chans) != 3 {
		t.Fatalf("built %d channels, want 3", len(chans))
	}
	for _, k := range []Kind{File, Feed, Webhook} {
		ch, ok := chans[k]
		if !ok || ch.Kind() != k {
			t.Errorf("channel for %s is missing or has the wrong kind", k)
		}
	}
	if _, ok := chans[TUI]; ok {
		t.Error("TUI should not be built")
	}
}

func TestBuildErrorsAndSkips(t *testing.T) {
	cases := []struct {
		name  string
		kinds []Kind
		s     Settings
		want  []string // substrings of the errors, in order; nil for none
	}{
		{"tui is skipped", []Kind{TUI}, Settings{}, nil},
		{"file not configured", []Kind{File}, Settings{}, []string{"channel: file: setting not configured: STOXICITY_FILE_DIR"}},
		{"webhook not configured", []Kind{Webhook}, Settings{}, []string{"channel: webhook: setting not configured: STOXICITY_WEBHOOK_URL"}},
		{"feed names each empty setting", []Kind{Feed}, Settings{FeedURL: "https://feeds.example.com/acme.xml"},
			[]string{"STOXICITY_FEED_PATH", "STOXICITY_FEED_TITLE"}},
		{"email names each empty setting", []Kind{Email}, Settings{},
			[]string{"STOXICITY_GMAIL_CLIENT_ID", "STOXICITY_MAIL_FROM", "STOXICITY_MAIL_TO", `run "stoxicity gmail auth"`}},
		{"unknown kind", []Kind{Kind("sms")}, Settings{}, []string{`channel: unknown kind "sms"`}},
		{"webhook over plain http", []Kind{Webhook}, Settings{WebhookURL: "http://hooks.example.com/" + secretPart}, []string{"webhook: http is allowed only"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			chans, errs := Build(c.kinds, c.s, nil)
			if len(chans) != 0 {
				t.Errorf("built %d channels, want none", len(chans))
			}
			if len(errs) != len(c.want) {
				t.Fatalf("got %d errors %v, want %d", len(errs), errs, len(c.want))
			}
			for i, err := range errs {
				if !strings.Contains(err.Error(), c.want[i]) {
					t.Errorf("error %q does not contain %q", err, c.want[i])
				}
				if strings.Contains(err.Error(), secretPart) {
					t.Errorf("error names the secret: %v", err)
				}
			}
		})
	}
}

func TestBuildContinuesPastFailures(t *testing.T) {
	dir := t.TempDir()
	chans, errs := Build([]Kind{Email, File, Webhook}, Settings{FileDir: filepath.Join(dir, "notes")}, nil)
	if len(errs) != 5 {
		t.Errorf("errors = %v, want the four email errors and the webhook error", errs)
	}
	if _, ok := chans[File]; !ok {
		t.Error("file channel was not built beside the failures")
	}
	if len(chans) != 1 {
		t.Errorf("built %d channels, want 1", len(chans))
	}
}
