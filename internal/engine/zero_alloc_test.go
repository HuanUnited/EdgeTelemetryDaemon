package engine

import "testing"

func BenchmarkZScoreDetectorUpdateDualHorizon(b *testing.B) {
	d := NewZScoreDetector(0.1, 0.01, 30, 3.5, 0.15)
	for range 100 {
		d.Update(10.0, 10.0)
	}

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

func TestZScoreDetectorUpdateDualHorizonAlloc(t *testing.T) {
	d := NewZScoreDetector(0.1, 0.01, 30, 3.5, 0.15)
	for range 100 {
		d.Update(10.0, 10.0)
	}

	const runs = 1000
	allocs := testing.AllocsPerRun(runs, func() {
		_ = d.Update(10.5, 10.5)
	})
	if allocs != 0 {
		t.Fatalf("ZScoreDetector.Update allocated %v times, want 0", allocs)
	}
}

func BenchmarkIsSaturated(b *testing.B) {
	var sat bool
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sat = IsSaturated(8_000_000, 350_000, 5.0)
	}
	_ = sat
}

func TestIsSaturatedAlloc(t *testing.T) {
	const runs = 1000
	allocs := testing.AllocsPerRun(runs, func() {
		_ = IsSaturated(8_000_000, 350_000, 5.0)
	})
	if allocs != 0 {
		t.Fatalf("IsSaturated allocated %v times, want 0", allocs)
	}
}

func TestStreamingStatsAlloc(t *testing.T) {
	stats := NewStreamingStats(0.1)
	const runs = 1000
	allocs := testing.AllocsPerRun(runs, func() {
		stats.Update(10.5)
	})
	if allocs != 0 {
		t.Fatalf("StreamingStats.Update allocated %v times, want 0", allocs)
	}
}
