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
	valBits atomic.Uint64
}

// Inc increments a counter or gauge metric by 1.
func (m *Metric) Inc() {
	m.valBits.Add(1)
}

// Add adds v to the counter or gauge metric.
func (m *Metric) Add(v uint64) {
	// Float bit handling in CAS loop
	if m.Type == TypeGauge {
		for {
			oldBits := m.valBits.Load()
			oldVal := math.Float64frombits(oldBits)
			newVal := oldVal + float64(v)
			newBits := math.Float64bits(newVal)

			if m.valBits.CompareAndSwap(oldBits, newBits) {
				return
			}
		}
	}
	m.valBits.Add(v)
}

// Set sets the value of a gauge metric.
func (m *Metric) Set(v uint64) {
	if m.Type == TypeGauge {
		m.valBits.Store(math.Float64bits(float64(v)))
		return
	}
	m.valBits.Add(v)
}

func (m *Metric) SetFloat64(v float64) {
	m.valBits.Store(math.Float64bits(v))
}

func (m *Metric) Float64Value() float64 {
	bits := m.valBits.Load()
	if m.Type == TypeGauge {
		return math.Float64frombits(bits)
	}
	return math.Float64frombits(m.valBits.Load())
}

// Value returns the current value of the metric.
func (m *Metric) Value() uint64 {
	if m.Type == TypeGauge {
		return uint64(m.Float64Value())
	}
	return m.valBits.Load()
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
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		if err := r.WriteText(w); err != nil {
			http.Error(w, "metrics export error", http.StatusInternalServerError)
		}
	}
}
