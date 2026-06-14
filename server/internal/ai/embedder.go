// Package ai предоставляет генерацию эмбеддингов для SEMANTIC_MATCH и
// AI_EMBED. Реальные эмбеддинги приходят из внешнего OpenAI-совместимого API
// (OpenAI, Ollama и т.п.); если AI не настроен, операции возвращают понятную
// ошибку вместо тихого mock-результата.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"strings"
	"time"
)

// Embedder генерирует векторное представление текста.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float64, error)
}

// HTTPEmbedder вызывает OpenAI / Ollama / любой совместимый embeddings API.
type HTTPEmbedder struct {
	Endpoint string // например https://api.openai.com/v1/embeddings
	Model    string // например text-embedding-3-small или nomic-embed-text
	APIKey   string
	Client   *http.Client
}

// NewHTTPEmbedder создаёт embedder для OpenAI-совместимого API.
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
			backoff := time.Duration(500<<uint(attempt-1)) * time.Millisecond

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

		// Retry только для временных ошибок
		if isRetryable(err) {
			lastErr = err
			continue
		}

		// Не-ретраябельная ошибка (401, 400) — сразу возвращаем
		return nil, err
	}

	return nil, fmt.Errorf("embedding API failed after %d attempts: %w", maxAttempts, lastErr)
}

func (e *HTTPEmbedder) doEmbed(ctx context.Context, text string) ([]float64, error) {
	body, err := json.Marshal(map[string]interface{}{
		"model": e.Model,
		"input": text,
		// Ollama (/api/embeddings) использует "prompt" вместо "input";
		// лишнее поле OpenAI игнорирует.
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

	// Поддерживаем два формата ответа:
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

// isRetryable определяет стоит ли делать retry.
func isRetryable(err error) bool {
	var httpErr *HTTPError
	if ok := errorAs(err, &httpErr); ok {
		// 429 (rate limit), 5xx (server errors) — retry
		return httpErr.StatusCode == 429 ||
			(httpErr.StatusCode >= 500 && httpErr.StatusCode < 600)
	}
	// Сетевые ошибки — retry
	var netErr net.Error
	if ok := errorAs(err, &netErr); ok {
		return netErr.Timeout()
	}
	return false
}

// errorAs is a helper to check if an error matches a target type.
func errorAs(err error, target interface{}) bool {
	if err == nil {
		return false
	}
	switch t := target.(type) {
	case **HTTPError:
		if e, ok := err.(*HTTPError); ok {
			*t = e
			return true
		}
	case *net.Error:
		if e, ok := err.(net.Error); ok {
			*t = e
			return true
		}
	}
	return false
}

// NoopEmbedder — заглушка, когда AI не настроен. Явно возвращает ошибку
// вместо тихого mock-результата.
type NoopEmbedder struct{}

func (NoopEmbedder) Embed(_ context.Context, _ string) ([]float64, error) {
	return nil, fmt.Errorf(
		"AI embedding is not configured. " +
			"Set ai.provider/ai.endpoint/ai.model in vaultdb.yaml " +
			"(and VAULTDB_AI_API_KEY if required) to enable SEMANTIC_MATCH and AI_EMBED")
}

// MockEmbedder — детерминированный keyword-based embedder для тестов.
// Не является ML-моделью и не должен использоваться в продакшене.
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
