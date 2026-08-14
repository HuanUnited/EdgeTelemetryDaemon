package outbox

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	// ErrQueueFull indicates the queue capacity was exceeded under DropNewest policy.
	ErrQueueFull = errors.New("outbox: queue capacity reached")
	// ErrQueueClosed indicates operations were attempted on a closed outbox.
	ErrQueueClosed = errors.New("outbox: queue is closed")
)

// EventType classifies outbox payload topics.
type EventType string

const (
	// EventAnomalyAlert identifies telemetry anomaly alert events.
	EventAnomalyAlert EventType = "anomaly_alert"
	// EventHeartbeat identifies periodic heartbeat metrics summaries.
	EventHeartbeat EventType = "heartbeat"
)

// Event represents a telemetry payload queued for outbound transmission.
type Event struct {
	ID        string    `json:"id"`
	Type      EventType `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Data      []byte    `json:"data"`
}

// DropPolicy defines queue overflow behavior when full.
type DropPolicy uint8

const (
	// DropNewest drops incoming events when the queue is at capacity.
	DropNewest DropPolicy = iota
	// DropOldest evicts the oldest queued event to make room for new ones.
	DropOldest
)

// OutboxConfig configures bounded queue capacity and overflow behavior.
type OutboxConfig struct {
	Capacity   int
	DropPolicy DropPolicy
}

// Outbox provides a bounded, thread-safe, context-aware queue for outbound payloads.
type Outbox struct {
	mu       sync.Mutex
	items    []Event
	head     int
	tail     int
	count    int
	capacity int
	policy   DropPolicy
	closed   bool
	notify   chan struct{}

	enqueued uint64
	dequeued uint64
	dropped  uint64
}

// NewOutbox builds an Outbox with the given configuration.
func NewOutbox(cfg OutboxConfig) *Outbox {
	if cfg.Capacity <= 0 {
		cfg.Capacity = 100
	}
	return &Outbox{
		items:    make([]Event, cfg.Capacity),
		capacity: cfg.Capacity,
		policy:   cfg.DropPolicy,
		notify:   make(chan struct{}, 1),
	}
}

// Push queues an event according to the configured DropPolicy.
func (o *Outbox) Push(evt Event) error {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return ErrQueueClosed
	}

	if o.count == o.capacity {
		o.dropped++
		if o.policy == DropNewest {
			o.mu.Unlock()
			return ErrQueueFull
		}

		// DropOldest: advance head to evict oldest item
		o.items[o.head] = Event{}
		o.head = (o.head + 1) % o.capacity
		o.count--
	}

	o.items[o.tail] = evt
	o.tail = (o.tail + 1) % o.capacity
	o.count++
	o.enqueued++

	o.mu.Unlock()

	// Signal waiting Pop goroutines without blocking
	select {
	case o.notify <- struct{}{}:
	default:
	}

	return nil
}

// Pop dequeues the next event, blocking until an event is ready or ctx is canceled.
func (o *Outbox) Pop(ctx context.Context) (Event, error) {
	for {
		o.mu.Lock()
		if o.count > 0 {
			evt := o.items[o.head]
			o.items[o.head] = Event{} // Clear reference to allow GC
			o.head = (o.head + 1) % o.capacity
			o.count--
			o.dequeued++
			o.mu.Unlock()
			return evt, nil
		}

		if o.closed {
			o.mu.Unlock()
			return Event{}, ErrQueueClosed
		}
		o.mu.Unlock()

		select {
		case <-ctx.Done():
			return Event{}, ctx.Err()
		case <-o.notify:
		}
	}
}

// Len returns the current number of queued events.
func (o *Outbox) Len() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.count
}

// Capacity returns the maximum capacity of the queue.
func (o *Outbox) Capacity() int {
	return o.capacity
}

// Stats returns cumulative counts of enqueued, dequeued, and dropped events.
func (o *Outbox) Stats() (enqueued, dequeued, dropped uint64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.enqueued, o.dequeued, o.dropped
}

// Close shuts down the outbox and unblocks waiting Pop calls.
func (o *Outbox) Close() {
	o.mu.Lock()
	if !o.closed {
		o.closed = true
		close(o.notify)
	}
	o.mu.Unlock()
}
