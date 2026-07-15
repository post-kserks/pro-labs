package benchmarks

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// LatencyTracker collects latency durations in nanoseconds in a thread-safe manner.
type LatencyTracker struct {
	mu        sync.Mutex
	latencies []int64
}

// NewLatencyTracker creates and returns a new initialized LatencyTracker.
func NewLatencyTracker() *LatencyTracker {
	return &LatencyTracker{
		latencies: make([]int64, 0, 1024),
	}
}

// Record records a latency duration.
func (lt *LatencyTracker) Record(d time.Duration) {
	lt.RecordNS(d.Nanoseconds())
}

// RecordNS records a latency duration directly in nanoseconds.
func (lt *LatencyTracker) RecordNS(ns int64) {
	lt.mu.Lock()
	lt.latencies = append(lt.latencies, ns)
	lt.mu.Unlock()
}

// Merge cleanly combines all recorded latencies from another tracker without
// lock contention on every single query operation.
func (lt *LatencyTracker) Merge(other *LatencyTracker) {
	if lt == nil || other == nil || lt == other {
		return
	}

	other.mu.Lock()
	if len(other.latencies) == 0 {
		other.mu.Unlock()
		return
	}
	otherLatencies := make([]int64, len(other.latencies))
	copy(otherLatencies, other.latencies)
	other.mu.Unlock()

	lt.mu.Lock()
	lt.latencies = append(lt.latencies, otherLatencies...)
	lt.mu.Unlock()
}

// LatencySummary holds aggregated statistical latency metrics.
type LatencySummary struct {
	TotalOps int
	Avg      int64
	P50      int64
	P95      int64
	P99      int64
	P999     int64
	Max      int64
}

// Calculate computes average and exact percentiles over all recorded latencies.
func (lt *LatencyTracker) Calculate() LatencySummary {
	if lt == nil {
		return LatencySummary{}
	}

	lt.mu.Lock()
	n := len(lt.latencies)
	if n == 0 {
		lt.mu.Unlock()
		return LatencySummary{}
	}
	sorted := make([]int64, n)
	copy(sorted, lt.latencies)
	lt.mu.Unlock()

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})

	var sum int64
	for _, v := range sorted {
		sum += v
	}
	avg := sum / int64(n)

	return LatencySummary{
		TotalOps: n,
		Avg:      avg,
		P50:      sorted[n*50/100],
		P95:      sorted[n*95/100],
		P99:      sorted[n*99/100],
		P999:     sorted[n*999/1000],
		Max:      sorted[n-1],
	}
}

// String formats the LatencySummary exactly as specified.
func (s LatencySummary) String() string {
	return fmt.Sprintf("Latency Summary:\n  Avg:    %d ns\n  p50:    %d ns\n  p95:    %d ns\n  p99:    %d ns\n  p99.9:  %d ns",
		s.Avg, s.P50, s.P95, s.P99, s.P999)
}

// PrintSummary computes the latency summary and outputs it formatted to standard output.
func (lt *LatencyTracker) PrintSummary(title string) {
	s := lt.Calculate()
	if title == "" {
		title = "Latency Summary:"
	}
	fmt.Println(title)
	fmt.Printf("  Avg:    %d ns\n", s.Avg)
	fmt.Printf("  p50:    %d ns\n", s.P50)
	fmt.Printf("  p95:    %d ns\n", s.P95)
	fmt.Printf("  p99:    %d ns\n", s.P99)
	fmt.Printf("  p99.9:  %d ns\n", s.P999)
}
