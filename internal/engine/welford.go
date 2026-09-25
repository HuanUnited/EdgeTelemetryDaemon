package engine

import "math"

// Deprecated: Welford is deprecated in favor of StreamingStats (EWMV) for non-stationary
// telemetry streams to avoid statistical calcification as N approaches infinity.
// Retained temporarily for backward compatibility with external integrations.
type Welford struct {
	count uint64
	mean  float64
	m2    float64
}

// Update folds the observation x into the running statistics.
func (w *Welford) Update(x float64) {
	w.count++
	delta := x - w.mean
	w.mean += delta / float64(w.count)
	delta2 := x - w.mean
	w.m2 += delta * delta2
}

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
	w.mean += delta * float64(o.count) / float64(count)
	w.m2 += o.m2 + delta*delta*float64(w.count)*float64(o.count)/float64(count)
	w.count = count
}

func (w *Welford) Count() uint64 { return w.count }
func (w *Welford) Mean() float64 {
	if w.count == 0 {
		return math.NaN()
	}
	return w.mean
}
func (w *Welford) Variance() float64 {
	if w.count < 2 {
		return math.NaN()
	}
	return w.m2 / float64(w.count-1)
}
func (w *Welford) PopulationVariance() float64 {
	if w.count == 0 {
		return math.NaN()
	}
	return w.m2 / float64(w.count)
}
func (w *Welford) StdDev() float64 { return math.Sqrt(w.Variance()) }
func (w *Welford) Reset() {
	w.count = 0
	w.mean = 0
	w.m2 = 0
}
