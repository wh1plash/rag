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

type Configer interface {
	SetConfig(context.Context, int, map[string]any) (types.ConfigParams, error)
	GetConfig(context.Context, int) (types.LLMConfig, error)
}
type DBStorer interface {
	Configer

	SaveDocument(context.Context, types.Document) error
	SaveTable(context.Context, types.FullTable) error
	GetDocumentByID(context.Context, uuid.UUID) (*types.Document, error)
	SaveChunk(context.Context, types.Chunk) error
	DeleteChunksByDocID(context.Context, uuid.UUID) error
	Search(context.Context, []float32, int) ([]types.Chunk, error)
	GetNeighbours(context.Context, uuid.UUID) ([]types.Chunk, error)
	GetTableByID(context.Context, uuid.UUID) (*types.FullTable, error)
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

func (p *PostgresStore) GetTableByID(ctx context.Context, tableID uuid.UUID) (*types.FullTable, error) {
	rows, err := p.pool.Query(ctx, "select * from tables where id =$1", tableID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() // Обязательно закрываем rows для освобождения соединения

	if !rows.Next() {
		return nil, sql.ErrNoRows
	}

	table := &types.FullTable{}
	if err := rows.Scan(
		&table.ID,
		&table.DocID,
		&table.Index,
		&table.Content); err != nil {
		return nil, err
	}
	return table, nil
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

func (p *PostgresStore) GetConfig(ctx context.Context, id int) (types.LLMConfig, error) {
	var cfg types.LLMConfig
	rows, err := p.pool.Query(ctx, "select * from config where id =$1", id)
	if err != nil {
		return cfg, err
	}
	defer rows.Close() // Обязательно закрываем rows для освобождения соединения

	if !rows.Next() {
		return cfg, sql.ErrNoRows
	}

	if err := rows.Scan(
		&id,
		&cfg.Url,
		&cfg.Model,
		&cfg.PromptStr); err != nil {
		return cfg, err
	}

	return cfg, nil
}

func (p *PostgresStore) SetConfig(ctx context.Context, id int, querySet map[string]any) (types.ConfigParams, error) {
	setClauses := []string{}
	args := []any{}
	argPos := 1

	for k, v := range querySet {
		setClauses = append(setClauses, fmt.Sprintf("%s = $%d", k, argPos))
		args = append(args, v)
		argPos++
	}

	args = append(args, id)

	query := fmt.Sprintf(`
	Update config 
	SET %s
	WHERE id=$%d 
	RETURNING id, llm_url, llm_model, prompt_str
	`, strings.Join(setClauses, ", "), argPos)

	updCfg := types.ConfigParams{}
	err := p.pool.QueryRow(ctx, query, args...).Scan(
		&id,
		&updCfg.Url,
		&updCfg.Model,
		&updCfg.PromptStr)

	if err != nil {
		fmt.Println("no rows found")
		fmt.Println(err)
		return updCfg, sql.ErrNoRows
	}

	return updCfg, nil
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

func (p *PostgresStore) SaveTable(ctx context.Context, t types.FullTable) error {
	query := `INSERT INTO tables (id, doc_id, index, content_md)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE SET
			id = EXCLUDED.id,
			doc_id = EXCLUDED.doc_id,
			index = EXCLUDED.index,
			content_md = EXCLUDED.content_md
			`
	_, err := p.pool.Exec(
		ctx,
		query,
		t.ID,
		t.DocID,
		t.Index,
		t.Content,
	)
	return err
}

func (p *PostgresStore) SaveChunk(ctx context.Context, c types.Chunk) error {
	query := `
    INSERT INTO chunks (id, doc_id, index, coherence_prev, type, section, key, table_id, content, embedding)
    VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
    `
	_, err := p.pool.Exec(ctx, query,
		c.ID,
		c.DocID,
		c.Index,
		c.CohPrev,
		c.Type,
		c.Section,
		c.Key,
		c.TableID,
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
		SELECT pc.id, pc.doc_id, pc.index, pc.type, pc.key, pc.table_id, pc.coherence_prev, pc.coherence_next, pc.content,
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
			&chunk.Type,
			&chunk.Key,
			&chunk.TableID,
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
	fmt.Println("Starting create tables...")
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
        type TEXT CHECK (type IN ('text','json','image','tablerow')),
        section TEXT,
		key TEXT,
		table_id UUID NULL,
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

	CREATE TABLE IF NOT EXISTS config (
		id INT NOT NULL PRIMARY KEY,
    	llm_url TEXT,    
		llm_model TEXT,
		prompt_str TEXT
    );

	CREATE TABLE IF NOT EXISTS tables (
		id           UUID PRIMARY KEY,
		doc_id       UUID NOT NULL,
		index  		 INT,
		content_md   TEXT
	);
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
