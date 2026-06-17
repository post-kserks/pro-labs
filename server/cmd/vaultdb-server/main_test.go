package main

import (
	"bufio"
	"fmt"
	"net"
	"testing"
	"time"
)

func TestConnectionRateLimiter(t *testing.T) {
	limiter := NewConnectionRateLimiter(10, 5)
	if limiter.rate != 10 {
		t.Errorf("rate = %v, want 10", limiter.rate)
	}
	if limiter.maxTokens != 5 {
		t.Errorf("maxTokens = %v, want 5", limiter.maxTokens)
	}
	if limiter.tokens != 5 {
		t.Errorf("tokens = %v, want 5", limiter.tokens)
	}

	for i := 0; i < 5; i++ {
		if !limiter.Allow() {
			t.Fatalf("Allow() = false on request %d, want true", i+1)
		}
	}

	if limiter.Allow() {
		t.Error("Allow() = true after burst exhausted, want false")
	}
}

func TestConnectionRateLimiterTokenRefill(t *testing.T) {
	limiter := NewConnectionRateLimiter(100, 1)

	limiter.Allow()

	limiter.lastTime = time.Now().Add(-100 * time.Millisecond)

	if !limiter.Allow() {
		t.Error("Allow() = false after refill period, want true")
	}
}

func TestConnectionRateLimiterBurstCap(t *testing.T) {
	limiter := NewConnectionRateLimiter(1000, 5)

	limiter.lastTime = time.Now().Add(-10 * time.Second)

	if !limiter.Allow() {
		t.Error("Allow() = false, want true")
	}

	if limiter.tokens > limiter.maxTokens {
		t.Errorf("tokens = %v exceeded maxTokens = %v", limiter.tokens, limiter.maxTokens)
	}
}

func TestScannerBufferSize(t *testing.T) {
	tests := []struct {
		name           string
		maxRequestSize int
		payloadSize    int
		expectTruncate bool
	}{
		{
			name:           "small payload within old 1MB limit",
			maxRequestSize: 64 * 1024 * 1024,
			payloadSize:    512 * 1024,
			expectTruncate: false,
		},
		{
			name:           "payload between 1MB and 64MB would be truncated with old limit",
			maxRequestSize: 64 * 1024 * 1024,
			payloadSize:    2 * 1024 * 1024,
			expectTruncate: false,
		},
		{
			name:           "payload exceeds maxRequestSize",
			maxRequestSize: 1 * 1024 * 1024,
			payloadSize:    2 * 1024 * 1024,
			expectTruncate: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, client := net.Pipe()

			payload := fmt.Sprintf(`{"id":"1","method":"ping","params":{"data":"%s"}}`, string(make([]byte, tt.payloadSize)))

			go func() {
				defer server.Close()
				fmt.Fprintf(server, "%s\n", payload)
			}()

			scanner := bufio.NewScanner(client)
			scanner.Buffer(make([]byte, 0, 64*1024), tt.maxRequestSize)

			scanned := scanner.Scan()
			client.Close()

			if tt.expectTruncate {
				if scanned {
					got := scanner.Bytes()
					t.Errorf("expected scanner to reject payload but got %d bytes", len(got))
				}
			} else {
				if !scanned {
					t.Fatalf("scanner.Scan() returned false: %v", scanner.Err())
				}
				got := scanner.Bytes()
				if len(got) != len(payload) {
					t.Errorf("payload size = %d, want %d", len(got), len(payload))
				}
			}
		})
	}
}
