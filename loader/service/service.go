package service

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strconv"

	"rag/loader/internal"
	"rag/loader/store"
	"rag/loader/types"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
)

// +371 29 16 74 29

type Service struct {
	logger *slog.Logger
	store  store.DBStorer
	loader *internal.PDFLoader
}

func NewConfig() types.Config {
	intervalStr := os.Getenv("LOADER_MONITORING_TIME")
	interval, err := time.ParseDuration(intervalStr)
	if err != nil {
		log.Fatal("Error to set ticker interval")
	}

	chunkSize, _ := strconv.Atoi(os.Getenv("CHUNK_SIZE"))
	chunkOverlap, _ := strconv.Atoi(os.Getenv("CHUNK_OVERLAP"))

	return types.Config{
		MonitoringTime: interval,
		SourceDir:      os.Getenv("LOADER_SOURCE_DIR"),
		ArchiveDir:     os.Getenv("LOADER_ARCHIVE_DIR"),
		BadDir:         os.Getenv("LOADER_BAD_DIR"),
		ChunkSize:      chunkSize,
		ChunkOverlap:   chunkOverlap,
	}
}

func New(storer store.DBStorer) *Service {
	cfg := NewConfig()
	return &Service{
		logger: slog.Default(),
		store:  storer,
		loader: internal.NewPDFLoader(cfg),
	}
}

func (s *Service) Stop() {
	s.logger.Info("Loader Service stopped")
}

// POSTGRES_DSN=postgres://postgres:postgres@localhost:5432/confluence?sslmode=disable
func (s *Service) Run() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fileChan := make(chan string, 10) // Буферизованный канал для предотвращения блокировок
	var wg sync.WaitGroup

	docChan := make(chan *types.Document)

	// Запуск горутины для мониторинга файлов
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(fileChan) // Закрываем канал при завершении watchFile
		s.loader.WatchFile(ctx, fileChan)
	}()

	// Запуск горутины для обработки файлов
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.loader.ProcessFile(ctx, fileChan, docChan)
	}()

	// Запуск горутины для сохранения документа
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.DocumentSave(ctx, docChan)
	}()

	// Обработка сигналов завершения
	sigch := make(chan os.Signal, 1)
	signal.Notify(sigch, os.Interrupt, syscall.SIGTERM)

	<-sigch
	log.Println("Received shutdown signal, shutting down gracefully...")

	// Отменяем контекст для остановки всех горутин
	cancel()

	// Останавливаем получение сигналов
	signal.Stop(sigch)
	close(sigch)

	// Ждем завершения всех горутин с таймаутом
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Println("All goroutines stopped successfully")
	case <-shutdownCtx.Done():
		log.Println("Timeout waiting for goroutines to stop, forcing shutdown...")
	}

	s.Stop()
	log.Println("Service stopped successfully")
}

func (s *Service) DocumentSave(ctx context.Context, docChan <-chan *types.Document) error {
	for {
		doc, ok := <-docChan
		if !ok {
			break
		}

		if !s.ShouldUpdateFile(ctx, doc.ID, doc.UpdatedAt) {
			s.loader.MoveToArchive(doc.SourcePath, 1)
			return nil
		}

		//remove old chunks from DB
		if err := s.store.DeleteChunksByDocID(ctx, doc.ID); err != nil {
			fmt.Println(err)
			return err
		}

		// Используем переданный контекст для операций с БД
		if err := s.store.SaveDocument(ctx, *doc); err != nil {
			fmt.Println(err)
			return err
		}

		for i := range doc.Chunks {
			if err := s.store.SaveChunk(ctx, doc.Chunks[i]); err != nil {
				fmt.Println(err)
				return err
			}
		}

		fmt.Printf("Successfuly Saved document\n")
		s.loader.MoveToArchive(doc.SourcePath, 0)
	}
	return nil
}

func (s *Service) ShouldUpdateFile(ctx context.Context, docID uuid.UUID, modTime time.Time) bool {
	doc, err := s.store.GetDocumentByID(ctx, docID)
	if err != nil {
		// Документ не найден в БД, значит нужно добавить
		fmt.Println("Document not found in DB. Inserting")
		return true
	}
	// Обновляем если файл изменился
	fmt.Println("Document exists in DB. Check for mod date")
	// fmt.Println(modTime)
	// fmt.Println(doc.UpdatedAt)
	fmt.Println("Need to update:", modTime.After(doc.UpdatedAt))
	return modTime.After(doc.UpdatedAt)
}
