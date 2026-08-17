package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuanUnited/edgetelemetrydaemon/internal/metrics"
	"github.com/HuanUnited/edgetelemetrydaemon/internal/outbox"
)

func TestDispatcherSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	ob := outbox.NewOutbox(outbox.OutboxConfig{Capacity: 10})
	reg := metrics.NewRegistry()
	disp := NewDispatcher(DispatcherConfig{
		TargetURL:      ts.URL,
		MaxRetries:     1,
		InitialBackoff: 5 * time.Millisecond,
	}, ob, reg)

	_ = ob.Push(outbox.Event{ID: "evt1", Data: []byte(`{"status":"ok"}`)})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	go func() {
		_ = disp.Run(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	if ob.Len() != 0 {
		t.Errorf("Expected event to be consumed, Len = %d", ob.Len())
	}
}

func TestDispatcherRetryAndRecovery(t *testing.T) {
	var attempts int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&attempts, 1)
		if count < 3 {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	ob := outbox.NewOutbox(outbox.OutboxConfig{Capacity: 5})
	reg := metrics.NewRegistry()
	disp := NewDispatcher(DispatcherConfig{
		TargetURL:      ts.URL,
		MaxRetries:     3,
		InitialBackoff: 2 * time.Millisecond,
	}, ob, reg)

	_ = ob.Push(outbox.Event{ID: "retry-evt", Data: []byte(`{"alert":true}`)})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	go func() {
		_ = disp.Run(ctx)
	}()

	time.Sleep(100 * time.Millisecond)
	if atomic.LoadInt32(&attempts) != 3 {
		t.Errorf("Expected 3 HTTP attempts (2 retries + 1 success), got %d", atomic.LoadInt32(&attempts))
	}
}
