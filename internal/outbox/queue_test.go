package outbox

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOutboxPushPop(t *testing.T) {
	ob := NewOutbox(Config{Capacity: 5, DropPolicy: DropNewest})
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
	ob := NewOutbox(Config{Capacity: 2, DropPolicy: DropNewest})
	defer ob.Close()

	_ = ob.Push(Event{ID: "1"})
	_ = ob.Push(Event{ID: "2"})

	err := ob.Push(Event{ID: "3"})
	if !errors.Is(ErrQueueFull, err) {
		t.Errorf("Push() 3 = %v, want ErrQueueFull", err)
	}

	enq, deq, dropped := ob.Stats()
	if enq != 2 || deq != 0 || dropped != 1 {
		t.Errorf("Stats() = %d, %d, %d; want 2, 0, 1", enq, deq, dropped)
	}
}

func TestOutboxDropOldest(t *testing.T) {
	ob := NewOutbox(Config{Capacity: 2, DropPolicy: DropOldest})
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
	ob := NewOutbox(Config{Capacity: 10})
	defer ob.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := ob.Pop(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Pop() on empty with timeout = %v, want DeadlineExceeded", err)
	}
}

func TestOutboxConcurrent(_ *testing.T) {
	ob := NewOutbox(Config{Capacity: 100, DropPolicy: DropOldest})
	defer ob.Close()

	var wg sync.WaitGroup
	const producers = 4
	const itemsPerProducer = 250

	for range producers {
		wg.Go(func() {
			for range itemsPerProducer {
				_ = ob.Push(Event{ID: "test", Timestamp: time.Now()})
			}
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	consumed := 0
	var consWg sync.WaitGroup
	consWg.Go(func() {
		for {
			_, err := ob.Pop(ctx)
			if err != nil {
				return
			}
			consumed++
		}
	})

	wg.Wait()
	time.Sleep(50 * time.Millisecond)
	cancel()
	consWg.Wait()
}

func TestOutboxNotificationMultiConsumerStarvation(t *testing.T) {
	ob := NewOutbox(Config{Capacity: 10, DropPolicy: DropOldest})
	defer ob.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	var wg sync.WaitGroup
	var err1, err2 error
	var evt1, evt2 Event

	wg.Go(func() {
		evt1, err1 = ob.Pop(ctx)
	})

	wg.Go(func() {
		evt2, err2 = ob.Pop(ctx)
	})

	time.Sleep(20 * time.Millisecond)

	pushDone := make(chan struct{})
	go func() {
		defer close(pushDone)
		if err := ob.Push(Event{ID: "event-1"}); err != nil {
			t.Errorf("Push event-1 failed: %v", err)
		}
		if err := ob.Push(Event{ID: "event-2"}); err != nil {
			t.Errorf("Push event-2 failed: %v", err)
		}
	}()

	<-pushDone
	wg.Wait()

	if err1 != nil {
		t.Errorf("consumer 1 Pop error: %v", err1)
	}
	if err2 != nil {
		t.Errorf("consumer 2 Pop error: %v", err2)
	}
	if evt1.ID == "" || evt2.ID == "" {
		t.Errorf("expected non-empty events, got evt1=%+v evt2=%+v", evt1, evt2)
	}
}

func TestOutboxNotificationRaceUnderClose(t *testing.T) {
	ob := NewOutbox(Config{Capacity: 50, DropPolicy: DropOldest})

	const producers = 10
	var wg sync.WaitGroup
	var closedErrors atomic.Int32

	for range producers {
		wg.Go(func() {
			for {
				err := ob.Push(Event{ID: "race-event"})
				if errors.Is(err, ErrQueueClosed) {
					closedErrors.Add(1)
					return
				}
			}
		})
	}

	time.Sleep(10 * time.Millisecond)
	ob.Close()

	wg.Wait()

	if int(closedErrors.Load()) != producers {
		t.Fatalf("producers receiving ErrQueueClosed = %d, want %d", closedErrors.Load(), producers)
	}
}
