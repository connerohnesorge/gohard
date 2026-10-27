package main

import (
	"math"
	"slices"
)

// Budget is the keystroke budget of #19 in milliseconds: a check runs
// as you type when its warm p95 on the large project is under it.
const Budget = 50.0

// Percentile returns the nearest-rank p-th percentile (0 < p <= 100)
// of xs, or NaN for no samples.
func Percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	i := int(math.Ceil(p/100*float64(len(s)))) - 1
	return s[min(max(i, 0), len(s)-1)]
}

// Tier is the #19 rule for one check from its warm p95 on the large
// project.
func Tier(p95 float64) string {
	if math.IsNaN(p95) {
		return "n/a"
	}
	if p95 < Budget {
		return "as you type"
	}
	return "on save"
}

// Gate is one #25 latency gate: the measured p95 against the limit.
type Gate struct {
	Name     string
	LimitMS  float64
	P95MS    float64
	Measured bool
}

// Pass reports whether the gate holds (an unmeasured gate does not).
func (g Gate) Pass() bool { return g.Measured && g.P95MS < g.LimitMS }
