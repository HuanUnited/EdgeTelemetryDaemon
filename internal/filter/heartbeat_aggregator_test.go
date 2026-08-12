package filter

import (
	"sync"
	"testing"
	"time"
)

func TestHeartbeatObserveAndFlush(t *testing.T) {
	hb := NewHeartbeatAggregator(100 * time.Millisecond)
	now := time.Now()

	hb.Observe(10.0, false, false)
	hb.Observe(20.0, true, false)
	hb.Observe(5.0, true, true)

	if hb.ShouldFlush(now) {
		t.Fatalf("ShouldFlush returned true before interval elapsed")
	}

	later := now.Add(150 * time.Millisecond)
	if !hb.ShouldFlush(later) {
		t.Fatalf("ShouldFlush returned false after interval elapsed")
	}

	var summary HeartbeatSummary
	hb.Flush(later, &summary)

	if summary.TotalSamples != 3 || summary.AnomalousCount != 2 || summary.SuppressedCount != 1 {
		t.Errorf("unexpected counts: %+v", summary)
	}
	if summary.MinVal != 5.0 || summary.MaxVal != 20.0 || summary.SumVal != 35.0 {
		t.Errorf("unexpected values: %+v", summary)
	}
}

func TestHeartbeatConcurrent(t *testing.T) {
	hb := NewHeartbeatAggregator(50 * time.Millisecond)
	var wg sync.WaitGroup

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				hb.Observe(float64(j), j%2 == 0, j%4 == 0)
				if j%50 == 0 {
					var s HeartbeatSummary
					_ = hb.Flush(time.Now(), &s)
				}
			}
		}()
	}
	wg.Wait()
}
