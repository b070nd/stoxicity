package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/alerts"
	"github.com/b070nd/stoxicity/app/src/store"
)

func regexpFind(t *testing.T, re, s string) string {
	t.Helper()
	m := regexp.MustCompile(re).FindString(s)
	if m == "" {
		t.Fatalf("%s not found in:\n%s", re, s)
	}
	return m
}

func recInput(kind, key, body string) store.RecordInput {
	return store.RecordInput{Kind: kind, Key: key, Body: []byte(body)}
}

func TestAlertsScanRaisesOnceDeliversAndReportsWhatWasNotSent(t *testing.T) {
	e, cands, fees, book := planFixture(t)
	files := filepath.Join(e.dir, "alerts-out")
	t.Setenv("STOXICITY_FILE_DIR", files)
	t.Setenv("STOXICITY_FEED_PATH", "")
	e.run(t, "plan", "generate", book, "-candidates", cands, "-fees", fees, "-as-of", "2026-11-03", "-rates", "USD=0.92").wantCode(t, exitOK)

	r := e.run(t, "alerts", "scan")
	r.wantCode(t, exitOK)
	r.wantOut(t, "new alert(s)", "plan_revision", "sec_apple")
	first := r.out
	r = e.run(t, "alerts", "scan")
	r.wantCode(t, exitOK)
	r.wantOut(t, "0 new alert(s)")
	if strings.Contains(r.out, "plan_revision") {
		t.Errorf("a known alert is not raised again:\n%s", r.out)
	}
	if !strings.Contains(first, "3 new alert(s)") {
		t.Errorf("one alert for each plan:\n%s", first)
	}

	// the file channel is configured, the feed is not: files are written, the feed waits
	r = e.run(t, "alerts", "deliver")
	r.wantCode(t, exitOK)
	r.wantOut(t, "sent: 3", "waiting: 3", "feed channel is not configured")
	entries, err := os.ReadDir(filepath.Join(files))
	if err != nil || len(entries) != 3 {
		t.Fatalf("one file for each alert: %v %v", entries, err)
	}
	body, _ := os.ReadFile(filepath.Join(files, entries[0].Name()))
	if !strings.Contains(string(body), "not an order") || strings.Contains(string(body), "<html") {
		t.Errorf("facts only:\n%s", body)
	}
	r = e.run(t, "alerts", "deliver")
	r.wantOut(t, "waiting: 3")
	r.wantNotOut(t, "sent:")
	entries, _ = os.ReadDir(files)
	if len(entries) != 3 {
		t.Errorf("nothing is sent twice: %d files", len(entries))
	}
	st := e.run(t, "alerts", "status")
	st.wantCode(t, exitOK)
	st.wantOut(t, "file sent", "feed pending", "not configured")

	var rows []struct {
		ID         string `json:"id"`
		Open       bool   `json:"open"`
		Deliveries []struct {
			Channel string `json:"channel"`
			State   string `json:"state"`
		} `json:"deliveries"`
	}
	js := e.run(t, "alerts", "status", "-json")
	if err := json.Unmarshal([]byte(js.out), &rows); err != nil || len(rows) != 3 {
		t.Fatalf("%v\n%s", err, js.out)
	}

	// the user answers; the alert is not rewritten and the feed copy is not sent later
	r = e.run(t, "alerts", "answer", rows[0].ID, "rejected", "-by", "me")
	r.wantCode(t, exitError)
	r = e.run(t, "alerts", "answer", rows[0].ID, "rejected", "-by", "me", "-note", "not now")
	r.wantCode(t, exitOK)
	r.wantOut(t, "unchanged", "does not move the plan")
	e.run(t, "alerts", "status").wantNotOut(t, rows[0].ID)
	e.run(t, "alerts", "status", "-all").wantOut(t, rows[0].ID, "rejected")
}

func TestAlertsConfigAndQuietHours(t *testing.T) {
	e := newTestEnv(t)
	e.run(t, "alerts", "config", "show").wantOut(t, "starter configuration", "plan_revision", "feed, file", "order_unknown")
	e.run(t, "alerts", "config", "set").wantCode(t, exitUsage)
	e.run(t, "alerts", "config", "set", "-quiet-start", "22:00").wantCode(t, exitUsage)
	e.run(t, "alerts", "config", "set", "-zone", "Mars/Base").wantCode(t, exitUsage)
	e.run(t, "alerts", "config", "set", "-channels", "nonsense").wantCode(t, exitUsage)
	e.run(t, "alerts", "config", "set", "-critical", "bogus").wantCode(t, exitUsage)
	r := e.run(t, "alerts", "config", "set", "-quiet-start", "22:00", "-quiet-end", "07:00", "-zone", "Europe/Budapest", "-critical", "order_unknown,broker_exception")
	r.wantCode(t, exitOK)
	r.wantNotOut(t, "leave this machine")
	e.run(t, "alerts", "config", "show").wantOut(t, "stored", "22:00 to 07:00, Europe/Budapest", "order_unknown, broker_exception")
	r = e.run(t, "alerts", "config", "set", "-channels", "income_missing=file,email")
	r.wantCode(t, exitOK)
	r.wantOut(t, "outside this machine")
	e.run(t, "alerts", "config", "show").wantOut(t, "income_missing", "email, file")
	e.run(t, "alerts", "config", "set", "-no-quiet").wantCode(t, exitOK)
	e.run(t, "alerts", "config", "show").wantOut(t, "quiet hours:   none")
}

func TestAlertsIncomeAndBrokerRulesFromStoredRecords(t *testing.T) {
	e := newTestEnv(t)
	inst := writeInstruments(t, e)
	e.run(t, "instruments", "import", "-file", inst, "-source", "fixture").wantCode(t, exitOK)
	e.run(t, "portfolio", "book", "create", "-account", "main", "-name", "core", "-env", "real", "-base", "USD").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "main/core", "deposit", "-date", "2026-11-02", "-amount", "10000.00 USD").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "main/core", "buy", "-date", "2026-11-03", "-security", "sec_apple", "-qty", "10", "-gross", "1500.00 USD").wantCode(t, exitOK)
	div := writeFile(t, e, "div.csv", divHeader+"ordinary,2026Q4,declared,0.25,USD,2026-11-06,2026-11-06,2026-11-13,XNAS,AAPL\n")
	e.run(t, "dividends", "import", "-file", div, "-source", "company").wantCode(t, exitOK)

	// the ex-date (6 November) is three days away
	r := e.run(t, "alerts", "scan")
	r.wantOut(t, "dividend_ex_date", "goes ex in 3 days")

	// a broker difference stored by "broker reconcile" becomes an alert, and clears with it
	_, db := workflowOf(t, e)
	body := `{"state":"open","episode":0,"book":"bk","difference":{"kind":"cash_mismatch","subject":"USD","local":"100.00","broker":"90.00","hint":"check the statement"}}`
	if _, _, err := db.Records("default").Append(context.Background(), recInput("broker.difference", "bk/cash_mismatch|USD|100.00|90.00", body)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	r = e.run(t, "alerts", "scan")
	r.wantOut(t, "broker_exception", "cash mismatch USD")
	r = e.run(t, "alerts", "scan")
	r.wantOut(t, "0 new alert(s)")

	// it clears at the broker before anyone was told: the alert is withdrawn, not announced
	_, db = workflowOf(t, e)
	cleared := strings.Replace(body, `"open"`, `"cleared"`, 1)
	if _, _, err := db.Records("default").Append(context.Background(), recInput("broker.difference", "bk/cash_mismatch|USD|100.00|90.00", cleared)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	r = e.run(t, "alerts", "scan")
	r.wantOut(t, "1 withdrawn")
	e.run(t, "alerts", "status").wantNotOut(t, "broker_exception")
	e.run(t, "alerts", "status", "-all").wantOut(t, "broker_exception", "ended")
}

func TestJournalLinksTheDecisionToTheThesisAndNeverClaimsWhatThePlanLacks(t *testing.T) {
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
		t.Fatalf("no plan:\n%s", r.out)
	}
	e.run(t, "plan", "propose", apple, "-by", "me").wantCode(t, exitOK)

	e.run(t, "journal", "add", apple, "-decision", "accepted", "-by", "me", "-horizon", "2027-01-15").wantCode(t, exitUsage)   // no reason
	e.run(t, "journal", "add", apple, "-decision", "accepted", "-reason", "steady income", "-by", "me").wantCode(t, exitUsage) // no horizon
	r = e.run(t, "journal", "add", apple, "-decision", "accepted", "-reason", "steady income", "-by", "me", "-horizon", "2027-01-15")
	r.wantCode(t, exitUsage)
	r.wantErr(t, "is proposed, not accepted")

	// an alert for the plan, answered through the journal
	e.run(t, "alerts", "scan").wantCode(t, exitOK)
	var rows []struct {
		ID, Title string
	}
	js := e.run(t, "alerts", "status", "-json")
	if err := json.Unmarshal([]byte(js.out), &rows); err != nil {
		t.Fatal(err)
	}
	var alertID string
	for _, x := range rows {
		if strings.Contains(x.Title, apple) {
			alertID = x.ID
		}
	}
	if alertID == "" {
		t.Fatalf("no alert for %s:\n%s", apple, js.out)
	}
	r = e.run(t, "journal", "add", apple, "-decision", "accepted", "-reason", "steady income", "-by", "me", "-horizon", "2027-01-15", "-apply", "-alert", alertID)
	r.wantCode(t, exitOK)
	r.wantOut(t, "proposed -> accepted", "recorded", "look back on 2027-01-15", "answered accepted")
	id := regexpFind(t, `jr_[0-9a-f]{10}`, r.out)
	e.run(t, "plan", "show", apple).wantOut(t, "accepted")

	// the same decision again stores nothing new
	r = e.run(t, "journal", "add", apple, "-decision", "accepted", "-reason", "steady income", "-by", "me", "-horizon", "2027-01-15", "-alert", alertID)
	r.wantOut(t, "already recorded")

	r = e.run(t, "journal", "show", id)
	r.wantCode(t, exitOK)
	r.wantOut(t, "steady income", "Thesis when decided", "Invalidation when decided", "the same revision", "Looks back:", "none yet")
	e.run(t, "journal", "list", "-due").wantOut(t, "no entries")
	e.run(t, "journal", "list").wantOut(t, id, "accepted", "2027-01-15", "no")

	e.run(t, "journal", "outcome", id, "-result", "great", "-note", "x", "-by", "me").wantCode(t, exitUsage)
	e.run(t, "journal", "outcome", id, "-result", "early", "-by", "me").wantCode(t, exitUsage)
	e.run(t, "journal", "outcome", id, "-result", "early", "-note", "nothing paid yet", "-by", "me").wantOut(t, "too early")
	e.run(t, "journal", "outcome", id, "-result", "held", "-note", "paid as planned", "-by", "me").wantOut(t, "thesis held", "unchanged")
	r = e.run(t, "journal", "show", id)
	r.wantOut(t, "nothing paid yet", "paid as planned")
	e.run(t, "journal", "show", "jr_missing").wantCode(t, exitError)
	e.run(t, "journal", "add", apple, "-decision", "noted", "-reason", "x", "-by", "me", "-horizon", "2027-01-15", "-apply").wantCode(t, exitUsage)

	// the alert answer is kept next to the alert and the plan was not touched by it
	e.run(t, "alerts", "status", "-all").wantOut(t, alertID, "accepted")
	var j struct {
		Entry struct {
			Thesis []string `json:"thesis"`
			Plan   string   `json:"plan"`
		} `json:"entry"`
		Outcomes []struct{ Result string } `json:"outcomes"`
	}
	if err := json.Unmarshal([]byte(e.run(t, "journal", "show", id, "-json").out), &j); err != nil || len(j.Outcomes) != 2 || !strings.HasPrefix(j.Entry.Plan, apple) {
		t.Fatalf("%v %+v", err, j)
	}
}

func TestDailyReviewEndsWithTheJournal(t *testing.T) {
	e, cands, fees, book := planFixture(t)
	e.run(t, "plan", "generate", book, "-candidates", cands, "-fees", fees, "-as-of", "2026-11-03", "-rates", "USD=0.92").wantCode(t, exitOK)
	r := e.run(t, "daily")
	r.wantCode(t, exitOK)
	r.wantOut(t, "Daily review for 2026-11-03", "Health", "Policy in force", "Plans waiting for a decision", "sec_apple", "Reviews due", "Income, next 14 days",
		"Orders needing attention", "Journal: outcomes to write", "No decision has reached its horizon", "Before you close: write the reasoning")
	var d struct {
		Day      string `json:"day"`
		Sections []struct {
			Title string   `json:"title"`
			Lines []string `json:"lines"`
		} `json:"sections"`
		Closing string `json:"closing"`
	}
	if err := json.Unmarshal([]byte(e.run(t, "daily", "-json").out), &d); err != nil || len(d.Sections) != 7 || d.Sections[len(d.Sections)-1].Title != "Journal: outcomes to write" {
		t.Fatalf("%v %+v", err, d)
	}
}

// A difference with the broker is urgent only for a real book, only after it
// was found by several syncs over some time, and not when rounding or an order
// in flight explains it. Each alert says why, and the urgent ones carry what a
// person needs to act.
func TestBrokerDifferenceIsUrgentOnlyForRealMoneyThatStaysUnexplained(t *testing.T) {
	e := newTestEnv(t)
	e.run(t, "portfolio", "book", "create", "-account", "U123", "-name", "live", "-env", "real", "-base", "USD").wantCode(t, exitOK)
	e.run(t, "portfolio", "book", "create", "-account", "paper", "-name", "sim", "-env", "paper_local", "-base", "USD").wantCode(t, exitOK)
	src, db := workflowOf(t, e)
	ctx := context.Background()
	books, err := src.Books(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, b := range books {
		ids[b.Name] = b.ID
	}
	ts := func(d time.Duration) string { return testNow.Add(-d).UTC().Format(time.RFC3339) }
	put := func(key, book string, seen int, firstAgo time.Duration, subject, local, brk string) {
		body := fmt.Sprintf(`{"state":"open","episode":0,"book":%q,"difference":{"kind":"cash_mismatch","subject":%q,"local":%q,"broker":%q,"hint":"compare with the statement"},"at":%q,"seen":%d,"first_seen":%q,"last_seen":%q}`,
			book, subject, local, brk, ts(0), seen, ts(firstAgo), ts(0))
		if _, _, err := db.Records("default").Append(ctx, recInput("broker.difference", key, body)); err != nil {
			t.Fatal(err)
		}
	}
	live, sim := ids["live"], ids["sim"]
	put("a", live, 1, 0, "USD", "100.00", "90.00")              // found once
	put("b", live, 3, 45*time.Minute, "EUR", "100.00", "90.00") // found again and again
	put("c", live, 3, 45*time.Minute, "GBP", "100.00", "99.60") // rounding
	put("d", sim, 5, 5*time.Hour, "CHF", "100.00", "90.00")     // paper money
	put("e", live, 3, 10*time.Minute, "JPY", "100.00", "90.00") // too recent
	sync := fmt.Sprintf(`{"book":%q,"at":%q,"differences":4,"last_clean":%q}`, live, ts(0), ts(26*time.Hour))
	if _, _, err := db.Records("default").Append(ctx, recInput("broker.sync", live, sync)); err != nil {
		t.Fatal(err)
	}
	cfg := alerts.DefaultConfig()
	sc, errs := src.AlertScan(ctx, cfg)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	urgent := map[string]bool{}
	body := map[string]string{}
	for _, a := range sc.Alerts {
		if a.Rule == alerts.BrokerException {
			urgent[a.Title] = a.Critical
			body[a.Title] = a.Body
		}
	}
	want := map[string]bool{"cash mismatch EUR": true, "cash mismatch USD": false, "cash mismatch GBP": false, "cash mismatch CHF": false, "cash mismatch JPY": false}
	for sub, w := range want {
		found := false
		for title, c := range urgent {
			if strings.HasSuffix(title, sub) {
				found = true
				if c != w {
					t.Errorf("%s: urgent = %v, want %v\n%s", sub, c, w, body[title])
				}
			}
		}
		if !found {
			t.Errorf("no alert for %s", sub)
		}
	}
	for title, b := range body {
		switch {
		case strings.HasSuffix(title, "EUR"):
			for _, w := range []string{"Account: U123", "Here: 100.00", "At the broker: 90.00", "Last sync that matched: " + ts(26*time.Hour), "Next: compare with the statement", "Why this is urgent"} {
				if !strings.Contains(b, w) {
					t.Errorf("urgent alert lacks %q:\n%s", w, b)
				}
			}
		case strings.HasSuffix(title, "USD"):
			if !strings.Contains(b, "found by 1 of 2 syncs") {
				t.Errorf("USD: %s", b)
			}
		case strings.HasSuffix(title, "GBP"):
			if !strings.Contains(b, "rounding tolerance") {
				t.Errorf("GBP: %s", b)
			}
		case strings.HasSuffix(title, "CHF"):
			if !strings.Contains(b, "paper book") {
				t.Errorf("CHF: %s", b)
			}
		case strings.HasSuffix(title, "JPY"):
			if !strings.Contains(b, "10 of 30 minutes") {
				t.Errorf("JPY: %s", b)
			}
		}
	}
}

func TestAlertsConfigSetsTheEscalationRules(t *testing.T) {
	e := newTestEnv(t)
	e.run(t, "alerts", "config", "set", "-repeat-hours", "-1").wantCode(t, exitUsage)
	e.run(t, "alerts", "config", "set", "-cash-tolerance", "abc").wantCode(t, exitUsage)
	e.run(t, "alerts", "config", "set", "-repeat-hours", "6", "-broker-syncs", "3", "-broker-minutes", "45", "-cash-tolerance", "2.50").wantCode(t, exitOK)
	e.run(t, "alerts", "config", "show").wantOut(t, "every 6 hours", "after 3 syncs over 45 minutes", "within 2.5")
}
