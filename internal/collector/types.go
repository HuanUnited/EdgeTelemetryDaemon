package collector

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
