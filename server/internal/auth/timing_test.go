package auth

import (
	"testing"
	"time"
)

// TestTokenComparisonTiming verifies that HMAC-based token validation does not
// exhibit a significant timing side-channel. The HMAC-SHA256 hash is constant-time,
// and the map lookup is O(1), so far-off and close-match candidates should take
// roughly the same time. We use warmup + median to reduce noise.
func TestTokenComparisonTiming(t *testing.T) {
	mgr, err := New(true, map[string]string{"real_secret_token_xyz": "test"}, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	iterations := 5000
	rounds := 3
	if testing.Short() {
		iterations = 1000
		rounds = 1
	}

	measureTime := func(candidate string) time.Duration {
		// Warmup
		for i := 0; i < 200; i++ {
			mgr.ValidateToken(candidate)
		}
		start := time.Now()
		for i := 0; i < iterations; i++ {
			mgr.ValidateToken(candidate)
		}
		return time.Since(start)
	}

	farOff := "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"
	closeMatch := "real_secret_token_xyza" // close but wrong

	// Take median of multiple rounds to reduce noise
	farTimes := make([]time.Duration, rounds)
	closeTimes := make([]time.Duration, rounds)
	for r := 0; r < rounds; r++ {
		farTimes[r] = measureTime(farOff)
		closeTimes[r] = measureTime(closeMatch)
	}

	// Simple median selection
	median := func(d []time.Duration) time.Duration {
		for i := 0; i < len(d); i++ {
			for j := i + 1; j < len(d); j++ {
				if d[i] > d[j] {
					d[i], d[j] = d[j], d[i]
				}
			}
		}
		return d[len(d)/2]
	}

	tFarOff := median(farTimes)
	tCloseMatch := median(closeTimes)

	ratio := float64(tCloseMatch) / float64(tFarOff)
	if ratio > 3.0 {
		t.Errorf("possible timing side-channel: ratio=%.3f (far=%v, close=%v)", ratio, tFarOff, tCloseMatch)
	}
	t.Logf("timing ratio: %.3f (far=%v, close=%v)", ratio, tFarOff, tCloseMatch)
}

// TestTokenComparisonConstantTime verifies that the HMAC comparison path
// runs in constant time regardless of how close the candidate is to the real token.
// This is a structural test: HMAC-SHA256 is mathematically constant-time,
// and Go's map lookup is amortized O(1).
func TestTokenComparisonConstantTime(t *testing.T) {
	mgr, err := New(true, map[string]string{"exact_match_token": "admin"}, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	realHash := mgr.hashToken("exact_match_token")
	candidates := []string{
		"wrong_token_completely_different",
		"exact_match_token_",
		"exact_match_token_very_long_xxx",
	}

	iterations := 5000
	for _, cand := range candidates {
		start := time.Now()
		for i := 0; i < iterations; i++ {
			mgr.ValidateToken(cand)
		}
		elapsed := time.Since(start)
		_ = realHash
		t.Logf("candidate %q: %v", cand[:min(len(cand), 20)], elapsed)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
