package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"
)

// OllamaEmbedder реализует создание эмбеддингов через Ollama
type OllamaEmbedder struct {
	apiURL string
	model  string
}

type OllamaEmbeddingRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type OllamaEmbeddingResponse struct {
	Embedding []float64 `json:"embedding"`
}

func NewOllamaEmbedder(apiURL, model string) *OllamaEmbedder {
	return &OllamaEmbedder{
		apiURL: apiURL,
		model:  model,
	}
}

func (e *OllamaEmbedder) Embed(text string) ([]float32, error) {
	req := OllamaEmbeddingRequest{
		Model:  e.model,
		Prompt: text,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, "POST", e.apiURL, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var ollamaResp OllamaEmbeddingResponse
	if err := json.Unmarshal(respBody, &ollamaResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	norm := normalize64(ollamaResp.Embedding)

	// Конвертируем float64 в float32
	embedding := make([]float32, len(norm))
	for i, v := range ollamaResp.Embedding {
		embedding[i] = float32(v)
	}

	return embedding, nil
}

// Normalize принимает срез float32 и возвращает нормализованный вектор
func normalize64(vec []float64) []float64 {
	var sum float64
	for _, v := range vec {
		sum += v * v
	}
	norm := math.Sqrt(sum)
	if norm == 0 {
		return vec // на случай пустого вектора
	}

	for i, x := range vec {
		vec[i] = x / norm
	}
	return vec
}
