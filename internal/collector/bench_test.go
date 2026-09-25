package collector

import (
	"os"
	"path/filepath"
	"testing"
)

// createProcFixture initializes a mock procfs directory containing stat and meminfo files.
func createProcFixture(tb testing.TB) string {
	tb.Helper()
	dir := tb.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(sampleProcStat), 0o644); err != nil {
		tb.Fatalf("write stat fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meminfo"), []byte(sampleMemInfo), 0o644); err != nil {
		tb.Fatalf("write meminfo fixture: %v", err)
	}
	return dir
}

func BenchmarkCollectCPU(b *testing.B) {
	dir := b.TempDir()
	statPath := filepath.Join(dir, "stat")
	if err := os.WriteFile(statPath, []byte(sampleProcStat), 0o644); err != nil {
		b.Fatalf("write stat fixture: %v", err)
	}

	var out CPUStats
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := CollectCPU(dir, &out); err != nil {
			b.Fatalf("CollectCPU: %v", err)
		}
	}
	_ = out
}

func BenchmarkCollectMem(b *testing.B) {
	dir := b.TempDir()
	memPath := filepath.Join(dir, "meminfo")
	if err := os.WriteFile(memPath, []byte(sampleMemInfo), 0o644); err != nil {
		b.Fatalf("write meminfo fixture: %v", err)
	}

	var out MemStats
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := CollectMem(dir, &out); err != nil {
			b.Fatalf("CollectMem: %v", err)
		}
	}
	_ = out
}

func BenchmarkAIGenNext(b *testing.B) {
	g := NewAIGen(DefaultAIGenConfig())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s := g.Next()
		_ = s
	}
}

func BenchmarkParseCPULine(b *testing.B) {
	data := []byte(sampleProcStat)
	var out CPUStats
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := parseCPULine(data, &out); err != nil {
			b.Fatalf("parseCPULine: %v", err)
		}
	}
	_ = out
}

func TestCollectCPUAlloc(t *testing.T) {
	dir := createProcFixture(t)
	var out CPUStats

	// Warm-up run
	if err := CollectCPU(dir, &out); err != nil {
		t.Fatalf("warm-up CollectCPU failed: %v", err)
	}

	const runs = 1000
	allocs := testing.AllocsPerRun(runs, func() {
		_ = CollectCPU(dir, &out)
	})
	if allocs != 0 {
		t.Fatalf("CollectCPU allocated %v times, want 0", allocs)
	}
}

func TestCollectMemAlloc(t *testing.T) {
	dir := createProcFixture(t)
	var out MemStats

	// Warm-up run
	if err := CollectMem(dir, &out); err != nil {
		t.Fatalf("warm-up CollectMem failed: %v", err)
	}

	const runs = 1000
	allocs := testing.AllocsPerRun(runs, func() {
		_ = CollectMem(dir, &out)
	})
	if allocs != 0 {
		t.Fatalf("CollectMem allocated %v times, want 0", allocs)
	}
}
