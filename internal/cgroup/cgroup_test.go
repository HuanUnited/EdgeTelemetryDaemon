//go:build linux

package cgroup

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestCgroupQuotaTransitions(t *testing.T) {
	dir := t.TempDir()
	cpuMaxPath := filepath.Join(dir, "cpu.max")
	_ = os.WriteFile(cpuMaxPath, []byte("max 100000\n"), 0o644) // Create writeable file

	ctrl := NewController(dir)

	if err := ctrl.SetBurst(); err != nil {
		t.Fatalf("SetBurst() failed: %v", err)
	}

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

	wantQuota := fmt.Sprintf("%d 100000\n", runtime.NumCPU()*5000)
	if string(data) != wantQuota {
		t.Fatalf("cpu.max = %q, want %q", string(data), wantQuota)
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
		t.Fatalf("mtime changed on idempotent SetQuiescent")
	}
}

func TestAutoDiscovery(t *testing.T) {
	origProc := procSelfCgroup
	origSys := sysCgroupRoot
	defer func() {
		procSelfCgroup = origProc
		sysCgroupRoot = origSys
	}()

	tmp := t.TempDir()
	procSelfCgroup = filepath.Join(tmp, "cgroup")
	sysCgroupRoot = filepath.Join(tmp, "sys_cgroup")

	_ = os.MkdirAll(sysCgroupRoot, 0o755)
	_ = os.WriteFile(procSelfCgroup, []byte("0::/user.slice/test.slice\n"), 0o644)

	targetSlice := filepath.Join(sysCgroupRoot, "user.slice/test.slice")
	_ = os.MkdirAll(targetSlice, 0o755)
	_ = os.WriteFile(filepath.Join(targetSlice, "cpu.max"), []byte("max 100000\n"), 0o644)

	ctrl := NewController("")
	if ctrl.cgroupRoot != targetSlice {
		t.Errorf("Auto-discovered root = %q, want %q", ctrl.cgroupRoot, targetSlice)
	}
	if ctrl.readOnly {
		t.Errorf("Expected readOnly to be false on writeable path")
	}
}

func TestReadOnlyFallback(t *testing.T) {
	tmp := t.TempDir()
	cpuMax := filepath.Join(tmp, "cpu.max")
	_ = os.WriteFile(cpuMax, []byte("max 100000\n"), 0o444) // Read-only

	ctrl := NewController(tmp)
	if !ctrl.readOnly {
		t.Fatalf("Expected readOnly = true for read-only path")
	}

	// Should safely return nil, executing telemetry-only execution
	if err := ctrl.SetBurst(); err != nil {
		t.Errorf("SetBurst returned error: %v", err)
	}
	if err := ctrl.SetQuiescent(); err != nil {
		t.Errorf("SetQuiescent returned error: %v", err)
	}

	data, _ := os.ReadFile(cpuMax)
	if string(data) != "max 100000\n" {
		t.Errorf("File incorrectly modified despite read-only fallback mode")
	}
}

func TestCgroupReadCPUStat(t *testing.T) {
	dir := t.TempDir()
	statPath := filepath.Join(dir, "cpu.stat")
	fixture := "usage_usec 150000\nnr_periods 1000\nnr_throttled 50\nthrottled_usec 25000\n"
	if err := os.WriteFile(statPath, []byte(fixture), 0o644); err != nil {
		t.Fatalf("write cpu.stat fixture: %v", err)
	}
	// Touch cpu.max so it doesn't trigger read-only fallback logging unnecessarily
	_ = os.WriteFile(filepath.Join(dir, "cpu.max"), []byte("max\n"), 0o644)

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
}

func TestCgroupMissingFiles(t *testing.T) {
	dir := t.TempDir()
	ctrl := NewController(filepath.Join(dir, "nonexistent"))

	if !ctrl.readOnly {
		t.Fatalf("expected missing dir to trigger read-only fallback mode")
	}

	if err := ctrl.SetBurst(); err != nil {
		t.Fatalf("SetBurst() on missing dir = %v, want nil (fallback mode)", err)
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
