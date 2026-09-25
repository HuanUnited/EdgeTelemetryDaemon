// cmd/agent/main.go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
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

	subsMu sync.RWMutex
	subs   map[chan outbox.Event]struct{}

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

	var aiGen *collector.AIGen
	if cfg.EnableSyntheticWorkload {
		aiGen = collector.NewAIGen(collector.DefaultAIGenConfig())
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
		aiGen:               aiGen,
		cgroupCtl:           cgroup.NewController(cfg.CgroupRoot),
		prevSuppressorState: filter.StateNormal,
		tauPrev:             cfg.RateTauMax,
		subs:                make(map[chan outbox.Event]struct{}),
		metricScrapes:       reg.NewCounter("etd_scrapes_total", "Total telemetry scrape cycles performed"),
		metricAnomalies:     reg.NewCounter("etd_anomalies_detected_total", "Total raw anomalies detected by engine"),
		metricAlerts:        reg.NewCounter("etd_alerts_triggered_total", "Total alerts tripped past deadband suppressor"),
		metricThroughput:    reg.NewGauge("etd_inferences_per_sec", "Current AI inference throughput metric"),
		metricCPU:           reg.NewGauge("etd_cpu_utilization_percent", "Host CPU utilization percentage"),
		metricMem:           reg.NewGauge("etd_mem_utilization_percent", "Host memory utilization percentage"),
	}

	if err := agent.cgroupCtl.SetQuiescent(); err != nil {
		slog.Warn("failed to initialize quiescent cgroup quota", "error", err)
	}

	return agent
}

func (a *Agent) subscribe() chan outbox.Event {
	ch := make(chan outbox.Event, 64)
	a.subsMu.Lock()
	a.subs[ch] = struct{}{}
	a.subsMu.Unlock()
	return ch
}

func (a *Agent) unsubscribe(ch chan outbox.Event) {
	a.subsMu.Lock()
	if _, ok := a.subs[ch]; ok {
		delete(a.subs, ch)
		close(ch)
	}
	a.subsMu.Unlock()
}

func (a *Agent) broadcast(evt outbox.Event) {
	a.subsMu.RLock()
	defer a.subsMu.RUnlock()
	for ch := range a.subs {
		select {
		case ch <- evt:
		default:
			// Drop event if subscriber is too slow
		}
	}
}

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
	if tauNext > tauPrev+deltaTau {
		tauNext = tauPrev + deltaTau
	}
	if tauNext < tauMin {
		tauNext = tauMin
	}
	return tauNext
}

func (a *Agent) tick(now time.Time) time.Duration {
	a.metricScrapes.Inc()
	a.prevTick = now

	var cpuPct, memPct float64

	if err := collector.CollectCPU(a.cfg.ProcfsPath, &a.cpuStats); err == nil {
		if a.hasPrevCPU && a.cpuStats.Total > a.prevCPU.Total {
			deltaTotal := float64(a.cpuStats.Total - a.prevCPU.Total)
			deltaWork := float64((a.cpuStats.User + a.cpuStats.Nice + a.cpuStats.System) - (a.prevCPU.User + a.prevCPU.Nice + a.prevCPU.System))
			cpuPct = (deltaWork / deltaTotal) * 100.0
			a.metricCPU.SetFloat64(cpuPct)
		} else {
			cpuPct = a.metricCPU.Float64Value()
		}
		a.prevCPU = a.cpuStats
		a.hasPrevCPU = true
	} else {
		cpuPct = a.metricCPU.Float64Value()
	}

	if err := collector.CollectMem(a.cfg.ProcfsPath, &a.memStats); err == nil && a.memStats.MemTotal > 0 && a.memStats.MemTotal >= a.memStats.MemAvailable {
		memPct = (1.0 - float64(a.memStats.MemAvailable)/float64(a.memStats.MemTotal)) * 100.0
		a.metricMem.SetFloat64(memPct)
	} else {
		memPct = a.metricMem.Float64Value()
	}

	var inferencesPerSec float64
	if a.aiGen != nil {
		sample := a.aiGen.Next()
		inferencesPerSec = sample.InferencesPerSec
	}

	for {
		spikes := a.injectSpikes.Load()
		if spikes <= 0 {
			break
		}
		if a.injectSpikes.CompareAndSwap(spikes, spikes-1) {
			if inferencesPerSec == 0 {
				inferencesPerSec = 500.0
			} else {
				inferencesPerSec *= 50.0
			}
			cpuPct += 90.0
			memPct += 90.0
			break
		}
	}
	a.metricThroughput.Set(uint64(inferencesPerSec))

	rawAnom := a.det.Update(cpuPct, memPct)
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

	a.ringBuf.Push(filter.SnapshotEntry{Timestamp: now, Value: cpuPct, ZScore: zScore, Anomalous: isAnom})
	a.hb.Observe(inferencesPerSec, isAnom, state == filter.StateSuppressed)

	if shouldAlert {
		a.metricAlerts.Inc()
		var history [20]filter.SnapshotEntry
		payload, err := json.Marshal(AnomalyPayload{
			Timestamp:   now,
			CPU:         cpuPct,
			Memory:      memPct,
			BaselineCPU: a.det.BaselineCPU(),
			BaselineMem: a.det.BaselineMem(),
			ZScore:      zScore,
			Threshold:   a.det.Threshold(),
			PreContext:  a.ringBuf.Snapshot(history[:0]),
		})
		if err == nil {
			evt := outbox.Event{ID: fmt.Sprintf("alert-%d", now.UnixNano()), Type: outbox.EventAnomalyAlert, Timestamp: now, Data: payload}
			_ = a.ob.Push(evt)
			a.broadcast(evt)
			slog.Warn("anomaly alert triggered", "z_score", zScore, "cpu_pct", cpuPct, "mem_pct", memPct)
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
			evt := outbox.Event{
				ID:        fmt.Sprintf("drift-%d", now.UnixNano()),
				Type:      outbox.EventDriftAlert,
				Timestamp: now,
				Data:      payload,
			}
			_ = a.ob.Push(evt)
			a.broadcast(evt)
			slog.Warn("drift alert triggered", "drift_index", a.det.DriftIndex())
		}
	}
	a.prevDrifting = isDrifting

	if a.hb.ShouldFlush(now) {
		var summary filter.HeartbeatSummary
		a.hb.Flush(now, &summary)
		if hbData, err := json.Marshal(summary); err == nil {
			evt := outbox.Event{ID: fmt.Sprintf("hb-%d", now.UnixNano()), Type: outbox.EventHeartbeat, Timestamp: now, Data: hbData}
			_ = a.ob.Push(evt)
			a.broadcast(evt)
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

	// Server-Sent Events (SSE) Live Stream
	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		ch := agent.subscribe()
		defer agent.unsubscribe(ch)

		for {
			select {
			case <-r.Context().Done():
				return
			case evt, ok := <-ch:
				if !ok {
					return
				}
				data, err := json.Marshal(evt)
				if err == nil {
					_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
					flusher.Flush()
				}
			}
		}
	})
	return mux
}

// startUDSServer runs a non-blocking IPC Unix Domain Socket server.
func startUDSServer(ctx context.Context, agent *Agent, socketPath string) error {
	_ = os.Remove(socketPath)
	l, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	_ = os.Chmod(socketPath, 0666)

	go func() {
		<-ctx.Done()
		_ = l.Close()
	}()

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				slog.Error("UDS accept error", "error", err)
				continue
			}
			go handleUDSConnection(ctx, agent, conn)
		}
	}()
	return nil
}

func handleUDSConnection(ctx context.Context, agent *Agent, conn net.Conn) {
	defer func(conn net.Conn) {
		_ = conn.Close()
	}(conn)
	ch := agent.subscribe()
	defer agent.unsubscribe(ch)

	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(evt)
			if err == nil {
				data = append(data, '\n')
				// Protect main loop by dropping slow clients
				_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
				if _, err := conn.Write(data); err != nil {
					return
				}
			}
		}
	}
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

	if err := startUDSServer(ctx, agent, cfg.SocketPath); err != nil {
		slog.Error("failed to start UDS server", "error", err)
	}

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP server error", "error", err)
		}
	}()

	var wg sync.WaitGroup
	if cfg.TargetURL != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := disp.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Error("dispatcher exited with error", "error", err)
			}
		}()
	}

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
