package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/corrections"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/store"
)

type correctionWorld struct {
	e                *testEnv
	cands            string
	unsentPlan       string
	sentPlan         string
	unsentOrder      string
	sentOrder        string
	articles         map[string]model.Article
	eventsRecordedAt time.Time
	correctionsRecAt time.Time
}

func (w *correctionWorld) save(t *testing.T, db *store.DB, ids ...string) {
	t.Helper()
	var as []model.Article
	for _, id := range ids {
		as = append(as, w.articles[id])
	}
	if err := db.Save(context.Background(), as); err != nil {
		t.Fatal(err)
	}
}

func article(id, title string) model.Article {
	return model.Article{ID: id, SourceID: "wire", Domain: "example.com", URL: "https://example.com/" + id, Title: title, Summary: title + " in full",
		Published: testNow.Add(-48 * time.Hour), Fetched: testNow.Add(-47 * time.Hour)}
}

func eventOf(id, ticker string, kind model.EventKind, title, art, version string) store.EventInput {
	return store.EventInput{EventID: id, Kind: kind, Title: title, PublicAt: testNow.Add(-48 * time.Hour), Materiality: 3, Relevance: 0.8, LensVersion: "l1",
		Securities: []model.EventSecurity{{Ticker: ticker, Role: model.RoleSubject}},
		Evidence:   []model.Evidence{{ArticleID: art, SourceID: "wire"}},
		Claims:     []model.EventClaim{{ArticleID: art, Seq: 0, Version: version, Extractor: "x", Names: true}}}
}

// newCorrectionWorld builds two paper books that each hold an accepted plan for
// the same security, which cites one event: in the first an order is confirmed
// and not sent, in the second it was sent.
func newCorrectionWorld(t *testing.T) *correctionWorld {
	t.Helper()
	e, cands, fees, _ := planFixture(t)
	w := &correctionWorld{e: e, cands: cands, articles: map[string]model.Article{
		"a1": article("a1", "Apple lifts its dividend"), "a3": article("a3", "Zeta wins a contract")}}
	for _, b := range []string{"unsent", "sent"} {
		e.run(t, "portfolio", "book", "create", "-account", "paper", "-name", b, "-env", "paper_local", "-base", "EUR").wantCode(t, exitOK)
		e.run(t, "portfolio", "add", "paper/"+b, "deposit", "-date", "2026-11-02", "-amount", "1000.00 EUR").wantCode(t, exitOK)
		e.run(t, "portfolio", "add", "paper/"+b, "deposit", "-date", "2026-11-02", "-amount", "300.00 USD").wantCode(t, exitOK)
	}
	_, db := workflowOf(t, e)
	w.save(t, db, "a1", "a3")
	if _, err := db.PutEvents(context.Background(), []store.EventInput{
		eventOf("evt_div", "AAPL", model.EventDividend, "Apple lifts its dividend", "a1", "v1"),
		eventOf("evt_zeta", "ZETA", model.EventAgreement, "Zeta wins a contract", "a3", "v1"),
	}, testNow.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	db.Close()

	planOf := func(book string) string {
		r := e.run(t, "plan", "generate", book, "-candidates", cands, "-fees", fees, "-as-of", "2026-11-03", "-rates", "USD=0.92")
		r.wantCode(t, exitOK)
		for _, line := range strings.Split(r.out, "\n") {
			if strings.Contains(line, "sec_apple") {
				return planIDRE.FindString(line)
			}
		}
		t.Fatalf("no plan for apple:\n%s", r.out)
		return ""
	}
	w.unsentPlan, w.sentPlan = planOf("paper/unsent"), planOf("paper/sent")
	for _, p := range []string{w.unsentPlan, w.sentPlan} {
		e.run(t, "plan", "show", p).wantOut(t, "evt_div r1", "dividend", "Apple lifts its dividend")
		e.run(t, "plan", "propose", p, "-by", "me").wantCode(t, exitOK)
		e.run(t, "plan", "accept", p, "-by", "me").wantCode(t, exitOK)
	}
	draft := func(book, plan string) string {
		r := e.run(t, "order", "draft", book, "-plan", plan, "-by", "me", "-fee", "1.25 USD", "-qty", "10")
		r.wantCode(t, exitOK)
		return orderIDRE.FindString(r.out)
	}
	w.unsentOrder, w.sentOrder = draft("paper/unsent", w.unsentPlan), draft("paper/sent", w.sentPlan)
	e.run(t, "order", "confirm", w.unsentOrder, "-by", "me").wantCode(t, exitOK)
	e.run(t, "order", "confirm", w.sentOrder, "-by", "me").wantCode(t, exitOK)
	e.run(t, "order", "submit", w.sentOrder, "-by", "me").wantCode(t, exitOK)
	return w
}

func countRule(t *testing.T, e *testEnv, rule string) int {
	t.Helper()
	var rows []struct {
		Rule  string `json:"rule"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal([]byte(e.run(t, "alerts", "status", "-all", "-json").out), &rows); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range rows {
		if r.Rule == rule {
			n++
		}
	}
	return n
}

func TestACorrectionRevisesTheAcceptedPlanOnceAndLeavesASentOrderAlone(t *testing.T) {
	w := newCorrectionWorld(t)
	e := w.e
	ctx := context.Background()

	e.run(t, "alerts", "scan").wantCode(t, exitOK) // the drafts of the new plans are announced once
	// nothing changed: the scan looks and does nothing
	r := e.run(t, "corrections", "scan")
	r.wantCode(t, exitOK)
	r.wantOut(t, "unchanged", "0 new alert(s)")
	r.wantNotOut(t, "revised")

	// A correction to an unrelated event, and another article joining the event the
	// plans cite, are seen and change nothing.
	_, db := workflowOf(t, e)
	w.articles["a3"] = article("a3", "Zeta contract was cancelled")
	w.save(t, db, "a3")
	w.articles["a2"] = article("a2", "Apple dividend: details")
	w.save(t, db, "a2")
	more := eventOf("evt_div", "AAPL", model.EventDividend, "Apple lifts its dividend", "a2", "v1")
	more.Claims[0].Names = false
	if _, err := db.PutEvents(ctx, []store.EventInput{eventOf("evt_zeta", "ZETA", model.EventAgreement, "Zeta contract was cancelled", "a3", "v2"), more}, testNow.Add(-12*time.Hour)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	r = e.run(t, "corrections", "scan")
	r.wantCode(t, exitOK)
	r.wantOut(t, "unchanged", "not material", "revision 2", "0 new alert(s)")
	r.wantNotOut(t, "revised")
	e.run(t, "plan", "show", w.unsentPlan+".r2").wantCode(t, exitError)
	e.run(t, "order", "show", w.unsentOrder).wantOut(t, "confirmed")

	// The evidence the plans rest on is corrected: the text of the article changed
	// at its source and the event now says something else.
	_, db = workflowOf(t, e)
	w.articles["a1"] = article("a1", "Apple holds its dividend")
	w.save(t, db, "a1")
	fixed := eventOf("evt_div", "AAPL", model.EventDividend, "Apple holds its dividend", "a1", "v2")
	fixed.Complete, fixed.Scope = true, map[string]bool{"a1": true}
	if _, err := db.PutEvents(ctx, []store.EventInput{fixed}, testNow.Add(-1*time.Hour)); err != nil {
		t.Fatal(err)
	}
	db.Close()

	r = e.run(t, "corrections", "scan")
	r.wantCode(t, exitOK)
	r.wantOut(t, "revised: revise", "was corrected", "2 new alert(s)")
	r.wantOut(t, "confirmation withdrawn from "+w.unsentOrder, "already sent and left exactly as it is: "+w.sentOrder)

	// a new revision explains the change; the accepted one and the decision stay
	r = e.run(t, "plan", "show", w.unsentPlan)
	r.wantOut(t, w.unsentPlan+".r2", "why this revision exists", "Apple holds its dividend", "evt_div r3", "draft")
	r = e.run(t, "plan", "show", w.unsentPlan+".r1")
	r.wantOut(t, "accepted")
	e.run(t, "plan", "diff", w.unsentPlan).wantOut(t, "evidence", "corrections")

	// the confirmation of the unsent order is gone and cannot be used
	e.run(t, "order", "show", w.unsentOrder).wantOut(t, "draft", "confirmation_revoked", "the evidence behind plan")
	sub := e.run(t, "order", "submit", w.unsentOrder, "-by", "me")
	sub.wantCode(t, exitError)
	sub.wantErr(t, "only a confirmed order is submitted")
	// the order already sent is exactly as it was, and visible
	e.run(t, "order", "show", w.sentOrder).wantOut(t, "acknowledged")
	e.run(t, "order", "list", "-open").wantOut(t, w.sentOrder)
	// the old revision cannot be drafted again, and the new one is not accepted yet
	e.run(t, "order", "draft", "paper/unsent", "-plan", w.unsentPlan+".r1", "-by", "me", "-qty", "1").wantCode(t, exitError)
	e.run(t, "order", "draft", "paper/unsent", "-plan", w.unsentPlan+".r2", "-by", "me", "-qty", "1").wantErr(t, "only an accepted plan")

	// exactly one logical alert for each plan, saying what happened
	if n := countRule(t, e, "plan_correction"); n != 2 {
		t.Fatalf("one alert for each of the two plans, got %d", n)
	}
	st := e.run(t, "alerts", "status").out
	if !strings.Contains(st, "plan_correction") {
		t.Fatal(st)
	}

	// reprocessing and starting again make nothing new
	for i := 0; i < 2; i++ {
		r = e.run(t, "corrections", "scan")
		r.wantCode(t, exitOK)
		r.wantNotOut(t, "revised")
		r.wantOut(t, "0 new alert(s)")
		e.run(t, "alerts", "scan").wantOut(t, "0 new alert(s)")
	}
	e.run(t, "plan", "show", w.unsentPlan+".r3").wantCode(t, exitError)
	if n := countRule(t, e, "plan_correction"); n != 2 {
		t.Fatalf("still one alert for each plan, got %d", n)
	}
	e.run(t, "corrections", "list").wantOut(t, w.unsentPlan, w.sentPlan, "done")

	// a scan interrupted after the change was found finishes it the same way
	rs, db2 := workflowOf(t, e)
	_ = rs
	cr := corrections.Repo{RS: db2.Records("default")}
	chs, err := cr.Changes(ctx)
	if err != nil || len(chs) != 2 {
		t.Fatalf("%v %v", chs, err)
	}
	for _, c := range chs {
		c.State, c.At = corrections.Detected, c.At.Add(time.Second) // a different body: an identical one is not written twice
		if err := cr.PutChange(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	db2.Close()
	r = e.run(t, "corrections", "scan")
	r.wantOut(t, "finished after an interruption")
	e.run(t, "plan", "show", w.unsentPlan+".r3").wantCode(t, exitError)
	if n := countRule(t, e, "plan_correction"); n != 2 {
		t.Fatalf("an interrupted scan finished makes no second alert, got %d", n)
	}
	e.run(t, "corrections", "list").wantNotOut(t, "detected")
}

func TestARetractionWithoutAWayToReassessAsksForReviewAndNeverBecomesHold(t *testing.T) {
	w := newCorrectionWorld(t)
	e := w.e
	// the file the plan was generated from is gone: it cannot be generated again
	if err := os.Remove(w.cands); err != nil {
		t.Fatal(err)
	}
	_, db := workflowOf(t, e)
	if n, err := db.WithdrawEvents(context.Background(), []store.Withdrawal{{EventID: "evt_div", Basis: "the claim was denied"}}, testNow.Add(-time.Hour)); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	db.Close()
	r := e.run(t, "corrections", "scan")
	r.wantCode(t, exitOK)
	r.wantOut(t, "was withdrawn")

	r = e.run(t, "plan", "show", w.unsentPlan)
	r.wantOut(t, "ABSTAIN", "review required", "could not be generated again", "proposes nothing")
	r.wantNotOut(t, "HOLD Apple")
	e.run(t, "plan", "propose", w.unsentPlan, "-by", "me").wantCode(t, exitOK)
	e.run(t, "plan", "accept", w.unsentPlan, "-by", "me").wantErr(t, "nothing to accept")
	e.run(t, "order", "show", w.unsentOrder).wantOut(t, "draft")
}

func TestAThesisIsBrokenOnlyByARuleYouRecordedAndAnythingElseNeedsReview(t *testing.T) {
	w := newCorrectionWorld(t)
	e := w.e
	e.run(t, "corrections", "rule", "add", "-security", "sec_apple", "-id", "solvency", "-kinds", "bankruptcy", "-effect", "broken", "-by", "me").wantCode(t, exitUsage)
	e.run(t, "corrections", "rule", "add", "-security", "sec_apple", "-id", "solvency", "-kinds", "bankruptcy,non_reliance", "-effect", "broken", "-note", "the case rests on solvency and clean accounts", "-by", "me").wantCode(t, exitOK)
	e.run(t, "corrections", "rule", "list").wantOut(t, "solvency", "bankruptcy,non_reliance", "broken", "in force")

	// an event that no rule names changes nothing
	_, db := workflowOf(t, e)
	w.articles["a9"] = article("a9", "Apple opens a store")
	w.save(t, db, "a9")
	if _, err := db.PutEvents(context.Background(), []store.EventInput{eventOf("evt_store", "AAPL", model.EventProduct, "Apple opens a store", "a9", "v1")}, testNow.Add(-6*time.Hour)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	e.run(t, "corrections", "scan").wantNotOut(t, "revised")

	// the event the rule names appears
	_, db = workflowOf(t, e)
	w.articles["a8"] = article("a8", "Apple restates its accounts")
	w.save(t, db, "a8")
	if _, err := db.PutEvents(context.Background(), []store.EventInput{eventOf("evt_restate", "AAPL", model.EventNonReliance, "Apple restates its accounts", "a8", "v1")}, testNow.Add(-5*time.Hour)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	r := e.run(t, "corrections", "scan")
	r.wantCode(t, exitOK)
	r.wantOut(t, "revised: broken", "rule solvency", "the thesis is broken")
	r = e.run(t, "plan", "show", w.unsentPlan)
	r.wantOut(t, "ABSTAIN", "review required", "rule solvency")
	r.wantNotOut(t, "HOLD Apple")
	e.run(t, "plan", "propose", w.unsentPlan, "-by", "me").wantCode(t, exitOK)
	e.run(t, "plan", "accept", w.unsentPlan, "-by", "me").wantErr(t, "nothing to accept")
	// the rule marks the plan; it sells nothing and records no decision of yours
	e.run(t, "order", "list", "-open").wantOut(t, w.sentOrder)
	e.run(t, "corrections", "rule", "retire", "-security", "sec_apple", "-id", "solvency", "-by", "me").wantCode(t, exitOK)
	e.run(t, "corrections", "rule", "list").wantOut(t, "retired")
}
