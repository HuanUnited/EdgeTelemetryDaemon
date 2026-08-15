package outbox

import (
	"context"
	"testing"
)

func BenchmarkOutboxContention(b *testing.B) {
	q := NewOutbox(OutboxConfig{Capacity: 10000, DropPolicy: DropOldest})
	ctx := context.Background()
	payload := []byte("test")

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = q.Push(Event{ID: "bench", Data: payload})
			_, _ = q.Pop(ctx)
		}
	})
}
