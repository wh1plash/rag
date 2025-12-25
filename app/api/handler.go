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
	fmt.Println(params.UseLocal)
	if errors := types.Validate(&params); len(errors) > 0 {
		fmt.Println(errors)
		return NewValidationError(errors)
	}

	prompt := params.Prompt

	embededPrompt, err := h.embedder.Embed(prompt) //TODO set cfg from DB id =1
	if err != nil {
		return err
	}

	similarChunks, err := h.contextStore.Search(context.Background(), embededPrompt, 3)
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
	promptContext, contextChunks := h.buildContext(cohChunks)

	sources, err := h.formatSources(contextChunks)
	if err != nil {
		fmt.Println("Handle the error:", err)
		return err
	}

	// fmt.Println("after builder: \n", promptContext)
	// return c.JSON("ok")

	if promptContext == "" {
		promptContext = "empty"
	}

	cfg, err := h.contextStore.GetConfig(context.Background(), 2)
	if err != nil {
		return err
	}

	var output string
	if params.UseLocal {
		output, err = agent.GenerateAnswer(promptContext, prompt, cfg)
	} else {
		output, err = agent.GenerateAnswerCohere(promptContext, prompt, cfg)
	}

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
	maxContextLength := 40000 // Максимальный размер контекста в символах
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
	var (
		sb            strings.Builder
		contextChunks []types.Chunk
		seenTables    = make(map[uuid.UUID]struct{})
	)

	for docID, docChunks := range grouped {

		sb.WriteString(fmt.Sprintf("Документ %s:\n", docID))

		docChunks = h.removeChunkOverlaps(docChunks, overlap)

		for i, ch := range docChunks {

			// =========================
			// 📊 TABLE ROW
			// =========================
			if ch.TableID.Valid {

				tableID := ch.TableID.UUID

				// таблицу уже добавляли → пропускаем
				if _, ok := seenTables[tableID]; ok {
					fmt.Println("filter tables")
					continue
				}

				// грузим таблицу целиком
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

			// =========================
			// 📝 TEXT / IMAGE
			// =========================
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
				fmt.Printf("[OVERLAP] Чанк %d пропущен полностью (текст короче overlap: %d < %d)\n", chunk.Index, len(chunk.Content), overlap)
			}

		} else {
			result = append(result, chunk)
		}
	}
	return result
}
