package internal

import (
	"bytes"
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"rag/model"
	"rag/types"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type PDFLoader struct {
	cfg       types.Config
	embedder  model.EmbedderInterface
	converter model.VisionModel

	FileMutex       sync.Mutex
	FileFirstSeen   map[string]time.Time
	FilesProcessing map[string]bool
}

func NewPDFLoader(cfg types.Config) *PDFLoader {
	createDirectories(cfg.SourceDir, cfg.ArchiveDir, cfg.BadDir)
	embedder := model.NewOllamaEmbedder()
	converter := model.NewLLaVA()
	return &PDFLoader{
		cfg:             cfg,
		FileFirstSeen:   make(map[string]time.Time),
		FilesProcessing: make(map[string]bool),
		embedder:        embedder,
		converter:       converter,
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

	chunks, tables, err := l.splitByChunks(filePath, id, l.cfg.ChunkSize, l.cfg.ChunkOverlap)
	if err != nil {
		return nil, err
	}

	doc := &types.Document{
		ID:         id,
		Title:      generateTitle(filePath),
		Chunks:     chunks,
		FullTable:  tables,
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

func (l *PDFLoader) addTextChunks(chunks *[]types.Chunk, text string, docID uuid.UUID, startIndex *int, chunkSize, overlap int) {
	words := strings.Fields(text)

	for i := 0; i < len(words); i += chunkSize - overlap {
		end := i + chunkSize
		if end > len(words) {
			end = len(words)
		}

		content := strings.Join(words[i:end], " ")
		if strings.TrimSpace(content) == "" {
			continue
		}

		embedding, err := l.embedder.Embed(content)
		if err != nil {
			log.Printf("embedding error: %v", err)
			continue
		}

		*chunks = append(*chunks, types.Chunk{
			ID:        uuid.New(),
			DocID:     docID,
			Index:     *startIndex,
			Type:      string(types.ChunkText),
			Content:   content,
			Embedding: embedding,
		})

		*startIndex++

		if end == len(words) {
			break
		}
	}
}

func (l *PDFLoader) splitByChunks(filePath string, id uuid.UUID, chunkSize, overlap int) ([]types.Chunk, []types.FullTable, error) {

	var chunks []types.Chunk
	var tables []types.FullTable
	pos := 0

	err := RemoveHeaderFooterCrop(filePath, filePath, 46, 57)
	if err != nil {
		return nil, tables, err
	}

	mdFile, err := convertPDFToMD(filePath)
	if err != nil {
		return nil, tables, err
	}

	// 1. Tokenize markdown (TEXT + IMAGE + TABLE)
	tokens := tokenizeMD(mdFile)

	// 2. Merge adjacent TEXT tokens
	tokens = mergeAdjacentText(tokens)

	// 3. Merge adjacent Tables tokens
	tokens = mergeAdjacentTables(tokens)

	// 4. Unified pass
	for _, token := range tokens {

		switch token.Type {

		// -------- TEXT --------
		case tokenText:
			l.addTextChunks(
				&chunks,
				token.Content,
				id,
				&pos,
				chunkSize,
				overlap,
			)

		// -------- IMAGE --------
		case tokenImage:
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Minute)

			jsonContent, err := l.converter.Retry(ctx, token.Content, 3)
			cancel()
			if err != nil {
				log.Printf("image recognition error: %v", err)
				continue
			}

			prev := sql.NullInt64{}
			if pos > 0 {
				prev.Int64 = int64(pos - 1)
				prev.Valid = true
			}

			embedding, err := l.embedder.Embed(jsonContent)
			if err != nil {
				log.Printf("embedding image json error: %v", err)
			}

			chunks = append(chunks, types.Chunk{
				ID:        uuid.New(),
				DocID:     id,
				Index:     pos,
				Type:      string(types.ChunkImage),
				Content:   jsonContent,
				CohPrev:   prev,
				Embedding: embedding,
			})

			pos++

		// -------- TABLE --------
		case tokenTable:
			tableID := uuid.New()

			var tableBuilder strings.Builder
			tableBuilder.WriteString("| Параметр | Описание |\n")
			tableBuilder.WriteString("|----------|----------|\n")

			for _, row := range token.Table {
				tableBuilder.WriteString("| ")
				tableBuilder.WriteString(row.Key)
				tableBuilder.WriteString(" | ")
				tableBuilder.WriteString(row.Value)
				tableBuilder.WriteString(" |\n")
			}

			fullTableMD := tableBuilder.String()

			fullTable := types.FullTable{
				ID:      tableID,
				DocID:   id,
				Content: fullTableMD,
			}
			tables = append(tables, fullTable)

			for _, row := range token.Table {

				var b strings.Builder
				b.WriteString("Параметр: ")
				b.WriteString(row.Key)
				b.WriteString(". Описание: ")
				b.WriteString(row.Value)
				b.WriteString(".")

				text := b.String()
				emb, _ := l.embedder.Embed(text)

				prev := sql.NullInt64{}
				if pos > 0 {
					prev.Int64 = int64(pos - 1)
					prev.Valid = true
				}

				chunks = append(chunks, types.Chunk{
					ID:    uuid.New(),
					DocID: id,
					Index: pos,
					Type:  string(types.ChunkTableRow),
					Key:   row.Key,
					TableID: uuid.NullUUID{
						UUID:  tableID,
						Valid: true,
					},
					Content:   row.Value,
					CohPrev:   prev,
					Embedding: emb,
				})

				pos++
			}
		}
	}

	return chunks, tables, nil
}

func convertPDFToMD(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("files", filePath)
	if err != nil {
		return "", err
	}

	_, err = io.Copy(part, file)
	if err != nil {
		return "", err
	}

	writer.Close()

	req, err := http.NewRequest("POST", "http://localhost:5001/v1/convert/file", &buf)
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var d types.DoclingResponse
	err = json.Unmarshal(body, &d)
	if err != nil {
		return "", err
	}

	return d.Document.MdContent, nil
}

// var footerPageRe = regexp.MustCompile(
// 	`(?m)^Система управления терминалами «XTMS»\s*\nстр\. \d+ из \d+\s*$`,
// )

// var headerRevisionRe = regexp.MustCompile(
// 	`(?m)^Редакция от:\s*\d{2}\.\d{2}\.\d{4}\s+\d{2}:\d{2}\s*$`,
// )

// func cleanDoclingArtifacts(md string) string {
// 	md = headerRevisionRe.ReplaceAllString(md, "")
// 	md = footerPageRe.ReplaceAllString(md, "")

// 	// подчистим лишние пустые строки
// 	md = regexp.MustCompile(`\n{3,}`).ReplaceAllString(md, "\n\n")

// 	return strings.TrimSpace(md)
// }

type mdTokenType int

const (
	tokenText mdTokenType = iota
	tokenImage
	tokenTable
)

type mdToken struct {
	Type     mdTokenType
	Content  string // text OR base64
	Section  string
	Table    []TableRow
	IsHeader bool
}

type TableRow struct {
	Key   string
	Value string
}

var imgRegex = regexp.MustCompile(
	`!\[[^\]]*\]\(data:image\/[a-zA-Z]+;base64,([^)]+)\)`,
)

func tokenizeMD(md string) []mdToken {
	lines := strings.Split(md, "\n")
	var tokens []mdToken

	var buf strings.Builder
	currentSection := ""

	flushText := func() {
		if buf.Len() > 0 {
			tokens = append(tokens, mdToken{
				Type:    tokenText,
				Content: strings.TrimSpace(buf.String()),
				Section: currentSection,
			})
			buf.Reset()
		}
	}

	for i := 0; i < len(lines); i++ {

		line := lines[i]

		// -------- SECTION (## ...) --------
		if isSeparatorRow(line) {
			rows, next := parseLooseMarkdownTable(lines, i)

			tokens = append(tokens, mdToken{
				Type:    tokenTable,
				Table:   rows,
				Section: currentSection,
			})

			i = next - 1
			continue
		}

		// -------- IMAGE --------
		if imgRegex.MatchString(line) {
			flushText()
			m := imgRegex.FindStringSubmatch(line)
			tokens = append(tokens, mdToken{
				Type:    tokenImage,
				Content: m[1], // base64
				Section: currentSection,
			})

			continue
		}

		// // -------- TABLE --------
		// if i+1 < len(lines) && isTableHeader(line) && isTableSeparator(lines[i+1]) {
		// 	flushText()
		// 	rows, next, hasHeader := parseMarkdownTableAt(lines, i)
		// 	tokens = append(tokens, mdToken{
		// 		Type:     tokenTable,
		// 		Table:    rows,
		// 		Section:  currentSection,
		// 		IsHeader: hasHeader,
		// 	})
		// 	i = next
		// 	continue
		// }

		// -------- TEXT --------
		buf.WriteString(line)
		buf.WriteString("\n")

	}

	flushText()
	return tokens
}

func isTableHeader(line string) bool {
	return strings.HasPrefix(line, "|") && strings.Contains(line, "|")
}

func isTableSeparator(line string) bool {
	return strings.HasPrefix(line, "|") && strings.Contains(line, "---")
}

func isTableRow(line string) bool {
	line = strings.TrimSpace(line)
	return strings.HasPrefix(line, "|") && strings.Count(line, "|") >= 2
}

func isSeparatorRow(line string) bool {
	line = strings.TrimSpace(line)
	return strings.HasPrefix(line, "|") && strings.Contains(line, "---")
}

func splitRow(line string) []string {
	parts := strings.Split(line, "|")
	var cells []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			cells = append(cells, p)
		}
	}
	return cells
}

// func parseTable(lines []string, start int) ([]TableRow, int) {
// 	// lines[start] — header
// 	// lines[start+1] — separator

// 	var rows []TableRow

// 	if start+1 >= len(lines) {
// 		return nil, start
// 	}

// 	header := splitRow(lines[start])
// 	_ = header // сейчас не используем, но можно сохранить

// 	if !isSeparatorRow(lines[start+1]) {
// 		return nil, start
// 	}

// 	i := start + 2
// 	for i < len(lines) && isTableRow(lines[i]) {
// 		cells := splitRow(lines[i])
// 		if len(cells) >= 2 {
// 			rows = append(rows, TableRow{
// 				Key:   cells[0],
// 				Value: cells[1],
// 			})
// 		}
// 		i++
// 	}

// 	return rows, i
// }

func mergeAdjacentText(tokens []mdToken) []mdToken {
	var result []mdToken
	var buf strings.Builder

	flush := func() {
		if buf.Len() > 0 {
			result = append(result, mdToken{
				Type:    tokenText,
				Content: strings.TrimSpace(buf.String()),
			})
			buf.Reset()
		}
	}

	for _, t := range tokens {
		if t.Type == tokenText {
			buf.WriteString(t.Content)
			buf.WriteString("\n")
			continue
		}

		flush()
		result = append(result, t)
	}

	flush()
	return result
}

func mergeAdjacentTables(tokens []mdToken) []mdToken {
	var out []mdToken

	for _, t := range tokens {
		if t.Type != tokenTable {
			out = append(out, t)
			continue
		}

		if len(out) > 0 {
			prev := &out[len(out)-1]

			if prev.Type == tokenTable && !t.IsHeader {
				prev.Table = append(prev.Table, t.Table...)
				continue
			}
		}

		out = append(out, t)
	}

	return out
}

func sameTableShape(a, b []TableRow) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	// сейчас 2 колонки: Key + Value
	return true
}

func parseLooseMarkdownTable(lines []string, sepIndex int) ([]TableRow, int) {
	var rows []TableRow

	// 1️⃣ Поднимаемся ВВЕРХ — собираем строки до separator
	start := sepIndex - 1
	for start >= 0 && isTableRow(lines[start]) {
		start--
	}
	start++ // вернулись на первую строку таблицы

	// 2️⃣ Идём ВНИЗ — собираем строки после separator
	i := sepIndex + 1

	for i < len(lines) && isTableRow(lines[i]) {
		cells := splitRow(lines[i])
		if len(cells) >= 2 {
			rows = append(rows, TableRow{
				Key:   cells[0],
				Value: cells[1],
			})
		}
		i++
	}

	// 3️⃣ Теперь добавляем строки ДО separator (в правильном порядке)
	for j := start; j < sepIndex; j++ {
		cells := splitRow(lines[j])
		if len(cells) >= 2 {
			rows = append([]TableRow{{
				Key:   cells[0],
				Value: cells[1],
			}}, rows...)
		}
	}

	return rows, i
}

func parseMarkdownTableAt(lines []string, start int) ([]TableRow, int, bool) {
	var rows []TableRow
	i := start
	hasHeader := false

	// Проверяем, есть ли header
	if i+1 < len(lines) && isTableHeader(lines[i]) && isTableSeparator(lines[i+1]) {
		hasHeader = true
		i += 2 // пропускаем header + separator
	}

	var currentKey string
	var valueBuf strings.Builder

	flush := func() {
		if currentKey != "" || valueBuf.Len() > 0 {
			rows = append(rows, TableRow{
				Key:   currentKey,
				Value: strings.TrimSpace(valueBuf.String()),
			})
		}
		currentKey = ""
		valueBuf.Reset()
	}

	for i < len(lines) {
		line := lines[i]
		if !isTableRow(line) {
			break
		}

		cells := splitRow(line)
		if len(cells) < 2 {
			i++
			continue
		}

		key := strings.TrimSpace(cells[0])
		val := strings.TrimSpace(cells[1])

		// новая логическая строка
		if key != "" {
			flush()
			currentKey = key
		}

		if val != "" {
			if valueBuf.Len() > 0 {
				valueBuf.WriteString(" ")
			}
			valueBuf.WriteString(val)
		}

		i++
	}

	flush() // обязательно после цикла, чтобы не потерять последнюю строку
	return rows, i, hasHeader
}

// func extractTables(md string) ([]TableRow, string) {
// 	var rows []TableRow
// 	var clean []string

// 	lines := strings.Split(md, "\n")
// 	inTable := false

// 	for _, line := range lines {
// 		if strings.HasPrefix(line, "|") {
// 			cells := strings.Split(line, "|")
// 			if len(cells) >= 3 {
// 				key := strings.TrimSpace(cells[1])
// 				val := strings.TrimSpace(cells[2])
// 				if key != "" && !strings.Contains(key, "---") {
// 					rows = append(rows, TableRow{Key: key, Value: val})
// 				}
// 			}
// 			inTable = true
// 			continue
// 		}

// 		if inTable {
// 			inTable = false
// 			continue
// 		}

// 		clean = append(clean, line)
// 	}

// 	return rows, strings.Join(clean, "\n")
// }
