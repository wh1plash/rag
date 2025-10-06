package store

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"rag/loader/types"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DBStorer interface {
	SaveDocument(context.Context, types.Document) error
	GetDocumentByID(context.Context, uuid.UUID) (*types.Document, error)
	SaveChunk(context.Context, types.Chunk) error
}

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(ctx context.Context, connStr string) (*PostgresStore, error) {
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		return nil, err
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}

	return &PostgresStore{
		pool: pool,
	}, nil
}

func (p *PostgresStore) GetDocumentByID(ctx context.Context, docID uuid.UUID) (*types.Document, error) {
	rows, err := p.pool.Query(ctx, "select * from documents where id =$1", docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() // Обязательно закрываем rows для освобождения соединения

	if !rows.Next() {
		return nil, sql.ErrNoRows
	}

	doc := &types.Document{}
	if err := rows.Scan(
		&doc.ID,
		&doc.Title,
		&doc.Source,
		&doc.SourcePath,
		&doc.CreatedAt,
		&doc.UpdatedAt,
		&doc.Version); err != nil {
		return nil, err
	}
	return doc, nil
}

func (p *PostgresStore) SaveDocument(ctx context.Context, doc types.Document) error {
	query := `INSERT INTO documents (id, title, source, source_path, created_at, updated_at, version)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (id) DO UPDATE SET
			title = EXCLUDED.title,
			source = EXCLUDED.source,
			source_path = EXCLUDED.source_path,
			updated_at = EXCLUDED.updated_at,
			version = EXCLUDED.version
			`
	_, err := p.pool.Exec(
		ctx,
		query,
		doc.ID,
		doc.Title,
		doc.Source,
		doc.SourcePath,
		doc.CreatedAt,
		doc.UpdatedAt,
		doc.Version,
	)
	fmt.Println("Saved")
	return err
}

func (p *PostgresStore) SaveChunk(ctx context.Context, c types.Chunk) error {
	query := `
    INSERT INTO chunks (id, doc_id, position, type, section, content, embedding)
    VALUES ($1, $2, $3, $4, $5, $6, $7)
    `
	_, err := p.pool.Exec(ctx, query,
		c.ID, c.DocID, c.Position, c.Type, c.Section, c.Content, toPgVector(c.Embedding),
	)
	return err
}

func toPgVector(v []float32) string {
	parts := make([]string, len(v))
	for i, x := range v {
		parts[i] = fmt.Sprintf("%f", x)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func (p *PostgresStore) createRagTables(ctx context.Context) error {

	query := `
	CREATE TABLE IF NOT EXISTS documents (
		id UUID PRIMARY KEY,
		title TEXT NOT NULL,
		source TEXT,
		source_path TEXT,
		created_at TIMESTAMP WITH TIME ZONE,
		updated_at TIMESTAMP WITH TIME ZONE,
		version INTEGER DEFAULT 1
	);

	CREATE INDEX IF NOT EXISTS idx_id ON documents(id);

    CREATE EXTENSION IF NOT EXISTS vector;

    CREATE TABLE IF NOT EXISTS chunks (
        id UUID PRIMARY KEY,
        doc_id UUID NOT NULL,
        position INT NOT NULL,
        type TEXT CHECK (type IN ('text','json')),
        section TEXT,
        content TEXT NOT NULL,
        embedding vector(768) -- если используем OpenAI ada-002
    );

	-- Индекс для быстрого поиска по вектору
	CREATE INDEX IF NOT EXISTS idx_chunks_embedding ON chunks USING ivfflat (embedding vector_cosine_ops)
	WITH (lists = 100);

	-- Индексы для фильтрации
	CREATE INDEX IF NOT EXISTS idx_chunks_doc_id ON chunks(doc_id);
	CREATE INDEX IF NOT EXISTS idx_chunks_type ON chunks(type);
	CREATE INDEX IF NOT EXISTS idx_chunks_section ON chunks(section);
    `
	_, err := p.pool.Exec(ctx, query)
	return err
}

func (p *PostgresStore) Init(ctx context.Context) error {
	return p.createRagTables(ctx)
}

// Close закрывает пул подключений
func (s *PostgresStore) Close() error {
	if s.pool != nil {
		s.pool.Close()
		log.Println("Postgres connection pool is closed")
	}
	return nil
}
