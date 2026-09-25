//go:build linux

// Package cgroup manages Linux cgroup v2 CPU quota allocation and statistics.
//
package cgroup

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"
)

// Mode represents the operational CPU quota mode for the daemon.
type Mode uint8

const (
	// ModeQuiescent limits CPU quota to a lower baseline.
	ModeQuiescent Mode = iota
	// ModeBurst removes quota ceiling to allow maximum CPU utilization.
	ModeBurst
)

// String returns the string representation of Mode.
func (m Mode) String() string {
	switch m {
	case ModeQuiescent:
		return "quiescent"
	case ModeBurst:
		return "burst"
	default:
		return "unknown"
	}
}

const (
	cpuMaxFile  = "cpu.max"
	cpuStatFile = "cpu.stat"

	burstQuota     = "max 100000\n"
	quiescentQuota = "5000 100000\n"

	keyUsageUsec     = "usage_usec"
	keyNrPeriods     = "nr_periods"
	keyNrThrottled   = "nr_throttled"
	keyThrottledUsec = "throttled_usec"

	statBufSize = 4096
	maxPathLen  = 256

	sysOPENAT uintptr = syscall.SYS_OPENAT
	sysCLOSE  uintptr = syscall.SYS_CLOSE
	sysREAD   uintptr = syscall.SYS_READ

	atFDCWD = ^uintptr(99)
)

var (
	errPathTooLong = errors.New("cgroup: path exceeds 255 bytes")
	errOpenStat    = errors.New("cgroup: failed to open stat file")
	errReadStat    = errors.New("cgroup: failed to read stat file")
)

// Controller manages a single cgroup v2 hierarchy's CPU quota. cgroupRoot is the
// filesystem path to the cgroup directory (e.g. "/sys/fs/cgroup" in production, a
// t.TempDir() in tests) - never hardcode the path, it must be constructor-injected for
// testability.
type Controller struct {
	mu         sync.Mutex
	cgroupRoot string
	mode       Mode
	hasMode    bool
}

// NewController creates a new Controller for the given cgroup root directory.
func NewController(cgroupRoot string) *Controller {
	return &Controller{
		cgroupRoot: cgroupRoot,
	}
}

// SetBurst writes "max 100000\n" to cgroupRoot/cpu.max. No-op (zero file writes) if
// already in ModeBurst.
func (c *Controller) SetBurst() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.hasMode && c.mode == ModeBurst {
		return nil
	}

	path := filepath.Join(c.cgroupRoot, cpuMaxFile)
	if err := os.WriteFile(path, []byte(burstQuota), 0o644); err != nil {
		return err
	}

	c.mode = ModeBurst
	c.hasMode = true
	return nil
}

// SetQuiescent writes "5000 100000\n" to cgroupRoot/cpu.max (5% of a 100ms period).
// No-op (zero file writes) if already in ModeQuiescent.
func (c *Controller) SetQuiescent() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.hasMode && c.mode == ModeQuiescent {
		return nil
	}

	path := filepath.Join(c.cgroupRoot, cpuMaxFile)
	if err := os.WriteFile(path, []byte(quiescentQuota), 0o644); err != nil {
		return err
	}

	c.mode = ModeQuiescent
	c.hasMode = true
	return nil
}

// Mode returns the current quota mode of the controller.
func (c *Controller) Mode() Mode {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mode
}

// CPUStat mirrors the fields of cgroup v2's cpu.stat file that this daemon cares about.
type CPUStat struct {
	UsageUsec     uint64
	NrPeriods     uint64
	NrThrottled   uint64
	ThrottledUsec uint64
}

// ReadCPUStat parses cgroupRoot/cpu.stat. Must allocate zero bytes on the heap on the
// success path - read into a fixed-size stack buffer and parse in place, following the
// same style as internal/collector/mem_linux.go's parseMemInfo (line-by-line key/value
// scan with bytes.Cut, no strings.Split, no regexp).
func (c *Controller) ReadCPUStat() (CPUStat, error) {
	rootLen := len(c.cgroupRoot)
	totalLen := rootLen + 1 + len(cpuStatFile)
	if totalLen >= maxPathLen {
		return CPUStat{}, errPathTooLong
	}

	var pathBuf [maxPathLen]byte
	n := copy(pathBuf[:], c.cgroupRoot)
	if n > 0 && pathBuf[n-1] == '/' {
		n += copy(pathBuf[n:], cpuStatFile)
	} else {
		pathBuf[n] = '/'
		n++
		n += copy(pathBuf[n:], cpuStatFile)
	}
	pathBuf[n] = 0

	fd, _, errno := syscall.Syscall6(sysOPENAT, atFDCWD, uintptr(unsafe.Pointer(&pathBuf[0])), uintptr(syscall.O_RDONLY), 0, 0, 0)
	if errno != 0 {
		return CPUStat{}, errOpenStat
	}
	defer func() {
		_, _, _ = syscall.Syscall(sysCLOSE, fd, 0, 0)
	}()

	var buf [statBufSize]byte
	readN, _, rerr := syscall.Syscall(sysREAD, fd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if rerr != 0 {
		return CPUStat{}, errReadStat
	}

	return parseCPUStat(buf[:readN])
}

func parseCPUStat(data []byte) (CPUStat, error) {
	var stat CPUStat
	for len(data) > 0 {
		var line []byte
		if idx := bytes.IndexByte(data, '\n'); idx >= 0 {
			line, data = data[:idx], data[idx+1:]
		} else {
			line, data = data, nil
		}
		line = bytes.TrimRight(line, "\r")
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		key, rest, ok := bytes.Cut(line, []byte(" "))
		if !ok {
			continue
		}
		val, ok := parseValue(rest)
		if !ok {
			continue
		}

		switch {
		case bytes.Equal(key, []byte(keyUsageUsec)):
			stat.UsageUsec = val
		case bytes.Equal(key, []byte(keyNrPeriods)):
			stat.NrPeriods = val
		case bytes.Equal(key, []byte(keyNrThrottled)):
			stat.NrThrottled = val
		case bytes.Equal(key, []byte(keyThrottledUsec)):
			stat.ThrottledUsec = val
		default:
			continue
		}
	}
	return stat, nil
}

func parseValue(rest []byte) (uint64, bool) {
	rest = bytes.TrimLeft(rest, " \t")
	var value uint64
	i := 0
	for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
		value = value*10 + uint64(rest[i]-'0')
		i++
	}
	if i == 0 {
		return 0, false
	}
	return value, true
}
