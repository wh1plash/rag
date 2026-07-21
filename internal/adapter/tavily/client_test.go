package tavily

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"rag/internal/port"
)

func TestClientSearchParsesResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			t.Fatalf("path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("auth: %s", got)
		}
		var req searchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Query != "golang channels" {
			t.Fatalf("query: %s", req.Query)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{
					"title":       "Go Channels",
					"url":         "https://example.com/channels",
					"content":     "snippet",
					"raw_content": "# full markdown",
					"score":       0.91,
				},
			},
		})
	}))
	defer srv.Close()

	client, err := New(Config{APIKey: "test-key", BaseURL: srv.URL, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}

	hits, err := client.Search(context.Background(), "golang channels", port.SearchOptions{
		MaxResults:  2,
		SearchDepth: "basic",
		IncludeRaw:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits: %d", len(hits))
	}
	if hits[0].URL != "https://example.com/channels" || hits[0].Raw != "# full markdown" {
		t.Fatalf("hit: %+v", hits[0])
	}
}
