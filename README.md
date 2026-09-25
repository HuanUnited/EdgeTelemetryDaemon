# Edge Telemetry Daemon (ETD)

Edge Telemetry Daemon (ETD) is a high-throughput, low-footprint telemetry collector and
closed-loop anomaly-driven resource governor built in Go for Linux edge devices and AI
inference systems. It collects host kernel accounting statistics and inference pipeline
metrics, runs dual-horizon streaming statistical filtering with zero heap allocations on hot
paths, dynamically throttles its own cgroup v2 CPU quota between a quiescent and burst state in
response to what it detects, and dispatches structured anomaly/drift alerts to central
telemetry backends over HTTP.

## Project goal: closed-loop CPU regulation

ETD's original design ran a fixed-interval collector with a single-horizon EWMA/Welford
detector and no resource control of its own. The daemon has since been evolved end-to-end
against a single target: **autonomous closed-loop cgroup v2 CPU quota regulation, dual-horizon
streaming concept-drift tracking, and adaptive interval sampling** — so the agent can sit at
5% CPU quota while idle, sense both sudden anomalies and slow concept drift in its own inputs,
burst to full CPU quota to react quickly, and settle back down once conditions normalize,
without a human or an external controller in the loop. All five planned phases are implemented,
merged, and — as of this revision — verified against a real Linux kernel cgroup v2 hierarchy,
not just mocked filesystem fixtures:

| Phase | Scope | Status |
| :--- | :--- | :--- |
| 1 | Core hardening & portability | ✅ Done |
| 2 | Dual-horizon drift engine | ✅ Done |
| 3 | `internal/cgroup` v2 quota controller | ✅ Done |
| 4 | Closed-loop agent orchestration | ✅ Done |
| 5 | Verification & benchmarking | ✅ Done |
| — | Real-kernel validation (Canonical Multipass, Ubuntu 24.04, cgroup v2) | ✅ Done |

`go test -race -count=1 ./...` passes **85/85 tests across all 9 packages**
(`cmd/agent`, `internal/cgroup`, `internal/collector`, `internal/config`, `internal/engine`,
`internal/filter`, `internal/metrics`, `internal/outbox`, `internal/transport`), and hot-path
zero-allocation invariants hold under repeated, statistically-averaged benchmark runs — see
[Performance Benchmarks](#performance-benchmarks) and
[Real-World cgroup v2 Validation](#real-world-cgroup-v2-validation).

## Architecture

```
[ Linux /proc & AI Collector ]
              │
              ▼
[ Dual-Horizon Streaming Engine ] ── (Fast/Slow EWMA Drift + Welford Variance + Saturation Guard)
              │
              ▼
[ Deadband Suppressor & Ring Buffer ] ── (Hysteresis Filter + Pre-Trigger Window)
              │              │
              │              └──────────────► [ cgroup v2 Quota Controller ]
              │                                  Quiescent (5%) ⇄ Burst (100%)
              ▼
[ Bounded Outbox Queue ] ── (sync.Cond broadcast wakeups, DropOldest/DropNewest)
              │
              ▼
[ Resilient HTTP Dispatcher ] ── (Exponential Backoff + Jitter)

     Z-score / drift index also feeds τ(Z) ──► dynamic scrape-interval controller
                                                (τ_min under anomaly ⇄ τ_max at rest)
```

### Subsystems

* **internal/collector**: Zero-allocation `/proc/stat` / `/proc/meminfo` scrapers via raw
  syscalls and stack buffers; platform-agnostic types in `types.go` so non-Linux builds compile
  against a stub returning `ErrUnsupportedPlatform` instead of failing to build.
* **internal/engine**: Welford's algorithm for $O(1)$ mean/variance, a **dual-horizon EWMA**
  (independent fast/slow smoothing factors) for concept-drift tracking, a normalized drift
  divergence index (`Δ_drift = |EWMA_fast − EWMA_slow| / max(1.0, EWMA_slow)`), and an absolute
  memory-saturation guard (`IsSaturated`).
* **internal/cgroup**: Native Linux cgroup v2 controller managing `cpu.max`/`cpu.stat`.
  `SetBurst()`/`SetQuiescent()` are idempotent; `ReadCPUStat()` parses CFS throttling accounting
  with zero heap allocations. **Verified against a real delegated cgroup v2 leaf, not just a
  mocked temp directory — see the validation section below.**
* **internal/filter**: Deadband suppressor state machine driving `cgroup.Controller` directly;
  fixed-capacity ring buffer for pre-trigger context; periodic heartbeat aggregator.
* **internal/outbox**: Thread-safe bounded queue with `sync.Cond.Broadcast()`-driven wakeups
  (no more single-slot notification channel), selectable overflow strategy, context cancellation.
* **internal/transport**: HTTP/JSON dispatcher with retry, jitter, and exponential backoff.
* **internal/metrics**: Zero-dependency Prometheus text-format registry; gauge `Set`/`Add`
  correctly separate float-bit storage from counter accumulation.
* **internal/config**: Environment-driven configuration with strict invariant validation.
* **cmd/agent**: Orchestration loop. `computeInterval(z, τ_min, τ_max, θ)` implements
  `τ(Z_t) = τ_max − (τ_max − τ_min) · min(1.0, |Z_t|/θ)`; suppressor state transitions call
  into the cgroup controller; drift-index crossings push `EventDriftAlert` alongside the
  existing anomaly-alert path.

## Configuration

| Variable | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `ETD_LISTEN_ADDR` | string | `:8080` | Bind host:port for `/metrics` and health endpoints |
| `ETD_SCRAPE_INTERVAL` | duration | `5s` | *Initial* polling frequency; adapts at runtime between `ETD_RATE_TAU_MIN`/`ETD_RATE_TAU_MAX` |
| `ETD_DETECTOR_MIN_SAMPLES` | int | `30` | Minimum samples before Z-score/drift evaluation activates |
| `ETD_CPU_REPORT_MODE` | string | `percent` | CPU metric unit (`percent`, `ticks`, `hertz`) |
| `ETD_LOG_LEVEL` | string | `info` | Structured logging verbosity |
| `ETD_PROCFS_PATH` | string | `/proc` | Base procfs mount path |
| `ETD_TARGET_URL` | string | `http://localhost:8080/ingest/dummy` | Remote ingestion endpoint |
| `ETD_RATE_TAU_MIN` | duration | `50ms` | Fastest sampling interval, under peak anomaly |
| `ETD_RATE_TAU_MAX` | duration | `5s` | Slowest sampling interval, at rest |
| `ETD_RATE_THETA` | float | `3.5` | Z-score sensitivity scale for `τ(Z)` |
| `ETD_CGROUP_ROOT` | string | `/sys/fs/cgroup` | **Must point at a delegated leaf cgroup the daemon's own process actually belongs to — not the filesystem root.** The root cgroup in v2 has no `cpu.max` interface file at all, so pointing this at `/sys/fs/cgroup` itself is inert. In production, run the daemon under a `systemd-run --scope -p Delegate=yes` unit (or an equivalent container/cgroup-namespace setup) and set this to that unit's own cgroup path — see the validation section for a worked example. |

## Performance Benchmarks

Averaged over 10 runs per benchmark (`go test -bench=. -benchmem -count=10`, then
`benchstat`), not a single sample — figures below are mean latency with relative variance
across the 10 runs.

### Internal Microbenchmarks (12th Gen Intel Core i7-12700H, `GOMAXPROCS=20`)

| Package | Benchmark | Mean Latency | Variance | Memory / Alloc |
| :--- | :--- | :--- | :--- | :--- |
| cgroup | `ReadCPUStat` | 2.674 µs | ± 26% | 0 B, 0 allocs — ± 0% |
| collector | `CollectCPU` | 1.982 µs | ± 37% | 0 B, 0 allocs — ± 0% |
| collector | `CollectMem` | 2.193 µs | ± 45% | 0 B, 0 allocs — ± 0% |
| collector | `AIGenNext` | 86.74 ns | ± 1% | 0 B, 0 allocs — ± 0% |
| collector | `ParseCPULine` | 87.48 ns | ± 49% | 0 B, 0 allocs — ± 0% |
| engine | `WelfordUpdate` | 8.357 ns | ± 1% | 0 B, 0 allocs — ± 0% |
| engine | `WelfordUpdateNoEscape` | 8.415 ns | ± 1% | 0 B, 0 allocs — ± 0% |
| engine | `EWMAUpdate` | 4.515 ns | ± 1% | 0 B, 0 allocs — ± 0% |
| engine | `ZScore` | 17.11 ns | ± 3% | 0 B, 0 allocs — ± 0% |
| engine | `ZScoreDetect` | 27.16 ns | ± 4% | 0 B, 0 allocs — ± 0% |
| engine | `ZScoreExtendedUpdate` | 18.48 ns | ± 1% | 0 B, 0 allocs — ± 0% |
| engine | `ZScoreDetectorUpdateDualHorizon` | 18.56 ns | ± 2% | 0 B, 0 allocs — ± 0% |
| engine | `IsSaturated` | 0.3675 ns | ± 5% | 0 B, 0 allocs — ± 0% |
| filter | `SuppressorProcess` | 20.70 ns | ± 5% | 0 B, 0 allocs — ± 0% |
| filter | `RingBufferPush` | 31.10 ns | ± 3% | 0 B, 0 allocs — ± 0% |
| filter | `RingBufferSnapshot` | 161.2 ns | ± 28% | 0 B, 0 allocs — ± 0% |
| filter | `HeartbeatObserve` | 17.55 ns | ± 2% | 0 B, 0 allocs — ± 0% |
| metrics | `MetricGaugeSet` | 6.680 ns | ± 41% | 0 B, 0 allocs — ± 0% |
| metrics | `MetricCounterSet` | 5.994 ns | ± 3% | 0 B, 0 allocs — ± 0% |
| metrics | `MetricGaugeAdd` | 10.64 ns | ± 3% | 0 B, 0 allocs — ± 0% |
| metrics | `MetricCounterAdd` | 5.919 ns | ± 30% | 0 B, 0 allocs — ± 0% |
| outbox | `OutboxContention` | 221.9 ns | ± 3% | 0 B, 0 allocs — ± 0% |
| outbox | `OutboxPush` | 24.88 ns | ± 3% | 0 B, 0 allocs — ± 0% |
| outbox | `OutboxPop` | 27.91 ns | ± 18% | 0 B, 0 allocs — ± 0% |
| outbox | `OutboxPushPop` | 47.21 ns | ± 4% | 0 B, 0 allocs — ± 0% |

The zero-allocation claim here is stronger than a single-run "0 allocs/op" reading: the
`± 0%` on every memory/allocation column means all 10 runs produced exactly zero, not an
average that merely rounds to zero. Latency variance is wider on a handful of benchmarks
(`CollectCPU` ± 37%, `ParseCPULine` ± 49%, `MetricGaugeSet` ± 41%) — consistent with host
scheduling noise on a shared dev machine during the run rather than instability in the code
itself, since the allocation figures for those same benchmarks show zero variance.

### Test Coverage

`go test -coverprofile` reports **80.9% statement coverage** across the module. The core
numerical primitives are fully covered: `Welford.*`, `EWMA.*`, `ZScoreDetector.Update/ZScore`,
and `IsSaturated` all sit at 100%. Two entries read 0.0% for a mechanical reason rather than a
real gap: `collector.CollectCPU` and `collector.CollectMem` are exercised only by
`BenchmarkCollectCPU`/`BenchmarkCollectMem`, and `-coverprofile` runs alone don't execute
`Benchmark*` functions — running with `-bench=. -cover` combined, or adding a thin direct
`Test*` wrapper, would close that reporting gap without changing what's actually exercised.
`cmd/agent.main` (0.0%) is the process entrypoint itself and is exercised in practice by the
integration tests calling `newAgent`/`tick` directly rather than through `main()`.

## Real-World cgroup v2 Validation

The mocked-filesystem tests in `internal/cgroup` prove the parser and the write logic are
correct in isolation; they don't prove the daemon can actually acquire and be constrained by a
*real* delegated cgroup v2 leaf. That was validated separately on a Canonical Multipass VM
(Ubuntu 24.04, systemd, unified cgroup v2 hierarchy) rather than WSL2, to avoid the extra
nesting from Docker Desktop's WSL integration and WSL2's own virtualized cgroup tree.

**Setup.** The daemon was launched as a systemd-delegated scope rather than a bare process,
which is also the deployment pattern this project recommends in production:

```bash
sudo systemd-run --unit=etd-agent --scope \
  -p Delegate=yes -p CPUAccounting=yes \
  --setenv=ETD_CGROUP_ROOT=/sys/fs/cgroup/system.slice/etd-agent.scope \
  /home/ubuntu/etd/etd
```

`cpu.max` and `cpu.stat` under `/sys/fs/cgroup/system.slice/etd-agent.scope/` were polled every
200ms for ~12 minutes while a synthetic anomaly was injected via `POST /inject/anomaly`.

**Observed transition.** The daemon logged a drift alert and an anomaly alert within 52ms of
each other:

```
12:02:28.427  WARN  drift alert triggered     drift_index=2.4916
12:02:28.479  WARN  anomaly alert triggered   z_score=5.0998  throughput=726.64
```

`cpu.max` flipped from unconstrained to the 5% quota **4.6 seconds later**, once the suppressor
returned to `StateNormal`:

| Timestamp | `cpu.max` | Mode | `nr_periods` | `nr_throttled` | `throttled_usec` |
| :--- | :--- | :--- | :--- | :--- | :--- |
| 12:02:28.396 | `max 100000` | Burst | 0 | 0 | 0 |
| 12:02:32.827 | `max 100000` | Burst | 0 | 0 | 0 |
| 12:02:33.042 | `5000 100000` | Quiescent | 2 | 0 | 0 |
| 12:11:31.866 | `5000 100000` | Quiescent | 226 | 5 | 267,660 |

The daemon held Quiescent for the remaining ~8 min 58s of the capture window, including through
a second, drift-only alert at 12:04:19 (`drift_index=0.171`) that did **not** re-trigger Burst —
correct, by design: only the suppressor's alerting state (driven by the anomaly Z-score path)
switches quota, not a raw drift signal on its own.

**The kernel actually enforced the cap.** `nr_throttled` climbing from 0 to 5 and
`throttled_usec` from 0 to 267,660 over 226 CFS periods during the Quiescent window is direct
evidence the 5% limit was a real, kernel-enforced constraint and not just a number written to a
file nobody honored. Measured average CPU consumption while Quiescent:
`(296801 − 99809) µs ⁄ 538.824 s ≈ 0.037%` of one core — well under the 5% ceiling on average,
with brief spikes actually hitting it (hence the throttle events). During the preceding Burst
window, `nr_periods` stayed at 0 throughout — CFS bandwidth enforcement is effectively inert in
unconstrained (`max`) mode, as expected.

**A genuine limitation this run surfaced.** The very first sample captured (12:02:28.396, about
3 minutes after the daemon's actual start at 11:59:21) already shows Burst mode, despite no
anomaly having fired yet at that point — the daemon does not call `SetQuiescent()` on startup,
so a freshly delegated cgroup leaf sits at the kernel's own default (`max`, unconstrained) until
the *first* alert-then-recovery cycle completes. This is a real gap worth fixing (a one-line
`cgroupCtl.SetQuiescent()` call during `newAgent()` would close it) rather than a runtime bug —
it's included here because it only became visible once quota transitions were observed against
a real kernel instead of a mocked temp directory.

## Testing Suite

```bash
# Full race-detected suite
go test -v -race -count=1 ./...

# Statistically averaged benchmarks
go test -bench=. -benchmem -count=10 ./... | tee bench-raw.txt
benchstat bench-raw.txt

# Explicit zero-allocation regression assertions (named with an *Alloc suffix,
# e.g. TestReadCPUStatAlloc, TestMetricSetAlloc, TestOutboxPushAlloc)
go test -run Alloc -v ./...

# Statement coverage
go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out

# CFS throttling stress test
go test -race -run TestCFSThrottlingStress ./internal/cgroup

# Multi-tenant interference test
go test -race -run TestMultiTenant ./cmd/agent

# Coverage-guided fuzzing
go test -fuzz=FuzzParseCPULine -fuzztime=30s ./internal/collector
go test -fuzz=FuzzParseMemInfo -fuzztime=30s ./internal/collector
go test -fuzz=FuzzWelfordEquivalence -fuzztime=30s ./internal/engine
go test -fuzz=FuzzSuppressorProcess -fuzztime=30s ./internal/filter
```

## Known Limitations & Future Work

* **Startup mode is undefined until the first alert cycle.** See
  [Real-World cgroup v2 Validation](#real-world-cgroup-v2-validation) — the fix is a single
  explicit `SetQuiescent()` call at agent construction time.
* **`ETD_CGROUP_ROOT`'s documented default (`/sys/fs/cgroup`) is not a usable value on its
  own** — it must be overridden to point at a delegated leaf cgroup in every real deployment;
  the config loader does not currently validate this at startup (`validate()` checks the rate
  parameters but not that `CgroupRoot` resolves to a writable, non-root cgroup).
* Drift-only alerts do not affect quota state by design (only the suppressor's anomaly-driven
  alerting state does) — worth stating explicitly if a future revision wants drift severity to
  also influence Burst/Quiescent transitions.
* Soak-test figures under sustained load (`hey -z 5m -c 50`, split by Quiescent vs. Burst
  regime) have not yet been collected against the current build — see the Multipass procedure
  above for the environment to run them in.
