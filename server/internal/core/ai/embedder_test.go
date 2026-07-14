package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestNoopEmbedder(t *testing.T) {
	var emb NoopEmbedder
	_, err := emb.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error from NoopEmbedder")
	}
	if err.Error() == "" {
		t.Fatal("expected non-empty error message")
	}
}

func TestMockEmbedder(t *testing.T) {
	var emb MockEmbedder
	ctx := context.Background()

	vec1, err := emb.Embed(ctx, "database systems")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vec1) == 0 {
		t.Fatal("expected non-empty vector")
	}

	vec2, err := emb.Embed(ctx, "database systems")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for i := range vec1 {
		if vec1[i] != vec2[i] {
			t.Errorf("index %d: expected same value for same input, got %f vs %f", i, vec1[i], vec2[i])
		}
	}
}

func TestMockEmbedderDifferentInput(t *testing.T) {
	var emb MockEmbedder
	ctx := context.Background()

	vec1, err := emb.Embed(ctx, "database storage")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	vec2, err := emb.Embed(ctx, "artificial intelligence neural network")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	same := true
	for i := range vec1 {
		if vec1[i] != vec2[i] {
			same = false
			break
		}
	}
	if same {
		t.Error("expected different vectors for different inputs")
	}

	if vec1[0] < 0.9 {
		t.Errorf("expected vec1[0] near 1.0 for 'database storage', got %f", vec1[0])
	}
	if vec2[4] < 0.9 {
		t.Errorf("expected vec2[4] near 1.0 for 'artificial intelligence neural network', got %f", vec2[4])
	}
}

func TestHTTPEmbedderRetry(t *testing.T) {
	var callCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&callCount, 1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":"internal"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{
				{"embedding": []float64{0.1, 0.2, 0.3}},
			},
		})
	}))
	defer server.Close()

	emb := NewHTTPEmbedder(server.URL+"/v1/embeddings", "test-model", "test-key")
	vec, err := emb.Embed(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("unexpected error after retries: %v", err)
	}
	if len(vec) != 3 {
		t.Fatalf("expected 3-dimensional vector, got %d", len(vec))
	}

	count := atomic.LoadInt32(&callCount)
	if count != 3 {
		t.Errorf("expected 3 calls (2 retries), got %d", count)
	}
}

func TestHTTPEmbedderNoRetry(t *testing.T) {
	var callCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"bad request"}`))
	}))
	defer server.Close()

	emb := NewHTTPEmbedder(server.URL+"/v1/embeddings", "test-model", "test-key")
	_, err := emb.Embed(context.Background(), "hello world")
	if err == nil {
		t.Fatal("expected error for 400 response")
	}

	count := atomic.LoadInt32(&callCount)
	if count != 1 {
		t.Errorf("expected 1 call for 400 error (no retry), got %d", count)
	}
}
