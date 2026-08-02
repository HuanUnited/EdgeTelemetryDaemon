package collector

import (
	"os"
	"path/filepath"
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

func TestParseCPULine(t *testing.T) {
	var out CPUStats
	if err := parseCPULine([]byte(sampleProcStat), &out); err != nil {
		t.Fatalf("parseCPULine() returned error: %v", err)
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
		t.Errorf("parseCPULine() = %+v, want %+v", out, want)
	}

	wantTotal := uint64(100 + 5 + 200 + 300 + 40 + 10 + 20 + 30 + 5 + 2)
	if out.Total != wantTotal {
		t.Errorf("Total = %d, want %d", out.Total, wantTotal)
	}
}

func TestParseCPULineMalformed(t *testing.T) {
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
			var out CPUStats
			if err := parseCPULine([]byte(tt.line), &out); err == nil {
				t.Errorf("parseCPULine(%q) = nil error, want error", tt.line)
			}
		})
	}
}

func TestScrapeProcStat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stat")
	if err := os.WriteFile(path, []byte(sampleProcStat), 0o644); err != nil {
		t.Fatalf("write sample stat: %v", err)
	}

	var out CPUStats
	if err := scrapeProcStat(path, &out); err != nil {
		t.Fatalf("scrapeProcStat() returned error: %v", err)
	}
	if out.User != 100 || out.System != 200 {
		t.Errorf("scrapeProcStat() = %+v, want User=100 System=200", out)
	}
}

func TestScrapeProcStatMissingFile(t *testing.T) {
	var out CPUStats
	if err := scrapeProcStat(filepath.Join(t.TempDir(), "nope"), &out); err == nil {
		t.Fatalf("scrapeProcStat() on missing file = nil error, want error")
	}
}

func FuzzParseCPULine(f *testing.F) {
	f.Add([]byte(sampleProcStat))
	f.Add([]byte("cpu  100 5 200 300 40 10 20 30 5 2\n"))
	f.Add([]byte("cpu 0 0 0 0 0 0 0 0 0 0"))
	f.Add([]byte("invalid garbage data 12345"))
	f.Add([]byte(""))

	f.Fuzz(func(t *testing.T, data []byte) {
		var out CPUStats
		_ = parseCPULine(data, &out)
	})
}
