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

func TestAWithdrawnEventLeavesTheCurrentStateButNotTheHistory(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	e := ev("evt_w", "Acme to buy Beta", 4, map[string]model.Role{"ACME": model.RoleSubject}, model.Evidence{ArticleID: "a1", SourceID: "wire"})
	e.Claims = []model.EventClaim{{ArticleID: "a1", Seq: 0, Version: "assert-2", Extractor: "rules", Names: true}}
	at := t0.Add(time.Hour)
	if _, err := db.PutEvents(ctx, []EventInput{e}, at); err != nil {
		t.Fatal(err)
	}
	if live, _ := db.LiveEvents(ctx, time.Time{}); len(live) != 1 || live[0].EventID != "evt_w" || len(live[0].Articles) != 1 {
		t.Fatalf("live = %+v", live)
	}
	later := at.Add(time.Hour)
	n, err := db.WithdrawEvents(ctx, []Withdrawal{{EventID: "evt_w", Basis: "the claim is denied"}, {EventID: "evt_unknown"}}, later)
	if err != nil || n != 1 {
		t.Fatalf("withdraw = %d, %v: only an event that exists is withdrawn", n, err)
	}
	// A second withdrawal changes nothing.
	if n, _ := db.WithdrawEvents(ctx, []Withdrawal{{EventID: "evt_w", Basis: "again"}}, later.Add(time.Minute)); n != 0 {
		t.Errorf("a withdrawn event was withdrawn again (%d)", n)
	}
	if live, _ := db.LiveEvents(ctx, time.Time{}); len(live) != 0 {
		t.Errorf("a withdrawn event is still live: %+v", live)
	}
	// As of a time before the withdrawal the event stood.
	if live, _ := db.LiveEvents(ctx, at.Add(time.Minute)); len(live) != 1 {
		t.Errorf("as of before the withdrawal: %+v", live)
	}
	hist, err := db.EventHistory(ctx, "evt_w")
	if err != nil || len(hist) != 2 || hist[1].Status != model.StatusWithdrawn || hist[1].Supersedes != 1 || len(hist[1].Evidence) != 1 || len(hist[1].Claims) != 1 {
		t.Fatalf("history keeps the evidence and claims: %+v, %v", hist, err)
	}
	if now, _ := db.SecurityTimeline(ctx, "ACME", time.Time{}, 10); len(now) != 0 {
		t.Errorf("a withdrawn event is on the timeline: %+v", now)
	}
	if then, _ := db.SecurityTimeline(ctx, "ACME", at.Add(time.Minute), 10); len(then) != 1 {
		t.Errorf("the timeline as of before the withdrawal = %+v", then)
	}
	rels, err := db.EventRelations(ctx, "evt_w", time.Time{})
	if err != nil || len(rels) != 1 || rels[0].Relation != model.RelationWithdrawn {
		t.Errorf("relations = %+v, %v", rels, err)
	}
	if rels, _ := db.EventRelations(ctx, "evt_w", at.Add(time.Minute)); len(rels) != 0 {
		t.Errorf("a relation recorded later shows in an earlier reading: %+v", rels)
	}
	if hk, _ := db.HomeKinds(ctx, []string{"evt_w", "evt_none"}); hk["evt_w"] != model.EventAcquisition || len(hk) != 1 {
		t.Errorf("home kinds = %v", hk)
	}
}

func TestACompleteDerivationReplacesOnlyWhatItLookedAt(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	first := ev("evt_c", "Acme buys Beta", 4, map[string]model.Role{"ACME": model.RoleSubject, "BETA": model.RoleCounterparty},
		model.Evidence{ArticleID: "a1", SourceID: "wire"}, model.Evidence{ArticleID: "a2", SourceID: "other"})
	at := t0.Add(time.Hour)
	db.PutEvents(ctx, []EventInput{first}, at)

	// A complete derivation that looked at only a1 cannot say a2 is gone.
	partial := ev("evt_c", "Acme buys Beta", 4, map[string]model.Role{"ACME": model.RoleSubject}, model.Evidence{ArticleID: "a1", SourceID: "wire"})
	partial.Complete, partial.Scope = true, map[string]bool{"a1": true}
	db.PutEvents(ctx, []EventInput{partial}, at.Add(time.Hour))
	hist, _ := db.EventHistory(ctx, "evt_c")
	if last := hist[len(hist)-1]; len(last.Evidence) != 2 || len(last.Securities) != 2 {
		t.Fatalf("an article that was not looked at lost its evidence: %+v", last)
	}

	// One that looked at every article replaces: Beta is no longer part of it.
	whole := ev("evt_c", "Acme buys Beta", 4, map[string]model.Role{"ACME": model.RoleSubject}, model.Evidence{ArticleID: "a1", SourceID: "wire"})
	whole.Complete, whole.Scope = true, map[string]bool{"a1": true, "a2": true}
	db.PutEvents(ctx, []EventInput{whole}, at.Add(2*time.Hour))
	hist, _ = db.EventHistory(ctx, "evt_c")
	last := hist[len(hist)-1]
	if len(last.Evidence) != 1 || len(last.Securities) != 1 || last.Securities[0].Ticker == "" && last.Securities[0].SecurityID == "" {
		t.Fatalf("a derivation that saw everything should replace: %+v", last)
	}
	// Repeating it is not a new revision, and the earlier revisions stay.
	n := len(hist)
	if w, _ := db.PutEvents(ctx, []EventInput{whole}, at.Add(3*time.Hour)); w != 0 {
		t.Errorf("a repeated rebuild wrote %d revisions", w)
	}
	if hist, _ = db.EventHistory(ctx, "evt_c"); len(hist) != n || len(hist[0].Evidence) != 2 {
		t.Errorf("history after a repeat: %d revisions, first has %d evidence", len(hist), len(hist[0].Evidence))
	}
}
