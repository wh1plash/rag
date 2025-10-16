package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

type GenerateRequest struct {
	Model  string `json:"model"`
	System string `json:"system"`
	Prompt string `json:"prompt"`
}

type GenerateResponse struct {
	Response string `json:"response"`
}

func GenerateAnswer(context string, question string) (string, error) {
	start := time.Now()
	defer func() {
		fmt.Printf("LLM answer tooks %v\n", time.Since(start))
	}()

	fmt.Println("Startin promt to LLM...")

	prompt := fmt.Sprintf(`Answer to the questions based on the given context. If there is no information in providet context or context is empty then answer 'No information for this request'. Nothing else.
Context:
%s
Question: 
%s
Answer:`, context, question)

	fmt.Println(prompt)
	fmt.Println("-----------")
	reqBody, _ := json.Marshal(GenerateRequest{
		Model: os.Getenv("LLM_MODEL"),
		System: `You are a smart assistant, responding to the user in the language they asked the question in. \n
				Answer clearly and to the point, without adding any additional information. \n
				If the context is empty or doesn't contain any information to answer, say, 'No information on this question.' \n
				Don't add introductions like 'Of course!' or 'Here's the answer:'`,
		Prompt: prompt,
	})

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
