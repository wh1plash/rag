package port

import "context"

// AnswerRequest — вход для генерации ответа по RAG-контексту.
type AnswerRequest struct {
	System    string
	Context   string
	Question  string
}

// AnswerGenerator — порт LLM (локальный или облачный).
type AnswerGenerator interface {
	Generate(ctx context.Context, req AnswerRequest) (string, error)
}
