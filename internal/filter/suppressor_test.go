package filter

import (
	"sync"
	"testing"
	"time"
)

func TestSuppressorStateTransitions(t *testing.T) {
	cfg := SuppressorConfig{
		HoldoffDuration:         100 * time.Millisecond,
		MinConsecutiveAnomalies: 2,
		MinConsecutiveNormals:   2,
	}
	s := NewSuppressor(cfg)
	baseTime := time.Now()

	// Step 1: First anomaly -> Normal (needs 2 consecutive)
	alert, state := s.Process(true, baseTime)
	if alert || state != StateNormal {
		t.Fatalf("step 1: got alert=%v state=%v, want false NORMAL", alert, state)
	}

	// Step 2: Second anomaly -> Alerting
	alert, state = s.Process(true, baseTime.Add(10*time.Millisecond))
	if !alert || state != StateAlerting {
		t.Fatalf("step 2: got alert=%v state=%v, want true ALERTING", alert, state)
	}

	// Step 3: Immediate 3rd anomaly within holdoff -> Suppressed
	alert, state = s.Process(true, baseTime.Add(20*time.Millisecond))
	if alert || state != StateSuppressed {
		t.Fatalf("step 3: got alert=%v state=%v, want false SUPPRESSED", alert, state)
	}

	// Step 4: Anomaly after holdoff -> Alerting
	alert, state = s.Process(true, baseTime.Add(150*time.Millisecond))
	if !alert || state != StateAlerting {
		t.Fatalf("step 4: got alert=%v state=%v, want true ALERTING", alert, state)
	}

	// Step 5: 1 Normal -> still Suppressed/Alerting window reset
	_, _ = s.Process(false, baseTime.Add(160*time.Millisecond))

	// Step 6: 2nd Normal -> Reset to Normal
	alert, state = s.Process(false, baseTime.Add(170*time.Millisecond))
	if alert || state != StateNormal {
		t.Fatalf("step 6: got alert=%v state=%v, want false NORMAL", alert, state)
	}
}

func TestSuppressorConcurrent(t *testing.T) {
	s := NewSuppressor(SuppressorConfig{
		HoldoffDuration:         50 * time.Millisecond,
		MinConsecutiveAnomalies: 1,
		MinConsecutiveNormals:   2,
	})

	var wg sync.WaitGroup
	const goroutines = 8
	const iterations = 500

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			now := time.Now()
			for j := 0; j < iterations; j++ {
				isAnom := (j%3 == 0)
				s.Process(isAnom, now.Add(time.Duration(j)*time.Millisecond))
				_ = s.State()
			}
		}(i)
	}
	wg.Wait()
}

func FuzzSuppressorProcess(f *testing.F) {
	f.Add(true, int64(1700000000))
	f.Add(false, int64(0))
	f.Add(true, int64(-1000))
	f.Add(false, int64(1700000050))

	f.Fuzz(func(t *testing.T, isAnomaly bool, unixSec int64) {
		s := NewSuppressor(SuppressorConfig{
			HoldoffDuration:         5 * time.Second,
			MinConsecutiveAnomalies: 2,
			MinConsecutiveNormals:   3,
		})
		_, _ = s.Process(isAnomaly, time.Unix(unixSec, 0))
	})
}
