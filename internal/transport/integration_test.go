package transport

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuanUnited/edgetelemetrydaemon/internal/metrics"
	"github.com/HuanUnited/edgetelemetrydaemon/internal/outbox"
)

func TestPhase4EndToEndIntegration(t *testing.T) {
	var receivedCount atomic.Int32
	mockIngress := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "invalid method", http.StatusMethodNotAllowed)
			return
		}
		receivedCount.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer mockIngress.Close()

	reg := metrics.NewRegistry()
	ob := outbox.NewOutbox(outbox.Config{Capacity: 50, DropPolicy: outbox.DropOldest})
	defer ob.Close()

	disp := NewDispatcher(DispatcherConfig{
		TargetURL:      mockIngress.URL,
		MaxRetries:     2,
		InitialBackoff: 5 * time.Millisecond,
		MaxBackoff:     20 * time.Millisecond,
	}, ob, reg)

	ctx := t.Context()

	go func() {
		_ = disp.Run(ctx)
	}()

	// Push 5 events into Outbox
	for i := 1; i <= 5; i++ {
		err := ob.Push(outbox.Event{
			ID:        "integration-evt",
			Type:      outbox.EventAnomalyAlert,
			Timestamp: time.Now(),
			Data:      []byte(`{"anomaly":true}`),
		})
		if err != nil {
			t.Fatalf("Failed to push event %d: %v", i, err)
		}
	}

	time.Sleep(100 * time.Millisecond)

	// Verify outbox emptied
	if ob.Len() != 0 {
		t.Errorf("Outbox Len = %d, want 0 after dispatch", ob.Len())
	}

	// Scrape Prometheus HTTP /metrics handler
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	reg.Handler().ServeHTTP(rec, req)

	res := rec.Result()
	bodyBytes, _ := io.ReadAll(res.Body)
	body := string(bodyBytes)

	if !strings.Contains(body, "etd_alerts_dispatched_total 5") {
		t.Errorf("Prometheus metrics missing expected dispatch count:\n%s", body)
	}
	if count := receivedCount.Load(); count != 5 {
		t.Errorf("Incorrect number of received events: got %d, want 5", count)
	}
}
