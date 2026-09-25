# Edge Telemetry Daemon (ETD)

[![Go Report Card](https://goreportcard.com/badge/github.com/HuanUnited/edgetelemetrydaemon)](https://goreportcard.com/report/github.com/HuanUnited/edgetelemetrydaemon)
[![Go Version](https://img.shields.io/github/go-mod/go-version/HuanUnited/edgetelemetrydaemon)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Edge Telemetry Daemon (ETD) is a high-throughput, low-footprint telemetry collector and closed-loop anomaly-driven resource governor built for Linux edge devices, robotics, and AI inference systems.

It acts as an **Edge Black-Box Flight Recorder**, solving the *Edge Telemetry Dilemma* (the tradeoff between cellular bandwidth costs and high-resolution observability). ETD idles at a low polling rate, maintaining a rolling in-memory context window. Upon detecting statistical anomalies or concept drift, it dynamically drops its polling interval to capture ultra-high-resolution telemetry, buffers the pre-incident context, and streams it via Unix Domain Sockets (UDS), HTTP Server-Sent Events (SSE), and outbound webhooks.

##  Mathematical Foundation

Unlike standard monitoring agents that rely on static thresholds or infinite-memory accumulators (which suffer from statistical calcification), ETD's detection engine is built on robust, streaming Applied Mathematics:

1. **Exponentially Weighted Moving Variance (EWMV)**
   Classical Welford variance degrades on non-stationary edge metrics. ETD uses paired EWMA instances with a configurable forgetting factor ($\alpha$) to compute variance over a fading temporal window.
2. **Multi-Metric Composite Euclidean Z-Score**
   CPU and Memory pressure are evaluated simultaneously using a continuous Euclidean norm to prevent anomaly masking: $Z_{\text{composite}} = \sqrt{Z_{\text{cpu}}^2 + Z_{\text{mem}}^2}$
3. **Dual-Horizon Concept Drift Tracking**
   ETD models long-term behavioral shifts using a discrete band-pass filter, computing divergence between a fast-reacting EWMA and a slow-tracking baseline.
4. **Relaxed Adaptive Sampling Controller**
   The polling interval $\tau(t)$ compresses proportionally to system instability, returning to rest via step-wise exponential relaxation to prevent control chattering.

##  Configuration

Configuration is environment-driven. Default values are optimized for edge environments.

| Variable | Default | Description |
| :--- | :--- | :--- |
| `ETD_LISTEN_ADDR` | `:8080` | Bind host:port for `/metrics` and `/events` endpoints. |
| `ETD_SOCKET_PATH` | `/tmp/etd.sock` | Filepath for the Unix Domain Socket IPC listener. |
| `ETD_SCRAPE_INTERVAL` | `5s` | *Initial* polling frequency; adapts automatically at runtime. |
| `ETD_RATE_TAU_MIN` | `50ms` | Fastest sampling interval, engaged under peak anomaly. |
| `ETD_RATE_TAU_MAX` | `5s` | Slowest sampling interval, engaged at rest. |
| `ETD_CGROUP_ROOT` | `""` *(Auto)* | Leave empty to auto-discover via `/proc/self/cgroup`. |
| `ETD_ENABLE_SYNTHETIC_WORKLOAD` | `false` | If `true`, injects simulated AI token throughput metrics. |
| `ETD_TARGET_URL` | *(None)* | Remote HTTP endpoint for webhook dispatches (optional). |
| `ETD_TARGET_AUTH_TOKEN` | *(None)* | Bearer token appended to webhook dispatches (optional). |
| `ETD_ENABLE_DEBUG_ENDPOINTS` | `false` | If `true`, enables the `/inject/anomaly` testing endpoint. |

##  Setup & Integration Guide

ETD can be run in **Telemetry-only Mode** (default, standalone monitoring) or **Active Governor Mode** (delegated cgroups, allowing ETD to throttle background processes).

### Method A: Standalone Telemetry (Quick Start)
The easiest way to get started. No root required. ETD will gracefully degrade to read-only mode and monitor the host.

```bash
# 1. Build the binary
make build

# 2. Run the daemon
./bin/edge-telemetry-daemon
```

### Method B: Active Governor (systemd cgroup v2)
To allow ETD to actively manage CPU quotas (Quiescent ⇄ Burst modes), run it under a delegated `systemd` scope so it has write access to `cpu.max`.

```bash
# Run with active cgroup governance
sudo systemd-run --unit=etd-agent --scope \
  -p Delegate=yes -p CPUAccounting=yes \
  ./bin/edge-telemetry-daemon
```
*Note: ETD will auto-discover its cgroup leaf and allocate 5% total CPU quota in quiescent mode, releasing to 100% when anomalies are detected.*

### Method C: Docker Compose
```bash
docker compose up -d --build
```
*Note: Due to Docker isolation, cgroup auto-discovery is disabled in the compose setup, defaulting to telemetry-only.*

---

##  Using the Telemetry Streams

ETD emits standard NDJSON payload streams via local Unix Sockets and HTTP SSE. This allows external Python scripts, `jq`, or sidecars to plug into the daemon instantly.

### 1. Unix Domain Socket (IPC)
Connect to the local socket to receive zero-latency JSON streams. The socket is locked down (`chmod 0600`) to the daemon owner.
```bash
nc -U /tmp/etd.sock | jq .
```

### 2. HTTP Server-Sent Events (SSE)
View real-time anomalies and pre-incident context windows in your terminal:
```bash
curl -N http://localhost:8080/events
```

### 3. Prometheus Metrics & Health
Standard instrumentation is exposed for scraping:
```bash
curl http://localhost:8080/metrics
curl http://localhost:8080/healthz
curl http://localhost:8080/version
```

### 4. Remote Webhook Dispatch
If `ETD_TARGET_URL` is set, ETD will dispatch payloads to it.
```bash
export ETD_TARGET_URL="https://api.yourdomain.com/ingest"
export ETD_TARGET_AUTH_TOKEN="super-secret-token"
./bin/edge-telemetry-daemon
```

### 5. Testing the Engine (Anomaly Injection)
To test the detection engine without artificially spiking your CPU, enable debug endpoints and hit the injection route:
```bash
export ETD_ENABLE_DEBUG_ENDPOINTS="true"
./bin/edge-telemetry-daemon &

# In a new terminal, trigger a simulated anomaly burst
curl -X POST http://localhost:8080/inject/anomaly
```
*Watch your `/events` stream to see the pre-context payload fire!*

##  Benchmarks & Performance

The codebase maintains strict performance constraints. Averaged over 1,000 iterations (`go test -bench=. -benchmem`), the critical path guarantees zero heap allocations:

| Component | Operation | Mean Latency | Memory / Alloc |
| :--- | :--- | :--- | :--- |
| **cgroup** | `ReadCPUStat` | ~2.5 µs | **0 B, 0 allocs/op** |
| **collector** | `CollectCPU` | ~1.9 µs | **0 B, 0 allocs/op** |
| **engine** | `EWMV Update` | ~8.4 ns | **0 B, 0 allocs/op** |
| **outbox** | `Push/Pop` | ~45.0 ns | **0 B, 0 allocs/op** |

