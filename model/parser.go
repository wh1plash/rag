package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type VisionModel interface {
	Describe(img string) (string, error)
	Retry(ctx context.Context, prompt string, maxAttempts int) (string, error)
}

type LLaVA struct {
	URL   string
	Model string
}

type LLaVARequest struct {
	Model      string   `json:"model"`
	Prompt     string   `json:"prompt"`
	Temerature float32  `json:"temperature"`
	TopP       float32  `json:"top_p"`
	TopK       int      `json:"top_k"`
	MaxTokens  int      `json:"max_tokens"`
	Images     []string `json:"images"`
}

type LLaVAResponse struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
}

func NewLLaVA() *LLaVA {
	URL := os.Getenv("OLLAMA_VL_URL")
	Model := os.Getenv("OLLAMA_VL_MODEL")
	return &LLaVA{
		URL:   URL,
		Model: Model,
	}
}

func (l *LLaVA) Describe(img string) (string, error) {
	fmt.Println("Starting prompt to LLaVA for describe immage")

	prompt := `You are a vision-language extraction model.

Your task is to extract all visible UI text from the provided image
and return it as a SINGLE valid JSON object.

IMPORTANT RULES (MANDATORY):

- Output MUST be valid JSON.
- Output MUST start with '{' and end with '}'.
- Do NOT include explanations, comments, or markdown.
- Do NOT include any text outside JSON.
- Do NOT invent or infer missing values.
- If something is unclear or unlabeled, use an empty string "".

JSON STRUCTURE (FIXED):

{
  "sections": [
    {
      "section_name": "",
      "fields": [
        {
          "label": "",
          "value": ""
        }
      ],
      "buttons": [],
      "other_text": []
    }
  ]
}

EXTRACTION RULES:

- Preserve exact wording, capitalization, punctuation, and numbers.
- Include all visible:
  - labels
  - input fields and their values
  - dropdowns with selected values
  - checkboxes and radio buttons with state ("checked"/"unchecked")
  - buttons and menu items
  - numeric values, units, and symbols
- If there are no buttons, use an empty array.
- If there is no other text, use an empty array.
- Every section MUST include all four keys:
  "section_name", "fields", "buttons", "other_text".

NOW analyze the image and return ONLY the JSON object.
`
	fmt.Printf("cfg: model - %s, url - %s\n", l.Model, l.URL)
	req := LLaVARequest{
		Model:      l.Model, // Using llava model for image analysis
		Prompt:     prompt,
		Temerature: 0.05,
		TopP:       0.9,
		TopK:       20,
		MaxTokens:  2048,
		Images:     []string{img},
	}
	// Marshal to JSON
	reqBody, err := json.Marshal(req)
	if err != nil {
		fmt.Printf("Error marshaling request: %v\n", err)
		return "", err
	}

	start := time.Now()
	defer func() {
		fmt.Printf("LLM answer tooks %v\n", time.Since(start))
	}()
	// 	str :=
	// `{
	//   "Промотор": [
	//     "10 EP",
	//     "11 EP",
	//     "12 SU",
	//     "13 SL",
	//     "16 ET",
	//     "17 OL",
	//     "18 OL",
	//     "51 SU",
	//     "95 EP",
	//     "96 EP"
	//   ],
	//   "Таблица тарифов": [
	//     {
	//       "Название карты": "10 ER",
	//       "Сeria карты": "10",
	//       "Тип проездного": "Тип ЕР",
	//       "Стоимость карты": "0",
	//       "Величина залога": "5000",
	//       "Тип транспорта": "автобус, метроп",
	//       "Срок действия": "90 дней",
	//       "Срок активации": "1 день",
	//       "Скдика при списании": "0",
	//       "Максималный баланс": "500000",
	//       "Активировать": "Да",
	//       "Разрешать возврат": "Да",
	//       "Сумма продаж": "0",
	//       "Зонами": "Номера",
	//       "Начальная зона": "0",
	//       "Конечная зона": "0",
	//       "Ресурс поездок": "0",
	//       "Схема скидок": "Постоянный процент",
	//       "Режим скидок": "Полопление"
	//     }
	//   ]
	// }`
	//	return str, nil

	// Создаём POST-запрос
	resp, err := http.Post(l.URL, "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		fmt.Printf("Error sending request: %v\n", err)
		return "", err
	}
	defer resp.Body.Close()

	decoder := json.NewDecoder(resp.Body)

	var b strings.Builder

	for {
		var llavaResp LLaVAResponse

		if err := decoder.Decode(&llavaResp); err == io.EOF {
			break
		} else if err != nil {
			return "", fmt.Errorf("decode response: %w", err)
		}

		b.WriteString(llavaResp.Response)

		if llavaResp.Done {
			break
		}
	}

	json, err := extractJSON(b.String())

	return json, err
}

func (l *LLaVA) Retry(ctx context.Context, prompt string, maxAttempts int) (string, error) {
	var lastErr error
	var raw string

	for attempt := 1; attempt <= maxAttempts; attempt++ {

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		var err error
		if attempt == 1 {
			fmt.Printf("try to describe an immage #%d\n", attempt)
			raw, err = l.Describe(prompt)
		} else {
			fmt.Printf("try to describe an immage #%d\n", attempt)
			repairPrompt := buildRepairPrompt(raw)
			raw, err = l.Describe(repairPrompt)
		}

		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(attempt) * 300 * time.Millisecond)
			continue
		}

		jsonStr, err := extractJSON(raw)
		if err == nil {
			return jsonStr, nil
		}

		lastErr = err
		time.Sleep(time.Duration(attempt) * 300 * time.Millisecond)
	}

	return "", fmt.Errorf("vision retry failed after %d attempts: %w", maxAttempts, lastErr)
}

func extractJSON(s string) (string, error) {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")

	if start == -1 || end == -1 || end <= start {
		return s, errors.New("no valid json found")
	}

	return s[start : end+1], nil
}

func buildRepairPrompt(badOutput string) string {
	return fmt.Sprintf(`
You previously returned an invalid JSON.

Your task is to FIX the JSON.

RULES:
- Output ONLY valid JSON
- Do NOT add or remove information
- Do NOT add explanations
- Do NOT include markdown
- Do NOT include text outside JSON

INVALID OUTPUT:
<<<
%s
>>>

Return the corrected JSON only.
`, badOutput)
}
