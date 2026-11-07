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
