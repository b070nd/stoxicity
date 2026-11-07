package store

import (
	"context"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/insider"
	"github.com/b070nd/stoxicity/app/src/model"
)

func TestInsiderFilingsAreReadOnceAndTheirTradesComeBackNewestFirst(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	form4 := func(id, form, cik string, age time.Duration) model.Article {
		a := art(id, now.Add(-age))
		a.Form, a.CIK = form, cik
		return a
	}
	if err := db.Save(ctx, []model.Article{
		form4("0000000001-26-000001", "4", "0000000001", time.Hour),
		form4("0000000001-26-000002", "4", "0000000001", 2*time.Hour),
		form4("0000000001-26-000003", "4/A", "0000000001", 3*time.Hour),    // an amendment is read too
		form4("0000000001-26-000004", "8-K", "0000000001", 4*time.Hour),    // not a Form 4
		form4("0000000001-26-000005", "4", "", 5*time.Hour),                // no company
		form4("0000000001-25-000006", "4", "0000000001", 300*24*time.Hour), // too old
	}); err != nil {
		t.Fatal(err)
	}
	list, total, err := db.UnreadForm4(ctx, now.Add(-insider.Window), 1)
	if err != nil || total != 3 || len(list) != 1 || list[0].Accession != "0000000001-26-000001" || list[0].Filed != "2026-11-07" {
		t.Fatalf("%+v, %d, %v", list, total, err)
	}

	trades := []model.InsiderTrade{
		{Seq: 1, Owner: "Doe Jane", OwnerCIK: "0000000009", Role: "director", Security: "Common Stock", Form: "4", Period: "2026-11-05", Date: "2026-11-05", Code: "P", Acquired: true, Shares: 100, Price: 12.5, After: 900, Direct: true},
		{Seq: 2, Owner: "Doe Jane", Date: "2026-11-06", Code: "S", Shares: 5, Price: 13, Plan: true},
	}
	if err := db.PutInsider(ctx, list[0], trades, "", now); err != nil {
		t.Fatal(err)
	}
	if err := db.PutInsider(ctx, insider.Filing{Accession: "0000000001-26-000002", CIK: "0000000001", Filed: "2026-11-07"}, nil, "not an ownership document", now); err != nil {
		t.Fatal(err)
	}
	if list, total, _ = db.UnreadForm4(ctx, now.Add(-insider.Window), 10); len(list) != 1 || total != 1 || list[0].Accession != "0000000001-26-000003" {
		t.Errorf("two were read, one with a problem; the amendment is left: %+v %d", list, total)
	}
	if err := db.PutInsider(ctx, list[0], nil, "", now); err != nil {
		t.Fatal(err)
	}
	// Writing the same filing again replaces its trades instead of adding.
	if err := db.PutInsider(ctx, insider.Filing{Accession: "0000000001-26-000001", CIK: "0000000001", Filed: "2026-11-07"}, trades, "", now); err != nil {
		t.Fatal(err)
	}
	got, err := db.InsiderTrades(ctx, "2026-11-01", "0000000001", 0)
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v, %v", got, err)
	}
	if got[0].Code != "S" || !got[0].Plan || got[1].Code != "P" || !got[1].Acquired || got[1].After != 900 || got[1].Role != "director" || got[1].CIK != "0000000001" {
		t.Errorf("%+v", got)
	}
	if only, _ := db.InsiderTrades(ctx, "2026-11-06", "", 1); len(only) != 1 || only[0].Code != "S" {
		t.Errorf("since and limit: %+v", only)
	}
	if none, _ := db.InsiderTrades(ctx, "2026-11-01", "0000000002", 0); len(none) != 0 {
		t.Errorf("another company: %+v", none)
	}
}

func TestInsiderTradesOlderThanAYearAreDropped(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	old := insider.Filing{Accession: "0000000001-25-000001", CIK: "0000000001", Filed: "2025-01-02"}
	tr := []model.InsiderTrade{{Seq: 1, Owner: "x", Date: "2025-01-02", Code: "P", Shares: 1, Price: 1}}
	if err := db.PutInsider(ctx, old, tr, "", now.AddDate(-1, 0, 0).Add(-10*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.PutInsider(ctx, insider.Filing{Accession: "0000000001-26-000001", CIK: "0000000001", Filed: "2026-11-06"}, nil, "", now); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.InsiderTrades(ctx, "2000-01-01", "", 0); len(got) != 0 {
		t.Errorf("a trade filed 400 days ago is gone: %+v", got)
	}
}
