package eventstudy

import "math"

// contextAt measures the conditions around an event from the bars before its
// entry session: bars[:entry] of the stock and market[:marketEntry] of the
// benchmark. marketEntry is negative when there is no benchmark bar for the
// entry day.
func contextAt(spec ContextSpec, bars []Bar, entry int, market []Bar, marketEntry int, marketCap float64) Context {
	ctx := unknownContext(spec)
	before := bars[:clamp(entry, len(bars))]

	if v, ok := volatility(before, spec.VolatilitySessions); ok {
		ctx.Volatility, ctx.VolatilityValue = bucketOf(v, spec.VolatilityEdges), &v
	}
	if v, ok := momentum(before, spec.MomentumSessions); ok {
		ctx.Momentum, ctx.MomentumValue = bucketOf(v, spec.MomentumEdges), &v
	}
	if spec.LiquiditySessions > 0 {
		if v, ok := tradedValue(before, spec.LiquiditySessions); ok {
			ctx.Liquidity, ctx.LiquidityValue = bucketOf(v, spec.LiquidityEdges), &v
		}
	}
	if marketCap > 0 && !math.IsInf(marketCap, 0) {
		ctx.Size = bucketOf(marketCap, spec.SizeEdges)
	}
	if marketEntry >= 0 {
		mb := market[:clamp(marketEntry, len(market))]
		if v, ok := trend(mb, spec.TrendSessions); ok {
			ctx.MarketTrend, ctx.MarketTrendValue = "down", &v
			if v > 0 {
				ctx.MarketTrend = "up"
			}
		}
		if v, ok := volatility(mb, spec.VolatilitySessions); ok {
			ctx.MarketVolatility, ctx.MarketVolatilityValue = bucketOf(v, spec.VolatilityEdges), &v
		}
		if len(spec.RelativeEdges) > 0 && ctx.MomentumValue != nil {
			if m, ok := momentum(mb, spec.MomentumSessions); ok {
				v := *ctx.MomentumValue - m
				ctx.RelativeMomentum, ctx.RelativeMomentumValue = bucketOf(v, spec.RelativeEdges), &v
			}
		}
	}
	return ctx
}

func clamp(i, n int) int {
	return max(0, min(i, n))
}

// bucketOf names the third of the range a value falls in: below the first edge
// low, below the second mid, otherwise high.
func bucketOf(v float64, edges []float64) string {
	if len(edges) < 2 || math.IsNaN(v) || math.IsInf(v, 0) {
		return Unknown
	}
	switch {
	case v < edges[0]:
		return "low"
	case v < edges[1]:
		return "mid"
	}
	return "high"
}

// volatility is the annualised sample standard deviation of the last n daily
// simple returns of the bars; false when the bars hold fewer than n+1 closes.
func volatility(bars []Bar, n int) (float64, bool) {
	if n < 2 || len(bars) < n+1 {
		return 0, false
	}
	tail := bars[len(bars)-n-1:]
	rets := make([]float64, n)
	var mean float64
	for i := range rets {
		rets[i] = tail[i+1].Close/tail[i].Close - 1
		mean += rets[i]
	}
	mean /= float64(n)
	var ss float64
	for _, r := range rets {
		ss += (r - mean) * (r - mean)
	}
	v := math.Sqrt(ss/float64(n-1)) * math.Sqrt(252)
	return v, !math.IsNaN(v) && !math.IsInf(v, 0)
}

// momentum is the last close over the close n sessions earlier, less one.
func momentum(bars []Bar, n int) (float64, bool) {
	if n < 1 || len(bars) < n+1 {
		return 0, false
	}
	v := bars[len(bars)-1].Close/bars[len(bars)-1-n].Close - 1
	return v, !math.IsNaN(v) && !math.IsInf(v, 0)
}

// tradedValue is the mean of close times volume over the last n bars; false
// when the bars hold fewer than n, one has a negative volume, or nothing traded.
func tradedValue(bars []Bar, n int) (float64, bool) {
	if n < 1 || len(bars) < n {
		return 0, false
	}
	var sum float64
	for _, b := range bars[len(bars)-n:] {
		if b.Volume < 0 {
			return 0, false
		}
		sum += b.Close * float64(b.Volume)
	}
	v := sum / float64(n)
	return v, v > 0 && !math.IsInf(v, 0)
}

// trend is the last close against the mean of the last n closes, less one.
func trend(bars []Bar, n int) (float64, bool) {
	if n < 1 || len(bars) < n {
		return 0, false
	}
	var sum float64
	for _, b := range bars[len(bars)-n:] {
		sum += b.Close
	}
	v := bars[len(bars)-1].Close/(sum/float64(n)) - 1
	return v, !math.IsNaN(v) && !math.IsInf(v, 0)
}
