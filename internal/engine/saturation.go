package engine

// IsSaturated reports whether host available memory has fallen below the
// configured safety floor percentage of total memory.
// It returns true when memAvailable/memTotal < floorPct/100.
// A memTotal of 0 is treated as not saturated to avoid divide-by-zero errors.
func IsSaturated(memTotal, memAvailable uint64, floorPct float64) bool {
	if memTotal == 0 {
		return false
	}
	return (float64(memAvailable) / float64(memTotal)) < (floorPct / 100.0)
}
