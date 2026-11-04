package httpx

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
)

var _ Client = (*Fake)(nil)

func TestFakeQueueAndRepeat(t *testing.T) {
	f := NewFake()
	f.Reply("http://a.test/x", Response{Status: 200, Body: []byte("one")})
	f.Reply("http://a.test/x", Response{Status: 200, Body: []byte("two")})
	f.Reply("http://a.test/y", Response{Status: 200, Body: []byte("y")})
	var got []string
	for range 4 {
		resp, err := f.Do(context.Background(), Request{URL: "http://a.test/x"})
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, string(resp.Body))
	}
	if want := []string{"one", "two", "two", "two"}; !equalStrings(got, want) {
		t.Errorf("bodies = %v, want %v", got, want)
	}
	if resp, err := f.Do(context.Background(), Request{URL: "http://a.test/y"}); err != nil || string(resp.Body) != "y" {
		t.Errorf("other URL: %v %v", resp, err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFakeFailAndUnknown(t *testing.T) {
	f := NewFake()
	boom := &Error{Class: Throttled, Status: 429, URL: "http://a.test/x"}
	f.Fail("http://a.test/x", boom)
	f.Reply("http://a.test/x", Response{Status: 200})
	if _, err := f.Do(context.Background(), Request{URL: "http://a.test/x"}); err != error(boom) {
		t.Errorf("err = %v, want the scripted error", err)
	}
	if _, err := f.Do(context.Background(), Request{URL: "http://a.test/x"}); err != nil {
		t.Errorf("second call: %v", err)
	}

	_, err := f.Do(context.Background(), Request{URL: "http://a.test/other?key=SECRET"})
	var he *Error
	if !errors.As(err, &he) || he.Class != Permanent {
		t.Fatalf("unknown URL: %v", err)
	}
	if he.URL != "http://a.test/other" {
		t.Errorf("URL = %q", he.URL)
	}
	if _, err := f.Do(context.Background(), Request{URL: "http://a.test/x?k=1"}); err == nil {
		t.Error("URL match must be exact")
	}
}

func TestFakeNotModifiedAndFinalURL(t *testing.T) {
	f := NewFake()
	f.Reply("http://a.test/x?k=1", Response{Status: 304})
	f.Reply("http://a.test/r", Response{Status: 200, FinalURL: "http://a.test/final"})
	resp, err := f.Do(context.Background(), Request{URL: "http://a.test/x?k=1"})
	if err != nil || !resp.NotModified || resp.Status != 304 {
		t.Fatalf("%+v %v", resp, err)
	}
	if resp.FinalURL != "http://a.test/x" {
		t.Errorf("FinalURL = %q", resp.FinalURL)
	}
	resp, _ = f.Do(context.Background(), Request{URL: "http://a.test/r"})
	if resp.FinalURL != "http://a.test/final" || resp.NotModified {
		t.Errorf("%+v", resp)
	}
}

func TestFakeCallsAndIsolation(t *testing.T) {
	f := NewFake()
	body := []byte("data")
	f.Reply("http://a.test/x", Response{Status: 200, Body: body, Header: http.Header{"X": {"1"}}})
	hdr := http.Header{"A": {"b"}}
	resp, _ := f.Do(context.Background(), Request{URL: "http://a.test/x", Header: hdr, ETag: `"e"`})
	f.Do(context.Background(), Request{URL: "http://a.test/missing", LastModified: "lm"})

	calls := f.Calls()
	if len(calls) != 2 || calls[0].URL != "http://a.test/x" || calls[0].ETag != `"e"` || calls[1].LastModified != "lm" {
		t.Fatalf("calls = %+v", calls)
	}
	hdr.Set("A", "changed")
	calls[0].Header.Set("A", "changed too")
	if got := f.Calls()[0].Header.Get("A"); got != "b" {
		t.Errorf("recorded header = %q", got)
	}
	resp.Body[0] = 'X'
	resp.Header.Set("X", "2")
	again, _ := f.Do(context.Background(), Request{URL: "http://a.test/x"})
	if string(again.Body) != "data" || again.Header.Get("X") != "1" {
		t.Errorf("scripted response was mutated: %q %v", again.Body, again.Header)
	}
}

func TestFakeContext(t *testing.T) {
	f := NewFake()
	f.Reply("http://a.test/x", Response{Status: 200})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Do(ctx, Request{URL: "http://a.test/x"}); err != context.Canceled {
		t.Errorf("err = %v", err)
	}
	if len(f.Calls()) != 0 {
		t.Error("cancelled call recorded")
	}
}

func TestFakeConcurrent(t *testing.T) {
	f := NewFake()
	f.Reply("http://a.test/x", Response{Status: 200, Body: []byte("x")})
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i%2 == 0 {
				f.Reply("http://a.test/x", Response{Status: 200})
			}
			f.Do(context.Background(), Request{URL: "http://a.test/x"})
			f.Calls()
		}()
	}
	wg.Wait()
	if n := len(f.Calls()); n != 20 {
		t.Errorf("calls = %d", n)
	}
}
