package engine

import (
	"math"
	"math/rand/v2"
	"testing"
)

// TestZScoreConstantStreamNeverFlags verifies a constant stream never trips the
// detector (zero scale, so Z-score is pinned to 0).
func TestZScoreConstantStreamNeverFlags(t *testing.T) {
	d := NewZScoreDetector(0.5, 0.05, 30, 3.5, 0.15)
	flagged := 0
	for range 500 {
		if d.Update(42) {
			flagged++
		}
	}
	if flagged != 0 {
		t.Errorf("constant stream flagged %d times, want 0", flagged)
	}
	if d.ZScore() != 0 {
		t.Errorf("ZScore() = %v, want 0 for zero-scale stream", d.ZScore())
	}
}

// TestZScoreSpikeDetectedAfterWarmup feeds a stable stream and then a clear
// outlier once the detector has warmed up. The outlier must be flagged, and
// the Z-score must exceed the configured threshold.
func TestZScoreSpikeDetectedAfterWarmup(t *testing.T) {
	d := NewZScoreDetector(0.5, 0.05, 30, 3.5, 0.15)

	// Warm up with a stable stream.
	for range 100 {
		if d.Update(10) {
			t.Fatalf("warm-up value 10 flagged as anomaly")
		}
	}

	// A huge outlier must be flagged.
	if !d.Update(1000) {
		t.Fatalf("outlier 1000 was not flagged")
	}
	if math.Abs(d.ZScore()) < d.Threshold() {
		t.Errorf("ZScore() = %v, want |z| >= threshold %v", d.ZScore(), d.Threshold())
	}
}

// TestZScoreNoFlagBeforeWarmup asserts the detector never flags during warm-up
// (before minSamples Welford observations).
func TestZScoreNoFlagBeforeWarmup(t *testing.T) {
	d := NewZScoreDetector(0.5, 0.05, 30, 0.001, 0.15) // tiny threshold: would flag anything after warm-up
	for i := range 30 {
		x := float64(i%3 + 1)
		if d.Update(x) {
			t.Fatalf("iteration %d flagged before warm-up completed", i)
		}
	}
}

// TestZScoreBoundedNoiseNoFalsePositives checks that a bounded noisy stream
// (variance well below the threshold) produces no false positives once warmed
// up.
func TestZScoreBoundedNoiseNoFalsePositives(t *testing.T) {
	d := NewZScoreDetector(0.5, 0.05, 50, 3.5, 0.15)
	rng := rand.New(rand.NewPCG(3, 9))
	flagged := 0
	for range 2000 {
		x := 100 + (rng.Float64()-0.5)*0.2 // uniform in [99.9, 100.1]
		if d.Update(x) {
			flagged++
		}
	}
	if flagged != 0 {
		t.Errorf("bounded noise flagged %d times, want 0", flagged)
	}
}

// TestZScoreDefaultParams verifies the documented defaults are applied when
// zero values are passed.
func TestZScoreDefaultParams(t *testing.T) {
	d := NewZScoreDetector(0, 0, 0, 0, 0)
	if d.Alpha() != 0.5 {
		t.Errorf("alpha = %v, want 0.5", d.Alpha())
	}
	if d.AlphaSlow() >= d.Alpha() {
		t.Errorf("alphaSlow = %v, want < alpha %v", d.AlphaSlow(), d.Alpha())
	}
	if d.Threshold() != 3.5 {
		t.Errorf("threshold = %v, want 3.5", d.Threshold())
	}
	if d.DriftThreshold() != 0.15 {
		t.Errorf("driftThreshold = %v, want 0.15", d.DriftThreshold())
	}
}

// TestZScoreTracking verifies the observable accessors track the stream.
func TestZScoreTracking(t *testing.T) {
	d := NewZScoreDetector(0.5, 0.05, 30, 3.5, 0.15)
	if !math.IsNaN(d.ZScore()) {
		t.Fatalf("ZScore() before any update = %v, want NaN", d.ZScore())
	}
	d.Update(5)
	if d.Count() != 1 {
		t.Errorf("Count() = %d, want 1", d.Count())
	}
	if d.Last() != 5 {
		t.Errorf("Last() = %v, want 5", d.Last())
	}
	if d.Baseline() != 5 {
		t.Errorf("Baseline() = %v, want 5 (seeded)", d.Baseline())
	}
}

// TestZScoreReset confirms Reset returns the detector to a usable fresh state.
func TestZScoreReset(t *testing.T) {
	d := NewZScoreDetector(0.5, 0.05, 30, 3.5, 0.15)
	for range 100 {
		d.Update(1)
	}
	d.Reset()
	if d.Count() != 0 {
		t.Errorf("Count() after Reset = %d, want 0", d.Count())
	}
	if !math.IsNaN(d.ZScore()) {
		t.Errorf("ZScore() after Reset = %v, want NaN", d.ZScore())
	}
	if d.DriftIndex() != 0 {
		t.Errorf("DriftIndex() after Reset = %v, want 0", d.DriftIndex())
	}
	if d.IsDrifting() {
		t.Errorf("IsDrifting() after Reset = true, want false")
	}
	// A fresh stream must re-warm and detect a spike again.
	for range 100 {
		d.Update(1)
	}
	if !d.Update(500) {
		t.Errorf("spike after Reset was not flagged")
	}
}

// TestZScoreRandomStreamNoPanic is a smoke test that arbitrary finite input
// never panics and the Z-score stays finite (or zero) once warmed up.
func TestZScoreRandomStreamNoPanic(t *testing.T) {
	rng := rand.New(rand.NewPCG(1234, 56))
	d := NewZScoreDetector(0.5, 0.05, 30, 3.5, 0.15)
	for i := range 2000 {
		x := rng.NormFloat64()*5 + 100
		d.Update(x)
		z := d.ZScore()
		if math.IsNaN(z) && d.Count() > 1 {
			t.Fatalf("iteration %d: ZScore() = NaN after warm-up", i)
		}
		if math.IsInf(z, 0) {
			t.Fatalf("iteration %d: ZScore() = Inf", i)
		}
	}
}

// TestDualHorizonSlowDriftDetection verifies that a slow ramp is not flagged as
// a single-sample anomaly but triggers drift alert crossing threshold around sample ~3200.
func TestDualHorizonSlowDriftDetection(t *testing.T) {
	d := NewZScoreDetector(1.0, 0.0002, 30, 3.5, 0.80)

	for range 500 {
		d.Update(100.0)
	}

	for i := 1; i <= 5000; i++ {
		val := 100.0 + float64(i)*0.05
		d.Update(val)

		if math.Abs(d.ZScore()) >= 3.5 {
			t.Fatalf("sample %d (val %f): |ZScore()| = %f >= 3.5", d.Count(), val, math.Abs(d.ZScore()))
		}

		if d.Count() < 2000 && d.IsDrifting() {
			t.Fatalf("sample %d: IsDrifting() = true before sample 2000", d.Count())
		}
		if d.Count() == 3500 && !d.IsDrifting() {
			t.Fatalf("sample %d: IsDrifting() = false at sample 3500, want true", d.Count())
		}
	}

	if !d.IsDrifting() {
		t.Fatalf("sample %d: IsDrifting() = false at stream end, want true", d.Count())
	}
}

// TestDualHorizonInstantSpikeRejection verifies that an instant single spike trips
// single-sample anomaly detection but does not trip drift detection or move slow EWMA by >= 0.1%.
func TestDualHorizonInstantSpikeRejection(t *testing.T) {
	d := NewZScoreDetector(0.001, 0.0001, 30, 3.5, 0.15)

	for range 500 {
		d.Update(100.0)
	}

	slowBefore := d.SlowBaseline()

	flagged := d.Update(500.0)
	if !flagged {
		t.Fatalf("Update(500.0) = false, want true")
	}

	slowAfter := d.SlowBaseline()
	relChange := math.Abs(slowAfter-slowBefore) / slowBefore
	if relChange >= 0.001 {
		t.Fatalf("slow EWMA moved by %f%% (>= 0.1%%)", relChange*100)
	}

	if d.IsDrifting() {
		t.Fatalf("IsDrifting() = true immediately after single spike, want false")
	}
}

// FuzzZScoreNoPanic asserts the detector never panics and never returns an
// anomalous flag for a stream made of the same finite value repeated.
func FuzzZScoreNoPanic(f *testing.F) {
	f.Add(float64(1.0))
	f.Add(-100.0)
	f.Add(0.0)
	f.Add(math.MaxFloat64)
	f.Add(1e-9)

	f.Fuzz(func(t *testing.T, base float64) {
		if math.IsNaN(base) || math.IsInf(base, 0) {
			return
		}
		d := NewZScoreDetector(0.5, 0.05, 30, 3.5, 0.15)
		flagged := 0
		for range 200 {
			if d.Update(base) {
				flagged++
			}
		}
		if flagged != 0 {
			t.Fatalf("constant stream of %v flagged %d times, want 0", base, flagged)
		}
		z := d.ZScore()
		if math.IsInf(z, 0) {
			t.Fatalf("ZScore() = Inf for constant stream %v", base)
		}
	})
}
