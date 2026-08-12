package filter

import (
	"sync"
	"time"
)

// HeartbeatSummary contains summary aggregate metrics over a completed heartbeat window.
type HeartbeatSummary struct {
	StartTime       time.Time
	EndTime         time.Time
	TotalSamples    uint64
	AnomalousCount  uint64
	SuppressedCount uint64
	MinVal          float64
	MaxVal          float64
	SumVal          float64
}

// HeartbeatAggregator accumulates periodic status metrics between alert triggers.
// Safe for concurrent use.
type HeartbeatAggregator struct {
	mu sync.Mutex

	interval    time.Duration
	windowStart time.Time

	totalSamples    uint64
	anomalousCount  uint64
	suppressedCount uint64

	minVal float64
	maxVal float64
	sumVal float64
	hasVal bool
}

// NewHeartbeatAggregator builds a heartbeat aggregator targeting the given duration window.
func NewHeartbeatAggregator(interval time.Duration) *HeartbeatAggregator {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	return &HeartbeatAggregator{
		interval:    interval,
		windowStart: time.Now(),
	}
}

// Observe records a sample into the current active heartbeat window.
func (h *HeartbeatAggregator) Observe(val float64, isAnomalous, isSuppressed bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.totalSamples++
	if isAnomalous {
		h.anomalousCount++
	}
	if isSuppressed {
		h.suppressedCount++
	}

	if !h.hasVal {
		h.minVal = val
		h.maxVal = val
		h.hasVal = true
	} else {
		if val < h.minVal {
			h.minVal = val
		}
		if val > h.maxVal {
			h.maxVal = val
		}
	}
	h.sumVal += val
}

// ShouldFlush checks if the current heartbeat window duration has elapsed.
func (h *HeartbeatAggregator) ShouldFlush(now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return !h.windowStart.IsZero() && now.Sub(h.windowStart) >= h.interval
}

// Flush generates a HeartbeatSummary snapshot for the window and resets counters for the next window.
func (h *HeartbeatAggregator) Flush(now time.Time, out *HeartbeatSummary) HeartbeatSummary {
	h.mu.Lock()
	defer h.mu.Unlock()

	res := HeartbeatSummary{
		StartTime:       h.windowStart,
		EndTime:         now,
		TotalSamples:    h.totalSamples,
		AnomalousCount:  h.anomalousCount,
		SuppressedCount: h.suppressedCount,
		MinVal:          h.minVal,
		MaxVal:          h.maxVal,
		SumVal:          h.sumVal,
	}

	if out != nil {
		*out = res
	}

	h.windowStart = now
	h.totalSamples = 0
	h.anomalousCount = 0
	h.suppressedCount = 0
	h.minVal = 0
	h.maxVal = 0
	h.sumVal = 0
	h.hasVal = false

	return res
}

// Reset clears aggregator state.
func (h *HeartbeatAggregator) Reset(now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.windowStart = now
	h.totalSamples = 0
	h.anomalousCount = 0
	h.suppressedCount = 0
	h.minVal = 0
	h.maxVal = 0
	h.sumVal = 0
	h.hasVal = false
}
