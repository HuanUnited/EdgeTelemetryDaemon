package collector

import (
	"os"
	"path/filepath"
	"testing"
)

// benchFixture writes sampleProcStat / sampleMemInfo into a temp file and
// returns its path, so benchmarks measure the scrape path (open, read, parse)
// and not file construction.
func benchFixture(tb testing.TB, name, content string) string {
	tb.Helper()
	path := filepath.Join(tb.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		tb.Fatalf("write %s fixture: %v", name, err)
	}
	return path
}

func BenchmarkCollectCPU(b *testing.B) {
	path := benchFixture(b, "stat", sampleProcStat)
	var out CPUStats
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := scrapeProcStat(path, &out); err != nil {
			b.Fatalf("scrapeProcStat: %v", err)
		}
	}
	_ = out
}

func BenchmarkCollectMem(b *testing.B) {
	path := benchFixture(b, "meminfo", sampleMemInfo)
	var out MemStats
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := scrapeProcMemInfo(path, &out); err != nil {
			b.Fatalf("scrapeProcMemInfo: %v", err)
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
