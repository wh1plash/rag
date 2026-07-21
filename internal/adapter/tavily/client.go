package tavily

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"rag/internal/port"
)

const defaultBaseURL = "https://api.tavily.com"

// Config — настройки клиента Tavily (без чтения env внутри методов).
type Config struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
	Timeout    time.Duration
}

// Client реализует port.WebSearcher через Tavily Search API.
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("tavily: api key is required")
	}
	base := cfg.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 45 * time.Second
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}
	return &Client{
		apiKey:     cfg.APIKey,
		baseURL:    strings.TrimRight(base, "/"),
		httpClient: httpClient,
	}, nil
}

type searchRequest struct {
	Query             string `json:"query"`
	SearchDepth       string `json:"search_depth,omitempty"`
	MaxResults        int    `json:"max_results,omitempty"`
	IncludeRawContent any    `json:"include_raw_content,omitempty"`
	IncludeAnswer     bool   `json:"include_answer,omitempty"`
}

type searchResponse struct {
	Results []struct {
		Title      string  `json:"title"`
		URL        string  `json:"url"`
		Content    string  `json:"content"`
		Score      float64 `json:"score"`
		RawContent string  `json:"raw_content"`
	} `json:"results"`
}

func (c *Client) Search(ctx context.Context, query string, opts port.SearchOptions) ([]port.SearchHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("tavily: empty query")
	}

	depth := opts.SearchDepth
	if depth == "" {
		depth = "basic"
	}
	maxResults := opts.MaxResults
	if maxResults <= 0 {
		maxResults = 3
	}

	reqBody := searchRequest{
		Query:       query,
		SearchDepth: depth,
		MaxResults:  maxResults,
	}
	if opts.IncludeRaw {
		reqBody.IncludeRawContent = "markdown"
	}
	if opts.IncludeAnswer {
		reqBody.IncludeAnswer = true
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("tavily: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/search", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("tavily: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("tavily: do request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("tavily: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tavily: status %d: %s", resp.StatusCode, truncate(string(body), 500))
	}

	var parsed searchResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("tavily: unmarshal: %w", err)
	}

	hits := make([]port.SearchHit, 0, len(parsed.Results))
	for _, r := range parsed.Results {
		if strings.TrimSpace(r.URL) == "" {
			continue
		}
		hits = append(hits, port.SearchHit{
			Title:   r.Title,
			URL:     r.URL,
			Content: r.Content,
			Raw:     r.RawContent,
			Score:   r.Score,
		})
	}
	return hits, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

var _ port.WebSearcher = (*Client)(nil)
