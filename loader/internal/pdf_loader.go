package internal

import (
	"bufio"
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"rag/model"
	"rag/types"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type PDFLoader struct {
	cfg      types.Config
	embedder model.EmbedderInterface

	FileMutex       sync.Mutex
	FileFirstSeen   map[string]time.Time
	FilesProcessing map[string]bool
}

func NewPDFLoader(cfg types.Config) *PDFLoader {
	createDirectories(cfg.SourceDir, cfg.ArchiveDir, cfg.BadDir)
	embedder := model.NewOllamaEmbedder()
	return &PDFLoader{
		cfg:             cfg,
		FileFirstSeen:   make(map[string]time.Time),
		FilesProcessing: make(map[string]bool),
		embedder:        embedder,
	}
}

func (l *PDFLoader) WatchFile(ctx context.Context, fileChan chan<- string) {
	fmt.Printf("Start monitoring folder: %s\n", l.cfg.SourceDir)

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	defer fmt.Println("File watcher stopped and cleaned up")

	for {
		// Проверяем контекст перед каждой итерацией для быстрой остановки
		if ctx.Err() != nil {
			fmt.Println("Stopping file watcher (pre-check)...")
			return
		}

		select {
		case <-ctx.Done():
			fmt.Println("Stopping file watcher (context cancelled)...")
			return
		case <-ticker.C:
			files, err := os.ReadDir(l.cfg.SourceDir)
			if err != nil {
				fmt.Printf("error while reading source directory: %s\n", err)
				continue
			}

			currentFiles := make(map[string]bool)

			for _, file := range files {
				if file.IsDir() {
					continue
				}

				filePath := filepath.Join(l.cfg.SourceDir, file.Name())
				currentFiles[filePath] = true

				l.FileMutex.Lock()
				// Проверяем, не находится ли файл в обработке
				if l.FilesProcessing[filePath] {
					l.FileMutex.Unlock()
					continue
				}

				// Если файл новый, добавляем его в отслеживание
				if _, exists := l.FileFirstSeen[filePath]; !exists {
					l.FileFirstSeen[filePath] = time.Now()
					fmt.Printf("New file detected: %s\n", filePath)
					l.FileMutex.Unlock()
					continue
				}

				// Проверяем, готов ли файл к отправке
				firstSeen := l.FileFirstSeen[filePath]
				l.FileMutex.Unlock()

				if time.Since(firstSeen) > l.cfg.MonitoringTime {
					fmt.Printf("The file %s has not been modified for more than %v seconds. Start processing...\n", filePath, l.cfg.MonitoringTime.Seconds())

					// Помечаем файл как находящийся в обработке
					l.FileMutex.Lock()
					l.FilesProcessing[filePath] = true
					l.FileMutex.Unlock()

					// Отправляем файл в канал (неблокирующая отправка с контекстом)
					select {
					case fileChan <- filePath:
					case <-ctx.Done():
						return
					}
				} else {
					fmt.Printf("The file %s is not ready for sending yet\n", filePath)
				}
			}

			// Удаляем из карты файлы, которых больше нет в директории
			l.FileMutex.Lock()
			for filePath := range l.FileFirstSeen {
				if !currentFiles[filePath] {
					delete(l.FileFirstSeen, filePath)
					delete(l.FilesProcessing, filePath)
					fmt.Printf("The file has been removed from tracking: %s\n", filePath)
				}
			}
			l.FileMutex.Unlock()
		}
	}
}

func (l *PDFLoader) ProcessFile(ctx context.Context, fileChan <-chan string, docChan chan<- *types.Document) {
	defer fmt.Println("File processor stopped and cleaned up")

	for {
		// Проверяем контекст перед каждой итерацией для быстрой остановки
		if ctx.Err() != nil {
			fmt.Println("Stopping file processor (pre-check)...")
			return
		}

		select {
		case <-ctx.Done():
			fmt.Println("Stopping file processor (context cancelled)...")
			return
		case filePath, ok := <-fileChan:
			if !ok {
				// Канал закрыт, завершаем работу
				fmt.Println("File channel closed, stopping processor...")
				return
			}

			// Проверяем контекст перед обработкой файла
			if ctx.Err() != nil {
				fmt.Println("Context cancelled before processing, stopping processor...")
				return
			}
			l.FileMutex.Lock()
			fmt.Printf("Processing file: %s\n", filePath)
			doc, err := l.fetchFile(ctx, filePath)
			if err != nil {
				fmt.Println("Error to fatch file", err)
			}
			docChan <- doc
			l.FileMutex.Unlock()

			// Проверяем был ли контекст отменён во время обработки
			if ctx.Err() != nil {
				fmt.Printf("File processing interrupted due to context cancellation: %s\n", filePath)
				// Очищаем состояние файла но не удаляем его из source
				l.FileMutex.Lock()
				delete(l.FilesProcessing, filePath)
				// НЕ удаляем из fileFirstSeen, чтобы файл был обработан при следующем запуске
				l.FileMutex.Unlock()
				return
			}

			if err != nil {
				fmt.Printf("Error processing file %s: %v\n", filePath, err)
			}

			// Удаляем файл из списка обрабатываемых
			l.FileMutex.Lock()
			delete(l.FilesProcessing, filePath)
			delete(l.FileFirstSeen, filePath)
			l.FileMutex.Unlock()
		}
	}
}

func (l *PDFLoader) fetchFile(ctx context.Context, filePath string) (*types.Document, error) {
	// Проверка существования файла перед его открытием
	fileInfo, err := os.Stat(filePath)
	os.IsNotExist(err)
	if err != nil {
		return nil, fmt.Errorf("file does not exist: %s", filePath)
	}

	docIDStr := generateDocumentID(filePath)

	id, err := uuid.Parse(docIDStr)
	if err != nil {
		fmt.Println("invalid uuid:", err)
		return nil, err
	}

	chunks, err := l.splitByChunks(filePath, id, l.cfg.ChunkSize, l.cfg.ChunkOverlap)
	if err != nil {
		return nil, err
	}

	doc := &types.Document{
		ID:         id,
		Title:      generateTitle(filePath),
		Chunks:     chunks,
		Source:     "pdf",
		SourcePath: filePath,
		CreatedAt:  fileInfo.ModTime(),
		UpdatedAt:  fileInfo.ModTime(),
		Version:    1,
	}
	//fmt.Println(doc)

	// Проверка контекста перед запросом к БД
	if ctx.Err() != nil {
		fmt.Println("Context cancelled before checking file update")
		return nil, ctx.Err()
	}

	// Перемещение файла в архив после успешной отправки
	//l.MoveToArchive(filePath, 0) // Убедитесь, что moveToArchive не возвращает ошибку
	//doc := &types.Document{}
	return doc, nil
}

func (l *PDFLoader) splitByChunks(filePath string, id uuid.UUID, chunkSize, overlap int) ([]types.Chunk, error) {

	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("error opening the file: %v", err)
	}
	defer func(file *os.File) {
		_ = file.Close()
	}(file)

	// Читаем всё содержимое построчно
	var builder strings.Builder
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		builder.WriteString(scanner.Text())
		builder.WriteByte(' ')
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	text := builder.String()
	words := strings.Fields(text) // Разделяем по пробелам

	var chunks []types.Chunk

	// Разбиваем на чанки
	for i, pos := 0, 0; i < len(words); i, pos = i+(chunkSize-overlap), pos+1 {
		end := i + chunkSize
		if end > len(words) {
			end = len(words)
		}
		content := strings.Join(words[i:end], " ")
		embedding, err := l.embedder.Embed(content)
		if err != nil {
			log.Printf("error creation embedding for chunk %d document %s: %v", i, id, err)
			continue
		}
		chunks = append(chunks, types.Chunk{
			ID:        uuid.New(),
			DocID:     id,
			Index:     pos,
			Type:      "text",
			Section:   "",
			Content:   content,
			Embedding: embedding,
		})

		if end == len(words) {
			break
		}
	}

	return chunks, nil
}

func generateTitle(filePath string) string {
	fileName := filepath.Base(filePath)
	// Удаляем расширение .pdf
	if strings.HasSuffix(strings.ToLower(fileName), ".pdf") {
		fileName = fileName[:len(fileName)-4]
	}
	// Заменяем подчеркивания и дефисы на пробелы
	fileName = strings.ReplaceAll(fileName, "_", " ")
	fileName = strings.ReplaceAll(fileName, "-", " ")
	return fileName
}

func generateDocumentID(filePath string) string {
	hash := md5.Sum([]byte(filePath))
	return fmt.Sprintf("%x", hash)
}

func (l *PDFLoader) MoveToArchive(filePath string, fileState int) {
	var state string
	switch fileState {
	case 1:
		state = l.cfg.BadDir
	default:
		state = l.cfg.ArchiveDir
	}

	currentDate := time.Now().Format("2006-01-02")
	destDir := filepath.Join(state, currentDate)

	// Проверка и создание директории
	if _, err := os.Stat(destDir); os.IsNotExist(err) {
		if err := os.MkdirAll(destDir, 0755); err != nil {
			fmt.Printf("error creating directory: %s\n", err)
			return
		}
	}

	destPath := filepath.Join(destDir, filepath.Base(filePath))

	// Обработка конфликтов имен файлов
	counter := 1
	for {
		if _, err := os.Stat(destPath); os.IsNotExist(err) {
			break
		}
		ext := filepath.Ext(destPath)
		baseName := strings.TrimSuffix(filepath.Base(destPath), ext)
		destPath = filepath.Join(destDir, fmt.Sprintf("%s_%d%s", baseName, counter, ext))
		counter++
	}

	// err := os.Rename(filePath, destPath)
	// if err != nil {
	// 	fmt.Printf("error moving file to archive: %s\n", err)
	// 	return
	// }

	in, err := os.Open(filePath)
	if err != nil {
		fmt.Printf("error open file: %s\n", err)
	}
	defer in.Close()

	out, err := os.Create(destPath)
	if err != nil {
		fmt.Printf("error create file: %s\n", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		fmt.Printf("error moving file to archive: %s\n", err)
	}

	fmt.Printf("File moved to archive: %s\n", destPath)
	in.Close()
	os.Remove(filePath)
}

func createDirectories(sourceDir, archiveDir, badDir string) error {
	dirs := []string{sourceDir, archiveDir, badDir}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return nil
}
