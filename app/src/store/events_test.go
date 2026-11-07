package store

import (
	"context"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
)

func ev(id string, title string, mat int, tickers map[string]model.Role, evidence ...model.Evidence) EventInput {
	in := EventInput{EventID: id, Kind: model.EventAcquisition, Title: title, PublicAt: t0, Materiality: mat, Relevance: 0.8,
		LensVersion: "l1", Evidence: evidence}
	for tk, r := range tickers {
		in.Securities = append(in.Securities, model.EventSecurity{Ticker: tk, Role: r})
	}
	return in
}

func TestAnEventGainsRevisionsAndNothingIsErased(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	e1 := ev("evt_1", "Acme to buy Beta", 4, map[string]model.Role{"ACME": model.RoleSubject, "BETA": model.RoleMentioned},
		model.Evidence{ArticleID: "a1", SourceID: "wire"})
	at1 := t0.Add(time.Hour)
	if n, err := db.PutEvents(ctx, []EventInput{e1}, at1); err != nil || n != 1 {
		t.Fatalf("first: %d, %v", n, err)
	}
	// The same understanding again is not a revision.
	if n, _ := db.PutEvents(ctx, []EventInput{e1}, at1.Add(time.Minute)); n != 0 {
		t.Errorf("an unchanged event wrote %d revisions", n)
	}
	// A second article of the story joins the event; Beta is now the counterparty.
	e2 := ev("evt_1", "Acme to buy Beta for cash", 4, map[string]model.Role{"BETA": model.RoleCounterparty},
		model.Evidence{ArticleID: "a2", SourceID: "other"})
	at2 := at1.Add(2 * time.Hour)
	if n, err := db.PutEvents(ctx, []EventInput{e2}, at2); err != nil || n != 1 {
		t.Fatalf("second: %d, %v", n, err)
	}
	hist, err := db.EventHistory(ctx, "evt_1")
	if err != nil || len(hist) != 2 {
		t.Fatalf("history = %+v, %v", hist, err)
	}
	r1, r2 := hist[0], hist[1]
	if len(r1.Evidence) != 1 || len(r2.Evidence) != 2 || r2.Supersedes != 1 {
		t.Errorf("evidence/supersedes: r1 %+v r2 %+v", r1.Evidence, r2)
	}
	roles := map[string]model.Role{}
	for _, s := range r2.Securities {
		roles[s.Ticker] = s.Role
	}
	if roles["ACME"] != model.RoleSubject || roles["BETA"] != model.RoleCounterparty {
		t.Errorf("roles in r2 = %v: the stronger role wins and the earlier security stays", roles)
	}
	if len(r2.Assessments) == 0 || r2.Assessments[0].Direction != model.DirectionUnknown {
		t.Errorf("assessments = %+v: an assessment records no direction it does not know", r2.Assessments)
	}

	// What was known at a time: before the second revision the timeline shows the first.
	early, err := db.SecurityTimeline(ctx, "BETA", at1.Add(time.Hour), 10)
	if err != nil || len(early) != 1 || early[0].Revision != 1 || early[0].Role != model.RoleMentioned || early[0].Evidence != 1 {
		t.Errorf("timeline at the first revision = %+v, %v", early, err)
	}
	now, _ := db.SecurityTimeline(ctx, "BETA", at2.Add(time.Hour), 10)
	if len(now) != 1 || now[0].Revision != 2 || now[0].Role != model.RoleCounterparty {
		t.Errorf("timeline now = %+v", now)
	}
	// Before anything was recorded, nothing was known.
	if none, _ := db.SecurityTimeline(ctx, "BETA", t0, 10); len(none) != 0 {
		t.Errorf("timeline before the event was recorded = %+v", none)
	}
}

func TestACorrectedObservationMakesTheEventCorrected(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	a := art("a1", t0)
	mustSave(t, db, a)
	e := ev("evt_a1", "Profit rises 20%", 3, map[string]model.Role{"ACME": model.RoleSubject}, model.Evidence{ArticleID: "a1", SourceID: "src"})
	db.PutEvents(ctx, []EventInput{e}, t0.Add(time.Minute))

	a.Title = "Correction: profit falls 20%"
	mustSave(t, db, a)
	e.Title = a.Title
	if n, err := db.PutEvents(ctx, []EventInput{e}, t0.Add(time.Hour)); err != nil || n != 1 {
		t.Fatalf("correction: %d, %v", n, err)
	}
	hist, _ := db.EventHistory(ctx, "evt_a1")
	if len(hist) != 2 || hist[0].Status != model.StatusReported || hist[1].Status != model.StatusCorrected || hist[0].Title != "Profit rises 20%" {
		t.Errorf("history = %+v: the first revision must keep what was said, the second be marked corrected", hist)
	}
}

func TestSecurityLabelsAreEffectiveDated(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	h, _ := db.baseHandle()
	mustExec := func(q string, args ...any) {
		if _, err := h.w.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`INSERT INTO ref_ticker (ticker, name, cik, exchange) VALUES ('ABC', 'Abacus', '0000000001', 'Nasdaq')`)
	rep, err := db.SyncSecurities(ctx, t0)
	if err != nil || rep.Created != 1 {
		t.Fatalf("sync: %+v, %v", rep, err)
	}
	if _, err := db.PutEvents(ctx, []EventInput{ev("evt_x", "Abacus news", 3, map[string]model.Role{"ABC": model.RoleSubject})}, t0); err != nil {
		t.Fatal(err)
	}
	// The ticker is reused by another company: the old security keeps its history.
	mustExec(`UPDATE ref_ticker SET name = 'Other Co', cik = '0000000002' WHERE ticker = 'ABC'`)
	rep, err = db.SyncSecurities(ctx, t0.Add(48*time.Hour))
	if err != nil || rep.Relabelled != 1 {
		t.Fatalf("resync: %+v, %v", rep, err)
	}
	var open, total int
	h.r.QueryRow(`SELECT COUNT(*) FILTER (WHERE to_day = ''), COUNT(*) FROM security_ticker WHERE ticker = 'ABC'`).Scan(&open, &total)
	if open != 1 || total != 2 {
		t.Errorf("labels: %d open of %d, want one open of two", open, total)
	}
	// An event of the old holder is not on the new holder's security.
	if _, err := db.PutEvents(ctx, []EventInput{ev("evt_y", "Other Co news", 3, map[string]model.Role{"ABC": model.RoleSubject})}, t0.Add(49*time.Hour)); err != nil {
		t.Fatal(err)
	}
	a, _ := db.EventHistory(ctx, "evt_x")
	b, _ := db.EventHistory(ctx, "evt_y")
	if a[0].Securities[0].SecurityID == b[0].Securities[0].SecurityID {
		t.Error("the reused ticker resolved to the same security for two different companies")
	}
}
