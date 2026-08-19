package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsCounterAndGauge(t *testing.T) {
	reg := NewRegistry()
	c := reg.NewCounter("test_counter_total", "Test counter description")
	g := reg.NewGauge("test_gauge", "Test gauge description")

	c.Inc()
	c.Add(4)
	if c.Value() != 5 {
		t.Errorf("Counter value = %d, want 5", c.Value())
	}

	g.Set(42)
	if g.Value() != 42 {
		t.Errorf("Gauge value = %d, want 42", g.Value())
	}
}

func TestMetricsHTTPHandler(t *testing.T) {
	reg := NewRegistry()
	c := reg.NewCounter("etd_test_events_total", "Total events count")
	c.Add(10)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)

	reg.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Handler returned status %d, want 200", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "# HELP etd_test_events_total Total events count") {
		t.Errorf("Missing HELP header in response body:\n%s", body)
	}
	if !strings.Contains(body, "etd_test_events_total 10") {
		t.Errorf("Missing metric value in response body:\n%s", body)
	}
}
