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

func TestGaugeSetNoCorruption(t *testing.T) {
	reg := NewRegistry()
	g := reg.NewGauge("test_gauge_corruption", "Test gauge corruption description")

	g.Set(42)
	if got := g.Float64Value(); got != 42.0 {
		t.Fatalf("Float64Value() = %v, want 42.0", got)
	}

	g.Set(100)
	if got := g.Float64Value(); got != 100.0 {
		t.Fatalf("Float64Value() = %v, want 100.0", got)
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

func TestMetricGaugeIncAndCounterSetBehavior(t *testing.T) {
	reg := NewRegistry()
	g := reg.NewGauge("test_gauge_inc", "Gauge Inc test")
	c := reg.NewCounter("test_counter_set", "Counter Set test")

	g.Set(10)
	g.Inc()
	if got := g.Float64Value(); got != 11.0 {
		t.Fatalf("Gauge Inc resulted in %v, want 11.0", got)
	}

	c.Set(50)
	if got := c.Value(); got != 50 {
		t.Fatalf("Counter Set resulted in %v, want 50", got)
	}
	c.Set(25)
	if got := c.Value(); got != 25 {
		t.Fatalf("Counter second Set resulted in %v, want 25", got)
	}

	if got := c.Float64Value(); got != 25.0 {
		t.Fatalf("Counter Float64Value resulted in %v, want 25.0", got)
	}
}
