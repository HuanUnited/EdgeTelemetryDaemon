package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("ETD_LISTEN_ADDR", "")
	t.Setenv("ETD_SCRAPE_INTERVAL", "")
	t.Setenv("ETD_CPU_REPORT_MODE", "")
	t.Setenv("ETD_LOG_LEVEL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() with defaults returned error: %v", err)
	}
	if cfg.ListenAddr != DefaultListenAddr {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, DefaultListenAddr)
	}
	if cfg.ScrapeInterval != DefaultScrapeInterval {
		t.Errorf("ScrapeInterval = %v, want %v", cfg.ScrapeInterval, DefaultScrapeInterval)
	}
	if cfg.CPUReportMode != DefaultCPUReportMode {
		t.Errorf("CPUReportMode = %q, want %q", cfg.CPUReportMode, DefaultCPUReportMode)
	}
	if cfg.LogLevel != DefaultLogLevel {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, DefaultLogLevel)
	}
}

func TestLoadFromEnv(t *testing.T) {
	t.Setenv("ETD_LISTEN_ADDR", "127.0.0.1:9999")
	t.Setenv("ETD_SCRAPE_INTERVAL", "250ms")
	t.Setenv("ETD_CPU_REPORT_MODE", "ticks")
	t.Setenv("ETD_LOG_LEVEL", "debug")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.ListenAddr != "127.0.0.1:9999" {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, "127.0.0.1:9999")
	}
	if cfg.ScrapeInterval != 250*time.Millisecond {
		t.Errorf("ScrapeInterval = %v, want %v", cfg.ScrapeInterval, 250*time.Millisecond)
	}
	if cfg.CPUReportMode != "ticks" {
		t.Errorf("CPUReportMode = %q, want %q", cfg.CPUReportMode, "ticks")
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "debug")
	}
}

func TestLoadInvalid(t *testing.T) {
	tests := []struct {
		name  string
		env   map[string]string
		valid bool
	}{
		{
			name: "empty listen addr",
			env:  map[string]string{"ETD_LISTEN_ADDR": " "},
		},
		{
			name: "invalid cpu mode",
			env:  map[string]string{"ETD_CPU_REPORT_MODE": "bogus"},
		},
		{
			name: "invalid log level",
			env:  map[string]string{"ETD_LOG_LEVEL": "verbose"},
		},
		{
			name:  "all valid",
			env:   map[string]string{"ETD_LOG_LEVEL": "warn", "ETD_CPU_REPORT_MODE": "hertz"},
			valid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Force defaults first, then overlay the test's overrides.
			t.Setenv("ETD_LISTEN_ADDR", DefaultListenAddr)
			t.Setenv("ETD_SCRAPE_INTERVAL", "5s")
			t.Setenv("ETD_CPU_REPORT_MODE", DefaultCPUReportMode)
			t.Setenv("ETD_LOG_LEVEL", DefaultLogLevel)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			_, err := Load()
			if tt.valid && err != nil {
				t.Fatalf("Load() = unexpected error %v", err)
			}
			if !tt.valid && err == nil {
				t.Fatalf("Load() = nil error, want validation failure")
			}
		})
	}
}

func TestInvalidScrapeIntervalFallsBack(t *testing.T) {
	t.Setenv("ETD_LISTEN_ADDR", "")
	t.Setenv("ETD_SCRAPE_INTERVAL", "not-a-duration")
	t.Setenv("ETD_CPU_REPORT_MODE", "")
	t.Setenv("ETD_LOG_LEVEL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.ScrapeInterval != DefaultScrapeInterval {
		t.Errorf("ScrapeInterval = %v, want fallback %v", cfg.ScrapeInterval, DefaultScrapeInterval)
	}
}
