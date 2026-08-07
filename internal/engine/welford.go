// Package engine implements the streaming statistics core of the Edge AI
// Telemetry Daemon: Welford's online mean/variance, an exponential moving
// average for drift tracking, and a streaming Z-score anomaly filter built on
// top of both.
//
// All types are engineered for zero heap allocations on the hot path: state
// lives in value-typed structs mutated through pointer receivers, so a
// long-running sample loop performs no garbage collection pressure.
package engine

import "math"

// Welford is an online (streaming) aggregator of mean and variance computed
// with Welford's algorithm. It maintains running sums of squared deviations
// from the current mean, yielding O(1) time and O(1) space per observation and
// numerical stability superior to the naive "sum of squares minus mean-squared"
// two-pass approach.
//
// The zero value is ready to use: Count starts at zero and the first Update
// transitions the running sums correctly.
type Welford struct {
	count uint64  // number of observations seen so far
	mean  float64 // current arithmetic mean of all observations
	m2    float64 // running sum of squared deviations from the mean
}

// Update folds the observation x into the running statistics. It is the only
// mutating entry point; it performs no allocation.
func (w *Welford) Update(x float64) {
	w.count++
	delta := x - w.mean
	w.mean += delta / float64(w.count)
	delta2 := x - w.mean
	w.m2 += delta * delta2
}

// Merge folds the statistics of another Welford state into w, enabling
// parallel aggregation across workers. The resulting state is identical to
// having fed every observation from both streams through a single Update.
//
// num1 and num2 (used below) are the count of each stream; the standard
// parallel-combination formulas from Chan et al. (1983) are applied. Merge
// allocates nothing.
func (w *Welford) Merge(o *Welford) {
	if o.count == 0 {
		return
	}
	if w.count == 0 {
		*w = *o
		return
	}

	count := w.count + o.count
	delta := o.mean - w.mean
	// Mean of the combined population is the count-weighted average.
	w.mean += delta * float64(o.count) / float64(count)
	// m2 combines by adding the two within-stream deviations and the
	// between-stream correction term.
	w.m2 += o.m2 + delta*delta*float64(w.count)*float64(o.count)/float64(count)
	w.count = count
}

// Count returns the number of observations aggregated so far.
func (w *Welford) Count() uint64 {
	return w.count
}

// Mean returns the arithmetic mean of all observations seen so far. It returns
// NaN when no observations have been fed.
func (w *Welford) Mean() float64 {
	if w.count == 0 {
		return math.NaN()
	}
	return w.mean
}

// Variance returns the unbiased sample variance of all observations seen so
// far. It requires at least two observations, otherwise it returns NaN.
func (w *Welford) Variance() float64 {
	if w.count < 2 {
		return math.NaN()
	}
	return w.m2 / float64(w.count-1)
}

// PopulationVariance returns the biased (n-divided) variance of all
// observations seen so far. It requires at least one observation, otherwise it
// returns NaN.
func (w *Welford) PopulationVariance() float64 {
	if w.count == 0 {
		return math.NaN()
	}
	return w.m2 / float64(w.count)
}

// StdDev returns the unbiased sample standard deviation of all observations
// seen so far. It requires at least two observations, otherwise it returns NaN.
func (w *Welford) StdDev() float64 {
	return math.Sqrt(w.Variance())
}

// Reset clears the running statistics, returning the instance to its zero
// state so it can be reused without reallocation.
func (w *Welford) Reset() {
	w.count = 0
	w.mean = 0
	w.m2 = 0
}
