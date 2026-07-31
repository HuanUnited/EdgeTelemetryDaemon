//go:build linux

// Package collector implements zero-allocation scrapers for Linux host
// telemetry (/proc/stat, /proc/meminfo) and a synthetic AI inference metric
// generator for development and load testing.
package collector

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// DefaultProcStatPath is the default location of the kernel CPU accounting
// file. Overridable for tests.
var DefaultProcStatPath = func() string {
	if p := os.Getenv("ETD_PROCFS_PATH"); p != "" {
		return p + "/stat"
	}
	return "/proc/stat"
}()

// CPUStats holds aggregate CPU tick counters parsed from /proc/stat's first
// line ("cpu  ..."). All values are expressed in user-space "ticks" (typically
// USER_HZ, 100 per second on Linux).
type CPUStats struct {
	User    uint64 // normal processes executing in user mode
	Nice    uint64 // niced processes executing in user mode
	System  uint64 // processes executing in kernel mode
	Idle    uint64 // idle (includes iowait on modern kernels)
	Iowait  uint64 // waiting for I/O to complete
	Irq     uint64 // servicing hardware interrupts
	Softirq uint64 // servicing software interrupts
	Steal   uint64 // involuntary wait (hypervisor)
	Guest   uint64 // running a normal guest
	GuestN  uint64 // running a niced guest

	// Total is the sum of every counter above, i.e. the total number of ticks
	// elapsed across all CPUs.
	Total uint64
}

// Modern Linux system call numbers for amd64 and arm64 architectures.
// By defining these explicitly, we decouple our code from standard library shifts.
const (
	sysOPENAT uintptr = 257 // Linux SYS_OPENAT
	sysCLOSE  uintptr = 3   // Linux SYS_CLOSE
	sysREAD   uintptr = 0   // Linux SYS_READ

	// AT_FDCWD (-100) tells openat to look relative to the current working directory.
	atFDCWD = ^uintptr(99)
)

// static error messages for no allocation error reporting
var (
	errPathTooLong = errors.New("collector: path exceeds 255 bytes")
	errOpenStat    = errors.New("collector: failed to open stat file")
	errReadStat    = errors.New("collector: failed to read stat file")
)

// CollectCPU populates out with aggregate CPU tick counters read from
// DefaultProcStatPath. No heap allocations occur on the success path.
func CollectCPU(procPath string, out *CPUStats) error {
	return scrapeProcStat(procPath+"/stat", out)
}

// scrapeProcStat reads the first line of the file at path (expected to be
// /proc/stat) and parses the aggregate "cpu" row into out. The caller supplies
// the output value by pointer so that the method allocates nothing on the hot
// path.
//
// out.Total is computed after parsing; any counters reported by the kernel
// beyond GuestN are ignored, matching the fields enumerated by CPUStats.
var procStatPath = []byte("/proc/stat\x00")

func scrapeProcStat(path string, out *CPUStats) error {
	if len(path) >= 256 {
		return errPathTooLong
	}
	var pathBuf [256]byte
	copy(pathBuf[:], path)
	pathBuf[len(path)] = 0

	fd, _, errno := syscall.Syscall6(sysOPENAT, atFDCWD, uintptr(unsafe.Pointer(&pathBuf[0])), uintptr(syscall.O_RDONLY), 0, 0, 0)
	if errno != 0 {
		return errOpenStat
	}
	defer syscall.Syscall(sysCLOSE, fd, 0, 0)

	var buf [512]byte
	n, _, rerr := syscall.Syscall(sysREAD, fd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if rerr != 0 {
		return errReadStat
	}
	return parseCPULine(buf[:n], out)
}

// parseCPULine parses a single /proc/stat "cpu" line. It expects the data to
// begin with the "cpu" prefix followed by a stream of decimal tick counters.
func parseCPULine(line []byte, out *CPUStats) error {
	rest := skipToken(line) // consume "cpu" or "cpu0"
	if rest == nil {
		return fmt.Errorf("collector: malformed cpu line (missing token)")
	}

	var err error
	// Field order follows the kernel's arch/x86/kernel/smpboot.c ordering:
	// user, nice, system, idle, iowait, irq, softirq, steal, guest, guest_nice.
	// Each parse step is written out explicitly to keep the hot path
	// allocation-free (no intermediate slice).
	targets := [...]*uint64{
		&out.User, &out.Nice, &out.System, &out.Idle, &out.Iowait,
		&out.Irq, &out.Softirq, &out.Steal, &out.Guest, &out.GuestN,
	}

	var total uint64
	for _, target := range targets {
		if rest, *target, err = nextUint(rest); err != nil {
			return fmt.Errorf("collector: parse cpu counters: %w", err)
		}
		total += *target
	}
	out.Total = total
	return nil
}

// skipToken returns the slice immediately after the first whitespace-delimited
// token in line. It returns nil if no token is present.
func skipToken(line []byte) []byte {
	i := 0
	for i < len(line) && line[i] != ' ' && line[i] != '\t' {
		i++
	}
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i >= len(line) {
		return nil
	}
	return line[i:]
}

var errInvalidUint = errors.New("collector: expected unsigned integer")

// nextUint parses an unsigned decimal integer at the start of b, returning the
// remainder of b after the number and its trailing whitespace, along with the
// parsed value. It returns an error when the field is absent or malformed.
func nextUint(b []byte) (rest []byte, val uint64, err error) {
	i := 0
	for i < len(b) && b[i] >= '0' && b[i] <= '9' {
		val = val*10 + uint64(b[i]-'0')
		i++
	}
	if i == 0 {
		return nil, 0, errInvalidUint
	}
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	return b[i:], val, nil
}
