package filter

import (
	"sync"
	"testing"
	"time"
)

func TestRingBufferPushAndSnapshot(t *testing.T) {
	rb := NewRingBuffer(3)
	now := time.Now()

	rb.Push(SnapshotEntry{Timestamp: now, Value: 1.0})
	rb.Push(SnapshotEntry{Timestamp: now.Add(time.Second), Value: 2.0})
	rb.Push(SnapshotEntry{Timestamp: now.Add(2 * time.Second), Value: 3.0})

	if rb.Len() != 3 {
		t.Fatalf("expected length 3, got %d", rb.Len())
	}

	snaps := rb.Snapshot(nil)
	if len(snaps) != 3 || snaps[0].Value != 1.0 || snaps[2].Value != 3.0 {
		t.Fatalf("unexpected snapshot sequence: %+v", snaps)
	}

	// Push 4th element -> overwrites 1st (1.0)
	rb.Push(SnapshotEntry{Timestamp: now.Add(3 * time.Second), Value: 4.0})

	snaps = rb.Snapshot(snaps)
	if len(snaps) != 3 || snaps[0].Value != 2.0 || snaps[2].Value != 4.0 {
		t.Fatalf("unexpected snapshot after overflow: %+v", snaps)
	}
}

func TestRingBufferConcurrent(t *testing.T) {
	rb := NewRingBuffer(20)
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				rb.Push(SnapshotEntry{
					Timestamp: time.Now(),
					Value:     float64(id*1000 + j),
				})
				var buf [20]SnapshotEntry
				_ = rb.Snapshot(buf[:0])
			}
		}(i)
	}
	wg.Wait()
}
