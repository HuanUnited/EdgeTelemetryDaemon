//go:build !linux

package collector

import "errors"

// ErrUnsupportedPlatform is returned when collection is requested on a non-Linux platform.
var ErrUnsupportedPlatform = errors.New("collector: unsupported platform")

// CollectCPU returns ErrUnsupportedPlatform on non-Linux platforms.
func CollectCPU(procPath string, out *CPUStats) error { return ErrUnsupportedPlatform }

// CollectMem returns ErrUnsupportedPlatform on non-Linux platforms.
func CollectMem(procPath string, out *MemStats) error { return ErrUnsupportedPlatform }
