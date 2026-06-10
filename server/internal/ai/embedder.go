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
		return nil, fmt.Errorf("embedding API: unexpected status %s", resp.Status)
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
