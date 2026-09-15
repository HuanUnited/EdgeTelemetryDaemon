package outbox

import (
	"context"
	"testing"
	"time"
)

func BenchmarkOutboxPush(b *testing.B) {
	ob := NewOutbox(Config{Capacity: 10000, DropPolicy: DropOldest})
	defer ob.Close()
	evt := Event{ID: "bench-id", Type: EventAnomalyAlert, Timestamp: time.Now()}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ob.Push(evt)
	}
}

func BenchmarkOutboxPop(b *testing.B) {
	ob := NewOutbox(Config{Capacity: 1024, DropPolicy: DropOldest})
	defer ob.Close()
	ctx := context.Background()
	evt := Event{ID: "bench-id", Type: EventAnomalyAlert, Timestamp: time.Now()}
	for range 1024 {
		_ = ob.Push(evt)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if (i&1023) == 0 && i > 0 {
			b.StopTimer()
			for range 1024 {
				_ = ob.Push(evt)
			}
			b.StartTimer()
		}
		_, _ = ob.Pop(ctx)
	}
}

func BenchmarkOutboxPushPop(b *testing.B) {
	ob := NewOutbox(Config{Capacity: 1000, DropPolicy: DropOldest})
	defer ob.Close()
	ctx := context.Background()
	evt := Event{ID: "bench-id", Type: EventAnomalyAlert, Timestamp: time.Now()}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ob.Push(evt)
		_, _ = ob.Pop(ctx)
	}
}

func TestOutboxPushAlloc(t *testing.T) {
	const runs = 1000
	ob := NewOutbox(Config{Capacity: runs + 10, DropPolicy: DropOldest})
	defer ob.Close()
	evt := Event{ID: "test-id", Type: EventAnomalyAlert, Timestamp: time.Now()}

	allocs := testing.AllocsPerRun(runs, func() {
		_ = ob.Push(evt)
	})
	if allocs != 0 {
		t.Fatalf("Outbox.Push allocated %v times, want 0", allocs)
	}
}

func TestOutboxPopAlloc(t *testing.T) {
	const runs = 1000
	ob := NewOutbox(Config{Capacity: runs + 10, DropPolicy: DropOldest})
	defer ob.Close()
	evt := Event{ID: "test-id", Type: EventAnomalyAlert, Timestamp: time.Now()}
	for range runs + 10 {
		_ = ob.Push(evt)
	}

	ctx := context.Background()
	allocs := testing.AllocsPerRun(runs, func() {
		_, _ = ob.Pop(ctx)
	})
	if allocs != 0 {
		t.Fatalf("Outbox.Pop allocated %v times, want 0", allocs)
	}
}

func TestOutboxPushPopAlloc(t *testing.T) {
	const runs = 1000
	ob := NewOutbox(Config{Capacity: 10, DropPolicy: DropOldest})
	defer ob.Close()
	ctx := context.Background()
	evt := Event{ID: "test-id", Type: EventAnomalyAlert, Timestamp: time.Now()}

	allocs := testing.AllocsPerRun(runs, func() {
		_ = ob.Push(evt)
		_, _ = ob.Pop(ctx)
	})
	if allocs != 0 {
		t.Fatalf("Outbox.Push/Pop allocated %v times, want 0", allocs)
	}
}
