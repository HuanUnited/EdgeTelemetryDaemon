package metrics

import "testing"

func BenchmarkMetricGaugeSet(b *testing.B) {
	reg := NewRegistry()
	g := reg.NewGauge("bench_gauge_set", "gauge help")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g.Set(uint64(i))
	}
}

func BenchmarkMetricCounterSet(b *testing.B) {
	reg := NewRegistry()
	c := reg.NewCounter("bench_counter_set", "counter help")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Set(uint64(i))
	}
}

func BenchmarkMetricGaugeAdd(b *testing.B) {
	reg := NewRegistry()
	g := reg.NewGauge("bench_gauge_add", "gauge help")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g.Add(1)
	}
}

func BenchmarkMetricCounterAdd(b *testing.B) {
	reg := NewRegistry()
	c := reg.NewCounter("bench_counter_add", "counter help")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Add(1)
	}
}

func TestMetricSetAlloc(t *testing.T) {
	reg := NewRegistry()
	g := reg.NewGauge("test_gauge_set", "gauge help")
	c := reg.NewCounter("test_counter_set", "counter help")

	const runs = 1000
	allocsG := testing.AllocsPerRun(runs, func() {
		g.Set(42)
	})
	if allocsG != 0 {
		t.Fatalf("Metric.Set (gauge) allocated %v times, want 0", allocsG)
	}

	allocsC := testing.AllocsPerRun(runs, func() {
		c.Set(42)
	})
	if allocsC != 0 {
		t.Fatalf("Metric.Set (counter) allocated %v times, want 0", allocsC)
	}
}

func TestMetricAddAlloc(t *testing.T) {
	reg := NewRegistry()
	c := reg.NewCounter("test_counter_add", "counter help")
	g := reg.NewGauge("test_gauge_add", "gauge help")

	const runs = 1000
	allocsC := testing.AllocsPerRun(runs, func() {
		c.Add(5)
	})
	if allocsC != 0 {
		t.Fatalf("Metric.Add (counter) allocated %v times, want 0", allocsC)
	}

	allocsG := testing.AllocsPerRun(runs, func() {
		g.Add(5)
	})
	if allocsG != 0 {
		t.Fatalf("Metric.Add (gauge) allocated %v times, want 0", allocsG)
	}
}
