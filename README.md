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
without a human or an external controller in the loop. All five planned phases are now
implemented and merged:

| Phase | Scope | Status |
| :--- | :--- | :--- |
| 1 | Core hardening & portability (non-Linux build stub, gauge-corruption fix, outbox multi-consumer wakeup fix) | ✅ Done |
| 2 | Dual-horizon drift engine (fast/slow EWMA, `Δ_drift`, saturation guard) | ✅ Done |
| 3 | `internal/cgroup` v2 quota controller | ✅ Done |
| 4 | Closed-loop agent orchestration (adaptive `τ(Z)` sampling, state-driven quota switching) | ✅ Done |
| 5 | Verification & benchmarking (zero-alloc regression suite, CFS stress test, multi-tenant interference test) | ✅ Done |

The full test suite (`go test -race -count=1 ./...`) is green across every package
(`cmd/agent`, `internal/cgroup`, `internal/collector`, `internal/config`, `internal/engine`,
`internal/filter`, `internal/metrics`, `internal/outbox`, `internal/transport`), and hot-path
zero-allocation invariants hold end to end — see [Performance Benchmarks](#performance-benchmarks).

## Architecture

The telemetry pipeline processes data through sequential stages designed for deterministic
latency and bounded memory consumption, with a feedback path from detection back into both the
sampling cadence and the daemon's own CPU quota:

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

* **internal/collector**: Zero-allocation metric scrapers parsing `/proc/stat` and
  `/proc/meminfo` via direct raw syscalls (`SYS_OPENAT`, `SYS_READ`, `SYS_CLOSE`) and stack
  buffers, alongside a concurrent synthetic AI inference generator (`AIGen`). Platform-agnostic
  types now live in `types.go`; non-Linux builds compile cleanly against a stub that returns a
  typed `ErrUnsupportedPlatform` instead of failing to build.
* **internal/engine**: Online statistical engine implementing Welford's algorithm for $O(1)$
  running mean and variance computation, a **dual-horizon EWMA** (independent fast and slow
  smoothing factors) for concept-drift tracking, a normalized drift divergence index
  (`Δ_drift = |EWMA_fast − EWMA_slow| / max(1.0, EWMA_slow)`), and an absolute memory-saturation
  floor guard (`IsSaturated`) that forces an anomaly regardless of Z-score when available memory
  drops below a configured percentage.
* **internal/cgroup**: Native Linux cgroup v2 controller managing `cpu.max` and `cpu.stat` under
  a configurable cgroup root. `SetBurst()`/`SetQuiescent()` are idempotent (zero filesystem
  writes on a repeated call in the same mode); `ReadCPUStat()` parses CFS throttling accounting
  (`nr_periods`, `nr_throttled`, `throttled_usec`) with zero heap allocations.
* **internal/filter**: Deadband suppressor state machine (`StateNormal` /
  `StateAlerting` / `StateSuppressed`) preventing alert storms — its state transitions now
  directly drive `cgroup.Controller.SetBurst`/`SetQuiescent` — a fixed-capacity circular ring
  buffer maintaining pre-trigger context for anomalous intervals, and a periodic heartbeat
  aggregator.
* **internal/outbox**: Thread-safe bounded queue with selectable overflow strategies
  (`DropOldest` vs `DropNewest`), context cancellation, and immediate garbage collection of
  evicted elements. Consumer wakeups are now driven by `sync.Cond.Broadcast()` on every mutation
  rather than a single-slot notification channel, so multiple concurrent `Pop()` callers can no
  longer miss a wakeup.
* **internal/transport**: HTTP/JSON telemetry dispatcher featuring retry loops with randomized
  jitter, exponential backoff, and graceful payload delivery.
* **internal/metrics**: Zero-dependency Prometheus text-format registry exposing runtime
  counters and gauges on `/metrics`. Gauge `Set`/`Add` correctly separate float-bit storage from
  counter accumulation (no cross-contamination between the two code paths).
* **internal/config**: Environment-driven runtime configuration with strict invariant
  validation, now including the adaptive-rate and cgroup parameters below.
* **cmd/agent**: The orchestration loop. `computeInterval(z, τ_min, τ_max, θ)` implements
  `τ(Z_t) = τ_max − (τ_max − τ_min) · min(1.0, |Z_t|/θ)` and re-arms the scrape ticker every
  tick; suppressor state transitions call into the cgroup controller; a fresh drift-index
  crossing pushes an `EventDriftAlert` to the outbox alongside the existing anomaly-alert path.

## Configuration

ETD is configured through environment variables:

| Variable | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `ETD_LISTEN_ADDR` | string | `:8080` | Bind host:port for Prometheus `/metrics` and health endpoints |
| `ETD_SCRAPE_INTERVAL` | duration | `5s` | *Initial* polling frequency for host collectors; contracts/expands adaptively at runtime between `ETD_RATE_TAU_MIN` and `ETD_RATE_TAU_MAX` |
| `ETD_DETECTOR_MIN_SAMPLES` | int | `30` | Minimum Welford sample count before Z-score and drift evaluation activate |
| `ETD_CPU_REPORT_MODE` | string | `percent` | CPU metric unit (`percent`, `ticks`, or `hertz`) |
| `ETD_LOG_LEVEL` | string | `info` | Structured logging verbosity (`debug`, `info`, `warn`, `error`) |
| `ETD_PROCFS_PATH` | string | `/proc` | Base procfs mount path for host metric collection (e.g. `/host/proc` in containers) |
| `ETD_TARGET_URL` | string | `http://localhost:8080/ingest/dummy` | Remote ingestion endpoint for dispatched alerts and heartbeats |
| `ETD_RATE_TAU_MIN` | duration | `50ms` | Lower-bound sampling interval under peak anomaly (fastest reaction) |
| `ETD_RATE_TAU_MAX` | duration | `5s` | Upper-bound sampling interval under quiescent conditions (lowest overhead) |
| `ETD_RATE_THETA` | float | `3.5` | Z-score sensitivity scale for the `τ(Z)` interval controller |
| `ETD_CGROUP_ROOT` | string | `/sys/fs/cgroup` | Filesystem root of the cgroup v2 hierarchy this daemon regulates |

## Quickstart with Docker Compose

Deploy the daemon alongside a mock ingestion sink:

```bash
# Launch daemon and mock ingress sink
docker compose up -d --build

# Verify container status
docker compose ps

# Scrape Prometheus metrics
curl -s http://localhost:8080/metrics

# Trigger a synthetic anomaly burst and watch the closed loop react
curl -s -X POST http://localhost:8080/inject/anomaly
```

### Docker Compose Configuration

```yaml
services:
  agent:
    build:
      context: .
      dockerfile: Dockerfile
    container_name: edge-telemetry-daemon
    ports:
      - "8080:8080"
    volumes:
      - /proc:/host/proc:ro
      - /sys/fs/cgroup:/sys/fs/cgroup:rw
    environment:
      - ETD_LISTEN_ADDR=:8080
      - ETD_SCRAPE_INTERVAL=100ms
      - ETD_DETECTOR_MIN_SAMPLES=30
      - ETD_CPU_REPORT_MODE=percent
      - ETD_LOG_LEVEL=debug
      - ETD_PROCFS_PATH=/host/proc
      - ETD_TARGET_URL=http://ingest-sink:80/post
      - ETD_RATE_TAU_MIN=50ms
      - ETD_RATE_TAU_MAX=5s
      - ETD_RATE_THETA=3.5
      - ETD_CGROUP_ROOT=/sys/fs/cgroup
    depends_on:
      - ingest-sink
    restart: unless-stopped

  ingest-sink:
    image: kennethreitz/httpbin
    container_name: telemetry-ingest-sink
    ports:
      - "8081:80"
    restart: unless-stopped
```

> **Note**: writing to `cpu.max` requires the container to have access to (and delegated
> control over) a real cgroup v2 leaf — on most container runtimes this means running with the
> cgroup mounted read-write, as above, and pointed at a delegated subtree rather than the host
> root in multi-tenant environments.

## Performance Benchmarks

All hot paths across collection, detection, filtering, and quota control operate with zero
heap allocations — verified both by the benchmarks below and by an explicit
`testing.AllocsPerRun` regression suite (`zero_alloc_test.go` in each package) added in Phase 5.

### Internal Microbenchmarks (12th Gen Intel Core i7-12700H, `GOMAXPROCS=20`)

| Package | Benchmark | Iterations | Latency | Memory / Op | Allocations / Op |
| :--- | :--- | :--- | :--- | :--- | :--- |
| cgroup | `ReadCPUStat` | 468,450 | 3473 ns/op | 0 B/op | 0 allocs/op |
| collector | `CollectCPU` | 439,970 | 2447 ns/op | 0 B/op | 0 allocs/op |
| collector | `CollectMem` | 173,450 | 6212 ns/op | 0 B/op | 0 allocs/op |
| collector | `AIGenNext` | 1,734,031 | 694.2 ns/op | 0 B/op | 0 allocs/op |
| collector | `ParseCPULine` | 1,496,222 | 810.7 ns/op | 0 B/op | 0 allocs/op |
| engine | `WelfordUpdate` | 36,234,565 | 32.45 ns/op | 0 B/op | 0 allocs/op |
| engine | `WelfordUpdateNoEscape` | 28,125,240 | 43.00 ns/op | 0 B/op | 0 allocs/op |
| engine | `EWMAUpdate` | 64,055,565 | 24.62 ns/op | 0 B/op | 0 allocs/op |
| engine | `ZScore` | 6,226,982 | 286.1 ns/op | 0 B/op | 0 allocs/op |
| engine | `ZScoreDetect` | 4,809,849 | 353.4 ns/op | 0 B/op | 0 allocs/op |
| engine | `ZScoreExtendedUpdate` | 6,315,078 | 191.9 ns/op | 0 B/op | 0 allocs/op |
| engine | `ZScoreDetectorUpdateDualHorizon` | 6,313,641 | 190.4 ns/op | 0 B/op | 0 allocs/op |
| engine | `IsSaturated` | 301,524,793 | 3.981 ns/op | 0 B/op | 0 allocs/op |
| filter | `SuppressorProcess` | 2,181,204 | 546.6 ns/op | 0 B/op | 0 allocs/op |
| filter | `RingBufferPush` | 2,389,594 | 513.9 ns/op | 0 B/op | 0 allocs/op |
| filter | `RingBufferSnapshot` | 594,888 | 6215 ns/op | 0 B/op | 0 allocs/op |
| filter | `HeartbeatObserve` | 2,368,240 | 505.8 ns/op | 0 B/op | 0 allocs/op |
| metrics | `MetricGaugeSet` | 7,987,080 | 153.4 ns/op | 0 B/op | 0 allocs/op |
| metrics | `MetricCounterSet` | 9,777,310 | 121.4 ns/op | 0 B/op | 0 allocs/op |
| metrics | `MetricGaugeAdd` | 7,847,577 | 146.6 ns/op | 0 B/op | 0 allocs/op |
| metrics | `MetricCounterAdd` | 9,878,384 | 121.4 ns/op | 0 B/op | 0 allocs/op |
| outbox | `OutboxContention` | 768,183 | 1565 ns/op | 0 B/op | 0 allocs/op |
| outbox | `OutboxPush` | 1,599,568 | 748.4 ns/op | 0 B/op | 0 allocs/op |
| outbox | `OutboxPop` | 1,664,654 | 717.0 ns/op | 0 B/op | 0 allocs/op |
| outbox | `OutboxPushPop` | 979,306 | 1329 ns/op | 0 B/op | 0 allocs/op |

Every benchmarked package (`cmd/agent`, `cgroup`, `collector`, `config`, `engine`, `filter`,
`metrics`, `outbox`, `transport`) reports `PASS` under `go test -race -count=1 ./...`, including
the new `TestEndToEndQuotaRegulation` integration test, which drives a full
inject → detect → suppress → burst-quota → cool-down → quiescent-quota cycle and was observed
correctly firing a mix of drift alerts (`drift_index` in the 0.16–3.4 range) and anomaly alerts
(`z_score` from ~6 to ~12 against injected throughput spikes) during the run.

> **Not yet refreshed**: the soak-test and `benchstat`-averaged figures from the pre-upgrade
> baseline are omitted here rather than left stale, since they predate the cgroup quota
> controller and would no longer reflect steady-state behavior (the daemon now spends most of
> its time at 5% quota instead of unconstrained). Re-run `hey -z 10m -c 50` and `benchstat`
> against the current build before publishing new soak numbers.

## Testing Suite

### Unit & Race Detection Tests

Execute unit test suites across all packages with race detection enabled:

```bash
go test -v -race -count=1 ./...
```

### Zero-Allocation Benchmarking

Verify execution performance and zero heap allocations across hot paths:

```bash
go test -bench=. -benchmem ./...
```

### Zero-Allocation Regression (explicit assertions)

Each package with a hot path carries a `zero_alloc_test.go` asserting
`testing.AllocsPerRun(...) == 0` directly, so a future change that introduces an accidental
allocation fails `go test`, not just a benchmark someone forgot to read:

```bash
go test -run TestZeroAlloc ./...
```

### CFS Throttling Stress Test

Exercises `internal/cgroup.Controller.ReadCPUStat` under concurrent readers against a
continuously-mutated `cpu.stat` fixture:

```bash
go test -race -run TestCgroupStress ./internal/cgroup
```

### Multi-Tenant Interference Test

Runs two independent `Agent` instances against two separate cgroup roots concurrently and
asserts neither's alert/quota state leaks into the other:

```bash
go test -race -run TestInterference ./cmd/agent
```

### Coverage-Guided Fuzz Testing

Execute fuzz testing against host parsers, mathematical engines, and deadband state machines:

```bash
# Fuzz Linux CPU parser
go test -fuzz=FuzzParseCPULine -fuzztime=30s ./internal/collector

# Fuzz Linux Meminfo parser
go test -fuzz=FuzzParseMemInfo -fuzztime=30s ./internal/collector

# Fuzz Welford online variance algorithm
go test -fuzz=FuzzWelfordEquivalence -fuzztime=30s ./internal/engine

# Fuzz deadband suppressor state transitions
go test -fuzz=FuzzSuppressorProcess -fuzztime=30s ./internal/filter
```