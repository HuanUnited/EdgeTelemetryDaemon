package engine

import (
	"math"
	"math/rand/v2"
	"testing"
)

func TestZScoreConstantStreamNeverFlags(t *testing.T) {
	d := NewZScoreDetector(0.5, 0.05, 30, 3.5, 0.15)
	flagged := 0
	for range 500 {
		if d.Update(42.0, 42.0) {
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

func TestZScoreSpikeDetectedAfterWarmup(t *testing.T) {
	d := NewZScoreDetector(0.5, 0.05, 30, 3.5, 0.15)
	for range 100 {
		if d.Update(10.0, 10.0) {
			t.Fatalf("warm-up value flagged as anomaly")
		}
	}
	if !d.Update(1000.0, 1000.0) {
		t.Fatalf("outlier 1000 was not flagged")
	}
	if math.Abs(d.ZScore()) < d.Threshold() {
		t.Errorf("ZScore() = %v, want |z| >= threshold %v", d.ZScore(), d.Threshold())
	}
}

func TestZScoreBoundedNoiseNoFalsePositives(t *testing.T) {
	// Adjusted for EWMV multidimensional stability:
	// Smoother variance decay (alphaFast = 0.1) and threshold = 4.0 to absorb Euclidean norm magnification
	d := NewZScoreDetector(0.1, 0.05, 50, 4.0, 0.15)
	rng := rand.New(rand.NewPCG(3, 9))
	flagged := 0
	for range 2000 {
		x := 100 + (rng.Float64()-0.5)*0.2
		if d.Update(x, x) {
			flagged++
		}
	}
	if flagged != 0 {
		t.Errorf("bounded noise flagged %d times, want 0", flagged)
	}
}

func TestZScoreRandomStreamNoPanic(t *testing.T) {
	rng := rand.New(rand.NewPCG(1234, 56))
	d := NewZScoreDetector(0.5, 0.05, 30, 3.5, 0.15)
	for i := range 2000 {
		x := rng.NormFloat64()*5 + 100
		y := rng.NormFloat64()*5 + 100
		d.Update(x, y)
		z := d.ZScore()
		if math.IsNaN(z) && d.Count() > 1 {
			t.Fatalf("iteration %d: ZScore() = NaN after warm-up", i)
		}
		if math.IsInf(z, 0) {
			t.Fatalf("iteration %d: ZScore() = Inf", i)
		}
	}
}

func FuzzZScoreNoPanic(f *testing.F) {
	f.Add(1.0, 1.0)
	f.Add(-100.0, 50.0)
	f.Add(0.0, 0.0)
	f.Add(math.MaxFloat64, math.MaxFloat64)
	f.Add(1e-9, 1e-9)

	f.Fuzz(func(t *testing.T, cpu, mem float64) {
		if math.IsNaN(cpu) || math.IsInf(cpu, 0) || math.IsNaN(mem) || math.IsInf(mem, 0) {
			return
		}
		d := NewZScoreDetector(0.5, 0.05, 30, 3.5, 0.15)
		flagged := 0
		for range 200 {
			if d.Update(cpu, mem) {
				flagged++
			}
		}
		if flagged != 0 {
			t.Fatalf("constant stream of %v/%v flagged %d times", cpu, mem, flagged)
		}
		z := d.ZScore()
		if math.IsInf(z, 0) {
			t.Fatalf("ZScore() = Inf for constant stream %v/%v", cpu, mem)
		}
	})
}

func TestZScoreDetectorNonFiniteGuards(t *testing.T) {
	d := NewZScoreDetector(0.1, 0.01, 30, 3.5, 0.15)
	for range 50 {
		d.Update(100.0, 100.0)
	}

	if d.Update(math.NaN(), 100.0) {
		t.Fatalf("Update(NaN) returned true, want false")
	}
	if d.Update(math.Inf(1), 100.0) {
		t.Fatalf("Update(+Inf) returned true, want false")
	}
	if d.Update(math.Inf(-1), 100.0) {
		t.Fatalf("Update(-Inf) returned true, want false")
	}

	if math.IsNaN(d.BaselineCPU()) || math.IsInf(d.BaselineCPU(), 0) {
		t.Fatalf("detector baseline poisoned by non-finite input: %v", d.BaselineCPU())
	}

	if !d.Update(10000.0, 10000.0) {
		t.Fatalf("legitimate spike after non-finite rejection was not detected")
	}
}

func TestDualHorizonSlowDriftDetection(t *testing.T) {
	d := NewZScoreDetector(1.0, 0.0002, 30, 3.5, 0.80)

	for range 500 {
		d.Update(100.0, 100.0)
	}

	for i := 1; i <= 5000; i++ {
		val := 100.0 + float64(i)*0.05
		d.Update(val, val)

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

func TestDualHorizonInstantSpikeRejection(t *testing.T) {
	d := NewZScoreDetector(0.001, 0.0001, 30, 3.5, 0.15)

	for range 500 {
		d.Update(100.0, 100.0)
	}

	slowBefore := d.SlowBaselineCPU()

	flagged := d.Update(500.0, 500.0)
	if !flagged {
		t.Fatalf("Update(500.0) = false, want true")
	}

	slowAfter := d.SlowBaselineCPU()
	relChange := math.Abs(slowAfter-slowBefore) / slowBefore
	if relChange >= 0.001 {
		t.Fatalf("slow EWMA moved by %f%% (>= 0.1%%)", relChange*100)
	}

	if d.IsDrifting() {
		t.Fatalf("IsDrifting() = true immediately after single spike, want false")
	}
}

func TestDualHorizonRampInvariance(t *testing.T) {
	d := NewZScoreDetector(1.0, 0.001, 30, 3.5, 0.20)

	for range 100 {
		d.Update(50.0, 50.0)
	}

	for i := 1; i <= 1000; i++ {
		val := 50.0 + float64(i)*0.1
		flagged := d.Update(val, val)

		if flagged {
			t.Fatalf("sample %d: ramp unexpectedly flagged as anomaly", d.Count())
		}

		if gotZ := math.Abs(d.ZScore()); gotZ >= 3.5 {
			t.Fatalf("sample %d: |ZScore()| = %v, want < 3.5", d.Count(), gotZ)
		}
	}

	if !d.IsDrifting() {
		t.Fatalf("detector failed to signal concept drift by end of ramp")
	}
	if d.DriftIndex() < d.DriftThreshold() {
		t.Fatalf("DriftIndex() = %v, want >= DriftThreshold %v", d.DriftIndex(), d.DriftThreshold())
	}
}
