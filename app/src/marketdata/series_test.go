package marketdata

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func seriesCandle(day string, price float64, volume int64, raw bool) Candle {
	return Candle{Ticker: "AAA", Day: day, Open: price, High: price, Low: price, Close: price, Volume: volume, Raw: raw, Currency: "USD"}
}

func flatBar(day string, price float64, volume int64) Bar {
	return Bar{Day: day, Open: price, High: price, Low: price, Close: price, Volume: volume}
}

func TestPutOnOneBasis(t *testing.T) {
	twoForOne := Split{Ticker: "AAA", Day: "2025-03-03", From: 1, To: 2}
	bad := seriesCandle("2025-03-04", 10, 100, true)
	bad.Low = 0
	upsideDown := seriesCandle("2025-03-05", 10, 100, true)
	upsideDown.High, upsideDown.Low = 9, 10
	noCurrency := seriesCandle("2025-02-27", 80, 100, true)
	noCurrency.Currency = ""
	tests := []struct {
		name    string
		candles []Candle
		splits  []Split
		through string
		want    Series
		why     string
	}{
		{name: "raw candles with a split inside, given newest first",
			candles: []Candle{seriesCandle("2025-03-03", 52, 100, true), seriesCandle("2025-02-28", 100, 100, true)},
			splits:  []Split{twoForOne},
			want:    Series{Ticker: "AAA", Currency: "USD", Bars: []Bar{flatBar("2025-02-28", 50, 200), flatBar("2025-03-03", 52, 100)}}},
		{name: "no split",
			candles: []Candle{seriesCandle("2025-02-28", 100, 100, true), seriesCandle("2025-03-03", 52, 100, true)},
			want:    Series{Ticker: "AAA", Currency: "USD", Bars: []Bar{flatBar("2025-02-28", 100, 100), flatBar("2025-03-03", 52, 100)}}},
		{name: "the split of another ticker changes nothing",
			candles: []Candle{seriesCandle("2025-02-28", 100, 100, true)},
			splits:  []Split{{Ticker: "BBB", Day: "2025-03-03", From: 1, To: 2}},
			want:    Series{Ticker: "AAA", Currency: "USD", Bars: []Bar{flatBar("2025-02-28", 100, 100)}}},
		{name: "a split after the newest candle still sets the basis",
			candles: []Candle{seriesCandle("2025-02-28", 100, 100, true)},
			splits:  []Split{twoForOne},
			want:    Series{Ticker: "AAA", Currency: "USD", Bars: []Bar{flatBar("2025-02-28", 50, 200)}}},
		{name: "a split after through is not applied",
			candles: []Candle{seriesCandle("2025-02-28", 100, 100, true)},
			splits:  []Split{twoForOne},
			through: "2025-03-02",
			want:    Series{Ticker: "AAA", Currency: "USD", Bars: []Bar{flatBar("2025-02-28", 100, 100)}}},
		{name: "two splits multiply, a reverse split shrinks the volume",
			candles: []Candle{seriesCandle("2025-02-28", 100, 100, true), seriesCandle("2025-03-03", 50, 100, true), seriesCandle("2025-03-10", 200, 100, true)},
			splits:  []Split{{Ticker: "aaa", Day: "2025-03-10", From: 4, To: 1}, twoForOne},
			want: Series{Ticker: "AAA", Currency: "USD", Bars: []Bar{
				flatBar("2025-02-28", 200, 50), flatBar("2025-03-03", 200, 25), flatBar("2025-03-10", 200, 100)}}},
		{name: "a split with an invalid ratio changes nothing",
			candles: []Candle{seriesCandle("2025-02-28", 100, 100, true)},
			splits:  []Split{{Ticker: "AAA", Day: "2025-03-03", From: 0, To: 2}},
			want:    Series{Ticker: "AAA", Currency: "USD", Bars: []Bar{flatBar("2025-02-28", 100, 100)}}},
		{name: "unknown basis with a later split",
			candles: []Candle{seriesCandle("2025-02-28", 100, 100, false), seriesCandle("2025-03-03", 52, 100, true)},
			splits:  []Split{twoForOne},
			why:     "candles of unknown basis and a split on 2025-03-03"},
		{name: "unknown basis without a later split is kept as it is",
			candles: []Candle{seriesCandle("2025-03-03", 52, 100, false), seriesCandle("2025-03-04", 53, 100, true)},
			splits:  []Split{twoForOne},
			want:    Series{Ticker: "AAA", Currency: "USD", Bars: []Bar{flatBar("2025-03-03", 52, 100), flatBar("2025-03-04", 53, 100)}}},
		{name: "unknown basis with a later split of ratio one is kept as it is",
			candles: []Candle{seriesCandle("2025-02-28", 100, 100, false)},
			splits:  []Split{{Ticker: "AAA", Day: "2025-03-03", From: 1, To: 1}},
			want:    Series{Ticker: "AAA", Currency: "USD", Bars: []Bar{flatBar("2025-02-28", 100, 100)}}},
		{name: "unknown basis with a split only after through is kept as it is",
			candles: []Candle{seriesCandle("2025-02-28", 100, 100, false)},
			splits:  []Split{twoForOne},
			through: "2025-03-02",
			want:    Series{Ticker: "AAA", Currency: "USD", Bars: []Bar{flatBar("2025-02-28", 100, 100)}}},
		{name: "bad bars are dropped",
			candles: []Candle{seriesCandle("2025-03-03", 52, 100, true), bad, upsideDown},
			want:    Series{Ticker: "AAA", Currency: "USD", Bars: []Bar{flatBar("2025-03-03", 52, 100)}}},
		{name: "only bad bars",
			candles: []Candle{bad, upsideDown},
			why:     "no bars"},
		{name: "of two candles of one day the later stays",
			candles: []Candle{seriesCandle("2025-03-03", 52, 100, true), seriesCandle("2025-03-03", 54, 300, true)},
			want:    Series{Ticker: "AAA", Currency: "USD", Bars: []Bar{flatBar("2025-03-03", 54, 300)}}},
		{name: "the currency is the first one stated",
			candles: []Candle{noCurrency, seriesCandle("2025-02-28", 100, 100, true)},
			want:    Series{Ticker: "AAA", Currency: "USD", Bars: []Bar{flatBar("2025-02-27", 80, 100), flatBar("2025-02-28", 100, 100)}}},
		{name: "empty input", why: "no bars"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			candles := append([]Candle(nil), tc.candles...)
			splits := append([]Split(nil), tc.splits...)
			got, why := PutOnOneBasis("AAA", tc.candles, tc.splits, tc.through)
			if why != tc.why || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, %q; want %+v, %q", got, why, tc.want, tc.why)
			}
			again, whyAgain := PutOnOneBasis("AAA", tc.candles, tc.splits, tc.through)
			if whyAgain != why || !reflect.DeepEqual(again, got) {
				t.Errorf("second call gave %+v, %q; first %+v, %q", again, whyAgain, got, why)
			}
			if !reflect.DeepEqual(candles, tc.candles) || !reflect.DeepEqual(splits, tc.splits) {
				t.Error("the input was changed")
			}
		})
	}
}

type fakeSeriesSource struct {
	candles         map[string][]Candle
	all, asOf       []Split
	candleErr       error
	splitErr        error
	asked           []string
	from, to        string
	candlesKnown    time.Time
	splitsKnown     time.Time
	readAll, readAt int
}

func (f *fakeSeriesSource) CandleSeries(_ context.Context, tickers []string, from, to string, known time.Time) (map[string][]Candle, error) {
	f.asked, f.from, f.to, f.candlesKnown = tickers, from, to, known
	return f.candles, f.candleErr
}

func (f *fakeSeriesSource) Splits(context.Context) ([]Split, error) {
	f.readAll++
	return f.all, f.splitErr
}

func (f *fakeSeriesSource) SplitsAsOf(_ context.Context, known time.Time) ([]Split, error) {
	f.readAt++
	f.splitsKnown = known
	return f.asOf, f.splitErr
}

func TestLoadSeries(t *testing.T) {
	known := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	source := func() *fakeSeriesSource {
		return &fakeSeriesSource{
			candles: map[string][]Candle{
				"AAA": {seriesCandle("2025-02-28", 100, 100, true)},
				"BBB": {seriesCandle("2025-02-28", 100, 100, false)},
			},
			all: []Split{{Ticker: "AAA", Day: "2025-03-03", From: 1, To: 2}, {Ticker: "BBB", Day: "2025-03-03", From: 1, To: 2}},
		}
	}
	tests := []struct {
		name       string
		known      time.Time
		wantClose  float64
		unresolved map[string]string
		all, at    int
	}{
		{name: "no time: every stored split", wantClose: 50, all: 1,
			unresolved: map[string]string{"BBB": "candles of unknown basis and a split on 2025-03-03", "CCC": "no bars"}},
		{name: "a time: the splits held then", known: known, wantClose: 100, at: 1,
			unresolved: map[string]string{"CCC": "no bars"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := source()
			series, unresolved, err := LoadSeries(context.Background(), f, []string{"CCC", "BBB", "", "AAA", "BBB"}, "2025-01-01", "2025-04-01", tc.known)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(f.asked, []string{"AAA", "BBB", "CCC"}) || f.from != "2025-01-01" || f.to != "2025-04-01" || !f.candlesKnown.Equal(tc.known) {
				t.Errorf("asked %v from %s to %s known %v", f.asked, f.from, f.to, f.candlesKnown)
			}
			if f.readAll != tc.all || f.readAt != tc.at || !f.splitsKnown.Equal(tc.known) {
				t.Errorf("splits read %d times, as of a time %d times (%v)", f.readAll, f.readAt, f.splitsKnown)
			}
			if a := series["AAA"]; len(a.Bars) != 1 || a.Bars[0].Close != tc.wantClose || a.Ticker != "AAA" || a.Currency != "USD" {
				t.Errorf("AAA = %+v", a)
			}
			if !reflect.DeepEqual(unresolved, tc.unresolved) || len(series)+len(unresolved) != 3 {
				t.Errorf("unresolved = %v, series = %v", unresolved, series)
			}
		})
	}
	t.Run("no ticker reads nothing", func(t *testing.T) {
		f := source()
		series, unresolved, err := LoadSeries(context.Background(), f, []string{""}, "", "", time.Time{})
		if err != nil || len(series) != 0 || len(unresolved) != 0 || f.asked != nil || f.readAll+f.readAt != 0 {
			t.Errorf("%v %v %v, asked %v", series, unresolved, err, f.asked)
		}
	})
	t.Run("errors of the source", func(t *testing.T) {
		boom := errors.New("boom")
		f := source()
		f.candleErr = boom
		if _, _, err := LoadSeries(context.Background(), f, []string{"AAA"}, "", "", time.Time{}); !errors.Is(err, boom) || err.Error() != "marketdata: candles: boom" {
			t.Errorf("candles: %v", err)
		}
		f = source()
		f.splitErr = boom
		if _, _, err := LoadSeries(context.Background(), f, []string{"AAA"}, "", "", known); !errors.Is(err, boom) || err.Error() != "marketdata: splits: boom" {
			t.Errorf("splits: %v", err)
		}
	})
}
