package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/HuanUnited/edgetelemetrydaemon/internal/collector"
	"github.com/HuanUnited/edgetelemetrydaemon/internal/config"
	"github.com/HuanUnited/edgetelemetrydaemon/internal/engine"
	"github.com/HuanUnited/edgetelemetrydaemon/internal/filter"
	"github.com/HuanUnited/edgetelemetrydaemon/internal/metrics"
	"github.com/HuanUnited/edgetelemetrydaemon/internal/outbox"
	"github.com/HuanUnited/edgetelemetrydaemon/internal/transport"
)

type AnomalyPayload struct {
	Timestamp     time.Time              `json:"timestamp"`
	InferencesSec float64                `json:"inferences_per_sec"`
	Baseline      float64                `json:"baseline"`
	ZScore        float64                `json:"z_score"`
	Threshold     float64                `json:"threshold"`
	PreContext    []filter.SnapshotEntry `json:"pre_context"`
}

type Agent struct {
	cfg          config.Config
	ob           *outbox.Outbox
	reg          *metrics.Registry
	det          *engine.ZScoreDetector
	supp         *filter.Suppressor
	ringBuf      *filter.RingBuffer
	hb           *filter.HeartbeatAggregator
	aiGen        *collector.AIGen
	injectSpikes atomic.Int32

	metricScrapes    *metrics.Metric
	metricAnomalies  *metrics.Metric
	metricAlerts     *metrics.Metric
	metricThroughput *metrics.Metric
	metricCPU        *metrics.Metric
	metricMem        *metrics.Metric

	cpuStats   collector.CPUStats
	prevCPU    collector.CPUStats
	hasPrevCPU bool
	memStats   collector.MemStats
}

func newAgent(cfg config.Config, ob *outbox.Outbox, reg *metrics.Registry) *Agent {
	return &Agent{
		cfg:              cfg,
		ob:               ob,
		reg:              reg,
		det:              engine.NewZScoreDetector(0.1, cfg.DetectorMinSamples, 3.0),
		supp:             filter.NewSuppressor(filter.SuppressorConfig{HoldoffDuration: 10 * time.Second, MinConsecutiveAnomalies: 2, MinConsecutiveNormals: 3}),
		ringBuf:          filter.NewRingBuffer(20),
		hb:               filter.NewHeartbeatAggregator(30 * time.Second),
		aiGen:            collector.NewAIGen(collector.DefaultAIGenConfig()),
		metricScrapes:    reg.NewCounter("etd_scrapes_total", "Total telemetry scrape cycles performed"),
		metricAnomalies:  reg.NewCounter("etd_anomalies_detected_total", "Total raw anomalies detected by engine"),
		metricAlerts:     reg.NewCounter("etd_alerts_triggered_total", "Total alerts tripped past deadband suppressor"),
		metricThroughput: reg.NewGauge("etd_inferences_per_sec", "Current AI inference throughput metric"),
		metricCPU:        reg.NewGauge("etd_cpu_utilization_percent", "Host CPU utilization percentage"),
		metricMem:        reg.NewGauge("etd_mem_used_bytes", "Host used memory in bytes"),
	}
}

func (a *Agent) tick(now time.Time) {
	a.metricScrapes.Inc()

	if err := collector.CollectCPU(a.cfg.ProcfsPath, &a.cpuStats); err == nil {
		if a.hasPrevCPU && a.cpuStats.Total > a.prevCPU.Total {
			deltaTotal := float64(a.cpuStats.Total - a.prevCPU.Total)
			deltaWork := float64((a.cpuStats.User + a.cpuStats.Nice + a.cpuStats.System) - (a.prevCPU.User + a.prevCPU.Nice + a.prevCPU.System))
			a.metricCPU.SetFloat64((deltaWork / deltaTotal) * 100.0)
		}
		a.prevCPU = a.cpuStats
		a.hasPrevCPU = true
	}

	if err := collector.CollectMem(a.cfg.ProcfsPath, &a.memStats); err == nil && a.memStats.MemTotal >= a.memStats.MemAvailable {
		a.metricMem.Set((a.memStats.MemTotal - a.memStats.MemAvailable) * 1024)
	}

	sample := a.aiGen.Next()
	if a.injectSpikes.Load() > 0 {
		sample.InferencesPerSec *= 50.0
		a.injectSpikes.Add(-1)
	}
	a.metricThroughput.Set(uint64(sample.InferencesPerSec))

	rawAnom := a.det.Update(sample.InferencesPerSec)
	zScore := a.det.ZScore()
	if rawAnom {
		a.metricAnomalies.Inc()
	}

	shouldAlert, state := a.supp.Process(rawAnom, now)
	a.ringBuf.Push(filter.SnapshotEntry{Timestamp: now, Value: sample.InferencesPerSec, ZScore: zScore, Anomalous: rawAnom})
	a.hb.Observe(sample.InferencesPerSec, rawAnom, state == filter.StateSuppressed)

	if shouldAlert {
		a.metricAlerts.Inc()
		var history [20]filter.SnapshotEntry
		payload, err := json.Marshal(AnomalyPayload{
			Timestamp:     now,
			InferencesSec: sample.InferencesPerSec,
			Baseline:      a.det.Baseline(),
			ZScore:        zScore,
			Threshold:     a.det.Threshold(),
			PreContext:    a.ringBuf.Snapshot(history[:0]),
		})
		if err == nil {
			_ = a.ob.Push(outbox.Event{ID: fmt.Sprintf("alert-%d", now.UnixNano()), Type: outbox.EventAnomalyAlert, Timestamp: now, Data: payload})
			slog.Warn("anomaly alert triggered", "z_score", zScore, "throughput", sample.InferencesPerSec)
		}
	}

	if a.hb.ShouldFlush(now) {
		var summary filter.HeartbeatSummary
		a.hb.Flush(now, &summary)
		if hbData, err := json.Marshal(summary); err == nil {
			_ = a.ob.Push(outbox.Event{ID: fmt.Sprintf("hb-%d", now.UnixNano()), Type: outbox.EventHeartbeat, Timestamp: now, Data: hbData})
			slog.Debug("heartbeat flushed", "samples", summary.TotalSamples)
		}
	}
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		_, err2 := fmt.Fprintf(os.Stderr, "failed to load configuration: %v\n", err)
		if err2 != nil {
			return
		}
		os.Exit(1)
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		level = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))

	reg := metrics.NewRegistry()
	metricHTTPRequests := reg.NewCounter("etd_http_requests_total", "Total HTTP requests served")
	ob := outbox.NewOutbox(outbox.OutboxConfig{Capacity: 500, DropPolicy: outbox.DropOldest})
	defer ob.Close()

	agent := newAgent(cfg, ob, reg)
	disp := transport.NewDispatcher(transport.DispatcherConfig{
		TargetURL: cfg.TargetURL, MaxRetries: 3, InitialBackoff: 100 * time.Millisecond, MaxBackoff: 3 * time.Second,
	}, ob, reg)

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", reg.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if ob.Len() >= ob.Capacity() {
			http.Error(w, "queue saturated", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})
	mux.HandleFunc("/livez", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})
	mux.HandleFunc("/inject/anomaly", func(w http.ResponseWriter, r *http.Request) {
		agent.injectSpikes.Store(3)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("anomaly burst scheduled (3 cycles)"))
	})

	httpServer := &http.Server{
		Addr: cfg.ListenAddr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			metricHTTPRequests.Inc()
			mux.ServeHTTP(w, r)
		}),
		ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP server error", "error", err)
		}
	}()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := disp.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("dispatcher exited with error", "error", err)
		}
	}()

	ticker := time.NewTicker(cfg.ScrapeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = httpServer.Shutdown(shutdownCtx)
			shutdownCancel()
			wg.Wait()
			return
		case now := <-ticker.C:
			agent.tick(now)
		}
	}
}
