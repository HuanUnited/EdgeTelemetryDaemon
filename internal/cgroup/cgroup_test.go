package cgroup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCgroupQuotaTransitions(t *testing.T) {
	dir := t.TempDir()
	ctrl := NewController(dir)

	if err := ctrl.SetBurst(); err != nil {
		t.Fatalf("SetBurst() failed: %v", err)
	}

	cpuMaxPath := filepath.Join(dir, "cpu.max")
	data, err := os.ReadFile(cpuMaxPath)
	if err != nil {
		t.Fatalf("read cpu.max: %v", err)
	}
	if string(data) != "max 100000\n" {
		t.Fatalf("cpu.max = %q, want %q", string(data), "max 100000\n")
	}
	if got := ctrl.Mode(); got != ModeBurst {
		t.Fatalf("Mode() = %v, want %v", got, ModeBurst)
	}

	if errSetBurst := ctrl.SetBurst(); errSetBurst != nil {
		t.Fatalf("second SetBurst() failed: %v", errSetBurst)
	}

	if errSetQuiescent := ctrl.SetQuiescent(); errSetQuiescent != nil {
		t.Fatalf("SetQuiescent() failed: %v", errSetQuiescent)
	}

	data, err = os.ReadFile(cpuMaxPath)
	if err != nil {
		t.Fatalf("read cpu.max: %v", err)
	}
	if string(data) != "5000 100000\n" {
		t.Fatalf("cpu.max = %q, want %q", string(data), "5000 100000\n")
	}
	if got := ctrl.Mode(); got != ModeQuiescent {
		t.Fatalf("Mode() = %v, want %v", got, ModeQuiescent)
	}

	infoBefore, err := os.Stat(cpuMaxPath)
	if err != nil {
		t.Fatalf("stat cpu.max: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	if errSetQuiesenct := ctrl.SetQuiescent(); errSetQuiesenct != nil {
		t.Fatalf("second SetQuiescent() failed: %v", errSetQuiesenct)
	}

	infoAfter, err := os.Stat(cpuMaxPath)
	if err != nil {
		t.Fatalf("stat cpu.max after no-op: %v", err)
	}

	if !infoBefore.ModTime().Equal(infoAfter.ModTime()) {
		t.Fatalf("mtime changed on idempotent SetQuiescent: before %v, after %v",
			infoBefore.ModTime(), infoAfter.ModTime())
	}

	dataAfter, err := os.ReadFile(cpuMaxPath)
	if err != nil {
		t.Fatalf("read cpu.max after no-op: %v", err)
	}
	if string(dataAfter) != "5000 100000\n" {
		t.Fatalf("content changed on idempotent SetQuiescent: got %q, want %q",
			string(dataAfter), "5000 100000\n")
	}
}

func TestCgroupReadCPUStat(t *testing.T) {
	dir := t.TempDir()
	statPath := filepath.Join(dir, "cpu.stat")
	fixture := "usage_usec 150000\nnr_periods 1000\nnr_throttled 50\nthrottled_usec 25000\n"
	if err := os.WriteFile(statPath, []byte(fixture), 0o644); err != nil {
		t.Fatalf("write cpu.stat fixture: %v", err)
	}

	ctrl := NewController(dir)

	got, err := ctrl.ReadCPUStat()
	if err != nil {
		t.Fatalf("ReadCPUStat() failed: %v", err)
	}

	want := CPUStat{
		UsageUsec:     150000,
		NrPeriods:     1000,
		NrThrottled:   50,
		ThrottledUsec: 25000,
	}
	if got != want {
		t.Fatalf("ReadCPUStat() = %+v, want %+v", got, want)
	}

	allocs := testing.AllocsPerRun(1000, func() {
		_, _ = ctrl.ReadCPUStat()
	})
	if allocs != 0 {
		t.Fatalf("testing.AllocsPerRun = %v, want 0", allocs)
	}
}

func TestCgroupReadCPUStatWithExtraKeys(t *testing.T) {
	dir := t.TempDir()
	statPath := filepath.Join(dir, "cpu.stat")
	fixture := "usage_usec 300000\nuser_usec 200000\nsystem_usec 100000\nnr_periods 2000\nunknown_key 9999\nnr_throttled 75\nthrottled_usec 50000\n"
	if err := os.WriteFile(statPath, []byte(fixture), 0o644); err != nil {
		t.Fatalf("write cpu.stat fixture: %v", err)
	}

	ctrl := NewController(dir)
	got, err := ctrl.ReadCPUStat()
	if err != nil {
		t.Fatalf("ReadCPUStat() failed: %v", err)
	}

	want := CPUStat{
		UsageUsec:     300000,
		NrPeriods:     2000,
		NrThrottled:   75,
		ThrottledUsec: 50000,
	}
	if got != want {
		t.Fatalf("ReadCPUStat() with extra keys = %+v, want %+v", got, want)
	}
}

func TestCgroupMissingFiles(t *testing.T) {
	dir := t.TempDir()
	ctrl := NewController(filepath.Join(dir, "nonexistent"))

	if err := ctrl.SetBurst(); err == nil {
		t.Fatalf("SetBurst() on nonexistent dir = nil, want error")
	}

	if _, err := ctrl.ReadCPUStat(); err == nil {
		t.Fatalf("ReadCPUStat() on nonexistent file = nil, want error")
	}
}

func TestModeString(t *testing.T) {
	if ModeQuiescent.String() != "quiescent" {
		t.Fatalf("ModeQuiescent.String() = %q, want %q", ModeQuiescent.String(), "quiescent")
	}
	if ModeBurst.String() != "burst" {
		t.Fatalf("ModeBurst.String() = %q, want %q", ModeBurst.String(), "burst")
	}
	if Mode(99).String() != "unknown" {
		t.Fatalf("Mode(99).String() = %q, want %q", Mode(99).String(), "unknown")
	}
}
