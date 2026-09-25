//go:build linux

package collector

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleProcStat = `cpu  100 5 200 300 40 10 20 30 5 2
cpu0 10 1 20 30 4 1 2 3 0 0
intr 12345 0 0 0 0 0 0 0
ctxt 99999
btime 1700000000
processes 42
procs_running 2
procs_blocked 0
`

func TestCollectCPU(t *testing.T) {
	dir := t.TempDir()
	statPath := filepath.Join(dir, "stat")
	if err := os.WriteFile(statPath, []byte(sampleProcStat), 0o644); err != nil {
		t.Fatalf("write sample stat: %v", err)
	}

	var out CPUStats
	if err := CollectCPU(dir, &out); err != nil {
		t.Fatalf("CollectCPU(%q) failed: %v", dir, err)
	}

	want := CPUStats{
		User:    100,
		Nice:    5,
		System:  200,
		Idle:    300,
		Iowait:  40,
		Irq:     10,
		Softirq: 20,
		Steal:   30,
		Guest:   5,
		GuestN:  2,
	}
	if out.User != want.User || out.Nice != want.Nice || out.System != want.System ||
		out.Idle != want.Idle || out.Iowait != want.Iowait || out.Irq != want.Irq ||
		out.Softirq != want.Softirq || out.Steal != want.Steal || out.Guest != want.Guest ||
		out.GuestN != want.GuestN {
		t.Errorf("CollectCPU() = %+v, want %+v", out, want)
	}

	wantTotal := uint64(100 + 5 + 200 + 300 + 40 + 10 + 20 + 30 + 5 + 2)
	if out.Total != wantTotal {
		t.Errorf("Total = %d, want %d", out.Total, wantTotal)
	}

	// Verify trailing slash handling does not corrupt stack buffer path
	var outTrailing CPUStats
	if err := CollectCPU(dir+"/", &outTrailing); err != nil {
		t.Fatalf("CollectCPU(%q) with trailing slash failed: %v", dir+"/", err)
	}
	if outTrailing.Total != wantTotal {
		t.Errorf("CollectCPU with trailing slash Total = %d, want %d", outTrailing.Total, wantTotal)
	}
}

func TestCollectCPUMalformed(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{"empty", ""},
		{"token only", "cpu"},
		{"no fields", "cpu\n"},
		{"missing counter", "cpu 1 2 3 4 5 6 7 8 9"},
		{"non-numeric", "cpu a b c d e f g h i j"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(tt.line), 0o644); err != nil {
				t.Fatalf("write malformed stat: %v", err)
			}
			var out CPUStats
			if err := CollectCPU(dir, &out); err == nil {
				t.Errorf("CollectCPU(%q) = nil error, want error", tt.line)
			}
		})
	}
}

func TestCollectCPUMissingFile(t *testing.T) {
	var out CPUStats
	if err := CollectCPU(filepath.Join(t.TempDir(), "nonexistent"), &out); err == nil {
		t.Fatalf("CollectCPU on missing directory = nil error, want error")
	}
}

func TestCollectCPUPathTooLong(t *testing.T) {
	longPath := "/" + strings.Repeat("a", 260)
	var out CPUStats
	err := CollectCPU(longPath, &out)
	if !errors.Is(err, errPathTooLong) {
		t.Fatalf("CollectCPU(longPath) err = %v, want %v", err, errPathTooLong)
	}
}

func FuzzParseCPULine(f *testing.F) {
	f.Add([]byte(sampleProcStat))
	f.Add([]byte("cpu  100 5 200 300 40 10 20 30 5 2\n"))
	f.Add([]byte("cpu 0 0 0 0 0 0 0 0 0 0"))
	f.Add([]byte("invalid garbage data 12345"))
	f.Add([]byte(""))

	f.Fuzz(func(_ *testing.T, data []byte) {
		var out CPUStats
		_ = parseCPULine(data, &out)
	})
}
