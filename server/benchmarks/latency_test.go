package benchmarks

import (
	"sync"
	"testing"
	"time"
)

func TestLatencyTrackerCalculateDeterministic(t *testing.T) {
	lt := NewLatencyTracker()
	for i := 1; i <= 10000; i++ {
		lt.RecordNS(int64(i))
	}

	summary := lt.Calculate()

	if summary.TotalOps != 10000 {
		t.Errorf("expected TotalOps=10000, got %d", summary.TotalOps)
	}
	if summary.Avg != 5000 {
		t.Errorf("expected Avg=5000, got %d", summary.Avg)
	}
	if summary.P50 != 5001 {
		t.Errorf("expected P50=5001, got %d", summary.P50)
	}
	if summary.P95 != 9501 {
		t.Errorf("expected P95=9501, got %d", summary.P95)
	}
	if summary.P99 != 9901 {
		t.Errorf("expected P99=9901, got %d", summary.P99)
	}
	if summary.P999 != 9991 {
		t.Errorf("expected P999=9991, got %d", summary.P999)
	}
	if summary.Max != 10000 {
		t.Errorf("expected Max=10000, got %d", summary.Max)
	}

	// Verify exact string output format
	expectedStr := "Latency Summary:\n  Avg:    5000 ns\n  p50:    5001 ns\n  p95:    9501 ns\n  p99:    9901 ns\n  p99.9:  9991 ns"
	if summary.String() != expectedStr {
		t.Errorf("expected String():\n%s\ngot:\n%s", expectedStr, summary.String())
	}
}

func TestLatencyTrackerEmpty(t *testing.T) {
	lt := NewLatencyTracker()
	summary := lt.Calculate()
	if summary.TotalOps != 0 || summary.Avg != 0 || summary.P50 != 0 {
		t.Errorf("expected zero summary for empty tracker, got %+v", summary)
	}
}

func TestLatencyTrackerConcurrentRecordAndMerge(t *testing.T) {
	globalTracker := NewLatencyTracker()
	const numGoroutines = 10
	const opsPerGoroutine = 500

	var wg sync.WaitGroup
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			workerTracker := NewLatencyTracker()
			for j := 0; j < opsPerGoroutine; j++ {
				ns := int64(workerID*opsPerGoroutine + j + 1)
				workerTracker.Record(time.Duration(ns) * time.Nanosecond)
				// Also test concurrent direct RecordNS on globalTracker
				if j%2 == 0 {
					globalTracker.RecordNS(ns)
				}
			}
			// Concurrently merge workerTracker into globalTracker
			globalTracker.Merge(workerTracker)
		}(i)
	}

	wg.Wait()

	summary := globalTracker.Calculate()
	expectedOps := numGoroutines*opsPerGoroutine + (numGoroutines * opsPerGoroutine / 2)
	if summary.TotalOps != expectedOps {
		t.Errorf("expected TotalOps=%d, got %d", expectedOps, summary.TotalOps)
	}
	if summary.Max <= 0 {
		t.Errorf("expected positive Max, got %d", summary.Max)
	}
}
