//go:build linux

package collector

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// DefaultProcMemInfoPath is the default location of the kernel memory
// accounting file. Overridable for tests.
var DefaultProcMemInfoPath = func() string {
	if p := os.Getenv("ETD_PROCFS_PATH"); p != "" {
		return p + "/meminfo"
	}
	return "/proc/meminfo"
}()

// Package-level key constants allow allocation-free field dispatch against
// lines parsed from /proc/meminfo. They are treated as read-only.
var (
	memKeyTotal     = []byte("MemTotal")
	memKeyFree      = []byte("MemFree")
	memKeyAvailable = []byte("MemAvailable")
	memKeyBuffers   = []byte("Buffers")
	memKeyCached    = []byte("Cached")
	memKeySwapTotal = []byte("SwapTotal")
	memKeySwapFree  = []byte("SwapFree")
)

// static error messages for zero alloc reporting
var (
	errOpenMemInfo       = errors.New("collector: failed to open meminfo file")
	errReadMemInfo       = errors.New("collector: failed to read meminfo file")
	errMemInfoBufferFull = errors.New("collector: meminfo file exceeds read buffer size")
)

// memBufSize is the stack buffer used to read /proc/meminfo. The file is
// comfortably under 4 KiB on all supported kernels.
const memBufSize = 4096

// MemStats holds the subset of /proc/meminfo values relevant to telemetry.
// All values are reported in kilobytes, matching the kernel's accounting
// units for this file.
type MemStats struct {
	MemTotal     uint64
	MemFree      uint64
	MemAvailable uint64
	Buffers      uint64
	Cached       uint64
	SwapTotal    uint64
	SwapFree     uint64
}

// CollectMem populates out with host memory statistics read from
// DefaultProcMemInfoPath. No heap allocations occur on the success path.
func CollectMem(procPath string, out *MemStats) error {
	return scrapeProcMemInfo(procPath+"/meminfo", out)
}

// scrapeProcMemInfo parses the file at path (expected to be /proc/meminfo)
// into out. The caller supplies the output value by pointer so that the method
// allocates nothing on the hot path. Fields not present in the file are left at
// their zero value; a file that contains no recognised fields is an error.
func scrapeProcMemInfo(path string, out *MemStats) error {
	if len(path) >= 256 {
		return errPathTooLong
	}
	var pathBuf [256]byte
	copy(pathBuf[:], path)
	pathBuf[len(path)] = 0

	fd, _, errno := syscall.Syscall6(sysOPENAT, atFDCWD, uintptr(unsafe.Pointer(&pathBuf[0])), uintptr(syscall.O_RDONLY), 0, 0, 0)
	if errno != 0 {
		return errOpenMemInfo
	}
	defer syscall.Syscall(sysCLOSE, fd, 0, 0)

	var buf [memBufSize]byte
	n, _, rerr := syscall.Syscall(sysREAD, fd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if rerr != 0 {
		return errReadMemInfo
	}
	if int(n) == memBufSize {
		return errMemInfoBufferFull
	}
	return parseMemInfo(buf[:n], out)
}

func parseMemInfo(data []byte, out *MemStats) error {
	seen := 0
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

		key, rest, ok := splitKey(line)
		if !ok {
			continue
		}
		value, ok := parseValue(rest)
		if !ok {
			continue
		}

		switch {
		case bytes.Equal(key, memKeyTotal):
			out.MemTotal = value
		case bytes.Equal(key, memKeyFree):
			out.MemFree = value
		case bytes.Equal(key, memKeyAvailable):
			out.MemAvailable = value
		case bytes.Equal(key, memKeyBuffers):
			out.Buffers = value
		case bytes.Equal(key, memKeyCached):
			out.Cached = value
		case bytes.Equal(key, memKeySwapTotal):
			out.SwapTotal = value
		case bytes.Equal(key, memKeySwapFree):
			out.SwapFree = value
		default:
			continue
		}
		seen++
	}

	if seen == 0 {
		return fmt.Errorf("collector: buffer contains no recognised meminfo fields")
	}
	return nil
}

// splitKey splits a /proc/meminfo line into its "Key:" prefix and the value
// remainder. It returns ok=false when no colon is found.
func splitKey(line []byte) (key, rest []byte, ok bool) {
	idx := bytes.IndexByte(line, ':')
	if idx < 0 {
		return nil, nil, false
	}
	return line[:idx], line[idx+1:], true
}

// parseValue extracts the leading unsigned integer from rest. It returns
// ok=false when no digit is present.
func parseValue(rest []byte) (value uint64, ok bool) {
	rest = bytes.TrimLeft(rest, " \t")
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
