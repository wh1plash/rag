package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/pkoukk/tiktoken-go"
)

type GenerateRequest struct {
	Model  string `json:"model"`
	System string `json:"system"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

type GenerateResponse struct {
	Response string `json:"response"`
}

func GenerateAnswer(context string, question, sysPrompt string) (string, error) {
	start := time.Now()
	defer func() {
		fmt.Printf("LLM answer tooks %v\n", time.Since(start))
	}()

	fmt.Println("Startin promt to LLM...")
	fmt.Println(sysPrompt)

	prompt := fmt.Sprintf(`Контекст з декількох документів:
Контекст:
%s
Запит:
%s 
Відповідь:`, context, question)

	reqBody, _ := json.Marshal(GenerateRequest{
		Model:  os.Getenv("LLM_MODEL"),
		System: sysPrompt,
		Prompt: prompt,
		Stream: false,
	})

	count, _ := CountTokensLlama(reqBody)
	fmt.Println("Size of Prompt with system in tokens:", count)

	fmt.Println("Size of Prompt with system in symbols:", len(reqBody))
	fmt.Println("-----------")

	//return "ok", nil

	resp, err := http.Post(os.Getenv("LLM_URL"),
		"application/json",
		bytes.NewBuffer(reqBody),
	)
	if err != nil {
		return "", nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var genResp GenerateResponse
	if err := json.Unmarshal(body, &genResp); err == nil && genResp.Response != "" {
		return genResp.Response, err
	}

	// Потоковый ответ: соберём всё в строку
	type StreamChunk struct {
		Response string `json:"response"`
	}
	var output string
	decoder := json.NewDecoder(bytes.NewReader(body))
	for decoder.More() {
		var chunk StreamChunk
		if err := decoder.Decode(&chunk); err == nil {
			output += chunk.Response
		}
	}
	return output, nil

}

func CountTokensLlama(data []byte) (int, error) {
	enc, err := tiktoken.EncodingForModel("gpt-3.5-turbo") // Можно заменить на любую совместимую модель
	if err != nil {
		return 0, err
	}
	tokens := enc.Encode(string(data), nil, nil)
	return len(tokens), nil
}
