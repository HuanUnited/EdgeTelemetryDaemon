package metrics

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"sync"
	"sync/atomic"
)

// MetricType defines Prometheus metric instrumentation types.
type MetricType string

const (
	TypeCounter MetricType = "counter"
	TypeGauge   MetricType = "gauge"
)

// Metric holds atomic numeric state for prometheus metrics exposition.
type Metric struct {
	Name    string
	Help    string
	Type    MetricType
	valBits uint64 // float64 or uint64 bits managed atomically
}

// Inc increments a counter or gauge metric by 1.
func (m *Metric) Inc() {
	atomic.AddUint64(&m.valBits, 1)
}

// Add adds v to the counter or gauge metric.
func (m *Metric) Add(v uint64) {
	atomic.AddUint64(&m.valBits, v)
}

// Set sets the value of a gauge metric.
func (m *Metric) Set(v uint64) {
	atomic.StoreUint64(&m.valBits, v)
}

func (m *Metric) SetFloat64(v float64) {
	atomic.StoreUint64(&m.valBits, math.Float64bits(v))
}

func (m *Metric) Float64Value() float64 {
	return math.Float64frombits(atomic.LoadUint64(&m.valBits))
}

// Value returns the current value of the metric.
func (m *Metric) Value() uint64 {
	return atomic.LoadUint64(&m.valBits)
}

// Registry collects and formats metrics into Prometheus text format.
type Registry struct {
	mu      sync.RWMutex
	metrics []*Metric
}

// NewRegistry allocates a fresh Prometheus metrics registry.
func NewRegistry() *Registry {
	return &Registry{
		metrics: make([]*Metric, 0, 16),
	}
}

// NewCounter registers and returns a new Prometheus counter.
func (r *Registry) NewCounter(name, help string) *Metric {
	m := &Metric{Name: name, Help: help, Type: TypeCounter}
	r.mu.Lock()
	r.metrics = append(r.metrics, m)
	r.mu.Unlock()
	return m
}

// NewGauge registers and returns a new Prometheus gauge.
func (r *Registry) NewGauge(name, help string) *Metric {
	m := &Metric{Name: name, Help: help, Type: TypeGauge}
	r.mu.Lock()
	r.metrics = append(r.metrics, m)
	r.mu.Unlock()
	return m
}

// WriteText serializes registered metrics into standard Prometheus text format.
func (r *Registry) WriteText(w io.Writer) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, m := range r.metrics {
		if m.Type == TypeGauge {
			if _, err := fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n%s %f\n",
				m.Name, m.Help, m.Name, m.Type, m.Name, m.Float64Value()); err != nil {
				return err
			}
		} else {
			if _, err := fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n%s %d\n",
				m.Name, m.Help, m.Name, m.Type, m.Name, m.Value()); err != nil {
				return err
			}
		}
	}
	return nil
}

// Handler returns an http.HandlerFunc that serves /metrics endpoints.
func (r *Registry) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		if err := r.WriteText(w); err != nil {
			http.Error(w, "metrics export error", http.StatusInternalServerError)
		}
	}
}
