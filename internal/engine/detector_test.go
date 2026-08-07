package engine

import (
	"math"
	"math/rand/v2"
	"testing"
)

// TestZScoreConstantStreamNeverFlags verifies a constant stream never trips the
// detector (zero scale, so Z-score is pinned to 0).
func TestZScoreConstantStreamNeverFlags(t *testing.T) {
	d := NewZScoreDetector(0.5, 30, 3.5)
	flagged := 0
	for i := 0; i < 500; i++ {
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
	d := NewZScoreDetector(0.5, 30, 3.5)

	// Warm up with a stable stream.
	for i := 0; i < 100; i++ {
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
	d := NewZScoreDetector(0.5, 30, 0.001) // tiny threshold: would flag anything after warm-up
	for i := 0; i < 30; i++ {
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
	d := NewZScoreDetector(0.5, 50, 3.5)
	rng := rand.New(rand.NewPCG(3, 9))
	flagged := 0
	for i := 0; i < 2000; i++ {
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
	d := NewZScoreDetector(0, 0, 0)
	if d.Alpha() != 0.5 {
		t.Errorf("alpha = %v, want 0.5", d.Alpha())
	}
	if d.Threshold() != 3.5 {
		t.Errorf("threshold = %v, want 3.5", d.Threshold())
	}
}

// TestZScoreTracking verifies the observable accessors track the stream.
func TestZScoreTracking(t *testing.T) {
	d := NewZScoreDetector(0.5, 30, 3.5)
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
	d := NewZScoreDetector(0.5, 30, 3.5)
	for i := 0; i < 100; i++ {
		d.Update(1)
	}
	d.Reset()
	if d.Count() != 0 {
		t.Errorf("Count() after Reset = %d, want 0", d.Count())
	}
	if !math.IsNaN(d.ZScore()) {
		t.Errorf("ZScore() after Reset = %v, want NaN", d.ZScore())
	}
	// A fresh stream must re-warm and detect a spike again.
	for i := 0; i < 100; i++ {
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
	d := NewZScoreDetector(0.5, 30, 3.5)
	for i := 0; i < 2000; i++ {
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
		d := NewZScoreDetector(0.5, 30, 3.5)
		flagged := 0
		for i := 0; i < 200; i++ {
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
