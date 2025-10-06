package types

import (
	"time"

	"github.com/google/uuid"
)

type Chunk struct {
	ID        uuid.UUID
	DocID     uuid.UUID
	Position  int
	Type      string
	Section   string
	Content   string
	Embedding []float32
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
