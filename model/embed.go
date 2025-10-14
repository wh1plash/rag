package model

import (
	"log"
	"os"
)

// EmbedderInterface определяет интерфейс для создания эмбеддингов
type EmbedderInterface interface {
	Embed(text string) ([]float32, error)
}

// Embedder основная структура для работы с эмбеддингами
type Embedder struct {
	ollamaEmbedder *OllamaEmbedder
	embeddingType  string
}

// NewEmbedder создает новый embedder (только Ollama)
func NewEmbedder() *Embedder {

	// Настраиваем Ollama эмбеддер
	ollamaURL := os.Getenv("OLLAMA_EMBEDDING_URL")
	ollamaModel := os.Getenv("OLLAMA_EMBEDDING_MODEL")
	ollamaEmbedder := NewOllamaEmbedder(ollamaURL, ollamaModel)

	log.Printf("[EMBEDDER] 🤖 Uses local Ollama for embeddings (%s)", ollamaModel)

	return &Embedder{
		ollamaEmbedder: ollamaEmbedder,
		embeddingType:  "ollama",
	}
}

// Embed создает эмбеддинг для текста
func (e *Embedder) Embed(text string) ([]float32, error) {
	embedding, err := e.ollamaEmbedder.Embed(text)
	if err != nil {
		log.Printf("[EMBEDDER] Error Ollama embeddings: %v", err)
		return nil, nil
	}
	return embedding, nil
}
