package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"rag/loader/service"
	"rag/store"
	"strconv"

	"github.com/joho/godotenv"
)

func init() {
	mustLoadEnvVariables()
}

func main() {
	ctx := context.Background()
	port, _ := strconv.Atoi(os.Getenv("PG_PORT"))
	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable", os.Getenv("PG_HOST"), port, os.Getenv("PG_USER"), os.Getenv("PG_PASS"), os.Getenv("PG_DB_NAME"))
	pool, err := store.NewPostgresStore(ctx, connStr)
	if err != nil {
		log.Fatal("error to connect to Postgres database", err)
		return
	}

	if err := pool.Init(ctx); err != nil {
		log.Fatal("error to create tables: ", err)
		return
	}

	// Запускаем сервис
	service.New(pool).Run()

	// Закрываем пул БД после завершения сервиса
	log.Println("Closing database connection pool...")
	if err := pool.Close(); err != nil {
		log.Printf("error closing pool: %v\n", err)
	} else {
		log.Println("Database connection pool closed successfully")
	}
}

func mustLoadEnvVariables() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}
}
