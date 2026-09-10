//go:build linux

package collector

import (
	"bytes"
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

// Package-level key constants allow allocation-free field dispatch against
// lines parsed from /proc/meminfo. They are treated as read-only.
const (
	memKeyTotal     = "MemTotal"
	memKeyFree      = "MemFree"
	memKeyAvailable = "MemAvailable"
	memKeyBuffers   = "Buffers"
	memKeyCached    = "Cached"
	memKeySwapTotal = "SwapTotal"
	memKeySwapFree  = "SwapFree"
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
	defer func() {
		_, _, _ = syscall.Syscall(sysCLOSE, fd, 0, 0)
	}()

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
		case bytes.Equal(key, []byte(memKeyTotal)):
			out.MemTotal = value
		case bytes.Equal(key, []byte(memKeyFree)):
			out.MemFree = value
		case bytes.Equal(key, []byte(memKeyAvailable)):
			out.MemAvailable = value
		case bytes.Equal(key, []byte(memKeyBuffers)):
			out.Buffers = value
		case bytes.Equal(key, []byte(memKeyCached)):
			out.Cached = value
		case bytes.Equal(key, []byte(memKeySwapTotal)):
			out.SwapTotal = value
		case bytes.Equal(key, []byte(memKeySwapFree)):
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
func splitKey(line []byte) ([]byte, []byte, bool) {
	return bytes.Cut(line, []byte(":"))
}

// parseValue extracts the leading unsigned integer from rest. It returns
// ok=false when no digit is present.
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
