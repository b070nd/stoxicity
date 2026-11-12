package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/eventfact"
	"github.com/b070nd/stoxicity/app/src/eventstudy"
	"github.com/b070nd/stoxicity/app/src/marketdata"
	"github.com/b070nd/stoxicity/app/src/playbook"
	"github.com/b070nd/stoxicity/app/src/store"
)

// labJSON is the output of an example command without its comment lines.
func labJSON(out string) string {
	var keep []string
	for _, l := range strings.Split(out, "\n") {
		if !strings.HasPrefix(l, "#") {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, "\n")
}

func TestLabExamplesParseAndValidate(t *testing.T) {
	e := newTestEnv(t)
	r := e.run(t, "study", "example")
	r.wantCode(t, exitOK)
	var spec eventstudy.Spec
	if err := json.Unmarshal([]byte(labJSON(r.out)), &spec); err != nil {
		t.Fatalf("study example: %v\n%s", err, r.out)
	}
	if err := spec.Validate(); err != nil || spec.ID != "insider-buys-v1" {
		t.Fatalf("study example does not validate: %v", err)
	}
	r = e.run(t, "playbook", "example")
	r.wantCode(t, exitOK)
	var p playbook.Playbook
	if err := json.Unmarshal([]byte(labJSON(r.out)), &p); err != nil {
		t.Fatalf("playbook example: %v\n%s", err, r.out)
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("playbook example does not validate: %v", err)
	}
}

func TestLabUsageErrors(t *testing.T) {
	e := newTestEnv(t)
	for _, args := range [][]string{{"eventdb"}, {"study", "run"}, {"playbook", "signals"}, {"eventdb", "list", "-type", "nope"}} {
		r := e.run(t, args...)
		r.wantCode(t, exitUsage)
		if r.out != "" {
			t.Errorf("%v: stdout not empty:\n%s", args, r.out)
		}
	}
}

func TestLabStudyAndPlaybookEndToEnd(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	db, err := store.Open(filepath.Join(e.dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	var recs []eventfact.Record
	for i, sym := range []string{"AAA", "BBB", "CCC"} {
		at := time.Date(2025, 6, 2+i, 14, 0, 0, 0, time.UTC)
		key := eventfact.NewKey(eventfact.Insider, sym, "buy", at.Format(time.DateOnly))
		recs = append(recs, eventfact.Record{ID: eventfact.ID(key), Key: key, Category: eventfact.Insider, Type: eventfact.InsiderBuy,
			Symbol: sym, PublishedAt: at, AvailableAt: at, Precision: eventfact.PrecisionTime, Source: eventfact.SourceFiling,
			SourceRef: "acc-" + sym, Evidence: []string{"acc-" + sym}, Basis: eventfact.Known, Extractor: "test", Version: "1"})
	}
	if _, err := db.PutEventFacts(ctx, recs, testNow); err != nil {
		t.Fatal(err)
	}
	var candles []marketdata.Candle
	for _, sym := range []string{"AAA", "BBB", "CCC", "SPY"} {
		px := 100.0
		for d := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC); d.Before(time.Date(2025, 10, 1, 0, 0, 0, 0, time.UTC)); d = d.AddDate(0, 0, 1) {
			if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
				continue
			}
			px *= 1.0005
			candles = append(candles, marketdata.Candle{Ticker: sym, Day: d.Format(time.DateOnly), Open: px, High: px * 1.01, Low: px * 0.99, Close: px, Volume: 1000, Raw: true, Currency: "USD"})
		}
	}
	if err := db.PutCandles(ctx, candles); err != nil {
		t.Fatal(err)
	}
	db.Close()

	spec := eventstudy.DefaultSpec()
	spec.ID, spec.Types, spec.Benchmark = "insider-buys-v1", []string{"insider_buy"}, "SPY"
	spec.Development = eventstudy.Window{From: "2024-01-01", To: "2025-05-31"}
	spec.Validation = eventstudy.Window{From: "2025-06-01", To: "2025-12-31"}
	sb, _ := json.Marshal(spec)
	specFile := writeFile(t, e, "study.json", string(sb))
	pb := e.run(t, "playbook", "example")
	pbFile := writeFile(t, e, "playbook.json", labJSON(pb.out))

	e.run(t, "study", "register", "-file", specFile).wantOut(t, "insider-buys-v1 registered", "1 study(ies)")
	e.run(t, "playbook", "register", "-file", pbFile).wantOut(t, "revision 1")
	e.run(t, "eventdb", "coverage").wantOut(t, "insider_buy")
	e.run(t, "study", "run", "insider-buys-v1").wantOut(t, "insufficient_history")
	e.run(t, "study", "run", "insider-buys-v1").wantOut(t, "has been read 1 time(s) before")
	e.run(t, "study", "list").wantOut(t, "insider-buys-v1", "insufficient_history")
	e.run(t, "study", "last", "insider-buys-v1").wantOut(t, "insufficient_history")

	r := e.run(t, "playbook", "signals", "insider-buys-pb-v1", "-from", "2025-06-01", "-to", "2025-06-30", "-save")
	r.wantCode(t, exitOK)
	r.wantOut(t, "WATCH", "saved 3 new signal(s)")
	e.run(t, "playbook", "signals", "insider-buys-pb-v1", "-from", "2025-06-01", "-to", "2025-06-30", "-save").wantOut(t, "saved 0 new signal(s)")
	e.run(t, "playbook", "history", "insider-buys-pb-v1").wantOut(t, "WATCH")

	db, err = store.Open(filepath.Join(e.dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	logs, err := db.Orders("default").Logs(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 0 {
		t.Fatalf("an order was written: %v", logs)
	}
}
