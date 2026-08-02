package collector

import (
	"sync"
	"testing"
)

func TestAIGenDefaults(t *testing.T) {
	def := DefaultAIGenConfig()
	if def.BaseInferencesPerSec <= 0 {
		t.Errorf("BaseInferencesPerSec = %v, want > 0", def.BaseInferencesPerSec)
	}
	if def.Jitter < 0 || def.Jitter >= 1 {
		t.Errorf("Jitter = %v, want in [0, 1)", def.Jitter)
	}
}

func TestAIGenNextWithinJitter(t *testing.T) {
	cfg := AIGenConfig{BaseInferencesPerSec: 100, Jitter: 0.1}
	g := NewAIGen(cfg)

	const samples = 1000
	for i := 0; i < samples; i++ {
		s := g.Next()
		if s.InferencesPerSec < 100*0.9 || s.InferencesPerSec > 100*1.1 {
			t.Fatalf("InferencesPerSec = %v, want within [90, 110] (jitter 10%%)", s.InferencesPerSec)
		}
		if s.TokensPerSec <= 0 {
			t.Fatalf("TokensPerSec = %v, want > 0", s.TokensPerSec)
		}
		if s.BatchSize == 0 {
			t.Fatalf("BatchSize = 0, want >= 1")
		}
		if s.Timestamp.IsZero() {
			t.Fatalf("Timestamp is zero")
		}
	}
}

func TestAIGenDefaultsApplied(t *testing.T) {
	// Zero config must be normalised to the documented defaults.
	g := NewAIGen(AIGenConfig{})
	if g.cfg.BaseInferencesPerSec <= 0 {
		t.Errorf("cfg.BaseInferencesPerSec = %v after normalisation, want > 0", g.cfg.BaseInferencesPerSec)
	}
	if g.cfg.Jitter < 0 || g.cfg.Jitter >= 1 {
		t.Errorf("cfg.Jitter = %v after normalisation, want in [0, 1)", g.cfg.Jitter)
	}
}

func TestAIGenConcurrentSafe(t *testing.T) {
	g := NewAIGen(DefaultAIGenConfig())

	const goroutines = 8
	const perGoroutine = 2000

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				s := g.Next()
				if s.InferencesPerSec <= 0 {
					t.Errorf("InferencesPerSec = %v, want > 0", s.InferencesPerSec)
					return
				}
			}
		}()
	}
	wg.Wait()
}
