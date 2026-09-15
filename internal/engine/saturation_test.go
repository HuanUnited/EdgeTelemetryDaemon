package engine

import "testing"

func TestAbsoluteSaturationGuard(t *testing.T) {
	tests := []struct {
		name         string
		memTotal     uint64
		memAvailable uint64
		floorPct     float64
		want         bool
	}{
		{
			name:         "saturated below floor 5pct",
			memTotal:     8_000_000,
			memAvailable: 350_000,
			floorPct:     5.0,
			want:         true,
		},
		{
			name:         "healthy above floor 5pct",
			memTotal:     8_000_000,
			memAvailable: 6_000_000,
			floorPct:     5.0,
			want:         false,
		},
		{
			name:         "zero memTotal guard against div zero",
			memTotal:     0,
			memAvailable: 0,
			floorPct:     5.0,
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsSaturated(tt.memTotal, tt.memAvailable, tt.floorPct)
			if got != tt.want {
				t.Errorf("IsSaturated(%d, %d, %v) = %v, want %v",
					tt.memTotal, tt.memAvailable, tt.floorPct, got, tt.want)
			}
		})
	}
}
