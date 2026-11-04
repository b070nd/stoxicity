package httpx

import "bytes"

// LooksLikeHTML reports whether body looks like an HTML page rather than a
// feed or JSON document: after an optional UTF-8 byte order mark and leading
// white space it starts with "<!doctype html" or "<html" (any case), or its
// first 512 bytes contain "<html" or "<body". Fetchers use it to spot
// rate-limit pages that a server sends with status 200.
func LooksLikeHTML(body []byte) bool {
	body = bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))
	body = bytes.TrimLeft(body, " \t\r\n\f\v")
	head := body[:min(len(body), 512)]
	lower := bytes.ToLower(head)
	return bytes.HasPrefix(lower, []byte("<!doctype html")) ||
		bytes.Contains(lower, []byte("<html")) ||
		bytes.Contains(lower, []byte("<body"))
}
