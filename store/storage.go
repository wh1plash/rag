package store

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"rag/types"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

type DBStorer interface {
	SaveDocument(context.Context, types.Document) error
	GetDocumentByID(context.Context, uuid.UUID) (*types.Document, error)
	SaveChunk(context.Context, types.Chunk) error
	DeleteChunksByDocID(context.Context, uuid.UUID) error
	Search(context.Context, []float32, int) ([]types.Chunk, error)
	GetNeighbours(context.Context, uuid.UUID) ([]types.Chunk, error)
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

func (p *PostgresStore) DeleteChunksByDocID(ctx context.Context, docID uuid.UUID) error {
	_, err := p.pool.Exec(ctx, "DELETE FROM chunks WHERE doc_id = $1", docID)
	// if err != nil {
	// 	return fmt.Errorf("error deleting old chunks: %w", err)
	// }
	return err
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

	return err
}

func (p *PostgresStore) SaveChunk(ctx context.Context, c types.Chunk) error {
	query := `
    INSERT INTO chunks (id, doc_id, index, type, section, content, embedding)
    VALUES ($1, $2, $3, $4, $5, $6, $7)
    `
	_, err := p.pool.Exec(ctx, query,
		c.ID,
		c.DocID,
		c.Index,
		c.Type,
		c.Section,
		c.Content,
		fmt.Sprintf("[%s]", toPgVector(c.Embedding)),
	)

	return err
}

func toPgVector(v []float32) string {
	parts := make([]string, len(v))
	for i, x := range v {
		parts[i] = fmt.Sprintf("%.6f", x)
	}
	//return "[" + strings.Join(parts, ",") + "]"
	return strings.Join(parts, ",")
}

func (p *PostgresStore) GetNeighbours(ctx context.Context, chunkIndex uuid.UUID) ([]types.Chunk, error) {
	query := `
			SELECT id, doc_id, index, coherence_prev, coherence_next, content
			FROM chunks
			WHERE doc_id = (
				SELECT doc_id FROM chunks WHERE id = $1
			)
			AND (
				index = (SELECT coherence_prev FROM chunks WHERE id = $1)
				OR
				index = (SELECT coherence_next FROM chunks WHERE id = $1)
			)
			order by index
	`
	rows, err := p.pool.Query(ctx, query, chunkIndex)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chunks []types.Chunk
	for rows.Next() {
		var chunk types.Chunk
		err := rows.Scan(
			&chunk.ID,
			&chunk.DocID,
			&chunk.Index,
			&chunk.CohPrev,
			&chunk.CohNext,
			&chunk.Content)
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, chunk)
	}
	return chunks, nil
}

func (p *PostgresStore) Search(ctx context.Context, queryVec []float32, limit int) ([]types.Chunk, error) {
	if len(queryVec) == 0 {
		return nil, fmt.Errorf("пустой вектор запроса")
	}

	vector := pgvector.NewVector(queryVec)

	query := `
		SELECT pc.id, pc.doc_id, pc.index, pc.coherence_prev, pc.coherence_next, pc.content,
		       1-(pc.embedding <=> $1) as distance
		FROM chunks pc
		JOIN documents doc ON pc.doc_id = doc.id
		WHERE pc.embedding IS NOT NULL 
		ORDER BY distance DESC
		LIMIT $2
	`
	rows, err := p.pool.Query(ctx, query, vector, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chunks []types.Chunk
	for rows.Next() {
		var chunk types.Chunk
		err := rows.Scan(
			&chunk.ID,
			&chunk.DocID,
			&chunk.Index,
			&chunk.CohPrev,
			&chunk.CohNext,
			&chunk.Content,
			&chunk.Distance)
		if err != nil {
			return nil, err
		}

		// // Конвертируем pgvector.Vector обратно в []float32
		// chunk.Embedding = embeddingVector.Slice()
		// // Сохраняем расстояние для сортировки и отображения
		// chunk.Distance = distance

		log.Printf("[SEARCH] Найден чанк: %s, Документ: %s, Index: %d, (расстояние: %.4f)\n", chunk.ID, chunk.DocID, chunk.Index, chunk.Distance)
		chunks = append(chunks, chunk)
	}
	return chunks, nil
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
        index INT NOT NULL,
        type TEXT CHECK (type IN ('text','json')),
        section TEXT,
		coherence_prev INT,
		coherence_next INT,
        content TEXT NOT NULL,
        embedding vector(1024)
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
