package types

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

type Chunk struct {
	ID        uuid.UUID
	DocID     uuid.UUID
	Index     int
	Type      string
	Section   string
	CohPrev   sql.NullInt64
	CohNext   sql.NullInt64
	Content   string
	Embedding []float32
	Distance  float64
}

type Document struct {
	ID         uuid.UUID // Уникальный идентификатор документа
	Title      string    // Заголовок документа
	Chunks     []Chunk
	Source     string    // Источник документа (confluence, pdf, etc.)
	SourcePath string    // URL или путь к источнику
	CreatedAt  time.Time // Время создания
	UpdatedAt  time.Time // Время последнего обновления
	Version    int       // Версия документа
}

type Config struct {
	MonitoringTime time.Duration
	SourceDir      string
	ArchiveDir     string
	BadDir         string
	ChunkSize      int
	ChunkOverlap   int
}

type LLMConfig struct {
	EmbeddingUrl   string
	EmbeddingModel string
	LLMUrl         string
	LLMModel       string
	PromptStr      string
}
