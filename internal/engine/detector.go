package engine

import "math"

// ZScoreDetector evaluates composite Z-scores across dual metrics (CPU and Memory)
// using Euclidean norm and EWMV-backed tracking.
type ZScoreDetector struct {
	alpha          float64
	alphaSlow      float64
	minSamples     uint64
	threshold      float64
	driftThreshold float64

	cpuStats StreamingStats
	memStats StreamingStats
	cpuSlow  EWMA
	memSlow  EWMA

	n          uint64
	lastCPU    float64
	lastMem    float64
	zscore     float64
	driftIndex float64
	drifting   bool
}

// NewZScoreDetector constructs a dual-metric anomaly detector.
func NewZScoreDetector(alphaFast, alphaSlow float64, minSamples uint64, threshold, driftThreshold float64) *ZScoreDetector {
	if alphaFast <= 0 {
		alphaFast = 0.5
	} else if alphaFast > 1 {
		alphaFast = 1.0
	}
	if alphaSlow <= 0 {
		alphaSlow = 0.5
	} else if alphaSlow > 1 {
		alphaSlow = 1.0
	}
	if alphaSlow >= alphaFast {
		alphaSlow = alphaFast * 0.1
	}
	if minSamples == 0 {
		minSamples = 30
	}
	if threshold <= 0 {
		threshold = 3.5
	}
	if driftThreshold <= 0 {
		driftThreshold = 0.15
	}

	return &ZScoreDetector{
		alpha:          alphaFast,
		alphaSlow:      alphaSlow,
		minSamples:     minSamples,
		threshold:      threshold,
		driftThreshold: driftThreshold,
		cpuStats:       NewStreamingStats(alphaFast),
		memStats:       NewStreamingStats(alphaFast),
		cpuSlow:        NewEWMA(alphaSlow),
		memSlow:        NewEWMA(alphaSlow),
		zscore:         math.NaN(),
	}
}

// Update feeds paired CPU and Memory observations to evaluate statistical deviation.
func (d *ZScoreDetector) Update(cpu, mem float64) bool {
	if math.IsNaN(cpu) || math.IsInf(cpu, 0) || math.IsNaN(mem) || math.IsInf(mem, 0) {
		return false
	}

	d.n++

	// 1. Evaluate Z-score using PRIOR state to prevent anomaly masking
	if d.n > d.minSamples {
		zCPU := calculateZScore(cpu, d.cpuStats.Mean(), d.cpuStats.StdDev())
		zMem := calculateZScore(mem, d.memStats.Mean(), d.memStats.StdDev())
		d.zscore = math.Hypot(zCPU, zMem)
	} else {
		d.zscore = 0
	}

	// 2. Update tracking statistics
	d.cpuStats.Update(cpu)
	d.memStats.Update(mem)
	d.cpuSlow.Update(cpu)
	d.memSlow.Update(mem)

	d.lastCPU = cpu
	d.lastMem = mem

	// 3. Compute Normalized Drift Divergence
	denomCPU := math.Max(1.0, math.Abs(d.cpuSlow.Value()))
	driftCPU := math.Abs(d.cpuStats.Mean()-d.cpuSlow.Value()) / denomCPU

	denomMem := math.Max(1.0, math.Abs(d.memSlow.Value()))
	driftMem := math.Abs(d.memStats.Mean()-d.memSlow.Value()) / denomMem

	// Use Chebyshev distance (max) for drift to preserve individual threshold semantics
	d.driftIndex = math.Max(driftCPU, driftMem)

	if d.n < d.minSamples {
		d.drifting = false
		return false
	}

	d.drifting = d.driftIndex > d.driftThreshold

	return d.zscore >= d.threshold
}

// Helper to keep Update complexity low and eliminate wasted assignments
func calculateZScore(val, mean, sd float64) float64 {
	if sd > 1e-12 && !math.IsNaN(sd) {
		return (val - mean) / sd
	}

	denom := math.Max(1.0, math.Abs(mean))
	return (val - mean) / denom
}

func (d *ZScoreDetector) ZScore() float64          { return d.zscore }
func (d *ZScoreDetector) Count() uint64            { return d.n }
func (d *ZScoreDetector) BaselineCPU() float64     { return d.cpuStats.Mean() }
func (d *ZScoreDetector) BaselineMem() float64     { return d.memStats.Mean() }
func (d *ZScoreDetector) SlowBaselineCPU() float64 { return d.cpuSlow.Value() }
func (d *ZScoreDetector) SlowBaselineMem() float64 { return d.memSlow.Value() }
func (d *ZScoreDetector) Threshold() float64       { return d.threshold }
func (d *ZScoreDetector) DriftThreshold() float64  { return d.driftThreshold }
func (d *ZScoreDetector) DriftIndex() float64      { return d.driftIndex }
func (d *ZScoreDetector) IsDrifting() bool         { return d.drifting }

func (d *ZScoreDetector) Reset() {
	d.cpuStats.Reset()
	d.memStats.Reset()
	d.cpuSlow.Reset()
	d.memSlow.Reset()
	d.n = 0
	d.lastCPU = 0
	d.lastMem = 0
	d.zscore = math.NaN()
	d.driftIndex = 0
	d.drifting = false
}
