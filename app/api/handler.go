package api

import (
	"context"
	"fmt"
	"log"
	"os"
	"rag/internal/port"
	"rag/internal/usecase/webingest"
	"rag/model"
	"rag/types"
	"sort"
	"strconv"
	"strings"
	"time"

	"rag/store"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type RequestHandler struct {
	contextStore     store.DBStorer
	embedder         model.EmbedderInterface
	webSearcher      port.WebSearcher
	webIngest        *webingest.Service
	webOpts          port.SearchOptions
	minChunkDistance float64
	maxContextLength int
	localLLM         port.AnswerGenerator
	remoteLLM        port.AnswerGenerator
}

type RequestHandlerDeps struct {
	Store            store.DBStorer
	Embedder         model.EmbedderInterface
	WebSearcher      port.WebSearcher // optional
	WebIngest        *webingest.Service
	WebOpts          port.SearchOptions
	MinChunkDistance float64 // 0 = взять из MIN_CHUNK_DISTANCE / default 0.5
	MaxContextLength int     // 0 = взять из MAX_CONTEXT_LENGTH / default 70000
	LocalLLM         port.AnswerGenerator
	RemoteLLM        port.AnswerGenerator
}

func NewRequestHandler(contextStore store.DBStorer) *RequestHandler {
	return NewRequestHandlerWithDeps(RequestHandlerDeps{
		Store:    contextStore,
		Embedder: model.NewOllamaEmbedder(),
	})
}

func NewRequestHandlerWithDeps(deps RequestHandlerDeps) *RequestHandler {
	embedder := deps.Embedder
	if embedder == nil {
		embedder = model.NewOllamaEmbedder()
	}
	opts := deps.WebOpts
	if opts.MaxResults <= 0 {
		opts.MaxResults = 3
	}
	if opts.SearchDepth == "" {
		opts.SearchDepth = "basic"
	}
	minDist := deps.MinChunkDistance
	if minDist <= 0 {
		minDist = envFloat("MIN_CHUNK_DISTANCE", 0.5)
	}
	maxCtx := deps.MaxContextLength
	if maxCtx <= 0 {
		maxCtx = envInt("MAX_CONTEXT_LENGTH", 70000)
	}
	return &RequestHandler{
		contextStore:     deps.Store,
		embedder:         embedder,
		webSearcher:      deps.WebSearcher,
		webIngest:        deps.WebIngest,
		webOpts:          opts,
		minChunkDistance: minDist,
		maxContextLength: maxCtx,
		localLLM:         deps.LocalLLM,
		remoteLLM:        deps.RemoteLLM,
	}
}

func (h *RequestHandler) HandleRequest(c *fiber.Ctx) error {
	var params types.QueryParams
	if c.BodyParser(&params) != nil {
		return ErrBadRequest()
	}
	fmt.Println(params.UseLocal)
	if errors := types.Validate(&params); len(errors) > 0 {
		fmt.Println(errors)
		return NewValidationError(errors)
	}

	prompt := params.Prompt
	ctx := c.Context()

	embededPrompt, err := h.embedder.Embed(prompt) //TODO set cfg from DB id =1
	if err != nil {
		return err
	}

	similarChunks, err := h.contextStore.Search(ctx, embededPrompt, 5)
	if err != nil {
		fmt.Println("error to get context from DB:", err)
		return err
	}

	// 4. Фильтруем чанки по качеству (distance = cosine similarity)
	qualityChunks, err := h.filterChunks(similarChunks)
	if err != nil {
		return err
	}

	confidence := 1.0
	if len(qualityChunks) > 0 {
		confidence = qualityChunks[0].Distance
	}

	var (
		promptContext string
		sources       []types.Source
	)

	if len(qualityChunks) == 0 && h.shouldUseWeb(params) {
		log.Printf("[WEB] no quality KB chunks — fallback to web search")
		promptContext, sources, confidence, err = h.webFallback(ctx, prompt)
		if err != nil {
			return err
		}
	} else {
		fmt.Println("Count chunks before extend", len(qualityChunks))
		cohChunks, err := h.extendChunks(qualityChunks)
		if err != nil {
			fmt.Println(err)
			return err
		}
		fmt.Println("Count chunks after extend", len(cohChunks))

		var contextChunks []types.Chunk
		promptContext, contextChunks = h.buildContext(cohChunks)
		sources, err = h.formatSources(contextChunks)
		if err != nil {
			fmt.Println("Handle the error:", err)
			return err
		}
	}

	if promptContext == "" {
		promptContext = "empty"
	}

	cfg, err := h.contextStore.GetConfig(ctx, 2)
	if err != nil {
		return err
	}

	gen := h.remoteLLM
	route := "remote"
	if params.UseLocal {
		gen = h.localLLM
		route = "local"
	}
	if gen == nil {
		return fmt.Errorf("llm generator is not configured (use_local=%v)", params.UseLocal)
	}

	log.Printf("[RAG] calling LLM route=%s use_local=%v context_chars=%d question=%q",
		route, params.UseLocal, len(promptContext), truncateRunes(prompt, 120))

	output, err := gen.Generate(ctx, port.AnswerRequest{
		System:   cfg.PromptStr,
		Context:  promptContext,
		Question: prompt,
	})
	if err != nil {
		log.Printf("[RAG] LLM route=%s error: %v", route, err)
		return err
	}
	log.Printf("[RAG] LLM route=%s done answer_chars=%d", route, len(output))

	resp := &types.SearchResponse{
		Answer:     output,
		Sources:    sources,
		Confidence: confidence,
		Timestamp:  time.Now(),
	}
	return c.JSON(resp)
}

func (h *RequestHandler) shouldUseWeb(params types.QueryParams) bool {
	if h.webSearcher == nil || h.webIngest == nil {
		return false
	}
	if params.AllowWeb != nil {
		return *params.AllowWeb
	}
	return true
}

func (h *RequestHandler) webFallback(ctx context.Context, query string) (string, []types.Source, float64, error) {
	hits, err := h.webSearcher.Search(ctx, query, h.webOpts)
	if err != nil {
		return "", nil, 0, fmt.Errorf("web search: %w", err)
	}
	if len(hits) == 0 {
		log.Printf("[WEB] search returned 0 hits for query=%q", query)
		return "", nil, 0, nil
	}
	log.Printf("[WEB] got %d hits, ingesting into KB...", len(hits))

	docs, err := h.webIngest.SaveHits(ctx, hits)
	if err != nil {
		return "", nil, 0, fmt.Errorf("web ingest: %w", err)
	}
	if len(docs) == 0 {
		return "", nil, 0, nil
	}

	promptContext, sources := webingest.BuildContext(docs, h.maxContextLength)
	confidence := hits[0].Score
	if confidence <= 0 {
		confidence = 1.0
	}
	return promptContext, sources, confidence, nil
}

func (h *RequestHandler) formatSources(chunks []types.Chunk) ([]types.Source, error) {
	sources := make([]types.Source, len(chunks))
	for i, chunk := range chunks {
		doc, err := h.contextStore.GetDocumentByID(context.Background(), chunk.DocID)
		if err != nil {
			return nil, err
		}

		srcType := "kb"
		if doc.Source == "web" {
			srcType = "web"
		}
		sources[i] = types.Source{
			DocID:     chunk.DocID.String(),
			Title:     doc.Title,
			ChunkText: chunk.Content,
			Index:     chunk.Index,
			URL:       doc.SourcePath,
			Type:      srcType,
		}
	}
	return sources, nil
}

func (h *RequestHandler) HandlePDF(c *fiber.Ctx) error {
	file, err := c.FormFile("file")
	if err != nil {
		return ErrBadRequest()
	}
	path := os.Getenv("LOADER_SOURCE_DIR") + "/" + file.Filename
	if err := c.SaveFile(file, path); err != nil {
		fmt.Println(err)
		return err
	}
	fmt.Printf("[UPLOAD] Fife successfuly saved to: %s\n", path)

	return c.JSON("ok")
}

func (h *RequestHandler) extendChunks(chunks []types.Chunk) ([]types.Chunk, error) {
	fmt.Println("Start begin extend")

	for _, chunk := range chunks {
		if chunk.Type != "text" {
			continue
		}
		res, err := h.contextStore.GetNeighbours(context.Background(), chunk.ID)
		if err != nil {
			return nil, err
		}
		fmt.Printf("[GETNEIGHBOURS] Neighbours of index: %d, chunk: %s, count: %d, \n", chunk.Index, chunk.ID, len(res))

		exists := make(map[string]struct{}, len(chunks))
		for _, ch := range chunks {
			key := ch.ID.String()
			exists[key] = struct{}{}
		}
		for _, ch := range res {
			key := ch.ID.String()
			if _, ok := exists[key]; !ok {
				fmt.Printf("[GETNEIGHBOURS] Adding by coherence: %d, index: %s\n", ch.Index, ch.ID)
				chunks = append(chunks, ch)
				exists[key] = struct{}{}
			}
		}
	}
	return chunks, nil
}

func (h *RequestHandler) filterChunks(chunks []types.Chunk) ([]types.Chunk, error) {
	result := make([]types.Chunk, 0, len(chunks))
	minDistance := h.minChunkDistance
	for _, chunk := range chunks {
		if chunk.Distance > minDistance {
			result = append(result, chunk)
		} else {
			log.Printf("[FILTER] Отфильтрован чанк с distance=%.4f (less then %.2f)", chunk.Distance, minDistance)
		}
	}
	return result, nil
}

func (h *RequestHandler) buildContext(chunks []types.Chunk) (string, []types.Chunk) {
	maxContextLength := h.maxContextLength
	overlap, _ := strconv.Atoi(os.Getenv("CHUNK_OVERLAP"))

	sort.SliceStable(chunks, func(i, j int) bool {
		wi := chunks[i].Distance
		wj := chunks[j].Distance
		return wi > wj
	})

	grouped := make(map[uuid.UUID][]types.Chunk)
	for _, ch := range chunks {
		grouped[ch.DocID] = append(grouped[ch.DocID], ch)
	}

	for id := range grouped {
		sort.SliceStable(grouped[id], func(i, j int) bool {
			return grouped[id][i].Index < grouped[id][j].Index
		})
	}

	var (
		sb            strings.Builder
		contextChunks []types.Chunk
		seenTables    = make(map[uuid.UUID]struct{})
	)

	for docID, docChunks := range grouped {

		sb.WriteString(fmt.Sprintf("Документ %s:\n", docID))

		docChunks = h.removeChunkOverlaps(docChunks, overlap)

		for i, ch := range docChunks {

			if ch.TableID.Valid {

				tableID := ch.TableID.UUID

				if _, ok := seenTables[tableID]; ok {
					fmt.Println("filter tables")
					continue
				}

				table, err := h.contextStore.GetTableByID(context.Background(), tableID)
				if err != nil {
					log.Printf("failed to load table %s: %v", tableID, err)
					continue
				}

				sb.WriteString("\n")
				sb.WriteString("Таблица:\n")
				sb.WriteString(table.Content)
				sb.WriteString("\n\n")

				seenTables[tableID] = struct{}{}
				contextChunks = append(contextChunks, ch)

				if sb.Len() > maxContextLength {
					log.Printf("[CONTEXT] limit reached (%d symbols)", maxContextLength)
					break
				}

				continue
			}

			if ch.Section != "" {
				sb.WriteString(fmt.Sprintf("## %s\n", ch.Section))
			}

			sb.WriteString(ch.Content)
			sb.WriteString("\n\n")

			contextChunks = append(contextChunks, ch)

			if sb.Len() > maxContextLength {
				log.Printf("[CONTEXT] limit reached (%d symbols) at chunk %d", maxContextLength, i)
				break
			}
		}

		sb.WriteString("\n")
	}

	log.Printf(
		"[CONTEXT] built: %d symbols from %d chunks (tables: %d)",
		sb.Len(),
		len(contextChunks),
		len(seenTables),
	)
	return sb.String(), contextChunks
}

func (h *RequestHandler) removeChunkOverlaps(chunks []types.Chunk, overlap int) []types.Chunk {
	if len(chunks) <= 1 {
		return chunks
	}
	result := make([]types.Chunk, 0, len(chunks))

	for i, chunk := range chunks {
		if i == 0 {
			result = append(result, chunk)
			continue
		}

		prevChunk := chunks[i-1]

		if chunk.Index == prevChunk.Index+1 &&
			chunk.DocID == prevChunk.DocID {
			fmt.Printf("[OVERLAP] Найдены последовательные чанки: %d -> %d (ID: %s)\n", prevChunk.Index, chunk.Index, chunk.ID)

			words := strings.Fields(chunk.Content)
			if len(words) > overlap {
				originalLength := len(chunk.Content)
				if chunk.Type == "json" {
					chunk.Content = strings.Join(words[0:], " ")
				} else {
					chunk.Content = strings.Join(words[overlap:], " ")
				}
				fmt.Printf("[OVERLAP] Обрезан текст чанка %d: %d -> %d символов\n", chunk.Index, originalLength, len(chunk.Content))
				result = append(result, chunk)
			} else {
				fmt.Printf("[OVERLAP] Чанк %d пропущен полностью (текст короче overlap: %d < %d)\n", chunk.Index, len(words), overlap)
			}

		} else {
			result = append(result, chunk)
		}
	}
	return result
}

func envInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envFloat(key string, fallback float64) float64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return n
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
