package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"rag/types"
	"strconv"
	"time"

	"github.com/pkoukk/tiktoken-go"
)

type Options struct {
	Temperature float64 `json:"temperature"`
	NumCtx      int     `json:"num_ctx"`
	NumThread   int     `json:"num_thread"`
	TopP        float64 `json:"top_p"`
	TopK        int     `json:"top_k"`
	Repeat      float64 `json:"repeat_penalty"`
}

type GenerateRequest struct {
	Model   string   `json:"model"`
	System  string   `json:"system"`
	Prompt  string   `json:"prompt"`
	Stream  bool     `json:"stream"`
	Stop    []string `json:"stop,omitempty"`
	Options Options  `json:"options"`
}

type GenerateResponse struct {
	Response string `json:"response"`
}

func ProcessFile(data string, cfg types.LLMConfig) (string, error) {
	start := time.Now()
	defer func() {
		fmt.Printf("Time for processing file: %v\n", time.Since(start))
	}()

	fmt.Println("Startin process file...")

	corrected, err := SendToOllama(MakePrompt(data), cfg)
	if err != nil {
		return "", err
	}

	return corrected, nil
}

func MakePrompt(text string) string {
	return fmt.Sprintf(`
Исправь орфографические, пунктуационные и грамматические ошибки в тексте.

Текст:
%s
`, text)
}

func SendToOllama(prompt string, cfg types.LLMConfig) (string, error) {
	payload := GenerateRequest{
		Model:  cfg.Model,
		System: cfg.PromptStr,
		Prompt: prompt,
		Stream: false,
		Options: Options{
			NumCtx:    envInt("LLM_LOCAL_NUM_CTX", 8192),
			NumThread: envInt("LLM_LOCAL_NUM_THREAD", 8),
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	resp, err := http.Post(
		cfg.Url,
		"application/json",
		bytes.NewBuffer(body),
	)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var llmResp GenerateResponse
	if err := json.Unmarshal(respBody, &llmResp); err != nil {
		return "", fmt.Errorf("unmarshal ollama response: %w", err)
	}

	return llmResp.Response, nil
}

func ConcatToBytes(s1, s2 string) []byte {
	b := make([]byte, 0, len(s1)+len(s2))
	b = append(b, s1...)
	b = append(b, s2...)
	return b
}

func CountTokensLlama(data []byte) (int, error) {
	enc, err := tiktoken.EncodingForModel("gpt-3.5-turbo")
	if err != nil {
		return 0, err
	}
	tokens := enc.Encode(string(data), nil, nil)
	return len(tokens), nil
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
