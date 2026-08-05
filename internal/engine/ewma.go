package engine

import (
	"math"
)

// EWMA is a streaming exponential moving average used to track a slowly
// drifting baseline (for example, a model's expected throughput) while
// remaining insensitive to short spikes.
//
// The smoothing is controlled by Alpha in (0, 1]; larger values weight recent
// observations more heavily. Because the update equation
//
//	value = alpha*x + (1-alpha)*value
//
// depends only on the previous value, EWMA is O(1) time and O(1) space per
// sample, with no allocation.
//
// The zero value is NOT ready to use: the first Update would produce a
// degenerate result (alpha=0 discards the observation entirely). Construct an
// instance with NewEWMA, which seeds the baseline from the first observation.
type EWMA struct {
	alpha float64
	value float64
	init  bool // reports whether a baseline has been established
}

// NewEWMA returns an EWMA with the given smoothing factor. alpha is clamped
// into (0, 1]: values <= 0 fall back to 0.5, values > 1 are clamped to 1.
// The instance starts uninitialised; the first Update seeds the baseline.
func NewEWMA(alpha float64) EWMA {
	switch {
	case alpha > 1:
		alpha = 1
	case alpha <= 0:
		alpha = 0.5
	}
	return EWMA{alpha: alpha}
}

// Update folds x into the moving average. The first call establishes the
// baseline equal to x; subsequent calls apply the alpha-weighted update.
// Update never allocates.
func (e *EWMA) Update(x float64) {
	if !e.init {
		e.value = x
		e.init = true
		return
	}
	e.value = e.alpha*x + (1-e.alpha)*e.value
}

// Value returns the current moving average. It returns NaN when no observation
// has been fed yet.
func (e *EWMA) Value() float64 {
	if !e.init {
		return math.NaN()
	}
	return e.value
}

// Alpha returns the configured smoothing factor.
func (e EWMA) Alpha() float64 {
	return e.alpha
}

// Reset returns the instance to its uninitialised state so it can be reused
// without reallocation.
func (e *EWMA) Reset() {
	e.init = false
	e.value = 0
}
