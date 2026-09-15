package engine

import "math"

// ZScoreDetector is a streaming anomaly filter that reports how many standard
// deviations the latest observation sits from an adaptive baseline and evaluates
// dual-horizon exponential moving average drift divergence.
//
// The baseline is maintained in complementary ways:
//   - a fast EWMA of incoming values tracking adaptive reference mean;
//   - a slow EWMA of incoming values tracking long-term baseline divergence; and
//   - a Welford accumulator over demixed observations estimating local scale.
//
// When the Welford accumulator has not yet seen enough samples to estimate a
// standard deviation (fewer than MinSamples), the detector falls back to a
// fractional deviation heuristic so early observations are not flagged.
//
// The zero value is NOT ready to use; construct instances via NewZScoreDetector.
type ZScoreDetector struct {
	alpha          float64 // fast EWMA smoothing factor
	alphaSlow      float64 // slow EWMA smoothing factor
	minSamples     uint64  // minimum Welford samples before Z-score and drift are trustworthy
	threshold      float64 // Z-score magnitude that triggers an anomaly
	driftThreshold float64 // normalized drift divergence threshold

	ewma       EWMA    // fast baseline tracking
	ewmaSlow   EWMA    // slow baseline tracking
	welf       Welford // running variance of the recent window
	n          uint64  // total observations seen
	last       float64 // most recent observation
	zscore     float64 // most recent Z-score (or NaN before MinSamples)
	driftIndex float64 // most recent normalized drift divergence index
	drifting   bool    // reports whether detector is in a drifting state
}

// NewZScoreDetector builds a detector with dual EWMA smoothing factors,
// warm-up sample count, anomaly threshold, and drift divergence threshold.
//
//   - alphaFast is the fast EWMA factor in (0, 1]. Values <= 0 fall back to 0.5;
//     values > 1 are clamped to 1.
//   - alphaSlow is the slow EWMA factor in (0, 1]. Values <= 0 fall back to 0.5;
//     values > 1 are clamped to 1. alphaSlow must be smaller than alphaFast; if
//     alphaSlow >= alphaFast, it is scaled below alphaFast.
//   - minSamples is the number of observations required before a real Z-score
//     and drift index are evaluated; defaults to 30 when 0 is passed.
//   - threshold is the Z-score magnitude that flags an anomaly; values <= 0 fall back to 3.5.
//   - driftThreshold is the normalized drift divergence magnitude that flags drift;
//     values <= 0 fall back to 0.15.
func NewZScoreDetector(alphaFast, alphaSlow float64, minSamples uint64, threshold, driftThreshold float64) *ZScoreDetector {
	if alphaFast <= 0 {
		alphaFast = 0.5
	} else if alphaFast > 1 {
		alphaFast = 1.0
	}

	if alphaSlow <= 0 {
		alphaSlow = 0.5
	} else if alphaSlow > 1 {
		alphaSlow = 1.0
	}

	if alphaSlow >= alphaFast {
		alphaSlow = alphaFast * 0.1
	}

	if minSamples == 0 {
		minSamples = 30
	}
	if threshold <= 0 {
		threshold = 3.5
	}
	if driftThreshold <= 0 {
		driftThreshold = 0.15
	}

	return &ZScoreDetector{
		alpha:          alphaFast,
		alphaSlow:      alphaSlow,
		minSamples:     minSamples,
		threshold:      threshold,
		driftThreshold: driftThreshold,
		ewma:           NewEWMA(alphaFast),
		ewmaSlow:       NewEWMA(alphaSlow),
		zscore:         math.NaN(),
		driftIndex:     0,
		drifting:       false,
	}
}

// Update feeds the next observation x into the detector and returns whether it
// is flagged as an anomaly. The first observation seeds the EWMA baselines and
// is never flagged.
//
// Both fast and slow EWMA horizons are updated on every call. Normalized drift
// divergence is computed and gated by minSamples.
func (d *ZScoreDetector) Update(x float64) bool {
	d.n++
	d.last = x

	d.ewma.Update(x)
	d.ewmaSlow.Update(x)

	driftDenom := math.Max(1.0, d.ewmaSlow.Value())
	d.driftIndex = math.Abs(d.ewma.Value()-d.ewmaSlow.Value()) / driftDenom

	if d.n == 1 {
		d.zscore = 0
		d.drifting = false
		return false
	}

	base := d.ewma.Value()
	dev := x - base

	d.welf.Update(dev)

	if d.welf.Count() < d.minSamples {
		denom := math.Abs(base)
		if denom < 1 {
			denom = 1
		}
		d.zscore = dev / denom
		d.drifting = false
		return false
	}

	d.drifting = d.driftIndex > d.driftThreshold

	sd := d.welf.StdDev()
	if sd == 0 || math.IsNaN(sd) {
		d.zscore = 0
		return false
	}

	d.zscore = dev / sd
	return math.Abs(d.zscore) >= d.threshold
}

// ZScore returns the Z-score of the most recent observation: the deviation
// from the EWMA baseline divided by the Welford standard deviation. It returns
// a signed deviation ratio during warm-up, and 0 for a constant stream (zero
// scale). It returns NaN if no observation has been fed.
func (d *ZScoreDetector) ZScore() float64 {
	return d.zscore
}

// Last returns the most recent observation fed to the detector.
func (d *ZScoreDetector) Last() float64 {
	return d.last
}

// Count returns the total number of observations fed to the detector.
func (d *ZScoreDetector) Count() uint64 {
	return d.n
}

// Baseline returns the current fast EWMA drift baseline.
func (d *ZScoreDetector) Baseline() float64 {
	return d.ewma.Value()
}

// SlowBaseline returns the current slow EWMA drift baseline.
func (d *ZScoreDetector) SlowBaseline() float64 {
	return d.ewmaSlow.Value()
}

// Alpha returns the configured fast EWMA smoothing factor.
func (d *ZScoreDetector) Alpha() float64 {
	return d.alpha
}

// AlphaSlow returns the configured slow EWMA smoothing factor.
func (d *ZScoreDetector) AlphaSlow() float64 {
	return d.alphaSlow
}

// Threshold returns the configured anomaly threshold.
func (d *ZScoreDetector) Threshold() float64 {
	return d.threshold
}

// DriftThreshold returns the configured drift divergence threshold.
func (d *ZScoreDetector) DriftThreshold() float64 {
	return d.driftThreshold
}

// DriftIndex returns the normalized drift divergence index between the fast
// and slow EWMA baselines.
func (d *ZScoreDetector) DriftIndex() float64 {
	return d.driftIndex
}

// IsDrifting reports whether the normalized drift divergence index exceeds
// the drift threshold after warm-up.
func (d *ZScoreDetector) IsDrifting() bool {
	return d.drifting
}

// Reset returns the detector to its initial state so it can be reused without
// reallocation.
func (d *ZScoreDetector) Reset() {
	d.ewma.Reset()
	d.ewmaSlow.Reset()
	d.welf.Reset()
	d.n = 0
	d.last = 0
	d.zscore = math.NaN()
	d.driftIndex = 0
	d.drifting = false
}
