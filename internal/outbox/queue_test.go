package outbox

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestOutboxPushPop(t *testing.T) {
	ob := NewOutbox(OutboxConfig{Capacity: 5, DropPolicy: DropNewest})
	defer ob.Close()

	evt1 := Event{ID: "1", Type: EventAnomalyAlert, Timestamp: time.Now(), Data: []byte("a")}
	evt2 := Event{ID: "2", Type: EventHeartbeat, Timestamp: time.Now(), Data: []byte("b")}

	if err := ob.Push(evt1); err != nil {
		t.Fatalf("Push(evt1) failed: %v", err)
	}
	if err := ob.Push(evt2); err != nil {
		t.Fatalf("Push(evt2) failed: %v", err)
	}

	if ob.Len() != 2 {
		t.Errorf("Len() = %d, want 2", ob.Len())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	out1, err := ob.Pop(ctx)
	if err != nil || out1.ID != "1" {
		t.Fatalf("Pop() 1 = %v, %v; want ID 1", out1, err)
	}

	out2, err := ob.Pop(ctx)
	if err != nil || out2.ID != "2" {
		t.Fatalf("Pop() 2 = %v, %v; want ID 2", out2, err)
	}
}

func TestOutboxDropNewest(t *testing.T) {
	ob := NewOutbox(OutboxConfig{Capacity: 2, DropPolicy: DropNewest})
	defer ob.Close()

	_ = ob.Push(Event{ID: "1"})
	_ = ob.Push(Event{ID: "2"})

	err := ob.Push(Event{ID: "3"})
	if err != ErrQueueFull {
		t.Errorf("Push() 3 = %v, want ErrQueueFull", err)
	}

	enq, deq, dropped := ob.Stats()
	if enq != 2 || deq != 0 || dropped != 1 {
		t.Errorf("Stats() = %d, %d, %d; want 2, 0, 1", enq, deq, dropped)
	}
}

func TestOutboxDropOldest(t *testing.T) {
	ob := NewOutbox(OutboxConfig{Capacity: 2, DropPolicy: DropOldest})
	defer ob.Close()

	_ = ob.Push(Event{ID: "1"})
	_ = ob.Push(Event{ID: "2"})
	_ = ob.Push(Event{ID: "3"}) // Evicts ID 1

	ctx := context.Background()
	out, err := ob.Pop(ctx)
	if err != nil || out.ID != "2" {
		t.Fatalf("Pop() expected ID 2 after eviction, got ID %s (err: %v)", out.ID, err)
	}
}

func TestOutboxContextCancellation(t *testing.T) {
	ob := NewOutbox(OutboxConfig{Capacity: 10})
	defer ob.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := ob.Pop(ctx)
	if err != context.DeadlineExceeded {
		t.Errorf("Pop() on empty with timeout = %v, want DeadlineExceeded", err)
	}
}

func TestOutboxConcurrent(t *testing.T) {
	ob := NewOutbox(OutboxConfig{Capacity: 100, DropPolicy: DropOldest})
	defer ob.Close()

	var wg sync.WaitGroup
	const producers = 4
	const itemsPerProducer = 250

	for i := 0; i < producers; i++ {
		wg.Add(1)
		go func(pid int) {
			defer wg.Done()
			for j := 0; j < itemsPerProducer; j++ {
				_ = ob.Push(Event{ID: "test", Timestamp: time.Now()})
			}
		}(i)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	consumed := 0
	var consWg sync.WaitGroup
	consWg.Add(1)
	go func() {
		defer consWg.Done()
		for {
			_, err := ob.Pop(ctx)
			if err != nil {
				return
			}
			consumed++
		}
	}()

	wg.Wait()
	time.Sleep(50 * time.Millisecond)
	cancel()
	consWg.Wait()
}
