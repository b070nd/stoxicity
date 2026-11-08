package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/b070nd/stoxicity/app/src/policy"
)

var hashLine = regexp.MustCompile(`-confirm ([0-9a-f]{64})`)

// writeReviewed writes the example as the user would leave it after reading it.
func writeReviewed(t *testing.T, e *testEnv, edit func(string) string) string {
	t.Helper()
	r := e.run(t, "policy", "example")
	r.wantCode(t, exitOK)
	body := r.out
	body = strings.Replace(body, "example: true", "example: false", 1)
	if edit != nil {
		body = edit(body)
	}
	path := filepath.Join(e.dir, "policy.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPolicyWorkflow(t *testing.T) {
	e := newTestEnv(t)

	r := e.run(t, "policy", "show")
	r.wantCode(t, exitOK)
	r.wantOut(t, "no policy was in force", "policy example")

	// The example cannot be activated as it is.
	ex := e.run(t, "policy", "example")
	ex.wantCode(t, exitOK)
	ex.wantOut(t, "example: true", "1000.00 EUR", "cash_reserve")
	raw := filepath.Join(e.dir, "example.yaml")
	os.WriteFile(raw, []byte(ex.out), 0o600)
	r = e.run(t, "policy", "check", "-file", raw)
	r.wantCode(t, exitError)
	r.wantOut(t, "example profile", "set example to false")

	file := writeReviewed(t, e, nil)
	r = e.run(t, "policy", "check", "-file", file)
	r.wantCode(t, exitOK)
	r.wantOut(t, "no findings", "unknown facts", "account.commission_schedule", "affordability", "paper workflows do not depend")

	r = e.run(t, "policy", "preview", "-file", file)
	r.wantCode(t, exitOK)
	r.wantOut(t, "no policy is in force yet", "to activate exactly this")
	m := hashLine.FindStringSubmatch(r.out)
	if m == nil {
		t.Fatalf("the preview printed no hash:\n%s", r.out)
	}
	hash := m[1]

	// Consent has to be the previewed hash.
	r = e.run(t, "policy", "activate", "-file", file, "-confirm", strings.Repeat("0", 64))
	r.wantCode(t, exitError)
	r.wantErr(t, hash)
	r = e.run(t, "policy", "activate", "-file", file)
	r.wantCode(t, exitUsage)

	r = e.run(t, "policy", "activate", "-file", file, "-confirm", hash)
	r.wantCode(t, exitOK)
	r.wantOut(t, "epoch 1", hash)

	r = e.run(t, "policy", "show")
	r.wantCode(t, exitOK)
	r.wantOut(t, "epoch 1", "conservative-eur-1000", "unknown facts")
	var rec policy.Record
	j := e.run(t, "policy", "show", "-json")
	j.wantCode(t, exitOK)
	if err := json.Unmarshal([]byte(j.out), &rec); err != nil || rec.Hash != hash || rec.Epoch != 1 {
		t.Fatalf("show -json: %v %+v", err, rec.Epoch)
	}
	// A time before the activation: nothing in force.
	r = e.run(t, "policy", "show", "-at", "2000-01-01T00:00:00Z")
	r.wantOut(t, "at that time")

	// The same file again: identical, nothing recorded.
	r = e.run(t, "policy", "preview", "-file", file)
	r.wantCode(t, exitOK)
	r.wantOut(t, "identical")
	r = e.run(t, "policy", "activate", "-file", file, "-confirm", hash)
	r.wantCode(t, exitOK)
	h := e.run(t, "policy", "history")
	h.wantCode(t, exitOK)
	if n := strings.Count(h.out, "conservative-eur-1000"); n != 1 {
		t.Errorf("history has %d rows:\n%s", n, h.out)
	}

	// A change that raises risk is previewed with its reasons and needs the flag.
	riskier := writeReviewed(t, e, func(s string) string {
		return strings.Replace(s, "max_issuer: \"0.10\"", "max_issuer: \"0.20\"", 1)
	})
	r = e.run(t, "policy", "preview", "-file", riskier)
	r.wantCode(t, exitOK)
	r.wantOut(t, "max_issuer", "-accept-risk", "confirmation", "rebuilt")
	hash2 := hashLine.FindStringSubmatch(r.out)[1]
	r = e.run(t, "policy", "activate", "-file", riskier, "-confirm", hash2)
	r.wantCode(t, exitError)
	r.wantErr(t, "-accept-risk")
	r = e.run(t, "policy", "activate", "-file", riskier, "-confirm", hash2, "-accept-risk")
	r.wantCode(t, exitOK)
	r.wantOut(t, "epoch 2")
	h = e.run(t, "policy", "history")
	h.wantOut(t, "yes")
}

func TestPolicyCheckReportsEveryError(t *testing.T) {
	e := newTestEnv(t)
	file := writeReviewed(t, e, func(s string) string {
		s = strings.Replace(s, "tier: 0", "tier: 9", 1)
		return strings.Replace(s, "max_sector: \"0.25\"", "max_sector: \"0.05\"", 1)
	})
	r := e.run(t, "policy", "check", "-file", file)
	r.wantCode(t, exitError)
	r.wantOut(t, "error", "tier", "constraints.max_sector", "issuer cap")

	// A misspelt key is an error, not a silently ignored limit.
	bad := filepath.Join(e.dir, "bad.yaml")
	os.WriteFile(bad, []byte("schema: 1\nmax_isuer: 0.1\n"), 0o600)
	r = e.run(t, "policy", "check", "-file", bad)
	r.wantCode(t, exitError)
	r.wantErr(t, "max_isuer")

	for _, args := range [][]string{{"policy"}, {"policy", "frob"}, {"policy", "check"}, {"policy", "example", "-profile", "nope"}, {"policy", "show", "-at", "yesterday"}} {
		r := e.run(t, args...)
		r.wantCode(t, exitUsage)
	}
}

func TestPolicyIsPerTenant(t *testing.T) {
	e := newTestEnv(t)
	file := writeReviewed(t, e, nil)
	r := e.run(t, "policy", "preview", "-file", file)
	hash := hashLine.FindStringSubmatch(r.out)[1]
	e.run(t, "policy", "activate", "-file", file, "-confirm", hash).wantCode(t, exitOK)

	r = e.run(t, "-tenant", "nobody", "policy", "show")
	r.wantCode(t, exitError)
}

// wantErr checks the standard error output.
func (r result) wantErr(t *testing.T, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(r.errOut, p) {
			t.Errorf("stderr lacks %q:\n%s", p, r.errOut)
		}
	}
}
