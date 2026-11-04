package httpx

import "testing"

func TestUserAgent(t *testing.T) {
	got, err := UserAgent("stoxicity", "v1.0.0", "me@example.com")
	if err != nil || got != "stoxicity/v1.0.0 (me@example.com)" {
		t.Fatalf("got %q, %v", got, err)
	}
	if got, err := UserAgent("stoxicity", "dev", "https://example.com/contact"); err != nil || got == "" {
		t.Errorf("an https URL is a valid contact: %q, %v", got, err)
	}
	for _, bad := range []string{"", "  ", "nobody", "http://example.com", "a@b.c\r\nX-Evil: 1", "(a@b.c)", "a b@c.d"} {
		if got, err := UserAgent("stoxicity", "dev", bad); err == nil {
			t.Errorf("contact %q accepted as %q", bad, got)
		}
	}
}
