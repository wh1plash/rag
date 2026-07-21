package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"rag/internal/port"
)

func TestClientGenerate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("auth %s", r.Header.Get("Authorization"))
		}
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Model != "openai/gpt-4o-mini" {
			t.Fatalf("model %s", req.Model)
		}
		if n, ok := req.Options["num_ctx"].(float64); !ok || n != 4096 {
			t.Fatalf("num_ctx %+v", req.Options["num_ctx"])
		}
		_ = json.NewEncoder(w).Encode(chatResponse{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{
				{Message: struct {
					Content string `json:"content"`
				}{Content: "ok answer"}},
			},
		})
	}))
	defer srv.Close()

	client, err := New(Config{
		Name:    "test",
		BaseURL: srv.URL,
		APIKey:  "test-key",
		Model:   "openai/gpt-4o-mini",
		NumCtx:  4096,
	})
	if err != nil {
		t.Fatal(err)
	}

	out, err := client.Generate(context.Background(), port.AnswerRequest{
		System:   "sys",
		Context:  "ctx",
		Question: "q?",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out != "ok answer" {
		t.Fatalf("got %q", out)
	}
}
