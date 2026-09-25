package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/HuanUnited/edgetelemetrydaemon/internal/cgroup"
	"github.com/HuanUnited/edgetelemetrydaemon/internal/collector"
	"github.com/HuanUnited/edgetelemetrydaemon/internal/config"
	"github.com/HuanUnited/edgetelemetrydaemon/internal/engine"
	"github.com/HuanUnited/edgetelemetrydaemon/internal/filter"
	"github.com/HuanUnited/edgetelemetrydaemon/internal/metrics"
	"github.com/HuanUnited/edgetelemetrydaemon/internal/outbox"
	"github.com/HuanUnited/edgetelemetrydaemon/internal/transport"
)

const (
	defaultHoldoffDuration         = 10 * time.Second
	defaultMinConsecutiveAnomalies = 2
	defaultMinConsecutiveNormals   = 3
	tauRecoveryStep                = 500 * time.Millisecond
)

type AnomalyPayload struct {
	Timestamp   time.Time              `json:"timestamp"`
	CPU         float64                `json:"cpu"`
	Memory      float64                `json:"memory"`
	BaselineCPU float64                `json:"baseline_cpu"`
	BaselineMem float64                `json:"baseline_mem"`
	ZScore      float64                `json:"z_score"`
	Threshold   float64                `json:"threshold"`
	PreContext  []filter.SnapshotEntry `json:"pre_context"`
}

type DriftPayload struct {
	Timestamp  time.Time `json:"timestamp"`
	DriftIndex float64   `json:"drift_index"`
}

type Agent struct {
	cfg                 config.Config
	suppCfg             filter.SuppressorConfig
	ob                  *outbox.Outbox
	reg                 *metrics.Registry
	det                 *engine.ZScoreDetector
	supp                *filter.Suppressor
	ringBuf             *filter.RingBuffer
	hb                  *filter.HeartbeatAggregator
	aiGen               *collector.AIGen
	cgroupCtl           *cgroup.Controller
	prevSuppressorState filter.SuppressorState
	prevDrifting        bool
	lastDriftAlert      time.Time
	prevTick            time.Time
	tauPrev             time.Duration
	injectSpikes        atomic.Int32

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
	suppCfg := filter.SuppressorConfig{
		HoldoffDuration:         defaultHoldoffDuration,
		MinConsecutiveAnomalies: defaultMinConsecutiveAnomalies,
		MinConsecutiveNormals:   defaultMinConsecutiveNormals,
	}
	agent := &Agent{
		cfg:                 cfg,
		suppCfg:             suppCfg,
		ob:                  ob,
		reg:                 reg,
		det:                 engine.NewZScoreDetector(0.1, 0.01, cfg.DetectorMinSamples, 3.0, 0.15),
		supp:                filter.NewSuppressor(suppCfg),
		ringBuf:             filter.NewRingBuffer(20),
		hb:                  filter.NewHeartbeatAggregator(30 * time.Second),
		aiGen:               collector.NewAIGen(collector.DefaultAIGenConfig()),
		cgroupCtl:           cgroup.NewController(cfg.CgroupRoot),
		prevSuppressorState: filter.StateNormal,
		tauPrev:             cfg.RateTauMax,
		metricScrapes:       reg.NewCounter("etd_scrapes_total", "Total telemetry scrape cycles performed"),
		metricAnomalies:     reg.NewCounter("etd_anomalies_detected_total", "Total raw anomalies detected by engine"),
		metricAlerts:        reg.NewCounter("etd_alerts_triggered_total", "Total alerts tripped past deadband suppressor"),
		metricThroughput:    reg.NewGauge("etd_inferences_per_sec", "Current AI inference throughput metric"),
		metricCPU:           reg.NewGauge("etd_cpu_utilization_percent", "Host CPU utilization percentage"),
		metricMem:           reg.NewGauge("etd_mem_used_bytes", "Host used memory in bytes"),
	}

	if err := agent.cgroupCtl.SetQuiescent(); err != nil {
		slog.Warn("failed to initialize quiescent cgroup quota", "error", err)
	}

	return agent
}

// computeInterval employs Effective Z-Score mapping with step-limited recovery to prevent relay chattering.
func computeInterval(zComposite, driftIndex, driftThresh, theta float64, tauMin, tauMax, tauPrev, deltaTau time.Duration) time.Duration {
	if math.IsNaN(zComposite) {
		zComposite = 0
	}
	if math.IsNaN(driftIndex) {
		driftIndex = 0
	}

	zEff := math.Max(zComposite, (driftIndex/driftThresh)*theta)
	ratio := math.Abs(zEff) / theta
	if ratio > 1.0 {
		ratio = 1.0
	}

	tMin := float64(tauMin)
	tMax := float64(tauMax)
	tauTarget := time.Duration(tMax - (tMax-tMin)*ratio)

	tauNext := tauTarget
	// Apply relaxation logic: bound recovery increments by deltaTau to eliminate severe whiplash
	if tauNext > tauPrev+deltaTau {
		tauNext = tauPrev + deltaTau
	}
	if tauNext < tauMin {
		tauNext = tauMin
	}
	return tauNext
}

//nolint:funlen
func (a *Agent) tick(now time.Time) time.Duration {
	a.metricScrapes.Inc()

	elapsedSec := a.cfg.ScrapeInterval.Seconds()
	if !a.prevTick.IsZero() && now.After(a.prevTick) {
		elapsedSec = now.Sub(a.prevTick).Seconds()
	}
	a.prevTick = now

	if err := collector.CollectCPU(a.cfg.ProcfsPath, &a.cpuStats); err == nil {
		if a.hasPrevCPU && a.cpuStats.Total > a.prevCPU.Total {
			deltaTotal := float64(a.cpuStats.Total - a.prevCPU.Total)
			deltaWork := float64((a.cpuStats.User + a.cpuStats.Nice + a.cpuStats.System) - (a.prevCPU.User + a.prevCPU.Nice + a.prevCPU.System))
			switch a.cfg.CPUReportMode {
			case "ticks":
				a.metricCPU.SetFloat64(deltaWork)
			case "hertz":
				if elapsedSec > 0 {
					a.metricCPU.SetFloat64(deltaTotal / elapsedSec)
				}
			default:
				a.metricCPU.SetFloat64((deltaWork / deltaTotal) * 100.0)
			}
		}
		a.prevCPU = a.cpuStats
		a.hasPrevCPU = true
	}

	if err := collector.CollectMem(a.cfg.ProcfsPath, &a.memStats); err == nil && a.memStats.MemTotal >= a.memStats.MemAvailable {
		a.metricMem.Set((a.memStats.MemTotal - a.memStats.MemAvailable) * 1024)
	}

	sample := a.aiGen.Next()
	cpuVal := a.metricCPU.Float64Value()
	memVal := a.metricMem.Float64Value()

	for {
		spikes := a.injectSpikes.Load()
		if spikes <= 0 {
			break
		}
		if a.injectSpikes.CompareAndSwap(spikes, spikes-1) {
			sample.InferencesPerSec *= 50.0
			cpuVal += 90.0 // Synthetic anomaly
			memVal += 1e9  // Synthetic anomaly
			break
		}
	}
	a.metricThroughput.Set(uint64(sample.InferencesPerSec))

	rawAnom := a.det.Update(cpuVal, memVal)
	zScore := a.det.ZScore()
	if rawAnom {
		a.metricAnomalies.Inc()
	}

	isAnom := rawAnom
	if engine.IsSaturated(a.memStats.MemTotal, a.memStats.MemAvailable, 5.0) {
		isAnom = true
	}

	shouldAlert, state := a.supp.Process(isAnom, now)
	if state != a.prevSuppressorState && state == filter.StateAlerting {
		if err := a.cgroupCtl.SetBurst(); err != nil {
			slog.Error("failed to set cgroup burst quota", "error", err)
		}
	} else if state != a.prevSuppressorState && state == filter.StateNormal {
		if err := a.cgroupCtl.SetQuiescent(); err != nil {
			slog.Error("failed to set cgroup quiescent quota", "error", err)
		}
	}
	a.prevSuppressorState = state

	a.ringBuf.Push(filter.SnapshotEntry{Timestamp: now, Value: cpuVal, ZScore: zScore, Anomalous: isAnom})
	a.hb.Observe(sample.InferencesPerSec, isAnom, state == filter.StateSuppressed)

	if shouldAlert {
		a.metricAlerts.Inc()
		var history [20]filter.SnapshotEntry
		payload, err := json.Marshal(AnomalyPayload{
			Timestamp:   now,
			CPU:         cpuVal,
			Memory:      memVal,
			BaselineCPU: a.det.BaselineCPU(),
			BaselineMem: a.det.BaselineMem(),
			ZScore:      zScore,
			Threshold:   a.det.Threshold(),
			PreContext:  a.ringBuf.Snapshot(history[:0]),
		})
		if err == nil {
			_ = a.ob.Push(outbox.Event{ID: fmt.Sprintf("alert-%d", now.UnixNano()), Type: outbox.EventAnomalyAlert, Timestamp: now, Data: payload})
			slog.Warn("anomaly alert triggered", "z_score", zScore, "cpu", cpuVal, "mem", memVal)
		}
	}

	isDrifting := a.det.IsDrifting()
	if isDrifting && (!a.prevDrifting || now.Sub(a.lastDriftAlert) >= a.suppCfg.HoldoffDuration) {
		a.lastDriftAlert = now
		payload, err := json.Marshal(DriftPayload{
			Timestamp:  now,
			DriftIndex: a.det.DriftIndex(),
		})
		if err == nil {
			_ = a.ob.Push(outbox.Event{
				ID:        fmt.Sprintf("drift-%d", now.UnixNano()),
				Type:      outbox.EventDriftAlert,
				Timestamp: now,
				Data:      payload,
			})
			slog.Warn("drift alert triggered", "drift_index", a.det.DriftIndex())
		}
	}
	a.prevDrifting = isDrifting

	if a.hb.ShouldFlush(now) {
		var summary filter.HeartbeatSummary
		a.hb.Flush(now, &summary)
		if hbData, err := json.Marshal(summary); err == nil {
			_ = a.ob.Push(outbox.Event{ID: fmt.Sprintf("hb-%d", now.UnixNano()), Type: outbox.EventHeartbeat, Timestamp: now, Data: hbData})
			slog.Debug("heartbeat flushed", "samples", summary.TotalSamples)
		}
	}

	nextTau := computeInterval(zScore, a.det.DriftIndex(), a.det.DriftThreshold(), a.cfg.RateTheta, a.cfg.RateTauMin, a.cfg.RateTauMax, a.tauPrev, tauRecoveryStep)
	a.tauPrev = nextTau
	return nextTau
}

func newMux(agent *Agent, reg *metrics.Registry, ob *outbox.Outbox) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", reg.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		if ob.Len() >= ob.Capacity() {
			http.Error(w, "queue saturated", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})
	mux.HandleFunc("/livez", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})
	mux.HandleFunc("/inject/anomaly", func(w http.ResponseWriter, _ *http.Request) {
		agent.injectSpikes.Store(3)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("anomaly burst scheduled (3 cycles)"))
	})
	return mux
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		level = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))

	reg := metrics.NewRegistry()
	metricHTTPRequests := reg.NewCounter("etd_http_requests_total", "Total HTTP requests served")
	ob := outbox.NewOutbox(outbox.Config{Capacity: 500, DropPolicy: outbox.DropOldest})
	defer ob.Close()

	agent := newAgent(cfg, ob, reg)
	disp := transport.NewDispatcher(transport.DispatcherConfig{
		TargetURL: cfg.TargetURL, MaxRetries: 3, InitialBackoff: 100 * time.Millisecond, MaxBackoff: 3 * time.Second,
	}, ob, reg)

	mux := newMux(agent, reg, ob)

	httpServer := &http.Server{
		Addr: cfg.ListenAddr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			metricHTTPRequests.Inc()
			mux.ServeHTTP(w, r)
		}),
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      10 * time.Second,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP server error", "error", err)
		}
	}()

	var wg sync.WaitGroup
	wg.Go(func() {
		if err := disp.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("dispatcher exited with error", "error", err)
		}
	})

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
			next := agent.tick(now)
			ticker.Reset(next)
		}
	}
}
