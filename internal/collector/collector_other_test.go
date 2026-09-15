//go:build !linux

package collector

import (
	"errors"
	"testing"
)

func TestCollectCPUUnsupportedPlatform(t *testing.T) {
	var stats CPUStats
	err := CollectCPU("/proc", &stats)
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("CollectCPU() error = %v, want %v", err, ErrUnsupportedPlatform)
	}
}
