// Package config provides typed, validated configuration for the Edge AI
// Telemetry Daemon.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Default configuration values. These are kept exported so that callers that
// do not need full CLI/env parsing can still rely on a sane baseline.
const (
	DefaultListenAddr     = ":8080"
	DefaultScrapeInterval = 5 * time.Second
	DefaultCPUReportMode  = "percent"
	DefaultLogLevel       = "info"
)

// Config holds the runtime configuration for the daemon. All fields are
// treated as immutable after construction; validate() is the single place
// where cross-field invariants are enforced.
type Config struct {
	// ListenAddr is the host:port the metrics endpoint binds to.
	ListenAddr string

	// ScrapeInterval controls how frequently the collectors sample the host.
	ScrapeInterval time.Duration

	// CPUReportMode selects how CPU utilisation is reported. One of
	// "percent" (default), "ticks", or "hertz".
	CPUReportMode string

	// LogLevel is the slog level name: "debug", "info", "warn", or "error".
	LogLevel string

	// TargetURL is the ingest target of the daemon
	TargetURL string

	// DetectorMinSamples samples before detector activates
	DetectorMinSamples uint64

	// ProcfsPath ensures runtime procfs overrides are validated and accessible
	ProcfsPath string
}

// Load reads configuration from environment variables, applies defaults for
// any value that is not set, and validates the resulting configuration.
//
// Recognised environment variables:
//
//	ETD_LISTEN_ADDR      host:port to bind (default ":8080")
//	ETD_SCRAPE_INTERVAL  duration string, e.g. "5s" (default "5s")
//	ETD_CPU_REPORT_MODE  "percent" | "ticks" | "hertz" (default "percent")
//	ETD_LOG_LEVEL        "debug" | "info" | "warn" | "error" (default "info")
func Load() (Config, error) {
	cfg := Config{
		ListenAddr:         strings.TrimSpace(getenv("ETD_LISTEN_ADDR", DefaultListenAddr)),
		ScrapeInterval:     getenvDuration("ETD_SCRAPE_INTERVAL", DefaultScrapeInterval),
		CPUReportMode:      getenv("ETD_CPU_REPORT_MODE", DefaultCPUReportMode),
		LogLevel:           getenv("ETD_LOG_LEVEL", DefaultLogLevel),
		TargetURL:          getenv("ETD_TARGET_URL", "http://localhost:8080/ingest/dummy"),
		DetectorMinSamples: uint64(getenvInt("ETD_DETECTOR_MIN_SAMPLES", 30)),
		ProcfsPath:         getenv("ETD_PROCFS_PATH", "/proc"),
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// validate enforces all cross-field invariants. It is invoked once at Load
// time; Config values are immutable afterwards.
func (c Config) validate() error {
	if c.ListenAddr == "" {
		return fmt.Errorf("config: listen address must not be empty")
	}
	if c.ScrapeInterval <= 0 {
		return fmt.Errorf("config: scrape interval must be positive, got %s", c.ScrapeInterval)
	}
	switch c.CPUReportMode {
	case "percent", "ticks", "hertz":
	default:
		return fmt.Errorf("config: unsupported CPU report mode %q (want percent, ticks, or hertz)", c.CPUReportMode)
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("config: unsupported log level %q (want debug, info, warn, or error)", c.LogLevel)
	}
	return nil
}

// getenv returns the value of the environment variable named by key, or def
// when the variable is unset or empty.
func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// getenvDuration parses the environment variable named by key as a duration,
// falling back to def when unset or unparseable.
func getenvDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil && i > 0 {
			return i
		}
	}
	return def
}
