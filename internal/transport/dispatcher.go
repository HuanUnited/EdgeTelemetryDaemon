package transport

import (
	"bytes"
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/HuanUnited/edgetelemetrydaemon/internal/metrics"
	"github.com/HuanUnited/edgetelemetrydaemon/internal/outbox"
)

// DispatcherConfig configures HTTP endpoint telemetry delivery retries and backoff.
type DispatcherConfig struct {
	TargetURL      string
	Timeout        time.Duration
	MaxRetries     int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	UserAgent      string
}

// Dispatcher consumes events from an Outbox and pushes them via HTTP/JSON to a remote endpoint.
type Dispatcher struct {
	cfg    DispatcherConfig
	client *http.Client
	outbox *outbox.Outbox
	rng    *rand.Rand

	metricDispatched *metrics.Metric
	metricFailures   *metrics.Metric
	metricRetries    *metrics.Metric
}

// NewDispatcher builds a Dispatcher instance.
func NewDispatcher(cfg DispatcherConfig, ob *outbox.Outbox, reg *metrics.Registry) *Dispatcher {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 3
	}
	if cfg.InitialBackoff <= 0 {
		cfg.InitialBackoff = 50 * time.Millisecond
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 2 * time.Second
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "EdgeTelemetryDaemon/1.0"
	}

	seq := uint64(time.Now().UnixNano())
	d := &Dispatcher{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.Timeout},
		outbox: ob,
		rng:    rand.New(rand.NewPCG(seq, ^seq)),
	}

	if reg != nil {
		d.metricDispatched = reg.NewCounter("etd_alerts_dispatched_total", "Total telemetry alerts successfully sent")
		d.metricFailures = reg.NewCounter("etd_dispatch_failures_total", "Total failed telemetry alert transmissions")
		d.metricRetries = reg.NewCounter("etd_dispatch_retries_total", "Total transmission retries executed")
	}

	return d
}

// Run starts consuming events from the outbox until ctx is canceled.
func (d *Dispatcher) Run(ctx context.Context) error {
	for {
		evt, err := d.outbox.Pop(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}

		if err := d.dispatchWithRetry(ctx, evt); err != nil {
			if d.metricFailures != nil {
				d.metricFailures.Inc()
			}
		} else {
			if d.metricDispatched != nil {
				d.metricDispatched.Inc()
			}
		}
	}
}

func (d *Dispatcher) dispatchWithRetry(ctx context.Context, evt outbox.Event) error {
	var lastErr error
	for attempt := 0; attempt <= d.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			if d.metricRetries != nil {
				d.metricRetries.Inc()
			}
			backoff := float64(d.cfg.InitialBackoff) * float64(uint64(1)<<uint(attempt-1))
			if backoff > float64(d.cfg.MaxBackoff) {
				backoff = float64(d.cfg.MaxBackoff)
			}

			// Apply ±25% randomized jitter via math/rand/v2
			jitter := (d.rng.Float64()*0.5 - 0.25) * backoff
			sleepDuration := time.Duration(backoff + jitter)

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(sleepDuration):
			}
		}

		err := d.postEvent(ctx, evt)
		if err == nil {
			return nil
		}
		lastErr = err
	}
	return fmt.Errorf("dispatcher: max retries reached: %w", lastErr)
}

func (d *Dispatcher) postEvent(ctx context.Context, evt outbox.Event) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.cfg.TargetURL, bytes.NewReader(evt.Data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", d.cfg.UserAgent)
	req.Header.Set("X-Event-ID", evt.ID)

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
}
