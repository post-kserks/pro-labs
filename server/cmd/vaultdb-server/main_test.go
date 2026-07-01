package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
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

func TestSetupLoggerDebug(t *testing.T) {
	logger := setupLogger("debug")
	if logger == nil {
		t.Fatal("setupLogger returned nil")
	}
}

func TestSetupLoggerInfo(t *testing.T) {
	logger := setupLogger("info")
	if logger == nil {
		t.Fatal("setupLogger returned nil")
	}
}

func TestSetupLoggerDefault(t *testing.T) {
	logger := setupLogger("")
	if logger == nil {
		t.Fatal("setupLogger returned nil")
	}
}

func TestGenerateToken(t *testing.T) {
	token, err := generateToken()
	if err != nil {
		t.Fatalf("generateToken() error: %v", err)
	}
	if !strings.HasPrefix(token, "vdb_sk_") {
		t.Errorf("token = %q, want prefix 'vdb_sk_'", token)
	}
	if len(token) != 55 { // "vdb_sk_" (7) + 48 hex chars (24 bytes)
		t.Errorf("token length = %d, want 55", len(token))
	}
}

func TestGenerateTokenUnique(t *testing.T) {
	tokens := make(map[string]bool)
	for i := 0; i < 100; i++ {
		token, err := generateToken()
		if err != nil {
			t.Fatalf("generateToken() error on iteration %d: %v", i, err)
		}
		if tokens[token] {
			t.Fatalf("duplicate token generated: %s", token)
		}
		tokens[token] = true
	}
}

func TestTokensFromEnvEmpty(t *testing.T) {
	os.Unsetenv("VAULTDB_API_TOKENS")
	tokens := tokensFromEnv()
	if tokens != nil {
		t.Errorf("tokensFromEnv() = %v, want nil for empty env", tokens)
	}
}

func TestTokensFromEnvSingle(t *testing.T) {
	t.Setenv("VAULTDB_API_TOKENS", "tok_abc123")
	tokens := tokensFromEnv()
	if len(tokens) != 1 {
		t.Fatalf("expected 1 token, got %d", len(tokens))
	}
	if _, ok := tokens["tok_abc123"]; !ok {
		t.Errorf("expected token 'tok_abc123' in map, got %v", tokens)
	}
}

func TestTokensFromEnvMultiple(t *testing.T) {
	t.Setenv("VAULTDB_API_TOKENS", "tok1,tok2,tok3")
	tokens := tokensFromEnv()
	if len(tokens) != 3 {
		t.Fatalf("expected 3 tokens, got %d", len(tokens))
	}
	for _, tok := range []string{"tok1", "tok2", "tok3"} {
		if _, ok := tokens[tok]; !ok {
			t.Errorf("expected token %q in map", tok)
		}
	}
}

func TestTokensFromEnvWithEmptyParts(t *testing.T) {
	t.Setenv("VAULTDB_API_TOKENS", "tok1,,tok2,")
	tokens := tokensFromEnv()
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens (skip empty), got %d", len(tokens))
	}
}

func TestTokensFromEnvLabels(t *testing.T) {
	t.Setenv("VAULTDB_API_TOKENS", "tok1,tok2")
	tokens := tokensFromEnv()
	if tokens["tok1"] != "env-token-1" {
		t.Errorf("label for tok1 = %q, want 'env-token-1'", tokens["tok1"])
	}
	if tokens["tok2"] != "env-token-2" {
		t.Errorf("label for tok2 = %q, want 'env-token-2'", tokens["tok2"])
	}
}
