package main

import "testing"

func TestRelinkNeedsNoNetworkAndSaysWhatItDid(t *testing.T) {
	e := newTestEnv(t)
	e.run(t, "universe", "update").wantCode(t, exitOK)
	r := e.run(t, "relink")
	r.wantCode(t, exitOK)
	r.wantOut(t, "relinked 0 filings")
	if r = e.run(t, "relink", "-days", "-1"); r.code == exitOK {
		t.Error("a negative -days should be refused")
	}
	if r = e.run(t, "relink", "extra"); r.code == exitOK {
		t.Error("an argument should be refused")
	}
}
