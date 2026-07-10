// Package ai provides embedding generation for SEMANTIC_MATCH and
// AI_EMBED. Real embeddings come from an external OpenAI-compatible API
// (OpenAI, Ollama, etc.); if AI is not configured, operations return a clear
// error instead of a silent mock result.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"strings"
	"time"
)

// Embedder generates a vector representation of text.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float64, error)
}

// HTTPEmbedder calls OpenAI / Ollama / any compatible embeddings API.
type HTTPEmbedder struct {
	Endpoint string // e.g. https://api.openai.com/v1/embeddings
	Model    string // e.g. text-embedding-3-small or nomic-embed-text
	APIKey   string
	Client   *http.Client
}

// NewHTTPEmbedder creates an embedder for OpenAI-compatible API.
func NewHTTPEmbedder(endpoint, model, apiKey string) *HTTPEmbedder {
	return &HTTPEmbedder{
		Endpoint: endpoint,
		Model:    model,
		APIKey:   apiKey,
		Client:   &http.Client{Timeout: 15 * time.Second},
	}
}

func (e *HTTPEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	const maxAttempts = 3
	var lastErr error

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 500ms, 1s, 2s
			backoff := time.Duration(500<<uint(attempt-1)) * time.Millisecond //nolint:gosec // attempt is bounded by maxAttempts=3

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		vec, err := e.doEmbed(ctx, text)
		if err == nil {
			return vec, nil
		}

		// Retry only for transient errors
		if isRetryable(err) {
			lastErr = err
			continue
		}

		// Non-retryable error (401, 400) — return immediately
		return nil, err
	}

	return nil, fmt.Errorf("embedding API failed after %d attempts: %w", maxAttempts, lastErr)
}

func (e *HTTPEmbedder) doEmbed(ctx context.Context, text string) ([]float64, error) {
	body, err := json.Marshal(map[string]interface{}{
		"model": e.Model,
		"input": text,
		// Ollama (/api/embeddings) uses "prompt" instead of "input";
		// the extra field is ignored by OpenAI.
		"prompt": text,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.APIKey)
	}

	resp, err := e.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{StatusCode: resp.StatusCode, Status: resp.Status}
	}

	// We support two response formats:
	//   OpenAI: {"data":[{"embedding":[...]}]}
	//   Ollama: {"embedding":[...]}
	var result struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Embedding []float64 `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("embedding API: decode response: %w", err)
	}

	if len(result.Data) > 0 && len(result.Data[0].Embedding) > 0 {
		return result.Data[0].Embedding, nil
	}
	if len(result.Embedding) > 0 {
		return result.Embedding, nil
	}
	return nil, fmt.Errorf("embedding API: empty embedding response")
}

// HTTPError represents an HTTP error response.
type HTTPError struct {
	StatusCode int
	Status     string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("embedding API: unexpected status %s", e.Status)
}

// isRetryable determines whether a retry should be attempted.
func isRetryable(err error) bool {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		// 429 (rate limit), 5xx (server errors) — retry
		return httpErr.StatusCode == 429 ||
			(httpErr.StatusCode >= 500 && httpErr.StatusCode < 600)
	}
	// Network errors — retry
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	return false
}

// NoopEmbedder — stub when AI is not configured. Explicitly returns an error
// instead of a silent mock result.
type NoopEmbedder struct{}

func (NoopEmbedder) Embed(_ context.Context, _ string) ([]float64, error) {
	return nil, fmt.Errorf(
		"AI embedding is not configured. " +
			"Set ai.provider/ai.endpoint/ai.model in vaultdb.yaml " +
			"(and VAULTDB_AI_API_KEY if required) to enable SEMANTIC_MATCH and AI_EMBED")
}

// MockEmbedder — deterministic keyword-based embedder for tests.
// Not an ML model and should not be used in production.
type MockEmbedder struct{}

func (MockEmbedder) Embed(_ context.Context, text string) ([]float64, error) {
	text = strings.ToLower(text)
	res := make([]float64, 8)

	if strings.Contains(text, "database") || strings.Contains(text, "sql") || strings.Contains(text, "storage") {
		res[0] = 1.0
	}
	if strings.Contains(text, "ai") || strings.Contains(text, "artificial") || strings.Contains(text, "intelligence") || strings.Contains(text, "neural") || strings.Contains(text, "network") {
		res[4] = 1.0
	}

	var h uint32
	for _, b := range []byte(text) {
		h = h*31 + uint32(b)
	}
	for i := range res {
		res[i] += math.Sin(float64(h)+float64(i)) * 0.1
	}
	return res, nil
}
