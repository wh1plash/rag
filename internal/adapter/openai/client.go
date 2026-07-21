package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"rag/internal/port"
)

// Config — OpenAI-compatible Chat Completions (OpenRouter, OpenAI, Ollama /v1, Groq, …).
type Config struct {
	Name       string // для логов: local | remote
	BaseURL    string // например https://openrouter.ai/api/v1 или http://ollama:11434/v1
	APIKey     string // Bearer; для Ollama обычно пусто
	Model      string
	NumCtx     int // Ollama: options.num_ctx (окно контекста)
	NumThread  int // Ollama: options.num_thread (CPU)
	Temperature float64
	Timeout    time.Duration
	// OpenRouter optional headers
	HTTPReferer string
	AppTitle    string
	HTTPClient  *http.Client
}

// Client реализует port.AnswerGenerator.
type Client struct {
	cfg        Config
	httpClient *http.Client
}

func New(cfg Config) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("openai llm: base url is required")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("openai llm: model is required")
	}
	if cfg.Temperature == 0 {
		cfg.Temperature = 0.2
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Minute // CPU-инференс может быть долгим
	}
	if cfg.Name == "" {
		cfg.Name = "openai"
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: cfg.Timeout}
	}
	cfg.BaseURL = base
	return &Client{cfg: cfg, httpClient: httpClient}, nil
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Stream      bool          `json:"stream"`
	Temperature float64       `json:"temperature,omitempty"`
	// Ollama OpenAI-compatible extension
	Options map[string]any `json:"options,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

func (c *Client) Generate(ctx context.Context, req port.AnswerRequest) (string, error) {
	user := fmt.Sprintf(`Контекст из документов:
%s

Вопрос:
%s

Ответь на вопрос, опираясь только на контекст.`, req.Context, req.Question)

	messages := make([]chatMessage, 0, 2)
	if strings.TrimSpace(req.System) != "" {
		messages = append(messages, chatMessage{Role: "system", Content: req.System})
	}
	messages = append(messages, chatMessage{Role: "user", Content: user})

	body := chatRequest{
		Model:       c.cfg.Model,
		Messages:    messages,
		Stream:      false,
		Temperature: c.cfg.Temperature,
	}
	if c.cfg.NumCtx > 0 || c.cfg.NumThread > 0 {
		opts := make(map[string]any)
		if c.cfg.NumCtx > 0 {
			opts["num_ctx"] = c.cfg.NumCtx
		}
		if c.cfg.NumThread > 0 {
			opts["num_thread"] = c.cfg.NumThread
		}
		body.Options = opts
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("%s: marshal: %w", c.cfg.Name, err)
	}

	endpoint := c.cfg.BaseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("%s: create request: %w", c.cfg.Name, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if key := strings.TrimSpace(c.cfg.APIKey); key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+key)
	}
	if c.cfg.HTTPReferer != "" {
		httpReq.Header.Set("HTTP-Referer", c.cfg.HTTPReferer)
	}
	if c.cfg.AppTitle != "" {
		httpReq.Header.Set("X-Title", c.cfg.AppTitle)
	}

	log.Printf("[LLM:%s] POST %s model=%s num_ctx=%d num_thread=%d temp=%.2f ctx_chars=%d question_chars=%d payload_bytes=%d",
		c.cfg.Name, endpoint, c.cfg.Model, c.cfg.NumCtx, c.cfg.NumThread, c.cfg.Temperature,
		len(req.Context), len(req.Question), len(payload))

	start := time.Now()
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		log.Printf("[LLM:%s] FAILED after %s: %v", c.cfg.Name, time.Since(start).Round(time.Millisecond), err)
		return "", fmt.Errorf("%s: do request: %w", c.cfg.Name, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	elapsed := time.Since(start).Round(time.Millisecond)
	if err != nil {
		log.Printf("[LLM:%s] FAILED read body after %s status=%d: %v", c.cfg.Name, elapsed, resp.StatusCode, err)
		return "", fmt.Errorf("%s: read body: %w", c.cfg.Name, err)
	}

	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		log.Printf("[LLM:%s] FAILED unmarshal after %s status=%d body=%s", c.cfg.Name, elapsed, resp.StatusCode, truncate(string(raw), 400))
		return "", fmt.Errorf("%s: unmarshal: %w; body=%s", c.cfg.Name, err, truncate(string(raw), 400))
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		log.Printf("[LLM:%s] API error after %s: %s", c.cfg.Name, elapsed, parsed.Error.Message)
		return "", fmt.Errorf("%s: api error: %s", c.cfg.Name, parsed.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		log.Printf("[LLM:%s] HTTP %d after %s body=%s", c.cfg.Name, resp.StatusCode, elapsed, truncate(string(raw), 500))
		return "", fmt.Errorf("%s: status %d: %s", c.cfg.Name, resp.StatusCode, truncate(string(raw), 500))
	}
	if len(parsed.Choices) == 0 {
		log.Printf("[LLM:%s] empty choices after %s body=%s", c.cfg.Name, elapsed, truncate(string(raw), 400))
		return "", fmt.Errorf("%s: empty choices", c.cfg.Name)
	}

	answer := strings.TrimSpace(parsed.Choices[0].Message.Content)
	log.Printf("[LLM:%s] OK after %s answer_chars=%d preview=%q",
		c.cfg.Name, elapsed, len(answer), truncate(answer, 200))
	return answer, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

var _ port.AnswerGenerator = (*Client)(nil)
