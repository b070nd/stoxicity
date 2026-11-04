// Package eval measures how good the lens is against human labels: is "relevant"
// right, and does a score of 0.7 mean 70%? It is also how two lens versions
// are compared before one replaces the other.
package eval

import "math"

// Case is one scored item and what the user said about it.
type Case struct {
	Score    float64 // the lens's probability that the item is relevant
	Relevant bool    // the human label
}

// PrecisionAtK is the share of the k highest-scored cases labelled relevant.
// cases must already be sorted by Score, highest first. A k above len(cases)
// is clamped.
func PrecisionAtK(cases []Case, k int) float64 {
	if k > len(cases) {
		k = len(cases)
	}
	if k <= 0 {
		return 0
	}
	hits := 0
	for _, c := range cases[:k] {
		if c.Relevant {
			hits++
		}
	}
	return float64(hits) / float64(k)
}

// Brier is the mean squared gap between score and outcome; lower is better.
func Brier(cases []Case) float64 {
	if len(cases) == 0 {
		return 0
	}
	var sum float64
	for _, c := range cases {
		o := 0.0
		if c.Relevant {
			o = 1
		}
		sum += (c.Score - o) * (c.Score - o)
	}
	return sum / float64(len(cases))
}

// ECE is the expected calibration error over equal-width bins: how far, on
// average, the stated probability is from the observed share of relevant
// items. bins <= 0 uses 10.
func ECE(cases []Case, bins int) float64 {
	if len(cases) == 0 {
		return 0
	}
	if bins <= 0 {
		bins = 10
	}
	count := make([]int, bins)
	score := make([]float64, bins)
	hits := make([]int, bins)
	for _, c := range cases {
		b := int(c.Score * float64(bins))
		if b >= bins {
			b = bins - 1
		}
		if b < 0 {
			b = 0
		}
		count[b]++
		score[b] += c.Score
		if c.Relevant {
			hits[b]++
		}
	}
	var ece float64
	for b := range count {
		if count[b] == 0 {
			continue
		}
		n := float64(count[b])
		ece += n / float64(len(cases)) * math.Abs(score[b]/n-float64(hits[b])/n)
	}
	return ece
}
