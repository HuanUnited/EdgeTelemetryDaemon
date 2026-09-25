package engine

import "math"

// StreamingStats implements Exponentially Weighted Moving Variance (EWMV)
// for non-stationary telemetry streams, replacing infinite-memory accumulators
// to avoid statistical calcification.
type StreamingStats struct {
	alpha float64
	mean  float64
	vr    float64
	count uint64
	init  bool
}

// NewStreamingStats initializes an EWMV tracker with smoothing factor alpha.
func NewStreamingStats(alpha float64) StreamingStats {
	if alpha <= 0 {
		alpha = 0.5
	} else if alpha > 1 {
		alpha = 1.0
	}
	return StreamingStats{alpha: alpha}
}

// Update ingests a new observation with strictly zero heap allocation.
func (s *StreamingStats) Update(x float64) {
	s.count++
	if !s.init {
		s.mean = x
		s.vr = 0
		s.init = true
		return
	}

	// Calculate difference against the prior mean
	diff := x - s.mean

	// mu_t = mu_{t-1} + alpha * (x_t - mu_{t-1})
	s.mean += s.alpha * diff

	// sigma^2_t = (1 - alpha) * sigma^2_{t-1} + alpha * (x_t - mu_{t-1})^2
	s.vr = (1-s.alpha)*s.vr + s.alpha*(diff*diff)
}

// Mean returns the exponentially weighted moving average.
func (s *StreamingStats) Mean() float64 {
	if !s.init {
		return math.NaN()
	}
	return s.mean
}

// Variance returns the exponentially weighted moving variance.
func (s *StreamingStats) Variance() float64 {
	if s.count < 2 {
		return math.NaN()
	}
	return s.vr
}

// StdDev returns the exponentially weighted moving standard deviation.
func (s *StreamingStats) StdDev() float64 {
	if s.count < 2 {
		return math.NaN()
	}
	return math.Sqrt(math.Max(0, s.vr))
}

// Count returns the number of observations fed so far.
func (s *StreamingStats) Count() uint64 {
	return s.count
}

// Reset clears the state so the instance can be reused without reallocation.
func (s *StreamingStats) Reset() {
	s.mean = 0
	s.vr = 0
	s.count = 0
	s.init = false
}
