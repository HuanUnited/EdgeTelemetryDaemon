package filter

import (
	"testing"
	"time"
)

func BenchmarkSuppressorProcess(b *testing.B) {
	s := NewSuppressor(SuppressorConfig{
		HoldoffDuration:         5 * time.Second,
		MinConsecutiveAnomalies: 1,
		MinConsecutiveNormals:   3,
	})
	now := time.Now()
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		s.Process(i%2 == 0, now)
	}
}

func BenchmarkRingBufferPush(b *testing.B) {
	rb := NewRingBuffer(50)
	entry := SnapshotEntry{Timestamp: time.Now(), Value: 42.0, ZScore: 2.1, Anomalous: false}
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		rb.Push(entry)
	}
}

func BenchmarkRingBufferSnapshot(b *testing.B) {
	rb := NewRingBuffer(50)
	for i := 0; i < 50; i++ {
		rb.Push(SnapshotEntry{Timestamp: time.Now(), Value: float64(i)})
	}
	var buf [50]SnapshotEntry
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = rb.Snapshot(buf[:0])
	}
}

func BenchmarkHeartbeatObserve(b *testing.B) {
	hb := NewHeartbeatAggregator(10 * time.Second)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		hb.Observe(12.5, i%10 == 0, i%20 == 0)
	}
}
