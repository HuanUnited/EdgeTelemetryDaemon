package cgroup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func formatCPUStat(k uint64) []byte {
	usage := k * 10000
	periods := k * 100
	throttled := k
	throttledUsec := k * 500
	return []byte(fmt.Sprintf("usage_usec %d\nnr_periods %d\nnr_throttled %d\nthrottled_usec %d\n",
		usage, periods, throttled, throttledUsec))
}

func TestCFSThrottlingStress(t *testing.T) {
	dir := t.TempDir()
	statPath := filepath.Join(dir, "cpu.stat")

	initial := formatCPUStat(1)
	if err := os.WriteFile(statPath, initial, 0o644); err != nil {
		t.Fatalf("write initial cpu.stat: %v", err)
	}

	ctrl := NewController(dir)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	var (
		writesCompleted atomic.Uint64
		readsSuccessful atomic.Uint64
		readsFailed     atomic.Uint64
		firstErrOnce    sync.Once
		firstErr        error
		wg              sync.WaitGroup
	)

	recordErr := func(err error) {
		firstErrOnce.Do(func() {
			firstErr = err
		})
	}

	// Writer goroutine repeatedly updates cpu.stat with incrementing values.
	wg.Go(func() {
		var seq uint64 = 2
		for {
			select {
			case <-ctx.Done():
				return
			default:
				data := formatCPUStat(seq)
				tmpPath := filepath.Join(dir, fmt.Sprintf(".cpu.stat.tmp.%d", seq%4))
				if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
					continue
				}
				if err := os.Rename(tmpPath, statPath); err != nil {
					continue
				}
				writesCompleted.Add(1)
				seq++
			}
		}
	})

	const numReaders = 50
	for range numReaders {
		wg.Go(func() {
			for {
				select {
				case <-ctx.Done():
					return
				default:
					stat, err := ctrl.ReadCPUStat()
					if err != nil {
						readsFailed.Add(1)
						continue
					}

					readsSuccessful.Add(1)

					// Validate internal consistency of parsed counters:
					// NrThrottled must be non-zero since all writes use seq >= 1.
					if stat.NrThrottled == 0 {
						recordErr(fmt.Errorf("torn read: NrThrottled is 0, full stat: %+v", stat))
						return
					}
					// ThrottledUsec must equal NrThrottled * 500 based on generation schema.
					if stat.ThrottledUsec != stat.NrThrottled*500 {
						recordErr(fmt.Errorf("torn read: NrThrottled=%d, ThrottledUsec=%d",
							stat.NrThrottled, stat.ThrottledUsec))
						return
					}
					// NrPeriods must equal NrThrottled * 100 based on generation schema.
					if stat.NrPeriods != stat.NrThrottled*100 {
						recordErr(fmt.Errorf("torn read: NrThrottled=%d, NrPeriods=%d",
							stat.NrThrottled, stat.NrPeriods))
						return
					}
					// UsageUsec must equal NrPeriods * 100 based on generation schema.
					if stat.UsageUsec != stat.NrPeriods*100 {
						recordErr(fmt.Errorf("torn read: NrPeriods=%d, UsageUsec=%d",
							stat.NrPeriods, stat.UsageUsec))
						return
					}
				}
			}
		})
	}

	wg.Wait()

	if firstErr != nil {
		t.Fatalf("concurrent ReadCPUStat returned corrupt/torn data: %v", firstErr)
	}
	if readsSuccessful.Load() == 0 {
		t.Fatalf("no successful ReadCPUStat calls completed")
	}
	if writesCompleted.Load() == 0 {
		t.Fatalf("no fixture writes completed")
	}
}
