package cgroup

import (
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkReadCPUStat(b *testing.B) {
	dir := b.TempDir()
	statPath := filepath.Join(dir, "cpu.stat")
	fixture := "usage_usec 150000\nnr_periods 1000\nnr_throttled 50\nthrottled_usec 25000\n"
	if err := os.WriteFile(statPath, []byte(fixture), 0o644); err != nil {
		b.Fatalf("write cpu.stat fixture: %v", err)
	}

	ctrl := NewController(dir)
	var stat CPUStat

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s, err := ctrl.ReadCPUStat()
		if err != nil {
			b.Fatalf("ReadCPUStat failed: %v", err)
		}
		stat = s
	}
	_ = stat
}

func TestReadCPUStatAlloc(t *testing.T) {
	dir := t.TempDir()
	statPath := filepath.Join(dir, "cpu.stat")
	fixture := "usage_usec 150000\nnr_periods 1000\nnr_throttled 50\nthrottled_usec 25000\n"
	if err := os.WriteFile(statPath, []byte(fixture), 0o644); err != nil {
		t.Fatalf("write cpu.stat fixture: %v", err)
	}

	ctrl := NewController(dir)
	if _, err := ctrl.ReadCPUStat(); err != nil {
		t.Fatalf("initial ReadCPUStat failed: %v", err)
	}

	const runs = 1000
	allocs := testing.AllocsPerRun(runs, func() {
		_, _ = ctrl.ReadCPUStat()
	})
	if allocs != 0 {
		t.Fatalf("ReadCPUStat allocated %v times, want 0", allocs)
	}
}
