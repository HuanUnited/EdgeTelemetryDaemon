//go:build linux

package collector

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestCollectMem(t *testing.T) {
	dir := t.TempDir()
	memPath := filepath.Join(dir, "meminfo")
	if err := os.WriteFile(memPath, []byte(sampleMemInfo), 0o644); err != nil {
		t.Fatalf("write sample meminfo: %v", err)
	}

	var out MemStats
	if err := CollectMem(dir, &out); err != nil {
		t.Fatalf("CollectMem(%q) failed: %v", dir, err)
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
		t.Errorf("CollectMem() = %+v, want %+v", out, want)
	}

	// Verify trailing slash handling does not corrupt stack buffer path
	var outTrailing MemStats
	if err := CollectMem(dir+"/", &outTrailing); err != nil {
		t.Fatalf("CollectMem(%q) with trailing slash failed: %v", dir+"/", err)
	}
	if outTrailing != want {
		t.Errorf("CollectMem with trailing slash = %+v, want %+v", outTrailing, want)
	}
}

func TestCollectMemMissingFields(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "meminfo"), []byte("Active: 123 kB\nDirty: 456 kB\n"), 0o644); err != nil {
		t.Fatalf("write meminfo: %v", err)
	}

	var out MemStats
	if err := CollectMem(dir, &out); err == nil {
		t.Fatalf("CollectMem on file with no recognised fields = nil error, want error")
	}
}

func TestCollectMemMissingFile(t *testing.T) {
	var out MemStats
	if err := CollectMem(filepath.Join(t.TempDir(), "nonexistent"), &out); err == nil {
		t.Fatalf("CollectMem on missing directory = nil error, want error")
	}
}

func TestCollectMemPathTooLong(t *testing.T) {
	longPath := "/" + strings.Repeat("m", 260)
	var out MemStats
	err := CollectMem(longPath, &out)
	if !errors.Is(err, errPathTooLong) {
		t.Fatalf("CollectMem(longPath) err = %v, want %v", err, errPathTooLong)
	}
}

func FuzzParseMemInfo(f *testing.F) {
	f.Add([]byte(sampleMemInfo))
	f.Add([]byte("MemTotal: 16384000 kB\nMemFree: 4194304 kB\n"))
	f.Add([]byte("invalid: key value\n"))
	f.Add([]byte(""))

	f.Fuzz(func(_ *testing.T, data []byte) {
		var out MemStats
		_ = parseMemInfo(data, &out)
	})
}
