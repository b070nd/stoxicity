package httpx

import "testing"

func TestLooksLikeHTML(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"empty", "", false},
		{"doctype", "<!DOCTYPE html><html></html>", true},
		{"doctype lower", "<!doctype html>", true},
		{"html tag", "<HTML lang=en>", true},
		{"leading space", "  \r\n\t<html>", true},
		{"bom", "\xef\xbb\xbf<!doctype html>", true},
		{"bom and space", "\xef\xbb\xbf \n<html>", true},
		{"html after prolog", "<?xml version=\"1.0\"?>\n<!-- x --><html><body>", true},
		{"body within 512", "<head></head><BODY>", true},
		{"rate limit page", "<?php ?>\n<title>Request Rate Threshold Exceeded</title><body>Your request rate</body>", true},
		{"html beyond 512", string(make([]byte, 600)) + "<html>", false},
		{"rss", "<?xml version=\"1.0\"?><rss version=\"2.0\"><channel></channel></rss>", false},
		{"atom", "<feed xmlns=\"http://www.w3.org/2005/Atom\"></feed>", false},
		{"json", `{"html": "<b>"}`, false},
		{"text", "just text", false},
		{"doctype xml", "<!DOCTYPE rss>", false},
	}
	for _, tc := range tests {
		if got := LooksLikeHTML([]byte(tc.body)); got != tc.want {
			t.Errorf("%s: LooksLikeHTML = %v, want %v", tc.name, got, tc.want)
		}
	}
}
