package engine

import (
	"math/rand/v2"
	"testing"
)

// warmStream pre-populates the detector with stable values so benchmarks
// exercise the post-warm-up Z-score path rather than the warm-up heuristic.
func warmStream(d *ZScoreDetector) {
	for i := 0; i < 100; i++ {
		d.Update(10)
	}
}

func BenchmarkWelfordUpdate(b *testing.B) {
	var w Welford
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		w.Update(float64(i))
	}
	_ = w
}

func BenchmarkWelfordUpdateNoEscape(b *testing.B) {
	var w Welford
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		w.Update(float64(i))
	}
	if w.Count() == 0 {
		b.Fatal("welford not updated")
	}
}

func BenchmarkEWMAUpdate(b *testing.B) {
	e := NewEWMA(0.5)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		e.Update(float64(i))
	}
	_ = e.Value()
}

func BenchmarkZScore(b *testing.B) {
	d := NewZScoreDetector(0.5, 30, 3.5)
	warmStream(d)

	// Alternate between normal and anomalous values so the Z-score path is
	// exercised without the compiler being able to prove constancy.
	x := 10.0
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		x = 10 + float64(i&1)
		d.Update(x)
	}
	if d.Count() == 0 {
		b.Fatal("detector not updated")
	}
}

func BenchmarkZScoreDetect(b *testing.B) {
	rng := rand.New(rand.NewPCG(7, 11))
	d := NewZScoreDetector(0.5, 30, 3.5)
	warmStream(d)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		x := 10 + rng.NormFloat64()
		if d.Update(x) {
			// Consume the flag; keep the branch so the result is observable.
			_ = d.ZScore()
		}
	}
}
