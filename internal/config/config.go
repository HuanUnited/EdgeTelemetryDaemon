package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Default configuration values.
const (
	DefaultListenAddr     = ":8080"
	DefaultScrapeInterval = 5 * time.Second
	DefaultCPUReportMode  = "percent"
	DefaultLogLevel       = "info"
	DefaultRateTauMin     = 50 * time.Millisecond
	DefaultRateTauMax     = 5 * time.Second
	DefaultRateTheta      = 3.5
	DefaultCgroupRoot     = "" // Empty triggers auto-discovery
	DefaultSocketPath     = "/tmp/etd.sock"
)

// Config holds the runtime configuration for the daemon. All fields are
// treated as immutable after construction; validate() is the single place
// where cross-field invariants are enforced.
type Config struct {
	ListenAddr              string
	ScrapeInterval          time.Duration
	CPUReportMode           string
	LogLevel                string
	TargetURL               string
	DetectorMinSamples      uint64
	ProcfsPath              string
	RateTauMin              time.Duration
	RateTauMax              time.Duration
	RateTheta               float64
	CgroupRoot              string
	SocketPath              string
	EnableSyntheticWorkload bool
}

// Load reads configuration from environment variables, applies defaults for
// any value that is not set, and validates the resulting configuration.
func Load() (Config, error) {
	cfg := Config{
		ListenAddr:              strings.TrimSpace(getenv("ETD_LISTEN_ADDR", DefaultListenAddr)),
		ScrapeInterval:          getenvDuration("ETD_SCRAPE_INTERVAL", DefaultScrapeInterval),
		CPUReportMode:           getenv("ETD_CPU_REPORT_MODE", DefaultCPUReportMode),
		LogLevel:                getenv("ETD_LOG_LEVEL", DefaultLogLevel),
		TargetURL:               getenv("ETD_TARGET_URL", "http://localhost:8080/ingest/dummy"),
		DetectorMinSamples:      uint64(getenvInt("ETD_DETECTOR_MIN_SAMPLES", 30)),
		ProcfsPath:              getenv("ETD_PROCFS_PATH", "/proc"),
		RateTauMin:              getenvDuration("ETD_RATE_TAU_MIN", DefaultRateTauMin),
		RateTauMax:              getenvDuration("ETD_RATE_TAU_MAX", DefaultRateTauMax),
		RateTheta:               getenvFloat("ETD_RATE_THETA", DefaultRateTheta),
		CgroupRoot:              getenv("ETD_CGROUP_ROOT", DefaultCgroupRoot),
		SocketPath:              getenv("ETD_SOCKET_PATH", DefaultSocketPath),
		EnableSyntheticWorkload: getenvBool("ETD_ENABLE_SYNTHETIC_WORKLOAD", false),
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

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
	if c.RateTauMin >= c.RateTauMax {
		return fmt.Errorf("config: rate tau min must be less than rate tau max, got min=%s max=%s", c.RateTauMin, c.RateTauMax)
	}
	if c.RateTheta <= 0 {
		return fmt.Errorf("config: rate theta must be positive, got %f", c.RateTheta)
	}
	return nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

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

func getenvFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func getenvBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}
