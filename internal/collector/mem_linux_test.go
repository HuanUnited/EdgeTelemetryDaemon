package collector

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleMemInfo = `MemTotal:       16384000 kB
MemFree:         4194304 kB
MemAvailable:   10485760 kB
Buffers:          262144 kB
Cached:          4194304 kB
SwapCached:            0 kB
Active:          5242880 kB
Inactive:        2097152 kB
SwapTotal:       2097152 kB
SwapFree:        1048576 kB
Dirty:               128 kB
`

func TestScrapeProcMemInfo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meminfo")
	if err := os.WriteFile(path, []byte(sampleMemInfo), 0o644); err != nil {
		t.Fatalf("write sample meminfo: %v", err)
	}

	var out MemStats
	if err := scrapeProcMemInfo(path, &out); err != nil {
		t.Fatalf("scrapeProcMemInfo() returned error: %v", err)
	}

	want := MemStats{
		MemTotal:     16384000,
		MemFree:      4194304,
		MemAvailable: 10485760,
		Buffers:      262144,
		Cached:       4194304,
		SwapTotal:    2097152,
		SwapFree:     1048576,
	}
	if out != want {
		t.Errorf("scrapeProcMemInfo() = %+v, want %+v", out, want)
	}
}

func TestScrapeProcMemInfoMissingFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meminfo")
	if err := os.WriteFile(path, []byte("Active: 123 kB\nDirty: 456 kB\n"), 0o644); err != nil {
		t.Fatalf("write meminfo: %v", err)
	}

	var out MemStats
	if err := scrapeProcMemInfo(path, &out); err == nil {
		t.Fatalf("scrapeProcMemInfo() on file with no recognised fields = nil error, want error")
	}
}

func TestScrapeProcMemInfoMissingFile(t *testing.T) {
	var out MemStats
	if err := scrapeProcMemInfo(filepath.Join(t.TempDir(), "nope"), &out); err == nil {
		t.Fatalf("scrapeProcMemInfo() on missing file = nil error, want error")
	}
}

func FuzzParseMemInfo(f *testing.F) {
	f.Add([]byte(sampleMemInfo))
	f.Add([]byte("MemTotal: 16384000 kB\nMemFree: 4194304 kB\n"))
	f.Add([]byte("invalid: key value\n"))
	f.Add([]byte(""))

	f.Fuzz(func(t *testing.T, data []byte) {
		var out MemStats
		_ = parseMemInfo(data, &out)
	})
}
