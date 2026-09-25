//go:build !linux

package cgroup

import "errors"

// ErrUnsupportedPlatform indicates cgroup control is not supported on non-Linux platforms.
var ErrUnsupportedPlatform = errors.New("cgroup: unsupported platform")

// Mode represents the operational CPU quota mode for the daemon.
type Mode uint8

const (
	ModeQuiescent Mode = iota
	ModeBurst
)

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

// Controller is a non-Linux stub for cgroup management.
type Controller struct {
	cgroupRoot string
}

// NewController returns a stub controller on non-Linux systems.
func NewController(cgroupRoot string) *Controller {
	return &Controller{cgroupRoot: cgroupRoot}
}

// SetBurst silently returns nil to allow telemetry-only execution on non-Linux targets.
func (c *Controller) SetBurst() error {
	return nil
}

// SetQuiescent silently returns nil to allow telemetry-only execution on non-Linux targets.
func (c *Controller) SetQuiescent() error {
	return nil
}

// Mode returns ModeQuiescent on non-Linux systems.
func (c *Controller) Mode() Mode {
	return ModeQuiescent
}

// CPUStat mirrors cgroup v2's cpu.stat accounting counters.
type CPUStat struct {
	UsageUsec     uint64
	NrPeriods     uint64
	NrThrottled   uint64
	ThrottledUsec uint64
}

// ReadCPUStat returns ErrUnsupportedPlatform on non-Linux systems.
func (c *Controller) ReadCPUStat() (CPUStat, error) {
	return CPUStat{}, ErrUnsupportedPlatform
}

// IsReadOnly returns true on non-Linux systems.
func (c *Controller) IsReadOnly() bool {
	return true
}
