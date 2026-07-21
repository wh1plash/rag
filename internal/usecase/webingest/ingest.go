package webingest

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
	"unicode"

	"rag/internal/port"
	"rag/model"
	"rag/store"
	"rag/types"

	"github.com/google/uuid"
)

// IngestedDoc — сохранённый веб-документ и его чанки (для ответа/sources).
type IngestedDoc struct {
	Document types.Document
	Chunks   []types.Chunk
	Hit      port.SearchHit
}

// Service сохраняет результаты веб-поиска в KB (chunk → embed → SaveDocument/SaveChunk).
type Service struct {
	store     store.DBStorer
	embedder  model.EmbedderInterface
	chunkSize int
	overlap   int
}

// Config — параметры чанкинга именно для web (отдельно от PDF CHUNK_SIZE).
type Config struct {
	ChunkSize int // слов в чанке; default 300
	Overlap   int // overlap для word-window; default 50
}

func New(st store.DBStorer, embedder model.EmbedderInterface, cfg Config) *Service {
	chunkSize := cfg.ChunkSize
	if chunkSize <= 0 {
		chunkSize = 300
	}
	overlap := cfg.Overlap
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= chunkSize {
		overlap = chunkSize / 5
	}
	return &Service{
		store:     st,
		embedder:  embedder,
		chunkSize: chunkSize,
		overlap:   overlap,
	}
}

// SaveHits дедуплицирует по URL (stable UUID), перезаписывает чанки и возвращает сохранённое.
func (s *Service) SaveHits(ctx context.Context, hits []port.SearchHit) ([]IngestedDoc, error) {
	out := make([]IngestedDoc, 0, len(hits))
	now := time.Now().UTC()

	for _, hit := range hits {
		url := strings.TrimSpace(hit.URL)
		if url == "" {
			continue
		}
		body := cleanWebText(pickBody(hit))
		if body == "" {
			log.Printf("[WEB_INGEST] skip empty body url=%s", url)
			continue
		}

		docID := DocIDFromURL(url)
		title := strings.TrimSpace(hit.Title)
		if title == "" {
			title = url
		}

		chunks, err := s.buildChunks(ctx, docID, title, body)
		if err != nil {
			return out, fmt.Errorf("build chunks for %s: %w", url, err)
		}
		if len(chunks) == 0 {
			log.Printf("[WEB_INGEST] no chunks for url=%s", url)
			continue
		}

		doc := types.Document{
			ID:         docID,
			Title:      title,
			Source:     "web",
			SourcePath: url,
			CreatedAt:  now,
			UpdatedAt:  now,
			Version:    1,
			Chunks:     chunks,
		}

		if existing, err := s.store.GetDocumentByID(ctx, docID); err == nil && existing != nil {
			doc.CreatedAt = existing.CreatedAt
			doc.Version = existing.Version + 1
		}

		if err := s.store.DeleteChunksByDocID(ctx, docID); err != nil {
			return out, fmt.Errorf("delete old chunks %s: %w", docID, err)
		}
		if err := s.store.SaveDocument(ctx, doc); err != nil {
			return out, fmt.Errorf("save document %s: %w", url, err)
		}
		for i := range chunks {
			if err := s.store.SaveChunk(ctx, chunks[i]); err != nil {
				return out, fmt.Errorf("save chunk %d for %s: %w", i, url, err)
			}
		}

		log.Printf("[WEB_INGEST] saved doc_id=%s title=%q chunks=%d words≈%d url=%s",
			docID, title, len(chunks), len(strings.Fields(body)), url)
		out = append(out, IngestedDoc{Document: doc, Chunks: chunks, Hit: hit})
	}
	return out, nil
}

// BuildContext собирает текстовый контекст для LLM из ingest-результатов.
func BuildContext(docs []IngestedDoc, maxChars int) (string, []types.Source) {
	if maxChars <= 0 {
		maxChars = 70000
	}
	var sb strings.Builder
	sources := make([]types.Source, 0)

	for _, d := range docs {
		sb.WriteString(fmt.Sprintf("Документ (web): %s\nURL: %s\n", d.Document.Title, d.Document.SourcePath))
		for _, ch := range d.Chunks {
			sb.WriteString(ch.Content)
			sb.WriteString("\n\n")
			sources = append(sources, types.Source{
				DocID:     d.Document.ID.String(),
				Title:     d.Document.Title,
				ChunkText: truncate(ch.Content, 400),
				Index:     ch.Index,
				URL:       d.Document.SourcePath,
				Type:      "web",
			})
			if sb.Len() >= maxChars {
				return sb.String(), sources
			}
		}
		sb.WriteString("\n")
	}
	return sb.String(), sources
}

func DocIDFromURL(url string) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(url))
}

func pickBody(hit port.SearchHit) string {
	if strings.TrimSpace(hit.Raw) != "" {
		return hit.Raw
	}
	return hit.Content
}

func cleanWebText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	prevEmpty := false
	for _, line := range lines {
		line = strings.TrimRightFunc(line, unicode.IsSpace)
		empty := strings.TrimSpace(line) == ""
		if empty {
			if prevEmpty || len(out) == 0 {
				continue
			}
			out = append(out, "")
			prevEmpty = true
			continue
		}
		out = append(out, strings.TrimSpace(line))
		prevEmpty = false
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// buildChunks:
//  - короткий текст (≤ WEB_CHUNK_SIZE слов) → один чанк (типичный Tavily snippet);
//  - длинный → сначала упаковка абзацев, иначе sliding window по словам с overlap.
func (s *Service) buildChunks(ctx context.Context, docID uuid.UUID, section, text string) ([]types.Chunk, error) {
	_ = ctx
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil, nil
	}

	var parts []string
	if len(words) <= s.chunkSize {
		parts = []string{strings.Join(words, " ")}
		log.Printf("[WEB_INGEST] short body (%d words ≤ %d) → single chunk", len(words), s.chunkSize)
	} else {
		parts = splitWebParts(text, s.chunkSize, s.overlap)
		log.Printf("[WEB_INGEST] long body (%d words) → %d parts (size=%d overlap=%d)",
			len(words), len(parts), s.chunkSize, s.overlap)
	}

	chunks := make([]types.Chunk, 0, len(parts))
	for i, content := range parts {
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		emb, err := s.embedder.Embed(content)
		if err != nil {
			return nil, fmt.Errorf("embed chunk %d: %w", i, err)
		}
		chunks = append(chunks, types.Chunk{
			ID:        uuid.New(),
			DocID:     docID,
			Index:     len(chunks),
			Type:      string(types.ChunkText),
			Section:   section,
			Content:   content,
			Embedding: emb,
			Distance:  1.0,
		})
	}
	return chunks, nil
}

// splitWebParts упаковывает абзацы до chunkSize слов; слишком длинный абзац режет окном с overlap.
func splitWebParts(text string, chunkSize, overlap int) []string {
	paras := splitParagraphs(text)
	if len(paras) == 0 {
		return splitByWords(strings.Fields(text), chunkSize, overlap)
	}

	parts := make([]string, 0)
	var buf []string
	bufWords := 0

	flush := func() {
		if len(buf) == 0 {
			return
		}
		parts = append(parts, strings.Join(buf, "\n\n"))
		buf = nil
		bufWords = 0
	}

	for _, p := range paras {
		pw := len(strings.Fields(p))
		if pw == 0 {
			continue
		}
		if pw > chunkSize {
			flush()
			parts = append(parts, splitByWords(strings.Fields(p), chunkSize, overlap)...)
			continue
		}
		if bufWords > 0 && bufWords+pw > chunkSize {
			flush()
		}
		buf = append(buf, p)
		bufWords += pw
	}
	flush()
	return parts
}

func splitParagraphs(text string) []string {
	raw := strings.Split(text, "\n")
	paras := make([]string, 0)
	var cur []string
	flush := func() {
		if len(cur) == 0 {
			return
		}
		p := strings.TrimSpace(strings.Join(cur, " "))
		if p != "" {
			paras = append(paras, p)
		}
		cur = nil
	}
	for _, line := range raw {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		cur = append(cur, strings.TrimSpace(line))
	}
	flush()
	return paras
}

func splitByWords(words []string, chunkSize, overlap int) []string {
	if len(words) == 0 {
		return nil
	}
	if len(words) <= chunkSize {
		return []string{strings.Join(words, " ")}
	}
	step := chunkSize - overlap
	if step <= 0 {
		step = chunkSize
	}
	out := make([]string, 0)
	for i := 0; i < len(words); i += step {
		end := i + chunkSize
		if end > len(words) {
			end = len(words)
		}
		out = append(out, strings.Join(words[i:end], " "))
		if end == len(words) {
			break
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
