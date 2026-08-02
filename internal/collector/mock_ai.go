package collector

import (
	"math/rand/v2"
	"sync"
	"time"
)

// AIGenConfig configures the synthetic AI inference workload generator.
type AIGenConfig struct {
	// BaseInferencesPerSec is the baseline rate of completed inferences.
	BaseInferencesPerSec float64
	// Jitter is the maximum proportional deviation from the base rate on any
	// single sample, in [0, 1).
	Jitter float64
}

// DefaultAIGenConfig returns the baseline generator configuration. Use it as a
// starting point before overriding individual fields.
func DefaultAIGenConfig() AIGenConfig {
	return AIGenConfig{
		BaseInferencesPerSec: 12.0,
		Jitter:               0.25,
	}
}

// AISample is one telemetry snapshot produced by the generator.
type AISample struct {
	// Timestamp marks when the sample was produced.
	Timestamp time.Time
	// InferencesPerSec is the (synthetic) rate of completed inferences.
	InferencesPerSec float64
	// TokensPerSec is the (synthetic) output token throughput.
	TokensPerSec float64
	// BatchSize is the (synthetic) number of requests in flight.
	BatchSize uint32
}

// AIGen is a stateful generator of synthetic AI inference metrics. It is safe
// for concurrent use by multiple goroutines.
//
// The zero value is not usable; construct instances via NewAIGen.
type AIGen struct {
	cfg  AIGenConfig
	mu   sync.Mutex
	rand *rand.Rand
	seq  uint64
}

// NewAIGen builds an AIGen with the given configuration. Invalid fields are
// replaced with the defaults from DefaultAIGenConfig.
func NewAIGen(cfg AIGenConfig) *AIGen {
	def := DefaultAIGenConfig()
	if cfg.BaseInferencesPerSec <= 0 {
		cfg.BaseInferencesPerSec = def.BaseInferencesPerSec
	}
	if cfg.Jitter < 0 || cfg.Jitter >= 1 {
		cfg.Jitter = def.Jitter
	}
	seq := uint64(time.Now().UnixNano())
	return &AIGen{
		cfg:  cfg,
		rand: rand.New(rand.NewPCG(seq, ^seq)),
		seq:  seq,
	}
}

// Next returns a new synthetic sample. It holds the internal mutex only while
// drawing from the RNG; no heap allocations occur on the call path.
func (g *AIGen) Next() AISample {
	g.mu.Lock()
	// jitter in [1-Jitter, 1+Jitter)
	j := 1.0 + (g.rand.Float64()*2-1.0)*g.cfg.Jitter
	rate := g.cfg.BaseInferencesPerSec * j
	tokens := rate * (0.8 + g.rand.Float64()*0.4) // 80-120 tokens per inference
	batch := uint32(1 + g.rand.Uint32N(16))
	now := time.Now()
	g.mu.Unlock()

	return AISample{
		Timestamp:        now,
		InferencesPerSec: rate,
		TokensPerSec:     tokens,
		BatchSize:        batch,
	}
}
