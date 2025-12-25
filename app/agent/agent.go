package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"rag/types"
	"time"

	"github.com/pkoukk/tiktoken-go"
)

type CohereMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type CohereRequest struct {
	Model    string          `json:"model"`
	Messages []CohereMessage `json:"messages"`
	Stream   bool            `json:"stream"`
}
type CohereResponse struct {
	ID      string `json:"id"`
	Message struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
}
type GenerateRequest struct {
	Model  string `json:"model"`
	System string `json:"system"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

type GenerateResponse struct {
	Response string `json:"response"`
}

func ProcessFile(data string, cfg types.LLMConfig) (string, error) {
	start := time.Now()
	defer func() {
		fmt.Printf("Time for processing file: %v\n", time.Since(start))
	}()

	fmt.Println("Startin process file...")

	corrected, err := SendToOllama(MakePrompt(data), cfg)
	if err != nil {
		return "", err
	}

	return corrected, nil
}

func MakePrompt(text string) string {
	return fmt.Sprintf(`
Исправь орфографические, пунктуационные и грамматические ошибки в тексте.

Текст:
%s
`, text)
}

func SendToOllama(prompt string, cfg types.LLMConfig) (string, error) {
	payload := GenerateRequest{
		Model:  cfg.Model,
		System: cfg.PromptStr, //"Ты корректор русского языка. Сохрани стиль и смысл. Верни только исправленный текст без пояснений. Ничего не додумывай и не изменяй смысл.",
		Prompt: prompt,
		Stream: false,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	resp, err := http.Post(
		cfg.Url,
		"application/json",
		bytes.NewBuffer(body),
	)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var llmResp GenerateResponse
	if err := json.Unmarshal(respBody, &llmResp); err != nil {
		return "", err
	}

	return llmResp.Response, nil
}

func GenerateAnswerCohere(context string, question string, cfg types.LLMConfig) (string, error) {
	url := "https://api.cohere.com/v2/chat"
	apiKey := "cQ9H6hdb9sshZTtgMvEDgHB4DoopOLZ3YxFuW0AS"
	start := time.Now()
	defer func() {
		fmt.Printf("LLM answer tooks %v\n", time.Since(start))
	}()

	fmt.Println("Startin promt to LLM...")

	prompt := fmt.Sprintf(`Контекст з декількох документів:
Контекст:
%s
Запит:
%s 
Відповідь:`, context, question)

	reqBody, _ := json.Marshal(CohereRequest{
		Model:  "command-a-03-2025",
		Stream: false,
		Messages: []CohereMessage{
			{
				Role:    "system",
				Content: cfg.PromptStr,
			},
			{
				Role:    "user",
				Content: prompt,
			},
		},
	})
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(reqBody))
	if err != nil {
		fmt.Println("Error to prompting Cohere")
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	fmt.Println("RAW RESPONSE:", string(body))
	var coResp CohereResponse
	_ = json.Unmarshal(body, &coResp)

	var answer string
	if len(coResp.Message.Content) > 0 {
		answer = coResp.Message.Content[0].Text
	}
	return answer, nil
}

func GenerateAnswer(context string, question string, cfg types.LLMConfig) (string, error) {
	start := time.Now()
	defer func() {
		fmt.Printf("LLM answer tooks %v\n", time.Since(start))
	}()

	fmt.Println("Startin promt to LLM...")

	prompt := fmt.Sprintf(`Контекст из нескольких документов:
Контекст:
%s
Вопрос:
%s 
Ответ:`, context, question)

	reqBody, _ := json.Marshal(GenerateRequest{
		Model:  os.Getenv("LLM_MODEL"),
		System: cfg.PromptStr,
		Prompt: prompt,
		Stream: false,
	})

	count, _ := CountTokensLlama(reqBody)
	fmt.Println("Size of Prompt with system in tokens:", count)

	fmt.Println("Size of Prompt with system in symbols:", len(reqBody))
	fmt.Println("-----------")

	// fmt.Println(prompt)
	// return "ok", nil

	resp, err := http.Post(cfg.Url,
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
