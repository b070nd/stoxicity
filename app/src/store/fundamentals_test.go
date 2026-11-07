package store

import (
	"context"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/fundamentals"
)

func TestFundamentalsAreReplacedPerPeriodAndOldOnesDropOut(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	if got, err := db.FundamentalsRead(ctx); err != nil || !got.IsZero() {
		t.Fatalf("never read: %v %v", got, err)
	}
	f := func(metric, start, end string, v float64) fundamentals.Fact {
		return fundamentals.Fact{CIK: "0000000001", Metric: metric, Start: start, End: end, Value: v}
	}
	if err := db.PutFundamentals(ctx, []fundamentals.Fact{
		f("revenue", "2025-01-01", "2025-12-31", 100),
		f("assets", "", "2025-12-31", 500),
		f("revenue", "2020-01-01", "2020-12-31", 1), // older than four years
	}, at); err != nil {
		t.Fatal(err)
	}
	if err := db.PutFundamentals(ctx, []fundamentals.Fact{f("revenue", "2025-01-01", "2025-12-31", 110)}, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := db.Fundamentals(ctx)
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v, %v", got, err)
	}
	for _, x := range got {
		if x.Metric == "revenue" && (x.Value != 110 || x.Start != "2025-01-01") {
			t.Errorf("the period was not replaced: %+v", x)
		}
	}
	if r, _ := db.FundamentalsRead(ctx); !r.Equal(at.Add(time.Hour)) {
		t.Errorf("read at %v", r)
	}
}

func TestFactHistoryIsAppendOnlyAndAFramesFactLearnsItsFilingDay(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	frames := fundamentals.Fact{CIK: "0000000001", Metric: fundamentals.Revenue, Start: "2024-01-01", End: "2024-12-31", Value: 100,
		Accession: "0001-25-000001", Taxonomy: "us-gaap", Concept: "Revenues", Unit: "USD"}
	if err := db.PutFundamentals(ctx, []fundamentals.Fact{frames}, at(1)); err != nil {
		t.Fatal(err)
	}
	got, _ := db.FactHistory(ctx, "0000000001")
	if len(got) != 1 || got[0].Filed != "" {
		t.Fatalf("the frames do not say when it was filed: %+v", got)
	}
	hist := frames
	hist.Filed, hist.Form = "2025-02-20", "10-K"
	restated := hist
	restated.Value, restated.Accession, restated.Filed, restated.Form = 110, "0001-25-000099", "2025-06-01", "10-K/A"
	n, err := db.PutFactHistory(ctx, []fundamentals.Fact{hist, restated}, at(2))
	if err != nil || n != 1 {
		t.Fatalf("one new fact (the restatement), the other only learned its day: %d, %v", n, err)
	}
	got, _ = db.FactHistory(ctx, "0000000001")
	if len(got) != 2 || got[0].Filed != "2025-02-20" || got[0].Value != 100 || got[1].Value != 110 {
		t.Errorf("history: %+v", got)
	}
	// The newest-figures table is untouched by the history.
	if cur, _ := db.Fundamentals(ctx); len(cur) != 1 || cur[0].Value != 100 {
		t.Errorf("current: %+v", cur)
	}
}
