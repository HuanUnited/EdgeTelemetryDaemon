package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/HuanUnited/edgetelemetrydaemon/internal/cgroup"
	"github.com/HuanUnited/edgetelemetrydaemon/internal/config"
	"github.com/HuanUnited/edgetelemetrydaemon/internal/metrics"
	"github.com/HuanUnited/edgetelemetrydaemon/internal/outbox"
)

func drainAlerts(ctx context.Context, ob *outbox.Outbox) []outbox.Event {
	var alerts []outbox.Event
	for ob.Len() > 0 {
		evt, err := ob.Pop(ctx)
		if err != nil {
			break
		}
		if evt.Type == outbox.EventAnomalyAlert {
			alerts = append(alerts, evt)
		}
	}
	return alerts
}

func drainAll(ctx context.Context, ob *outbox.Outbox) {
	for ob.Len() > 0 {
		if _, err := ob.Pop(ctx); err != nil {
			break
		}
	}
}

func TestMultiTenantInterference(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()

	// Pre-create cpu.max so cgroup controller doesn't fall back to read-only mode
	_ = os.WriteFile(filepath.Join(dirA, "cpu.max"), []byte("max 100000\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dirB, "cpu.max"), []byte("max 100000\n"), 0o644)

	cfgA := config.Config{
		ListenAddr:         ":0",
		ScrapeInterval:     5 * time.Second,
		CPUReportMode:      "percent",
		LogLevel:           "info",
		TargetURL:          "http://localhost:8080/ingest-a",
		DetectorMinSamples: 30,
		ProcfsPath:         dirA,
		RateTauMin:         50 * time.Millisecond,
		RateTauMax:         5 * time.Second,
		RateTheta:          3.5,
		CgroupRoot:         dirA,
	}

	cfgB := config.Config{
		ListenAddr:         ":0",
		ScrapeInterval:     5 * time.Second,
		CPUReportMode:      "percent",
		LogLevel:           "info",
		TargetURL:          "http://localhost:8080/ingest-b",
		DetectorMinSamples: 30,
		ProcfsPath:         dirB,
		RateTauMin:         50 * time.Millisecond,
		RateTauMax:         5 * time.Second,
		RateTheta:          3.5,
		CgroupRoot:         dirB,
	}

	regA := metrics.NewRegistry()
	regB := metrics.NewRegistry()

	obA := outbox.NewOutbox(outbox.Config{Capacity: 100, DropPolicy: outbox.DropOldest})
	defer obA.Close()
	obB := outbox.NewOutbox(outbox.Config{Capacity: 100, DropPolicy: outbox.DropOldest})
	defer obB.Close()

	agentA := newAgent(cfgA, obA, regA)
	agentB := newAgent(cfgB, obB, regB)

	baseTime := time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)
	now := baseTime
	var totalA, userA, totalB, userB uint64

	for i := 0; i < 250; i++ {
		now = now.Add(time.Second)
		totalA += 1000
		userA += 100
		totalB += 1000
		userB += 100
		writeProc(dirA, userA, totalA, 1000000, 900000)
		writeProc(dirB, userB, totalB, 1000000, 900000)
		agentA.tick(now)
		agentB.tick(now)
	}

	warmCtx, warmCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	drainAll(warmCtx, obA)
	drainAll(warmCtx, obB)
	warmCancel()

	agentA.supp.Reset()
	agentB.supp.Reset()
	alertsBeforeA := agentA.metricAlerts.Value()
	alertsBeforeB := agentB.metricAlerts.Value()

	var wg sync.WaitGroup
	wg.Add(2)

	// Agent A driver: exactly one anomaly burst (3 spikes).
	go func() {
		defer wg.Done()
		currA := now
		for i := 0; i < 60; i++ {
			currA = currA.Add(time.Second)
			totalA += 1000
			if i >= 5 && i < 8 {
				userA += 900 // 90% load
				writeProc(dirA, userA, totalA, 1000000, 100000)
			} else {
				userA += 100
				writeProc(dirA, userA, totalA, 1000000, 900000)
			}
			agentA.tick(currA)
		}
	}()

	// Agent B driver: two distinct anomaly bursts separated by more than holdoff duration (10s).
	go func() {
		defer wg.Done()
		currB := now
		for i := 0; i < 60; i++ {
			currB = currB.Add(time.Second)
			totalB += 1000
			if (i >= 5 && i < 8) || (i >= 35 && i < 38) {
				userB += 900
				writeProc(dirB, userB, totalB, 1000000, 100000)
			} else {
				userB += 100
				writeProc(dirB, userB, totalB, 1000000, 900000)
			}
			agentB.tick(currB)
		}
	}()

	wg.Wait()

	drainCtx, drainCancel := context.WithTimeout(context.Background(), time.Second)
	defer drainCancel()

	alertsA := drainAlerts(drainCtx, obA)
	alertsB := drainAlerts(drainCtx, obB)

	if len(alertsA) != 1 {
		t.Fatalf("Agent A outbox alert count = %d, want 1", len(alertsA))
	}
	if len(alertsB) != 2 {
		t.Fatalf("Agent B outbox alert count = %d, want 2", len(alertsB))
	}

	anomsA, alertsFiredA, _ := agentA.supp.Stats()
	anomsB, alertsFiredB, _ := agentB.supp.Stats()

	if alertsFiredA != 1 {
		t.Fatalf("Agent A suppressor alerts fired = %d, want 1", alertsFiredA)
	}
	if alertsFiredB != 2 {
		t.Fatalf("Agent B suppressor alerts fired = %d, want 2", alertsFiredB)
	}

	if anomsA < 2 {
		t.Fatalf("Agent A total anomalies = %d, want >= 2", anomsA)
	}
	if anomsB < 4 {
		t.Fatalf("Agent B total anomalies = %d, want >= 4", anomsB)
	}

	if got := agentA.metricAlerts.Value() - alertsBeforeA; got != 1 {
		t.Fatalf("Agent A metricAlerts delta = %d, want 1", got)
	}
	if got := agentB.metricAlerts.Value() - alertsBeforeB; got != 2 {
		t.Fatalf("Agent B metricAlerts delta = %d, want 2", got)
	}

	if got := agentA.cgroupCtl.Mode(); got != cgroup.ModeQuiescent {
		t.Fatalf("Agent A final cgroup mode = %v, want %v", got, cgroup.ModeQuiescent)
	}
	if got := agentB.cgroupCtl.Mode(); got != cgroup.ModeQuiescent {
		t.Fatalf("Agent B final cgroup mode = %v, want %v", got, cgroup.ModeQuiescent)
	}

	expectedQuota := fmt.Sprintf("%d 100000\n", runtime.NumCPU()*5000)

	cpuMaxA, err := os.ReadFile(filepath.Join(dirA, "cpu.max"))
	if err != nil {
		t.Fatalf("read dirA/cpu.max: %v", err)
	}
	if string(cpuMaxA) != expectedQuota {
		t.Fatalf("dirA/cpu.max = %q, want %q", string(cpuMaxA), expectedQuota)
	}

	cpuMaxB, err := os.ReadFile(filepath.Join(dirB, "cpu.max"))
	if err != nil {
		t.Fatalf("read dirB/cpu.max: %v", err)
	}
	if string(cpuMaxB) != expectedQuota {
		t.Fatalf("dirB/cpu.max = %q, want %q", string(cpuMaxB), expectedQuota)
	}
}

func TestMultiTenantConcurrentModeIsolation(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()

	// Pre-create cpu.max so cgroup controller doesn't fall back to read-only mode
	_ = os.WriteFile(filepath.Join(dirA, "cpu.max"), []byte("max 100000\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dirB, "cpu.max"), []byte("max 100000\n"), 0o644)

	cfgA := config.Config{
		ListenAddr:         ":0",
		ScrapeInterval:     5 * time.Second,
		CPUReportMode:      "percent",
		LogLevel:           "info",
		TargetURL:          "http://localhost:8080/ingest-a",
		DetectorMinSamples: 30,
		ProcfsPath:         dirA,
		RateTauMin:         50 * time.Millisecond,
		RateTauMax:         5 * time.Second,
		RateTheta:          3.5,
		CgroupRoot:         dirA,
	}

	cfgB := config.Config{
		ListenAddr:         ":0",
		ScrapeInterval:     5 * time.Second,
		CPUReportMode:      "percent",
		LogLevel:           "info",
		TargetURL:          "http://localhost:8080/ingest-b",
		DetectorMinSamples: 30,
		ProcfsPath:         dirB,
		RateTauMin:         50 * time.Millisecond,
		RateTauMax:         5 * time.Second,
		RateTheta:          3.5,
		CgroupRoot:         dirB,
	}

	regA := metrics.NewRegistry()
	regB := metrics.NewRegistry()

	obA := outbox.NewOutbox(outbox.Config{Capacity: 100, DropPolicy: outbox.DropOldest})
	defer obA.Close()
	obB := outbox.NewOutbox(outbox.Config{Capacity: 100, DropPolicy: outbox.DropOldest})
	defer obB.Close()

	agentA := newAgent(cfgA, obA, regA)
	agentB := newAgent(cfgB, obB, regB)

	now := time.Date(2026, 9, 15, 11, 0, 0, 0, time.UTC)
	var totalA, userA, totalB, userB uint64

	for range 250 {
		now = now.Add(time.Second)
		totalA += 1000
		userA += 100
		totalB += 1000
		userB += 100
		writeProc(dirA, userA, totalA, 1000000, 900000)
		writeProc(dirB, userB, totalB, 1000000, 900000)
		agentA.tick(now)
		agentB.tick(now)
	}

	drainCtx, drainCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	drainAll(drainCtx, obA)
	drainAll(drainCtx, obB)
	drainCancel()

	agentA.supp.Reset()
	agentB.supp.Reset()

	var wg sync.WaitGroup
	wg.Add(2)

	// Agent A is driven with spikes to stay in Burst mode.
	go func() {
		defer wg.Done()
		currA := now
		for i := range 10 {
			currA = currA.Add(time.Second)
			totalA += 1000
			if i >= 0 && i < 5 {
				userA += 900
				writeProc(dirA, userA, totalA, 1000000, 100000)
			} else {
				userA += 100
				writeProc(dirA, userA, totalA, 1000000, 900000)
			}
			agentA.tick(currA)
		}
	}()

	// Agent B runs strictly normal cycles.
	go func() {
		defer wg.Done()
		currB := now
		for range 10 {
			currB = currB.Add(time.Second)
			totalB += 1000
			userB += 100
			writeProc(dirB, userB, totalB, 1000000, 900000)
			agentB.tick(currB)
		}
	}()

	wg.Wait()

	if anomsB, alertsB, _ := agentB.supp.Stats(); anomsB != 0 || alertsB != 0 {
		t.Fatalf("Agent B contaminated by Agent A burst: anoms=%d alerts=%d", anomsB, alertsB)
	}
	if agentB.metricAlerts.Value() != 0 {
		t.Fatalf("Agent B metricAlerts contaminated = %d, want 0", agentB.metricAlerts.Value())
	}

	if _, err := os.Stat(filepath.Join(dirB, "cpu.max")); err == nil {
		dataB, rerr := os.ReadFile(filepath.Join(dirB, "cpu.max"))
		if rerr == nil && string(dataB) == "max 100000\n" {
			t.Fatalf("Agent B cgroup root contains burst quota written by Agent A")
		}
	}
}
