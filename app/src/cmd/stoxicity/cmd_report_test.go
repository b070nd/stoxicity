package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
)

type refusingTransport struct{ calls atomic.Int32 }

func (r *refusingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.calls.Add(1)
	return nil, http.ErrHandlerTimeout
}

func TestReportExportsABookWithoutSecretsAndWithoutTheNetwork(t *testing.T) {
	e, cands, fees, book := planFixture(t)
	e.run(t, "plan", "generate", book, "-candidates", cands, "-fees", fees, "-as-of", "2026-11-03", "-rates", "USD=0.92").wantCode(t, exitOK)
	e.run(t, "broker", "config", "set", "-transport", "ibkr", "-url", "https://localhost:5000", "-ca", writeFile(t, e, "secret-ca.pem", "x"), "-by", "me").wantCode(t, exitOK)

	// the export runs on a client whose transport refuses every request
	rt := &refusingTransport{}
	client, err := httpx.New(httpx.Config{UserAgent: "stoxicity-test/0 (tester@example.com)", Transport: rt, AllowPrivate: true, MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	offline := func(args ...string) result {
		return runWith(t, runOptions{cwd: e.dir, client: client, now: func() time.Time { return testNow }}, append([]string{"-config-dir", e.dir}, args...)...)
	}

	r := offline("report", "export", book, "-as-of", "2026-11-03", "-format", "json")
	r.wantCode(t, exitOK)
	if rt.calls.Load() != 0 {
		t.Fatalf("the export made %d network requests", rt.calls.Load())
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(r.out), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, r.out)
	}
	if doc["schema"] != "stoxicity.report/1" || doc["environment"] != "real" || doc["base_currency"] != "EUR" {
		t.Fatalf("header wrong: %v", doc)
	}
	for _, k := range []string{"cash", "holdings", "reservations", "orders", "plans", "income", "capabilities", "omitted", "broker", "ledger_fingerprint", "tier", "policy"} {
		if _, ok := doc[k]; !ok {
			t.Errorf("key %q missing", k)
		}
	}
	if plans, _ := doc["plans"].([]any); len(plans) != 3 {
		t.Fatalf("want the 3 plans of the book, got %v", doc["plans"])
	}
	if b, _ := doc["broker"].(map[string]any); b["transport"] != "ibkr" || b["mode"] != "read_only" {
		t.Fatalf("broker: %v", doc["broker"])
	}
	// nothing that is a secret or an address, in either form
	md := offline("report", "export", book, "-as-of", "2026-11-03")
	md.wantCode(t, exitOK)
	for _, s := range []string{r.out, md.out} {
		for _, bad := range []string{"localhost:5000", "secret-ca.pem", "https://"} {
			if strings.Contains(s, bad) {
				t.Errorf("the report contains %q", bad)
			}
		}
	}
	md.wantOut(t, "# Report for main/core", "1000.00 EUR", "sec_apple", "BUY", "No article text", "stoxicity.report/1", "broker transport ibkr, mode read_only")
	if rt.calls.Load() != 0 {
		t.Fatal("the Markdown export used the network")
	}

	// the same records give the same report
	again := offline("report", "export", book, "-as-of", "2026-11-03", "-format", "json")
	if again.out != r.out {
		t.Fatal("two exports of the same records differ")
	}
}

func TestReportOutFileIsKeptUnlessForced(t *testing.T) {
	e, _, _, book := planFixture(t)
	out := filepath.Join(e.dir, "r.md")
	e.run(t, "report", "export", book, "-out", out).wantCode(t, exitOK)
	raw, err := os.ReadFile(out)
	if err != nil || !strings.Contains(string(raw), "# Report for main/core") {
		t.Fatalf("file: %v %q", err, raw)
	}
	e.run(t, "report", "export", book, "-out", out).wantCode(t, exitError)
	e.run(t, "report", "export", book, "-out", out, "-force").wantCode(t, exitOK)
}

func TestReportUsageAndErrors(t *testing.T) {
	e, _, _, book := planFixture(t)
	e.run(t, "report").wantCode(t, exitUsage)
	e.run(t, "report", "nope").wantCode(t, exitUsage)
	e.run(t, "report", "export").wantCode(t, exitUsage)
	e.run(t, "report", "export", book, "-format", "xml").wantCode(t, exitUsage)
	e.run(t, "report", "export", book, "-as-of", "yesterday").wantCode(t, exitUsage)
	e.run(t, "report", "export", "main/missing").wantCode(t, exitError)
}

func TestPlanExportCarriesTheWholeRevisionAndNoArticleText(t *testing.T) {
	e, cands, fees, book := planFixture(t)
	r := e.run(t, "plan", "generate", book, "-candidates", cands, "-fees", fees, "-as-of", "2026-11-03", "-rates", "USD=0.92")
	r.wantCode(t, exitOK)
	var apple string
	for _, line := range strings.Split(r.out, "\n") {
		if strings.Contains(line, "sec_apple") {
			apple = planIDRE.FindString(line)
		}
	}
	if apple == "" {
		t.Fatalf("no plan id:\n%s", r.out)
	}
	js := e.run(t, "plan", "export", apple, "-format", "json")
	js.wantCode(t, exitOK)
	var doc struct {
		Schema    string `json:"schema"`
		Intact    bool   `json:"intact"`
		Effective string `json:"effective_state"`
		Revision  struct {
			PlanID       string `json:"plan_id"`
			ManifestHash string `json:"manifest_hash"`
		} `json:"revision"`
	}
	if err := json.Unmarshal([]byte(js.out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, js.out)
	}
	if doc.Schema != "stoxicity.plan-export/1" || !doc.Intact || doc.Revision.PlanID != apple || doc.Revision.ManifestHash == "" {
		t.Fatalf("export: %+v", doc)
	}
	md := e.run(t, "plan", "export", apple+".r1")
	md.wantCode(t, exitOK)
	md.wantOut(t, "# Plan "+apple, "sec_apple", "not an order", "Manifest hash")
	e.run(t, "plan", "export").wantCode(t, exitUsage)
	e.run(t, "plan", "export", "pl_000000000000").wantCode(t, exitError)
	e.run(t, "plan", "export", apple, "-format", "csv").wantCode(t, exitUsage)
}
