package engine

import (
	"math"
	"math/rand/v2"
	"testing"
)

// TestWelfordBasicStream exercises a hand-computed stream and checks that the
// running mean and variance converge on the exact two-pass result.
func TestWelfordBasicStream(t *testing.T) {
	var w Welford

	// A simple constant stream keeps the hand-check deterministic.
	xs := []float64{2, 4, 4, 4, 5, 5, 7, 9}
	var sum, sumsq float64
	for _, x := range xs {
		w.Update(x)
		sum += x
		sumsq += x * x
	}

	if w.Count() != uint64(len(xs)) {
		t.Errorf("Count() = %d, want %d", w.Count(), len(xs))
	}
	wantMean := sum / float64(len(xs))
	if math.Abs(w.Mean()-wantMean) > 1e-12 {
		t.Errorf("Mean() = %v, want %v", w.Mean(), wantMean)
	}
	// Sample variance: (sumsq - sum^2/n) / (n-1).
	wantVar := (sumsq - sum*sum/float64(len(xs))) / float64(len(xs)-1)
	if math.Abs(w.Variance()-wantVar) > 1e-12 {
		t.Errorf("Variance() = %v, want %v", w.Variance(), wantVar)
	}
	wantPopVar := (sumsq - sum*sum/float64(len(xs))) / float64(len(xs))
	if math.Abs(w.PopulationVariance()-wantPopVar) > 1e-12 {
		t.Errorf("PopulationVariance() = %v, want %v", w.PopulationVariance(), wantPopVar)
	}
	if math.Abs(w.StdDev()-math.Sqrt(wantVar)) > 1e-12 {
		t.Errorf("StdDev() = %v, want %v", w.StdDev(), math.Sqrt(wantVar))
	}
}

// TestWelfordEquivalence streams a larger, noisier dataset and asserts that
// the online algorithm matches the offline two-pass computation to within
// floating-point tolerance.
func TestWelfordEquivalence(t *testing.T) {
	rng := rand.New(rand.NewPCG(42, 7))
	var w Welford

	const n = 10_000
	xs := make([]float64, 0, n)
	var sum float64
	for i := 0; i < n; i++ {
		x := rng.NormFloat64()*3 + 10
		xs = append(xs, x)
		sum += x
		w.Update(x)
	}

	wantMean := sum / n
	if math.Abs(w.Mean()-wantMean) > 1e-9 {
		t.Errorf("Mean() = %v, want %v", w.Mean(), wantMean)
	}

	var varSum float64
	for _, x := range xs {
		d := x - wantMean
		varSum += d * d
	}
	wantVar := varSum / float64(n-1)
	if math.Abs(w.Variance()-wantVar) > 1e-8 {
		t.Errorf("Variance() = %v, want %v", w.Variance(), wantVar)
	}
}

// TestWelfordMerge verifies that merging two partial aggregations reproduces
// the statistics of the concatenated stream.
func TestWelfordMerge(t *testing.T) {
	a := []float64{1, 2, 3}
	b := []float64{4, 5, 6, 7}

	var wa, wb, wAll Welford
	for _, x := range a {
		wa.Update(x)
		wAll.Update(x)
	}
	for _, x := range b {
		wb.Update(x)
		wAll.Update(x)
	}

	wa.Merge(&wb)
	if wa.Count() != wAll.Count() {
		t.Errorf("merged Count() = %d, want %d", wa.Count(), wAll.Count())
	}
	if math.Abs(wa.Mean()-wAll.Mean()) > 1e-12 {
		t.Errorf("merged Mean() = %v, want %v", wa.Mean(), wAll.Mean())
	}
	if math.Abs(wa.Variance()-wAll.Variance()) > 1e-12 {
		t.Errorf("merged Variance() = %v, want %v", wa.Variance(), wAll.Variance())
	}
}

// TestWelfordMergeEmpty confirms merging an empty state is a no-op, and that
// merging into an empty state copies the source.
func TestWelfordMergeEmpty(t *testing.T) {
	var w, e Welford
	w.Update(1)
	w.Update(2)
	e.Merge(&w)
	if e.Count() != w.Count() || math.Abs(e.Mean()-w.Mean()) > 1e-12 {
		t.Errorf("merge into empty = %+v, want %+v", e, w)
	}

	// Snapshot before the merge so we can assert a no-op on the mutated value.
	want := w
	var empty Welford // a fresh empty accumulator
	w.Merge(&empty)
	if w.Count() != want.Count() || math.Abs(w.Mean()-want.Mean()) > 1e-12 {
		t.Errorf("merge with empty changed state: got %+v, want %+v", w, want)
	}
}

// TestWelfordVarianceEdgeCases covers the boundary behaviour of the variance
// accessors.
func TestWelfordVarianceEdgeCases(t *testing.T) {
	var w Welford
	if !math.IsNaN(w.Mean()) {
		t.Errorf("Mean() on empty = %v, want NaN", w.Mean())
	}
	if !math.IsNaN(w.Variance()) {
		t.Errorf("Variance() on empty = %v, want NaN", w.Variance())
	}
	if !math.IsNaN(w.PopulationVariance()) {
		t.Errorf("PopulationVariance() on empty = %v, want NaN", w.PopulationVariance())
	}

	w.Update(5)
	if w.Mean() != 5 {
		t.Errorf("Mean() after one = %v, want 5", w.Mean())
	}
	if !math.IsNaN(w.Variance()) {
		t.Errorf("Variance() with one sample = %v, want NaN", w.Variance())
	}
	if w.PopulationVariance() != 0 {
		t.Errorf("PopulationVariance() with one sample = %v, want 0", w.PopulationVariance())
	}
}

// TestWelfordReset confirms Reset returns the instance to a usable zero state.
func TestWelfordReset(t *testing.T) {
	var w Welford
	for i := 0; i < 100; i++ {
		w.Update(float64(i))
	}
	w.Reset()
	if w.Count() != 0 {
		t.Errorf("Count() after Reset = %d, want 0", w.Count())
	}
	if !math.IsNaN(w.Mean()) {
		t.Errorf("Mean() after Reset = %v, want NaN", w.Mean())
	}
	// Feeding again must work from a clean slate.
	w.Update(10)
	w.Update(20)
	if w.Mean() != 15 {
		t.Errorf("Mean() after Reset+reuse = %v, want 15", w.Mean())
	}
}

// TestWelfordSingleValueLarge confirms Welford remains stable with a single
// very large observation (exercises the mean-tracking branch).
func TestWelfordSingleValueLarge(t *testing.T) {
	var w Welford
	w.Update(1e15)
	w.Update(1e15 + 1)
	if math.Abs(w.Mean()-1e15-0.5) > 1e-6 {
		t.Errorf("Mean() = %v, want %v", w.Mean(), 1e15+0.5)
	}
	// Variance of two values distance 1 apart is 0.5 (sample variance).
	if math.Abs(w.Variance()-0.5) > 1e-6 {
		t.Errorf("Variance() = %v, want 0.5", w.Variance())
	}
}

// FuzzWelfordEquivalence asserts that the online mean/variance never diverges
// from the offline computation by more than a small tolerance for arbitrary
// finite inputs.
func FuzzWelfordEquivalence(f *testing.F) {
	f.Add(float64(1.0), float64(2.0), float64(3.0))
	f.Add(0.0, -1e-9, 1e-9)
	f.Add(1e300, -1e300, 1e-300)
	f.Add(math.MaxFloat64, 1.0, 2.0)
	f.Add(-1e9, 1e9, 0.0)

	f.Fuzz(func(t *testing.T, x1, x2, x3 float64) {
		xs := []float64{x1, x2, x3}
		var w Welford
		var sum, sumsq float64
		for _, x := range xs {
			if math.IsNaN(x) || math.IsInf(x, 0) {
				// Guard: Welford tracks any finite value; NaN/Inf inputs are
				// well-defined (they poison the state) but comparing against an
				// offline computation of the same stream is still exact, so
				// these are acceptable. We simply skip the NaN case to keep the
				// property meaningful for finite data.
				w.Update(x)
				continue
			}
			w.Update(x)
			sum += x
			sumsq += x * x
		}

		if w.Count() != 3 {
			t.Fatalf("Count() = %d, want 3", w.Count())
		}
		if math.IsNaN(x1) || math.IsInf(x1, 0) ||
			math.IsNaN(x2) || math.IsInf(x2, 0) ||
			math.IsNaN(x3) || math.IsInf(x3, 0) {
			return
		}
		wantMean := sum / 3
		// sum itself loses precision when large values cancel; compare the mean
		// against a scale-aware bound derived from the data magnitude.
		meanScale := math.Max(1, math.Max(math.Abs(x1), math.Max(math.Abs(x2), math.Abs(x3))))
		if math.Abs(w.Mean()-wantMean) > 1e-7*meanScale {
			t.Fatalf("Mean() = %v, want %v (x=%v, scale=%v)", w.Mean(), wantMean, xs, meanScale)
		}
		wantVar := (sumsq - sum*sum/3) / 2
		if wantVar < 0 {
			return
		}
		// The two-pass reference (sumsq - sum^2/n) suffers catastrophic
		// cancellation when the data is large but the variance is small. Welford
		// computes the same quantity online and is typically MORE accurate, so
		// the oracle must tolerate a relative error against the data scale
		// rather than the variance itself.
		dataScale := math.Max(1, math.Max(math.Abs(x1), math.Max(math.Abs(x2), math.Abs(x3))))
		tol := 1e-7 * dataScale * dataScale
		if math.Abs(w.Variance()-wantVar) > tol {
			t.Fatalf("Variance() = %v, want %v (x=%v, dataScale=%v)", w.Variance(), wantVar, xs, dataScale)
		}
	})
}
