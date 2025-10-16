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

	"rag/store"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type RequestHandler struct {
	contextStore store.DBStorer
	embedder     model.EmbedderInterface
}

func NewRequestHandler(contextStore store.DBStorer) *RequestHandler {
	embedder := model.NewEmbedder()
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

	similarChunks, err := h.contextStore.Search(context.Background(), embededPrompt, 10)
	if err != nil {
		fmt.Println("error to get context from DB", err)
		return err
	}

	// 4. Фильтруем чанки по качеству (distance)
	var qualityChunks []types.Chunk
	maxDistance := 0.4 // Максимальный допустимый distance для релевантного результата

	for _, chunk := range similarChunks {
		if chunk.Distance >= maxDistance {
			qualityChunks = append(qualityChunks, chunk)
		} else {
			log.Printf("[FILTER] Отфильтрован чанк с distance=%.4f (less then %.2f)", chunk.Distance, maxDistance)
		}
	}

	// 5. Формируем контекст из найденных чанков
	context := h.buildContext(qualityChunks)

	fmt.Println("-----------------")

	//fmt.Println("after builder: \n", context)
	if context == "" {
		context = "empty"
	}

	output, err := agent.GenerateAnswer(context, prompt)
	if err != nil {
		return err
	}
	return c.JSON(output)
}

func (h *RequestHandler) buildContext(chunks []types.Chunk) string {
	// var context string
	maxContextLength := 12000 // Максимальный размер контекста в символах
	// currentLength := len(context)
	overlap, _ := strconv.Atoi(os.Getenv("CHUNK_OVERLAP"))

	// Группируем чанки по doc_id
	grouped := make(map[uuid.UUID][]types.Chunk)
	for _, ch := range chunks {
		grouped[ch.DocID] = append(grouped[ch.DocID], ch)
	}

	// Сортируем внутри каждой группы по позиции
	for id := range grouped {
		sort.SliceStable(grouped[id], func(i, j int) bool {
			return grouped[id][i].Index < grouped[id][j].Index
		})
	}

	//originalCount := len(chunks)
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
			sb.WriteString(ch.Content)
			if sb.Len() > maxContextLength {
				fmt.Printf("[CONTEXT] Достигнут лимит контекста (%d symbols), используем %d чанков\n", maxContextLength, i)
				break
			}
		}
		sb.WriteString("\n")
	}
	// chunks = h.removeChunkOverlaps(chunks, overlap)
	// fmt.Printf("[OVERLAP] Обработано чанков: %d -> %d (overlap: %d words)\n", originalCount, len(chunks), overlap)

	// for i, chunk := range chunks {
	// 	//For numeratin chunk index we can use index of slice
	// 	//chunkText := fmt.Sprintf("%d. %s\n", i+1, chunk.Content)
	// 	chunkText := fmt.Sprintf("%s ", chunk.Content)
	// 	// Проверяем, не превысим ли лимит
	// 	if currentLength+len(chunkText) > maxContextLength {
	// 		fmt.Printf("[CONTEXT] Достигнут лимит контекста (%d symbols), используем %d чанков\n", maxContextLength, i)
	// 		break
	// 	}

	// 	context += chunkText
	// 	currentLength += len(chunkText)
	// }

	fmt.Printf("[CONTEXT] Сформирован контекст: %d символов из %d чанков\n", len(sb.String()), len(chunks))
	return sb.String()
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
			// fmt.Println("++++++++++++++", words)
			if len(words) > overlap {
				originalLength := len(chunk.Content)
				chunk.Content = strings.Join(words[overlap:], " ")
				fmt.Printf("[OVERLAP] Обрезан текст чанка %d: %d -> %d символов\n", chunk.Index, originalLength, len(chunk.Content))
				result = append(result, chunk)
				// fmt.Println("++++++++After removing Overlap:", chunk.Content)
			} else {
				fmt.Printf("[OVERLAP] Чанк %d пропущен полностью (текст короче overlap: %d < %d)\n", chunk.Index, len(chunk.Content), overlap)
			}

		} else {
			result = append(result, chunk)
		}
	}
	return result
}
