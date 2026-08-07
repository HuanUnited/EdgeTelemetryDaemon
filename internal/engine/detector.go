package engine

import "math"

// ZScoreDetector is a streaming anomaly filter that reports how many standard
// deviations the latest observation sits from an adaptive baseline.
//
// The baseline is maintained in two complementary ways:
//   - an EWMA of the incoming values, which tracks slow drift and is used as
//     the reference mean; and
//   - a Welford accumulator over the (demixed) observations, which provides a
//     robust estimate of the local scale.
//
// When the Welford accumulator has not yet seen enough samples to estimate a
// standard deviation (fewer than MinSamples), the detector falls back to a
// fractional deviation heuristic so that early observations are not flagged.
//
// The zero value is NOT ready to use; construct instances via NewZScoreDetector.
type ZScoreDetector struct {
	alpha      float64 // EWMA smoothing factor
	minSamples uint64  // minimum Welford samples before a Z-score is trustworthy
	threshold  float64 // Z-score magnitude that triggers an anomaly

	ewma   EWMA    // drift-tracking baseline
	welf   Welford // running variance of the recent window
	n      uint64  // total observations seen
	last   float64 // most recent observation
	zscore float64 // most recent Z-score (or NaN before MinSamples)
}

// NewZScoreDetector builds a detector with the given smoothing factor,
// warm-up sample count and anomaly threshold.
//
//   - alpha is the EWMA factor in (0, 1]; larger values track drift faster.
//     Values <= 0 fall back to 0.5; values > 1 are clamped to 1.
//   - minSamples is the number of Welford observations required before a
//     real Z-score is produced; the default of 30 is used when 0 is passed.
//   - threshold is the Z-score magnitude that flags an observation as an
//     anomaly; values <= 0 fall back to 3.5.
func NewZScoreDetector(alpha float64, minSamples uint64, threshold float64) *ZScoreDetector {
	if alpha <= 0 || alpha > 1 {
		alpha = 0.5
	}
	if minSamples == 0 {
		minSamples = 30
	}
	if threshold <= 0 {
		threshold = 3.5
	}
	return &ZScoreDetector{
		alpha:      alpha,
		minSamples: minSamples,
		threshold:  threshold,
		ewma:       NewEWMA(alpha),
		zscore:     math.NaN(),
	}
}

// Update feeds the next observation x into the detector and returns whether it
// is flagged as an anomaly. The first observation seeds the EWMA baseline and
// is never flagged.
//
// Until minSamples Welford observations have accumulated, the returned Z-score
// is a signed deviation ratio (see ZScore) and Update never returns true.
func (d *ZScoreDetector) Update(x float64) bool {
	d.n++
	d.last = x

	if d.n == 1 {
		d.ewma.Update(x)
		d.zscore = 0
		return false
	}

	d.ewma.Update(x)
	base := d.ewma.Value()
	dev := x - base

	d.welf.Update(dev)

	if d.welf.Count() < d.minSamples {
		denom := math.Abs(base)
		if denom < 1 {
			denom = 1
		}
		d.zscore = dev / denom
		return false
	}

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

// Baseline returns the current EWMA drift baseline.
func (d *ZScoreDetector) Baseline() float64 {
	return d.ewma.Value()
}

// Alpha returns the configured EWMA smoothing factor.
func (d *ZScoreDetector) Alpha() float64 {
	return d.alpha
}

// Threshold returns the configured anomaly threshold.
func (d *ZScoreDetector) Threshold() float64 {
	return d.threshold
}

// Reset returns the detector to its initial state so it can be reused without
// reallocation.
func (d *ZScoreDetector) Reset() {
	d.ewma.Reset()
	d.welf.Reset()
	d.n = 0
	d.last = 0
	d.zscore = math.NaN()
}
