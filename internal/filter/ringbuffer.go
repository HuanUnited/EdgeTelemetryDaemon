package filter

import (
	"sync"
	"time"
)

// SnapshotEntry holds a telemetry measurement context recorded prior to or during an anomaly.
type SnapshotEntry struct {
	Timestamp time.Time
	Value     float64
	ZScore    float64
	Anomalous bool
}

// RingBuffer provides a fixed-capacity ring buffer for capturing pre-trigger anomaly contexts.
// It is safe for concurrent access.
type RingBuffer struct {
	mu       sync.RWMutex
	buf      []SnapshotEntry
	capacity int
	head     int
	count    int
}

// NewRingBuffer allocates a RingBuffer with fixed storage capacity.
func NewRingBuffer(capacity int) *RingBuffer {
	if capacity <= 0 {
		capacity = 10
	}
	return &RingBuffer{
		buf:      make([]SnapshotEntry, capacity),
		capacity: capacity,
	}
}

// Push records an entry into the ring buffer. Zero heap allocations on execution.
func (r *RingBuffer) Push(entry SnapshotEntry) {
	r.mu.Lock()
	r.buf[r.head] = entry
	r.head = (r.head + 1) % r.capacity
	if r.count < r.capacity {
		r.count++
	}
	r.mu.Unlock()
}

// Snapshot extracts stored entries in chronological order (oldest to newest).
// Callers may pass a destination slice to eliminate heap allocations.
func (r *RingBuffer) Snapshot(dst []SnapshotEntry) []SnapshotEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.count == 0 {
		return dst[:0]
	}

	if cap(dst) < r.count {
		dst = make([]SnapshotEntry, r.count)
	} else {
		dst = dst[:r.count]
	}

	start := 0
	if r.count == r.capacity {
		start = r.head
	}

	for i := 0; i < r.count; i++ {
		idx := (start + i) % r.capacity
		dst[i] = r.buf[idx]
	}

	return dst
}

// Capacity returns the maximum number of entries the buffer can retain.
func (r *RingBuffer) Capacity() int {
	return r.capacity
}

// Len returns the current number of retained entries.
func (r *RingBuffer) Len() int {
	r.mu.RLock()
	n := r.count
	r.mu.RUnlock()
	return n
}

// Reset clears stored entries without deallocating backing storage.
func (r *RingBuffer) Reset() {
	r.mu.Lock()
	r.head = 0
	r.count = 0
	r.mu.Unlock()
}
