package main

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/marketdata"
	"github.com/b070nd/stoxicity/app/src/store"
	"github.com/b070nd/stoxicity/app/src/workspace"
)

func TestPublicHistoryFetchFillsBothStripsWithoutAKeyAndKeepsDailyCandle(t *testing.T) {
	e := newTestEnv(t)
	t.Setenv("STOXICITY_CONTACT", "")
	t.Setenv("STOXICITY_POLYGON_KEY", "")
	t.Setenv("MASSIVE_API_KEY", "")
	writeFile(t, e, "config.yaml", "data_dir: ./data\ncompany:\n  enabled: false\n  history: yahoo\n  history_url: "+e.server.URL+"/history/\n  quotes: off\n")
	var hits atomic.Int32
	e.server.Config.Handler.(*http.ServeMux).HandleFunc("/history/ACME", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		var stamps []int64
		var closes []float64
		for day := time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC); !day.After(testNow); day = day.AddDate(0, 1, 0) {
			stamps = append(stamps, day.Unix())
			closes = append(closes, 100+float64(len(closes))*.5)
		}
		json.NewEncoder(w).Encode(map[string]any{"chart": map[string]any{"result": []any{map[string]any{"meta": map[string]any{"symbol": "ACME", "currency": "USD", "exchangeTimezoneName": "UTC", "dataGranularity": "1mo", "regularMarketTime": testNow.Add(-time.Hour).Unix()}, "timestamp": stamps, "indicators": map[string]any{"quote": []any{map[string]any{"close": closes}}}}}}})
	})
	db, err := store.Open(filepath.Join(e.dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutCandles(context.Background(), []marketdata.Candle{{Ticker: "ACME", Day: "2026-11-02", Open: 42, High: 42, Low: 42, Close: 42, Raw: true}}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	e.run(t, "universe", "history", "ACME").wantOut(t, "1 asked, 1 stored")
	e.run(t, "universe", "history", "ACME").wantOut(t, "0 asked")
	e.run(t, "universe", "history", "-status", "ACME").wantOut(t, "1 symbols stored", "ACME: 131 months")
	if hits.Load() != 1 {
		t.Fatalf("history fetched repeatedly: %d", hits.Load())
	}
	w, err := workspace.Open(workspace.Options{Root: e.dir, Cwd: e.dir, Now: func() time.Time { return testNow }})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	calendars, err := w.PriceCalendars()
	if err != nil {
		t.Fatal(err)
	}
	for _, month := range calendars["ACME"].Months {
		if month.Day == "" || !month.HasChange {
			t.Fatalf("12M not filled: %+v", month)
		}
	}
	for _, year := range calendars["ACME"].Years {
		if year.Day == "" || !year.HasChange {
			t.Fatalf("10Y not filled: %+v", year)
		}
	}
	if w.Quotes()["ACME"].Close != 42 {
		t.Fatal("daily candle replaced by monthly aggregate")
	}
}
