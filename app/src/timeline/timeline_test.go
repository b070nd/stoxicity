package timeline

import (
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
)

func article(day string, relevance float64) model.Article {
	t, _ := time.Parse("2006-01-02", day)
	return model.Article{Published: t.Add(9 * time.Hour), Score: model.Score{Relevance: relevance}}
}

func TestDaily(t *testing.T) {
	from, _ := time.Parse("2006-01-02", "2026-11-01")
	to, _ := time.Parse("2006-01-02", "2026-11-04")
	got := Daily([]model.Article{
		article("2026-11-01", 0.9),
		article("2026-11-01", 0.1),
		article("2026-11-03", 0.8),
		article("2026-10-31", 0.9), // before range
		article("2026-11-09", 0.9), // after range
	}, 0.5, from, to, time.UTC)
	want := []struct{ total, relevant int }{{2, 1}, {0, 0}, {1, 1}, {0, 0}}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Total != w.total || got[i].Relevant != w.relevant {
			t.Errorf("day %d = %+v, want total %d relevant %d", i, got[i], w.total, w.relevant)
		}
	}
}

func TestDailyBackwardsRange(t *testing.T) {
	a, _ := time.Parse("2006-01-02", "2026-11-04")
	b, _ := time.Parse("2006-01-02", "2026-11-01")
	if got := Daily(nil, 0.5, a, b, time.UTC); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestEventsNewestFirstAndRelevantOnly(t *testing.T) {
	got := Events([]model.Article{
		article("2026-11-01", 0.9),
		article("2026-11-03", 0.2),
		article("2026-11-02", 0.7),
	}, 0.5)
	if len(got) != 2 || !got[0].At.After(got[1].At) {
		t.Fatalf("events = %+v", got)
	}
}

func TestDailyUsesTheMarketTimeZone(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tz database:", err)
	}
	// 9 pm on 3 November in New York is 02:00 UTC on the 4th.
	late := model.Article{
		Published: time.Date(2026, 11, 3, 21, 0, 0, 0, ny),
		Score:     model.Score{Relevance: 0.9},
	}
	day := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC) // only the date is used
	next := day.AddDate(0, 0, 1)

	inNY := Daily([]model.Article{late}, 0.5, day, next, ny)
	if inNY[0].Total != 1 || inNY[1].Total != 0 {
		t.Errorf("New York: days = %+v, want the filing on 3 November", inNY)
	}
	inUTC := Daily([]model.Article{late}, 0.5, day, next, time.UTC)
	if inUTC[0].Total != 0 || inUTC[1].Total != 1 {
		t.Errorf("UTC: days = %+v, want the filing on 4 November", inUTC)
	}
}

func TestDailyAcrossDaylightSavingEnd(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tz database:", err)
	}
	// 1 November 2026 has 25 hours in New York; every date must still appear once.
	from := time.Date(2026, 10, 31, 0, 0, 0, 0, ny)
	to := time.Date(2026, 11, 2, 0, 0, 0, 0, ny)
	days := Daily(nil, 0.5, from, to, ny)
	if len(days) != 3 {
		t.Fatalf("got %d days, want 3: %+v", len(days), days)
	}
}
