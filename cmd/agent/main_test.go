package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuanUnited/edgetelemetrydaemon/internal/cgroup"
	"github.com/HuanUnited/edgetelemetrydaemon/internal/config"
	"github.com/HuanUnited/edgetelemetrydaemon/internal/metrics"
	"github.com/HuanUnited/edgetelemetrydaemon/internal/outbox"
)

func TestAdaptiveLoopIntervalScaling(t *testing.T) {
	tauMin := 50 * time.Millisecond
	tauMax := 5 * time.Second
	theta := 3.5
	deltaTau := 500 * time.Millisecond

	tests := []struct {
		name       string
		zComposite float64
		driftIndex float64
		tauPrev    time.Duration
		want       time.Duration
	}{
		{"zero", 0.0, 0.0, tauMax, 5000 * time.Millisecond},
		{"midpoint", 1.75, 0.0, tauMax, 2525 * time.Millisecond},
		{"theta_boundary", 3.5, 0.0, tauMax, 50 * time.Millisecond},
		{"clamped_extreme", 10.0, 0.0, tauMax, 50 * time.Millisecond},

		{"drift_override", 0.0, 0.30, tauMax, 50 * time.Millisecond},
		{"drift_midpoint", 0.0, 0.075, tauMax, 2525 * time.Millisecond},

		{"fast_recovery_clamped", 0.0, 0.0, 50 * time.Millisecond, 550 * time.Millisecond},
		{"partial_recovery_clamped", 1.0, 0.0, 50 * time.Millisecond, 550 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeInterval(tt.zComposite, tt.driftIndex, 0.15, theta, tauMin, tauMax, tt.tauPrev, deltaTau)
			if got != tt.want {
				t.Fatalf("computeInterval(%v, drift=%v, prev=%v) = %v, want %v",
					tt.zComposite, tt.driftIndex, tt.tauPrev, got, tt.want)
			}
		})
	}
}

func TestEndToEndQuotaRegulation(t *testing.T) {
	tempDir := t.TempDir()
	cfg := config.Config{
		ListenAddr:         ":0",
		ScrapeInterval:     5 * time.Second,
		CPUReportMode:      "percent",
		LogLevel:           "info",
		TargetURL:          "http://localhost:8080/ingest",
		DetectorMinSamples: 30,
		ProcfsPath:         tempDir,
		RateTauMin:         50 * time.Millisecond,
		RateTauMax:         5 * time.Second,
		RateTheta:          3.5,
		CgroupRoot:         tempDir,
	}

	reg := metrics.NewRegistry()
	ob := outbox.NewOutbox(outbox.Config{Capacity: 100, DropPolicy: outbox.DropOldest})
	defer ob.Close()

	agent := newAgent(cfg, ob, reg)
	mux := newMux(agent, reg, ob)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	now := time.Now()

	for range 100 {
		now = now.Add(time.Second)
		agent.tick(now)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	for ob.Len() > 0 {
		_, _ = ob.Pop(ctx)
	}

	resp, err := http.Post(ts.URL+"/inject/anomaly", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /inject/anomaly failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /inject/anomaly status = %d, want 200", resp.StatusCode)
	}

	var alertEvt outbox.Event
	var alertFired bool

	for range 20 {
		now = now.Add(time.Second)
		agent.tick(now)
		for ob.Len() > 0 {
			evt, errPop := ob.Pop(ctx)
			if errPop != nil {
				break
			}
			if evt.Type == outbox.EventAnomalyAlert {
				alertEvt = evt
				alertFired = true
				break
			}
		}
		if alertFired {
			break
		}
	}

	if !alertFired {
		t.Fatalf("expected anomaly alert to fire after anomaly injection")
	}

	cpuMaxPath := filepath.Join(tempDir, "cpu.max")
	data, err := os.ReadFile(cpuMaxPath)
	if err != nil {
		t.Fatalf("failed to read cpu.max: %v", err)
	}
	if string(data) != "max 100000\n" {
		t.Fatalf("cpu.max = %q, want %q", string(data), "max 100000\n")
	}

	var payload AnomalyPayload
	if errUnmarshal := json.Unmarshal(alertEvt.Data, &payload); errUnmarshal != nil {
		t.Fatalf("failed to unmarshal alert payload: %v", errUnmarshal)
	}
	if len(payload.PreContext) == 0 {
		t.Fatalf("alert payload PreContext is empty, want non-empty")
	}

	for agent.injectSpikes.Load() > 0 {
		now = now.Add(time.Second)
		agent.tick(now)
	}

	for range agent.suppCfg.MinConsecutiveNormals {
		now = now.Add(time.Second)
		agent.tick(now)
	}

	data, err = os.ReadFile(cpuMaxPath)
	if err != nil {
		t.Fatalf("failed to read cpu.max after recovery: %v", err)
	}
	if string(data) != "5000 100000\n" {
		t.Fatalf("cpu.max after recovery = %q, want %q", string(data), "5000 100000\n")
	}
}

func TestAgentStartupQuiescentMode(t *testing.T) {
	tempDir := t.TempDir()
	cfg := config.Config{
		ListenAddr:         ":0",
		ScrapeInterval:     5 * time.Second,
		CPUReportMode:      "percent",
		LogLevel:           "info",
		TargetURL:          "http://localhost:8080/ingest",
		DetectorMinSamples: 30,
		ProcfsPath:         tempDir,
		RateTauMin:         50 * time.Millisecond,
		RateTauMax:         5 * time.Second,
		RateTheta:          3.5,
		CgroupRoot:         tempDir,
	}

	reg := metrics.NewRegistry()
	ob := outbox.NewOutbox(outbox.Config{Capacity: 10, DropPolicy: outbox.DropOldest})
	defer ob.Close()

	agent := newAgent(cfg, ob, reg)

	if got := agent.cgroupCtl.Mode(); got != cgroup.ModeQuiescent {
		t.Fatalf("agent startup cgroup mode = %v, want %v", got, cgroup.ModeQuiescent)
	}

	cpuMaxPath := filepath.Join(tempDir, "cpu.max")
	data, err := os.ReadFile(cpuMaxPath)
	if err != nil {
		t.Fatalf("failed to read cpu.max on startup: %v", err)
	}
	if string(data) != "5000 100000\n" {
		t.Fatalf("startup cpu.max = %q, want %q", string(data), "5000 100000\n")
	}
}
