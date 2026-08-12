package filter

import (
	"sync"
	"time"
)

// SuppressorState represents the operational state of the deadband suppressor state machine.
type SuppressorState uint8

const (
	// StateNormal indicates steady-state non-anomalous telemetry flow.
	StateNormal SuppressorState = iota
	// StateAlerting indicates an anomaly alert transition has just fired.
	StateAlerting
	// StateSuppressed indicates active anomalies are being holdoff-suppressed to avoid alert storms.
	StateSuppressed
)

// String returns the human-readable name of the state.
func (s SuppressorState) String() string {
	switch s {
	case StateNormal:
		return "NORMAL"
	case StateAlerting:
		return "ALERTING"
	case StateSuppressed:
		return "SUPPRESSED"
	default:
		return "UNKNOWN"
	}
}

// SuppressorConfig configures state machine thresholds and holdoff timings.
type SuppressorConfig struct {
	// HoldoffDuration is the minimum quiet period between alert triggers.
	HoldoffDuration time.Duration
	// MinConsecutiveAnomalies is the number of consecutive anomalous readings required to trip alert.
	MinConsecutiveAnomalies int
	// MinConsecutiveNormals is the number of consecutive normal readings required to return to StateNormal.
	MinConsecutiveNormals int
}

// Suppressor manages state transitions for deadband anomaly suppression.
// It is safe for concurrent use across goroutines.
type Suppressor struct {
	mu sync.Mutex

	cfg SuppressorConfig

	state            SuppressorState
	consecutiveAnoms int
	consecutiveNorms int
	lastAlertTime    time.Time

	totalAnomalies   uint64
	totalSuppressed  uint64
	totalAlertsFired uint64
}

// NewSuppressor initializes a Suppressor with sane baseline defaults for any invalid configuration options.
func NewSuppressor(cfg SuppressorConfig) *Suppressor {
	if cfg.MinConsecutiveAnomalies <= 0 {
		cfg.MinConsecutiveAnomalies = 1
	}
	if cfg.MinConsecutiveNormals <= 0 {
		cfg.MinConsecutiveNormals = 3
	}
	if cfg.HoldoffDuration < 0 {
		cfg.HoldoffDuration = 0
	}
	return &Suppressor{
		cfg:   cfg,
		state: StateNormal,
	}
}

// Process evaluates an anomaly signal observation against the deadband state machine.
// Returns shouldAlert (true only when a fresh alert fires) and the current state.
func (s *Suppressor) Process(isAnomalous bool, now time.Time) (shouldAlert bool, state SuppressorState) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if isAnomalous {
		s.totalAnomalies++
		s.consecutiveNorms = 0
		s.consecutiveAnoms++

		switch s.state {
		case StateNormal:
			if s.consecutiveAnoms >= s.cfg.MinConsecutiveAnomalies {
				s.state = StateAlerting
				s.lastAlertTime = now
				s.totalAlertsFired++
				return true, s.state
			}
			return false, s.state

		case StateAlerting, StateSuppressed:
			if s.cfg.HoldoffDuration > 0 && !s.lastAlertTime.IsZero() && now.Sub(s.lastAlertTime) >= s.cfg.HoldoffDuration {
				s.state = StateAlerting
				s.lastAlertTime = now
				s.totalAlertsFired++
				return true, s.state
			}
			s.state = StateSuppressed
			s.totalSuppressed++
			return false, s.state
		}
	} else {
		s.consecutiveAnoms = 0
		s.consecutiveNorms++

		if s.consecutiveNorms >= s.cfg.MinConsecutiveNormals {
			s.state = StateNormal
		}
		return false, s.state
	}

	return false, s.state
}

// State returns the current operational state of the suppressor.
func (s *Suppressor) State() SuppressorState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Stats returns cumulative counts of total anomalies, alerts fired, and suppressed events.
func (s *Suppressor) Stats() (totalAnomalies, totalAlertsFired, totalSuppressed uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.totalAnomalies, s.totalAlertsFired, s.totalSuppressed
}

// Reset clears state counters back to initial clean state.
func (s *Suppressor) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = StateNormal
	s.consecutiveAnoms = 0
	s.consecutiveNorms = 0
	s.lastAlertTime = time.Time{}
	s.totalAnomalies = 0
	s.totalSuppressed = 0
	s.totalAlertsFired = 0
}
