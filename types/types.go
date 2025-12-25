package types

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

type ChunkType string

const (
	ChunkText     ChunkType = "text"
	ChunkTableRow ChunkType = "tablerow"
	ChunkImage    ChunkType = "image"
)

type Chunk struct {
	ID        uuid.UUID
	DocID     uuid.UUID
	Index     int
	Type      string
	Section   string
	Key       string
	TableID   uuid.NullUUID
	CohPrev   sql.NullInt64
	CohNext   sql.NullInt64
	Content   string
	Embedding []float32
	Distance  float64
}

type FullTable struct {
	ID      uuid.UUID
	DocID   uuid.UUID
	Index   int
	Content string
}

type Document struct {
	ID         uuid.UUID // Уникальный идентификатор документа
	Title      string    // Заголовок документа
	Chunks     []Chunk
	FullTable  []FullTable
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
	Url       string
	Model     string
	PromptStr string
}

type DoclingResponse struct {
	Document struct {
		MdContent string `json:"md_content"`
	} `json:"document"`
}
