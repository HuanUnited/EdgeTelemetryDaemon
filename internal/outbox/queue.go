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
	// EventDriftAlert identifies telemetry drift divergence alert events.
	EventDriftAlert EventType = "drift_alert"
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

// Config configures bounded queue capacity and overflow behavior.
type Config struct {
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

	closedCh chan struct{}
	sem      chan struct{}

	enqueued uint64
	dequeued uint64
	dropped  uint64
}

// NewOutbox builds an Outbox with the given configuration.
func NewOutbox(cfg Config) *Outbox {
	if cfg.Capacity <= 0 {
		cfg.Capacity = 100
	}
	return &Outbox{
		items:    make([]Event, cfg.Capacity),
		capacity: cfg.Capacity,
		policy:   cfg.DropPolicy,
		closedCh: make(chan struct{}),
		sem:      make(chan struct{}, cfg.Capacity),
	}
}

// Push queues an event according to the configured DropPolicy.
func (o *Outbox) Push(evt Event) error {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return ErrQueueClosed
	}

	needsToken := o.count < o.capacity

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

	if needsToken {
		select {
		case o.sem <- struct{}{}:
		default:
		}
	}
	o.mu.Unlock()

	return nil
}

// Pop dequeues the next event, blocking until an event is ready or ctx is canceled.
func (o *Outbox) Pop(ctx context.Context) (Event, error) {
	if err := ctx.Err(); err != nil {
		return Event{}, err
	}

	// Fast path: if items are already present, dequeue immediately with zero allocations.
	o.mu.Lock()
	if o.count > 0 {
		evt := o.items[o.head]
		o.items[o.head] = Event{} // Clear reference to allow GC
		o.head = (o.head + 1) % o.capacity
		o.count--
		o.dequeued++

		// Opportunistically consume a semaphore token to keep it synchronized
		select {
		case <-o.sem:
		default:
		}

		o.mu.Unlock()
		return evt, nil
	}
	if o.closed {
		o.mu.Unlock()
		return Event{}, ErrQueueClosed
	}
	o.mu.Unlock()

	// Slow path: allocation-free pure channel select
	for {
		select {
		case <-ctx.Done():
			return Event{}, ctx.Err()
		case <-o.closedCh:
			o.mu.Lock()
			if o.count > 0 {
				evt := o.items[o.head]
				o.items[o.head] = Event{}
				o.head = (o.head + 1) % o.capacity
				o.count--
				o.dequeued++
				select {
				case <-o.sem:
				default:
				}
				o.mu.Unlock()
				return evt, nil
			}
			o.mu.Unlock()
			return Event{}, ErrQueueClosed
		case <-o.sem:
			o.mu.Lock()
			if o.count > 0 {
				evt := o.items[o.head]
				o.items[o.head] = Event{}
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
func (o *Outbox) Stats() (uint64, uint64, uint64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.enqueued, o.dequeued, o.dropped
}

// Close shuts down the outbox and unblocks waiting Pop calls.
func (o *Outbox) Close() {
	o.mu.Lock()
	if !o.closed {
		o.closed = true
		close(o.closedCh)
	}
	o.mu.Unlock()
}
