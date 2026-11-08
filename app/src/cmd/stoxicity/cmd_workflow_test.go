package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/store"
	"github.com/b070nd/stoxicity/app/src/workflow"
)

// source opens the test environment's stores the way the terminal interface does.
func workflowOf(t *testing.T, e *testEnv) (workflow.Source, *store.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(e.dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return workflow.Source{DB: db, Tenant: "default", Now: func() time.Time { return testNow }}, db
}

func TestWorkflowConfirmsAPaperDraftOnlyWhenTheIDIsTyped(t *testing.T) {
	e := newTestEnv(t)
	e.run(t, "portfolio", "book", "create", "-account", "paper", "-name", "sim", "-env", "paper_local", "-base", "EUR").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "paper/sim", "deposit", "-date", "2026-10-01", "-amount", "1000.00 EUR").wantCode(t, exitOK)
	r := e.run(t, "order", "draft", "paper/sim", "-security", "sec_apple", "-side", "buy", "-qty", "10", "-limit", "50", "-fee", "2.00 EUR", "-by", "me")
	r.wantCode(t, exitOK)
	id := orderIDRE.FindString(r.out)

	src, _ := workflowOf(t, e)
	ctx := context.Background()
	rows, err := src.OrderRows(ctx)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows %v %v", rows, err)
	}
	if o := rows[0]; o.ID != id || !o.Confirmable || o.Env != "paper_local" || o.State != "draft" || o.Qty != "10" {
		t.Fatalf("row %+v", o)
	}
	// two days later the draft has lapsed: it cannot be confirmed, and the screen says why
	late := src
	late.Now = func() time.Time { return testNow.Add(48 * time.Hour) }
	lrows, err := late.OrderRows(ctx)
	if err != nil || lrows[0].Confirmable || !strings.Contains(lrows[0].NotBecause, "lapsed") {
		t.Fatalf("a lapsed draft: %+v %v", lrows, err)
	}
	if err := late.ConfirmOrder(ctx, id, id, "tui:me"); err == nil || !strings.Contains(err.Error(), "lapsed") {
		t.Fatalf("a lapsed draft was confirmed: %v", err)
	}
	if rows[0].BookName != "paper/sim" {
		t.Fatalf("book name %q", rows[0].BookName)
	}
	if err := src.ConfirmOrder(ctx, id, "not it", "tui:me"); err == nil || !strings.Contains(err.Error(), "type the order ID") {
		t.Fatalf("a wrong ID was accepted: %v", err)
	}
	if err := src.ConfirmOrder(ctx, id, id, ""); err == nil {
		t.Fatal("a confirmation without a person was accepted")
	}
	if err := src.ConfirmOrder(ctx, id, id, "tui:me"); err != nil {
		t.Fatal(err)
	}
	e.run(t, "order", "show", id).wantOut(t, "confirmed", "tui:me")
	// a second confirmation of a confirmed order is refused
	if err := src.ConfirmOrder(ctx, id, id, "tui:me"); err == nil {
		t.Fatal("a confirmed order was confirmed again")
	}
	rows, _ = src.OrderRows(ctx)
	if rows[0].Confirmable || rows[0].State != "confirmed" || rows[0].ConfirmedBy != "tui:me" {
		t.Fatalf("after: %+v", rows[0])
	}
	// the local engine, not a broker, is all a paper order can reach
	e.run(t, "order", "submit", id, "-by", "me").wantOut(t, "No broker was contacted")
}

func TestWorkflowRefusesABookBoundToABrokerAndTheTierGateStillApplies(t *testing.T) {
	e := allocateEnv(t)
	e.run(t, "portfolio", "book", "create", "-account", "main", "-name", "core", "-env", "real", "-base", "EUR").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "main/core", "deposit", "-date", "2026-08-30", "-amount", "1000.00 EUR").wantCode(t, exitOK)
	tierUp(t, e, 1, nil)
	r := e.run(t, "order", "draft", "main/core", "-security", "sec_apple", "-side", "buy", "-qty", "5", "-limit", "40", "-by", "me")
	r.wantCode(t, exitOK)
	id := orderIDRE.FindString(r.out)

	src, db := workflowOf(t, e)
	ctx := context.Background()
	rows, err := src.OrderRows(ctx)
	if err != nil || len(rows) != 1 || !rows[0].Confirmable || rows[0].Env != "real" || len(rows[0].Warnings) == 0 {
		t.Fatalf("rows %+v %v", rows, err)
	}

	// bind the book to a broker account: the interface must hand it back to the command line
	bookID := rows[0].Book
	body, _ := json.Marshal(map[string]string{"book": bookID, "account": "DU1", "by": "me"})
	if _, _, err := db.Records("default").Append(ctx, store.RecordInput{Kind: workflow.KindBrokerBind, Key: bookID, Body: body}); err != nil {
		t.Fatal(err)
	}
	rows, _ = src.OrderRows(ctx)
	if rows[0].Confirmable || !strings.Contains(rows[0].NotBecause, "bound to a broker account") {
		t.Fatalf("bound book: %+v", rows[0])
	}
	if err := src.ConfirmOrder(ctx, id, id, "tui:me"); err == nil || !strings.Contains(err.Error(), "bound to a broker") {
		t.Fatalf("a bound book was confirmed from the interface: %v", err)
	}
	e.run(t, "order", "show", id).wantOut(t, "draft")
}

func TestWorkflowReadsPlansPolicyBooksAndIncomeAsTheCommandsDo(t *testing.T) {
	e, cands, fees, book := planFixture(t)
	e.run(t, "plan", "generate", book, "-candidates", cands, "-fees", fees, "-as-of", "2026-11-03", "-rates", "USD=0.92").wantCode(t, exitOK)
	src, _ := workflowOf(t, e)
	ctx := context.Background()

	rows, err := src.PlanRows(ctx)
	if err != nil || len(rows) != 3 {
		t.Fatalf("plans %v %v", rows, err)
	}
	for _, r := range rows {
		if r.Book != "main/core" || r.Currency != "EUR" && r.Qty != "" {
			t.Errorf("row %+v", r)
		}
	}
	doc, err := src.PlanDoc(ctx, rows[0].Revision)
	if err != nil || !doc.Intact || doc.Revision.ID() != rows[0].Revision {
		t.Fatalf("doc %v %v", doc.Revision.ID(), err)
	}
	if revs, err := src.Revisions(ctx, doc.Revision.PlanID); err != nil || len(revs) != 1 {
		t.Fatalf("revisions %v %v", revs, err)
	}
	if _, err := src.PlanDoc(ctx, "pl_000000000000"); err == nil {
		t.Fatal("a missing plan was found")
	}
	if _, err := src.PlanDoc(ctx, "pl_x.rzero"); err == nil {
		t.Fatal("a malformed reference was accepted")
	}

	pol, err := src.PolicyStatus(ctx)
	if err != nil || !pol.Active || pol.Epoch < 1 {
		t.Fatalf("policy %+v %v", pol, err)
	}
	bs, err := src.Books(ctx)
	if err != nil || len(bs) != 1 || bs[0].Ref() != "main/core" || bs[0].Env != "real" {
		t.Fatalf("books %+v %v", bs, err)
	}
	iv, err := src.Income(ctx, "2026-11-03")
	if err != nil || iv.Day != "2026-11-03" {
		t.Fatalf("income %+v %v", iv, err)
	}
	// the export and the interface agree on the book
	rep, err := src.BookReport(ctx, "main/core", "2026-11-03")
	if err != nil || len(rep.Plans) != 3 || rep.Env != "real" {
		t.Fatalf("report %+v %v", rep, err)
	}
	js := e.run(t, "report", "export", "main/core", "-as-of", "2026-11-03", "-format", "json")
	want, _ := rep.JSON()
	if strings.TrimSpace(js.out) != strings.TrimSpace(string(want)) {
		t.Error("the command and the shared layer produce different reports")
	}
}
