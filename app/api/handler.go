package api

import (
	"context"
	"fmt"
	"log"
	"os"
	"rag/app/agent"
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
	contextStore store.DBStorer
	embedder     model.EmbedderInterface
}

func NewRequestHandler(contextStore store.DBStorer) *RequestHandler {
	embedder := model.NewOllamaEmbedder()
	return &RequestHandler{
		contextStore: contextStore,
		embedder:     embedder,
	}
}

func (h *RequestHandler) HandleRequest(c *fiber.Ctx) error {
	var params types.QueryParams
	if c.BodyParser(&params) != nil {
		return ErrBadRequest()
	}

	if errors := types.Validate(&params); len(errors) > 0 {
		return NewValidationError(errors)
	}

	prompt := params.Prompt

	embededPrompt, err := h.embedder.Embed(prompt)
	if err != nil {
		return err
	}

	similarChunks, err := h.contextStore.Search(context.Background(), embededPrompt, 5)
	if err != nil {
		fmt.Println("error to get context from DB", err)
		return err
	}

	// 4. Фильтруем чанки по качеству (distance)
	qualityChunks, err := h.filterChunks(similarChunks)
	if err != nil {
		return err
	}

	confidence := 1.0
	if len(qualityChunks) > 0 {
		confidence = qualityChunks[0].Distance
	}

	fmt.Println("Count chunks before extend", len(qualityChunks))
	// 4.1 Обогащаем выборку когерентными чанками
	cohChunks, err := h.extendChunks(qualityChunks)
	if err != nil {
		fmt.Println(err)
		return err
	}
	fmt.Println("Count chunks after extend", len(cohChunks))

	// 5. Формируем контекст из найденных чанков
	context, contextChunks := h.buildContext(cohChunks)

	sources, err := h.formatSources(contextChunks)
	if err != nil {
		fmt.Println("Handle the error:", err)
		return err
	}
	fmt.Println("-----------------")

	//fmt.Println("after builder: \n", context)

	if context == "" {
		context = "empty"
	}

	output, err := agent.GenerateAnswer(context, prompt)
	if err != nil {
		return err
	}

	//return c.JSON(output)
	resp := &types.SearchResponse{
		Answer:     output,
		Sources:    sources,
		Confidence: confidence,
		Timestamp:  time.Now(),
	}
	return c.JSON(resp)
}

func (h *RequestHandler) formatSources(chunks []types.Chunk) ([]types.Source, error) {
	sources := make([]types.Source, len(chunks))
	for i, chunk := range chunks {
		doc, err := h.contextStore.GetDocumentByID(context.Background(), chunk.DocID)
		if err != nil {
			return nil, err
		}

		sources[i] = types.Source{
			DocID:     chunk.DocID.String(),
			Title:     doc.Title,
			ChunkText: chunk.Content,
			Index:     chunk.Index,
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
	minDistance := 0.55 // Минимальный допустимый distance для релевантного результата
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
	// var context string
	maxContextLength := 20000 // Максимальный размер контекста в символах
	// currentLength := len(context)
	overlap, _ := strconv.Atoi(os.Getenv("CHUNK_OVERLAP"))

	// 1️⃣ Сначала сортируем все чанки по Weight (по убыванию)
	sort.SliceStable(chunks, func(i, j int) bool {
		wi := chunks[i].Distance
		wj := chunks[j].Distance
		return wi > wj
	})

	// 2️⃣ Группируем чанки по doc_id
	grouped := make(map[uuid.UUID][]types.Chunk)
	for _, ch := range chunks {
		grouped[ch.DocID] = append(grouped[ch.DocID], ch)
	}

	// 3️⃣ Сортируем внутри каждой группы по позиции (Index)
	for id := range grouped {
		sort.SliceStable(grouped[id], func(i, j int) bool {
			return grouped[id][i].Index < grouped[id][j].Index
		})
	}

	//originalCount := len(chunks)
	var contextChunks []types.Chunk
	var sb strings.Builder
	for docID, docChunks := range grouped {
		sb.WriteString(fmt.Sprintf("Документ %s:\n", docID))
		// Удаляем перекрытия из последовательных чанков
		chunks = h.removeChunkOverlaps(docChunks, overlap)
		originalCount := len(docChunks)
		fmt.Printf("[OVERLAP] Обработано чанков: %d -> %d (overlap: %d words)\n", originalCount, len(docChunks), overlap)
		for i, ch := range chunks {
			if ch.Section != "" {
				sb.WriteString(fmt.Sprintf("## %s\n", ch.Section))
			}
			// sb.WriteString(fmt.Sprintf("Векторное сходство: %f\n", ch.Distance))
			sb.WriteString(ch.Content)
			if sb.Len() > maxContextLength {
				fmt.Printf("[CONTEXT] Достигнут лимит контекста (%d symbols), используем %d чанков\n", maxContextLength, i)
				break
			}
			contextChunks = append(contextChunks, ch)
		}

		sb.WriteString("\n")
	}
	fmt.Printf("[CONTEXT] Сформирован контекст: %d символов из %d чанков\n", len(sb.String()), len(chunks))
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
				chunk.Content = strings.Join(words[overlap:], " ")
				fmt.Printf("[OVERLAP] Обрезан текст чанка %d: %d -> %d символов\n", chunk.Index, originalLength, len(chunk.Content))
				result = append(result, chunk)
			} else {
				fmt.Printf("[OVERLAP] Чанк %d пропущен полностью (текст короче overlap: %d < %d)\n", chunk.Index, len(chunk.Content), overlap)
			}

		} else {
			result = append(result, chunk)
		}
	}
	return result
}
