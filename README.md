# Edge Telemetry Daemon (ETD)

Edge Telemetry Daemon (ETD) is a high-throughput, low-footprint telemetry collector and streaming anomaly detection agent built in Go for Linux edge devices and AI inference systems. It collects host kernel accounting statistics and inference pipeline metrics, executes online statistical filtering with zero heap allocations on hot paths, and dispatches structured anomalies to central telemetry backends over HTTP.

## Architecture

The telemetry pipeline processes data through sequential stages designed for deterministic latency and bounded memory consumption:

```
[ Linux /proc & AI Collector ]
              │
              ▼
[ Streaming Anomaly Engine ] ── (Welford Variance + EWMA Baseline)
              │
              ▼
[ Deadband Suppressor & Ring Buffer ] ── (Hysteresis Filter + Pre-Trigger Window)
              │
              ▼
[ Bounded Outbox Queue ] ── (Configurable Drop Policies)
              │
              ▼
[ Resilient HTTP Dispatcher ] ── (Exponential Backoff + Jitter)
```

### Subsystems

* **internal/collector**: Zero-allocation metric scrapers parsing `/proc/stat` and `/proc/meminfo` via direct raw syscalls (`SYS_OPENAT`, `SYS_READ`, `SYS_CLOSE`) and stack buffers, alongside a concurrent synthetic AI inference generator (`AIGen`).
* **internal/engine**: Online statistical engine implementing Welford's algorithm for $O(1)$ running mean and variance computation, Exponential Moving Average (EWMA) for drift tracking, and a streaming Z-score outlier detector with outlier-resilient baseline updates.
* **internal/filter**: Deadband suppressor state machine preventing alert storms, a fixed-capacity circular ring buffer maintaining pre-trigger context for anomalous intervals, and a periodic heartbeat aggregator.
* **internal/outbox**: Thread-safe bounded queue with selectable overflow strategies (`DropOldest` vs `DropNewest`), context cancellation, and immediate garbage collection of evicted elements.
* **internal/transport**: HTTP/JSON telemetry dispatcher featuring retry loops with randomized jitter, exponential backoff, and graceful payload delivery.
* **internal/metrics**: Zero-dependency Prometheus text-format registry exposing runtime counters and gauges on `/metrics`.
* **internal/config**: Environment-driven runtime configuration with strict invariant validation.

## Configuration

ETD is configured through environment variables:

| Variable | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `ETD_LISTEN_ADDR` | string | `:8080` | Bind host:port for Prometheus `/metrics` and health endpoints |
| `ETD_SCRAPE_INTERVAL` | duration | `5s` | Polling frequency for host collectors (e.g. `100ms`, `1s`, `5s`) |
| `ETD_DETECTOR_MIN_SAMPLES` | int | `30` | Minimum Welford sample count before Z-score evaluation activates |
| `ETD_CPU_REPORT_MODE` | string | `percent` | CPU metric unit (`percent`, `ticks`, or `hertz`) |
| `ETD_LOG_LEVEL` | string | `info` | Structured logging verbosity (`debug`, `info`, `warn`, `error`) |
| `ETD_PROCFS_PATH` | string | `/proc` | Base procfs mount path for host metric collection (e.g. `/host/proc` in containers) |
| `ETD_TARGET_URL` | string | `http://localhost:8080/ingest/dummy` | Remote ingestion endpoint for dispatched alerts and heartbeats |

## Quickstart with Docker Compose

Deploy the daemon alongside a mock ingestion sink:

```bash
# Launch daemon and mock ingress sink
docker compose up -d --build

# Verify container status
docker compose ps

# Scrape Prometheus metrics
curl -s http://localhost:8080/metrics
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
    environment:
      - ETD_LISTEN_ADDR=:8080
      - ETD_SCRAPE_INTERVAL=100ms
      - ETD_DETECTOR_MIN_SAMPLES=30
      - ETD_CPU_REPORT_MODE=percent
      - ETD_LOG_LEVEL=debug
      - ETD_PROCFS_PATH=/host/proc
      - ETD_TARGET_URL=http://ingest-sink:80/post
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

## Performance Benchmarks

All hot paths in the collection and filtering engine operate with zero heap allocations.

### Internal Microbenchmarks (12th Gen Intel Core i7-12700H)

| Benchmark | Operations / sec | Latency | Memory / Op | Allocations / Op |
| :--- | :--- | :--- | :--- | :--- |
| `BenchmarkCollectCPU` | 276,558 ops | 3912 ns/op | 0 B/op | 0 allocs/op |
| `BenchmarkCollectMem` | 481,312 ops | 3472 ns/op | 0 B/op | 0 allocs/op |
| `BenchmarkAIGenNext` | 12,815,146 ops | 93.53 ns/op | 0 B/op | 0 allocs/op |
| `BenchmarkParseCPULine` | 15,286,612 ops | 78.93 ns/op | 0 B/op | 0 allocs/op |
| `BenchmarkWelfordUpdate` | 198,410,570 ops | 8.61 ns/op | 0 B/op | 0 allocs/op |
| `BenchmarkEWMAUpdate` | 417,299,035 ops | 4.60 ns/op | 0 B/op | 0 allocs/op |
| `BenchmarkZScore` | 95,897,695 ops | 12.61 ns/op | 0 B/op | 0 allocs/op |
| `BenchmarkSuppressorProcess` | 87,799,250 ops | 21.15 ns/op | 0 B/op | 0 allocs/op |
| `BenchmarkRingBufferPush` | 37,831,089 ops | 31.79 ns/op | 0 B/op | 0 allocs/op |
| `BenchmarkRingBufferSnapshot` | 7,325,864 ops | 166.60 ns/op | 0 B/op | 0 allocs/op |
| `BenchmarkHeartbeatObserve` | 67,028,605 ops | 17.01 ns/op | 0 B/op | 0 allocs/op |
| `BenchmarkOutboxContention` | 4,689,410 ops | 225.40 ns/op | 0 B/op | 0 allocs/op |

### Statistical Verification (`benchstat` N=10)

Measurements gathered inside an isolated Linux container on an Intel Core i7-12700H across 10 independent test executions:

| Benchmark | Latency (mean ± var) | Memory (B/op) | Allocs/op |
| :--- | :--- | :--- | :--- |
| `CollectCPU` | 3.904 µs ± 28% | 0 B/op ± 0% | 0 allocs/op ± 0% |
| `CollectMem` | 4.551 µs ± 1% | 0 B/op ± 0% | 0 allocs/op ± 0% |
| `AIGenNext` | 92.48 ns ± 1% | 0 B/op ± 0% | 0 allocs/op ± 0% |
| `ParseCPULine` | 77.36 ns ± 3% | 0 B/op ± 0% | 0 allocs/op ± 0% |
| `WelfordUpdate` | 8.343 ns ± 2% | 0 B/op ± 0% | 0 allocs/op ± 0% |
| `EWMAUpdate` | 4.519 ns ± 1% | 0 B/op ± 0% | 0 allocs/op ± 0% |
| `ZScore` | 12.24 ns ± 2% | 0 B/op ± 0% | 0 allocs/op ± 0% |
| `ZScoreDetect` | 20.68 ns ± 1% | 0 B/op ± 0% | 0 allocs/op ± 0% |

### Continuous Soak & Load Test (10-Minute Sustained Execution)

Stress tested via `hey -z 10m -c 50 http://localhost:8080/metrics`:

* **Sustained Throughput**: 103,041 requests/sec
* **Total Egress Data Transferred**: 85.1 GB
* **Latency Profile**:
	* Median (p50): 0.20 ms
	* 90th percentile (p90): 1.30 ms
	* 99th percentile (p99): 3.60 ms
	* Max Latency: 18.40 ms
* **Success Rate**: 100% (0 errors across 1,000,000+ status 200 responses)
* **Memory Boundedness (RSS)**: Stably constrained between 13.89 MiB and 17.55 MiB with zero GC memory leaks
* **Idle Resource Usage**: 0.38% CPU, 14.30 MiB RAM

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