package main

import (
	"log"
	"os"
	"os/signal"
	"rag/app/server"
	"syscall"

	"github.com/joho/godotenv"
)

func init() {
	mustLoadEnvVariables()
}

func main() {
	s := server.NewServer(os.Getenv("SERVER_ADDR"))

	go s.Run()

	sigch := make(chan os.Signal, 1)
	signal.Notify(sigch, os.Interrupt, syscall.SIGTERM)
	<-sigch
	log.Println("Received shutdown signal, shutting down server...")
	s.Stop()
}

func mustLoadEnvVariables() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

}
