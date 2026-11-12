package eventstudy

import (
	"math"
	"math/rand/v2"
	"sort"
)

// summarize describes one measure over the events of a group. values[i] belongs
// to the block of entry days clusters[i] (same length).
func summarize(values []float64, clusters []string, sessions int, alpha float64, resamples int, seed uint64) Stats {
	n := len(values)
	if n == 0 {
		return Stats{P: 1}
	}
	byCluster := map[string][]float64{}
	for i, v := range values {
		label := ""
		if i < len(clusters) {
			label = clusters[i]
		}
		byCluster[label] = append(byCluster[label], v)
	}
	labels := sortedKeys(byCluster)

	var sum float64
	worst, best := values[0], values[0]
	wins := 0
	for _, v := range values {
		sum += v
		worst, best = math.Min(worst, v), math.Max(best, v)
		if v > 0 {
			wins++
		}
	}
	mean := sum / float64(n)
	var std float64
	if n >= 2 {
		var ss float64
		for _, v := range values {
			ss += (v - mean) * (v - mean)
		}
		std = math.Sqrt(ss / float64(n-1))
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	median := sorted[n/2]
	if n%2 == 0 {
		median = (sorted[n/2-1] + sorted[n/2]) / 2
	}
	var risk float64
	if std > 0 && sessions >= 1 {
		risk = mean / std * math.Sqrt(252/float64(sessions))
	}

	st := Stats{
		N: n, Clusters: len(labels), Mean: mean, Median: median,
		WinRate: float64(wins) / float64(n), StdDev: std, Worst: worst, Best: best,
		RiskAdjusted: risk, Lower: mean, Upper: mean, P: 1,
	}
	if len(labels) >= 2 && resamples > 0 {
		means := bootstrap(byCluster, labels, resamples, seed)
		sort.Float64s(means)
		st.Lower = quantile(means, alpha/2)
		st.Upper = quantile(means, 1-alpha/2)
		le, ge := 0, 0
		for _, m := range means {
			if m <= 0 {
				le++
			}
			if m >= 0 {
				ge++
			}
		}
		// One is added to both counts: a resampling of B draws cannot show a
		// probability below 1/(B+1), and reporting zero would pass any
		// correction for the number of groups tested.
		st.P = math.Min(1, 2*(math.Min(float64(le), float64(ge))+1)/float64(len(means)+1))
	}
	return finite(st)
}

// bootstrap draws as many clusters as there are, with replacement, resamples
// times, and returns the mean of the pooled values of each draw.
func bootstrap(byCluster map[string][]float64, labels []string, resamples int, seed uint64) []float64 {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	sums := make([]float64, len(labels))
	counts := make([]int, len(labels))
	for i, l := range labels {
		for _, v := range byCluster[l] {
			sums[i] += v
		}
		counts[i] = len(byCluster[l])
	}
	means := make([]float64, resamples)
	for r := range means {
		var s float64
		c := 0
		for range labels {
			k := rng.IntN(len(labels))
			s += sums[k]
			c += counts[k]
		}
		means[r] = s / float64(c)
	}
	return means
}

// quantile is the q-quantile of sorted values by linear interpolation.
func quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	pos := math.Min(math.Max(q, 0), 1) * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	return sorted[lo] + (sorted[hi]-sorted[lo])*(pos-float64(lo))
}

// finite replaces any value that is not a finite number with zero, so that no
// report holds a NaN or an infinity.
func finite(s Stats) Stats {
	for _, p := range []*float64{&s.Mean, &s.Median, &s.WinRate, &s.StdDev, &s.Worst, &s.Best, &s.RiskAdjusted, &s.Lower, &s.Upper, &s.P} {
		if math.IsNaN(*p) || math.IsInf(*p, 0) {
			*p = 0
		}
	}
	return s
}

// holm returns the Holm step-down adjusted p-values, in the order given.
func holm(ps []float64) []float64 {
	m := len(ps)
	out := make([]float64, m)
	if m == 0 {
		return out
	}
	order := make([]int, m)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return ps[order[a]] < ps[order[b]] })
	running := 0.0
	for j, idx := range order {
		running = math.Max(running, math.Min(1, float64(m-j)*ps[idx]))
		out[idx] = running
	}
	return out
}
