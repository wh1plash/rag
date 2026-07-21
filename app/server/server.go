package server

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"rag/app/api"
	"rag/app/middleware"
	"rag/internal/adapter/openai"
	"rag/internal/adapter/tavily"
	"rag/internal/port"
	"rag/internal/usecase/webingest"
	"rag/model"
	"rag/store"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
)

var config = fiber.Config{
	ErrorHandler: api.ErrorHandler,
}

type Server struct {
	listenAddr string
	logger     *slog.Logger
}

func NewServer(addr string) *Server {
	return &Server{
		listenAddr: addr,
		logger:     slog.Default(),
	}
}

func (s *Server) Stop() {
	s.logger.Info("server stopped")
}

func (s *Server) Run() {
	ctx := context.Background()
	pgPort, _ := strconv.Atoi(os.Getenv("PG_PORT"))
	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable", os.Getenv("PG_HOST"), pgPort, os.Getenv("PG_USER"), os.Getenv("PG_PASS"), os.Getenv("PG_DB_NAME"))
	pool, err := store.NewPostgresStore(ctx, connStr)
	if err != nil {
		log.Fatal("error to connect to Postgres database", err)
		return
	}
	if err := pool.Init(ctx); err != nil {
		log.Fatal("error to init Postgres schema", err)
		return
	}
	ensureDefaultLLMConfig(ctx, pool)

	embedder := model.NewOllamaEmbedder()
	webSearcher, webIngest, webOpts := wireWebSearch(pool, embedder)
	localLLM, remoteLLM := wireLLMs()

	var (
		app            = fiber.New(config)
		checkHandler   = api.NewCheckHandler
		requestHandler = api.NewRequestHandlerWithDeps(api.RequestHandlerDeps{
			Store:       pool,
			Embedder:    embedder,
			WebSearcher: webSearcher,
			WebIngest:   webIngest,
			WebOpts:     webOpts,
			LocalLLM:    localLLM,
			RemoteLLM:   remoteLLM,
		})
		fileHandler        = api.NewRequestHandler(pool)
		configHandler      = api.NewConfigHandler(pool)
		fileProcessHandler = api.NewFileHandler(pool)
		check              = app.Group("/check")
		apiv1              = app.Group("/api/v1")
	)

	app.Use(cors.New(cors.Config{
		AllowOrigins: strings.Join(corsOrigins(), ","),
		AllowMethods: "GET,POST,OPTIONS",
		AllowHeaders: "Content-Type,Accept,Origin",
	}))

	check.Get("/healthy", checkHandler().HandleHealthy)
	apiv1.Post("/request", requestHandler.HandleRequest)
	apiv1.Post("/upload", fileHandler.HandlePDF)
	apiv1.Post("/process", fileProcessHandler.ProcessFile)
	apiv1.Post("/config/:id", configHandler.HandleSetConfig)

	app.Use(middleware.PlugStatic("/"))
	app.Static("/", "./public")

	err = app.Listen(s.listenAddr)
	if err != nil {
		s.logger.Error("error to start server", "error", err.Error())
		return
	}
}

func ensureDefaultLLMConfig(ctx context.Context, st store.DBStorer) {
	const id = 2
	if _, err := st.GetConfig(ctx, id); err == nil {
		return
	}
	pg, ok := st.(*store.PostgresStore)
	if !ok {
		log.Printf("[CONFIG] cannot seed default config: store is not PostgresStore")
		return
	}
	url := envOr("LLM_LOCAL_BASE_URL", envOr("LLM_URL", "http://localhost:11434/v1"))
	modelName := envOr("LLM_LOCAL_MODEL", envOr("LLM_MODEL", "qwen2.5:7b"))
	prompt := envOr("LLM_SYSTEM_PROMPT", "Отвечай только на основе предоставленного контекста. Если в контексте нет ответа — скажи об этом.")
	if err := pg.SeedConfig(ctx, id, url, modelName, prompt); err != nil {
		log.Printf("[CONFIG] seed id=%d failed: %v", id, err)
		return
	}
	log.Printf("[CONFIG] seeded default llm config id=%d model=%s", id, modelName)
}

func wireLLMs() (local port.AnswerGenerator, remote port.AnswerGenerator) {
	localBase := envOr("LLM_LOCAL_BASE_URL", "http://localhost:11434/v1")
	localModel := envOr("LLM_LOCAL_MODEL", "qwen2.5:7b")
	numCtx := envInt("LLM_LOCAL_NUM_CTX", 8192)
	numThread := envInt("LLM_LOCAL_NUM_THREAD", 8)
	localClient, err := openai.New(openai.Config{
		Name:        "local",
		BaseURL:     localBase,
		APIKey:      os.Getenv("LLM_LOCAL_API_KEY"),
		Model:       localModel,
		NumCtx:      numCtx,
		NumThread:   numThread,
		Temperature: envFloat("LLM_LOCAL_TEMPERATURE", 0.2),
	})
	if err != nil {
		log.Printf("[LLM] local disabled: %v", err)
	} else {
		local = localClient
		log.Printf("[LLM] local base=%s model=%s num_ctx=%d num_thread=%d", localBase, localModel, numCtx, numThread)
	}

	remoteBase := strings.TrimSpace(os.Getenv("LLM_REMOTE_BASE_URL"))
	remoteKey := strings.TrimSpace(os.Getenv("LLM_REMOTE_API_KEY"))
	remoteModel := envOr("LLM_REMOTE_MODEL", "openai/gpt-4o-mini")
	if remoteBase == "" {
		log.Println("[LLM] remote disabled: set LLM_REMOTE_BASE_URL (e.g. https://openrouter.ai/api/v1)")
		return local, nil
	}
	if remoteKey == "" {
		log.Println("[LLM] remote disabled: LLM_REMOTE_API_KEY is empty")
		return local, nil
	}
	remoteClient, err := openai.New(openai.Config{
		Name:        "remote",
		BaseURL:     remoteBase,
		APIKey:      remoteKey,
		Model:       remoteModel,
		Temperature: envFloat("LLM_REMOTE_TEMPERATURE", 0.2),
		HTTPReferer: envOr("LLM_REMOTE_HTTP_REFERER", "http://localhost:3000"),
		AppTitle:    envOr("LLM_REMOTE_APP_TITLE", "rag"),
	})
	if err != nil {
		log.Printf("[LLM] remote disabled: %v", err)
		return local, nil
	}
	log.Printf("[LLM] remote base=%s model=%s", remoteBase, remoteModel)
	return local, remoteClient
}

func wireWebSearch(pool store.DBStorer, embedder model.EmbedderInterface) (port.WebSearcher, *webingest.Service, port.SearchOptions) {
	opts := port.SearchOptions{
		MaxResults:  envInt("TAVILY_MAX_RESULTS", 3),
		SearchDepth: envOr("TAVILY_SEARCH_DEPTH", "basic"),
		IncludeRaw:  envBool("TAVILY_INCLUDE_RAW", true),
	}

	enabled := envBool("WEB_SEARCH_ENABLED", true)
	apiKey := strings.TrimSpace(os.Getenv("TAVILY_API_KEY"))
	if !enabled || apiKey == "" {
		if enabled && apiKey == "" {
			log.Println("[WEB] WEB_SEARCH_ENABLED but TAVILY_API_KEY empty — web fallback disabled")
		} else {
			log.Println("[WEB] web fallback disabled")
		}
		return nil, nil, opts
	}

	client, err := tavily.New(tavily.Config{APIKey: apiKey})
	if err != nil {
		log.Printf("[WEB] failed to init tavily: %v — web fallback disabled", err)
		return nil, nil, opts
	}

	// PDF loader uses CHUNK_SIZE / CHUNK_OVERLAP.
	// Web ingest: WEB_CHUNK_* (крупнее; короткие сниппеты → 1 чанк).
	chunkSize := envInt("WEB_CHUNK_SIZE", 300)
	overlap := envInt("WEB_CHUNK_OVERLAP", 50)
	ingest := webingest.New(pool, embedder, webingest.Config{
		ChunkSize: chunkSize,
		Overlap:   overlap,
	})

	log.Printf("[WEB] tavily enabled max_results=%d depth=%s include_raw=%v web_chunk_size=%d web_chunk_overlap=%d",
		opts.MaxResults, opts.SearchDepth, opts.IncludeRaw, chunkSize, overlap)
	return client, ingest, opts
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envFloat(key string, fallback float64) float64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return n
}

func envBool(key string, fallback bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if v == "" {
		return fallback
	}
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func corsOrigins() []string {
	if raw := os.Getenv("ALLOWED_ORIGINS"); raw != "" {
		parts := strings.Split(raw, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return []string{
		"http://localhost:5173",
		"http://localhost:3000",
		"http://127.0.0.1:5173",
	}
}
