package engine

import (
	"math/rand/v2"
	"testing"
)

func warmStream(d *ZScoreDetector) {
	for range 100 {
		d.Update(10.0, 10.0)
	}
}

func BenchmarkEWMVUpdateNoEscape(b *testing.B) {
	s := NewStreamingStats(0.1)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s.Update(float64(i))
	}
	if s.Count() == 0 {
		b.Fatal("stats not updated")
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
	d := NewZScoreDetector(0.5, 0.05, 30, 3.5, 0.15)
	warmStream(d)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		x := 10 + float64(i&1)
		d.Update(x, x)
	}
	if d.Count() == 0 {
		b.Fatal("detector not updated")
	}
}

func BenchmarkZScoreDetect(b *testing.B) {
	rng := rand.New(rand.NewPCG(7, 11))
	d := NewZScoreDetector(0.5, 0.05, 30, 3.5, 0.15)
	warmStream(d)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		x := 10 + rng.NormFloat64()
		if d.Update(x, x) {
			_ = d.ZScore()
		}
	}
}

func BenchmarkZScoreExtendedUpdate(b *testing.B) {
	d := NewZScoreDetector(0.1, 0.01, 30, 3.5, 0.15)
	warmStream(d)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		x := 10.0 + float64(i&1)*0.1
		_ = d.Update(x, x)
	}
	if d.Count() == 0 {
		b.Fatal("detector not updated")
	}
}
