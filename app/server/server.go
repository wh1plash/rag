package server

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"rag/app/api"
	"rag/store"
	"strconv"

	"github.com/gofiber/fiber/v2"
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
	port, _ := strconv.Atoi(os.Getenv("PG_PORT"))
	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable", os.Getenv("PG_HOST"), port, os.Getenv("PG_USER"), os.Getenv("PG_PASS"), os.Getenv("PG_DB_NAME"))
	pool, err := store.NewPostgresStore(ctx, connStr)
	if err != nil {
		log.Fatal("error to connect to Postgres database", err)
		return
	}

	if err := pool.Init(ctx); err != nil {
		log.Fatal("error to create tables", err)
		return
	}

	var (
		app            = fiber.New(config)
		checkHandler   = api.NewCheckHandler
		requestHandler = api.NewRequestHandler(pool)
		// userHandler  = api.NewUserHandler(db)
		// authHandler  = api.NewAuthHandler(db)
		check = app.Group("/check")
		apiv1 = app.Group("/api/v1")
	)

	check.Get("/healthy", checkHandler().HandleHealthy)
	apiv1.Post("/request", requestHandler.HandleRequest)

	err = app.Listen(s.listenAddr)
	if err != nil {
		s.logger.Error("error to start server", "error", err.Error())
		return
	}
}
