package engine

import (
	"math"
	"math/rand/v2"
	"testing"
)

// TestEWMASeedsFromFirstObservation confirms the first Update becomes the
// baseline verbatim.
func TestEWMASeedsFromFirstObservation(t *testing.T) {
	e := NewEWMA(0.5)
	if !math.IsNaN(e.Value()) {
		t.Fatalf("Value() before any update = %v, want NaN", e.Value())
	}
	e.Update(10)
	if e.Value() != 10 {
		t.Errorf("Value() after first update = %v, want 10 (seed)", e.Value())
	}
}

// TestEWMAConvergesToConstant checks that a constant stream leaves the moving
// average constant (alpha > 0).
func TestEWMAConvergesToConstant(t *testing.T) {
	e := NewEWMA(0.3)
	for i := 0; i < 100; i++ {
		e.Update(7)
	}
	if math.Abs(e.Value()-7) > 1e-12 {
		t.Errorf("Value() = %v, want 7", e.Value())
	}
}

// TestEWMAHandComputed verifies the update equation by hand for alpha=0.25.
func TestEWMAHandComputed(t *testing.T) {
	e := NewEWMA(0.25)
	e.Update(10) // seed
	e.Update(20) // 0.25*20 + 0.75*10 = 5 + 7.5 = 12.5
	if math.Abs(e.Value()-12.5) > 1e-12 {
		t.Fatalf("Value() = %v, want 12.5", e.Value())
	}
	e.Update(0) // 0.25*0 + 0.75*12.5 = 9.375
	if math.Abs(e.Value()-9.375) > 1e-12 {
		t.Errorf("Value() = %v, want 9.375", e.Value())
	}
}

// TestEWMALeapTowardsRecentValue confirms a larger alpha tracks a step change
// faster than a smaller alpha (drift responsiveness).
func TestEWMALeapTowardsRecentValue(t *testing.T) {
	fast := NewEWMA(0.9)
	slow := NewEWMA(0.1)
	for i := 0; i < 10; i++ {
		fast.Update(1)
		slow.Update(1)
	}
	for i := 0; i < 10; i++ {
		fast.Update(100)
		slow.Update(100)
	}
	if !(fast.Value() > slow.Value()) {
		t.Errorf("fast alpha tracked the step better than slow: fast=%v slow=%v", fast.Value(), slow.Value())
	}
}

// TestEWMAAfterStepGap performs a closed-form check of the recursive update
// after a few steps.
func TestEWMAAfterStepGap(t *testing.T) {
	e := NewEWMA(0.5)
	for _, x := range []float64{100, 200, 300} {
		e.Update(x)
	}
	// seed=100; then 0.5*200+0.5*100=150; then 0.5*300+0.5*150=225.
	if math.Abs(e.Value()-225) > 1e-12 {
		t.Errorf("Value() = %v, want 225", e.Value())
	}
}

// TestEWMAAlphaClamping covers the clamping behaviour of NewEWMA.
func TestEWMAAlphaClamping(t *testing.T) {
	if got := NewEWMA(0).Alpha(); got != 0.5 {
		t.Errorf("NewEWMA(0).Alpha() = %v, want 0.5", got)
	}
	if got := NewEWMA(-3).Alpha(); got != 0.5 {
		t.Errorf("NewEWMA(-3).Alpha() = %v, want 0.5", got)
	}
	if got := NewEWMA(1.5).Alpha(); got != 1 {
		t.Errorf("NewEWMA(1.5).Alpha() = %v, want 1", got)
	}
	if got := NewEWMA(0.4).Alpha(); got != 0.4 {
		t.Errorf("NewEWMA(0.4).Alpha() = %v, want 0.4", got)
	}
}

// TestEWMAAlphaOne keeps only the latest observation.
func TestEWMAAlphaOne(t *testing.T) {
	e := NewEWMA(1)
	e.Update(1)
	e.Update(2)
	e.Update(3)
	if e.Value() != 3 {
		t.Errorf("Value() with alpha=1 = %v, want 3", e.Value())
	}
}

// TestEWMAReset confirms Reset clears the baseline and the instance can be
// reused.
func TestEWMAReset(t *testing.T) {
	e := NewEWMA(0.5)
	e.Update(5)
	e.Reset()
	if !math.IsNaN(e.Value()) {
		t.Errorf("Value() after Reset = %v, want NaN", e.Value())
	}
	e.Update(9)
	if e.Value() != 9 {
		t.Errorf("Value() after Reset+reuse = %v, want 9", e.Value())
	}
}

// TestEWMAStabilityAgainstReference recomputes the same stream with a
// brute-force reference and checks the values agree after every sample.
func TestEWMAStabilityAgainstReference(t *testing.T) {
	rng := rand.New(rand.NewPCG(99, 1))
	e := NewEWMA(0.3)
	var ref float64
	seen := false
	for i := 0; i < 2000; i++ {
		x := rng.NormFloat64()
		e.Update(x)
		if !seen {
			ref = x
			seen = true
		} else {
			ref = 0.3*x + 0.7*ref
		}
		if math.Abs(e.Value()-ref) > 1e-12 {
			t.Fatalf("iteration %d: Value() = %v, reference = %v", i, e.Value(), ref)
		}
	}
}

// FuzzEWMAConverges asserts that a constant stream converges to that constant
// regardless of the smoothing factor and the exact values fed.
func FuzzEWMAConverges(f *testing.F) {
	f.Add(float64(0.5), float64(42))
	f.Add(float64(0.1), float64(-7))
	f.Add(float64(1.0), float64(1e6))
	f.Add(float64(0.75), 0.0)
	f.Add(-1.0, 3.14)

	f.Fuzz(func(t *testing.T, alpha, x float64) {
		if math.IsNaN(alpha) || math.IsInf(alpha, 0) ||
			math.IsNaN(x) || math.IsInf(x, 0) {
			return
		}
		e := NewEWMA(alpha)
		// The first observation seeds the baseline; it must be excluded from
		// the convergence check because it is adopted verbatim.
		e.Update(x)
		for i := 0; i < 1000; i++ {
			e.Update(x)
		}
		if math.Abs(e.Value()-x) > 1e-9*math.Max(1, math.Abs(x)) {
			t.Fatalf("alpha=%v x=%v: Value() = %v, want %v", alpha, x, e.Value(), x)
		}
	})
}
