package port

import "context"

// SearchHit — нормализованный результат веб-поиска (без vendor-полей).
type SearchHit struct {
	Title   string
	URL     string
	Content string  // сниппет / summary от поисковика
	Raw     string  // полный извлечённый текст страницы (если есть)
	Score   float64 // релевантность провайдера (0..1)
}

// SearchOptions — параметры запроса к веб-поиску.
type SearchOptions struct {
	MaxResults    int
	SearchDepth   string // basic | advanced | fast | ultra-fast
	IncludeRaw    bool
	IncludeAnswer bool
}

// WebSearcher — порт поиска в интернете.
type WebSearcher interface {
	Search(ctx context.Context, query string, opts SearchOptions) ([]SearchHit, error)
}
