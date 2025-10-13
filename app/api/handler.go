package api

import (
	"context"
	"fmt"
	"rag/app/agent"
	"rag/app/types"
	"rag/model"

	"rag/loader/store"

	"github.com/gofiber/fiber/v2"
)

type RequestHandler struct {
	contextStore store.DBStorer
	embedder     model.EmbedderInterface
}

func NewRequestHandler(contextStore store.DBStorer) *RequestHandler {
	embedder := model.NewEmbedder()
	return &RequestHandler{
		contextStore: contextStore,
		embedder:     embedder,
	}
}

func (h *RequestHandler) HandleRequest(c *fiber.Ctx) error {
	var params types.QueryParams
	if c.BodyParser(&params) != nil {
		return ErrBadRequest()
	}

	if errors := types.Validate(&params); len(errors) > 0 {
		return NewValidationError(errors)
	}

	prompt := params.Prompt

	embededPrompt, err := h.embedder.Embed(prompt)
	if err != nil {
		return err
	}

	chunks, err := h.contextStore.Search(context.Background(), embededPrompt, 5)
	if err != nil {
		fmt.Println("error to get context from DB", err)
		return err
	}

	var contextTexts string
	for _, c := range chunks {
		if c.Distance > 0.7 {
			contextTexts += c.Content + "\n\n"
		}
	}

	output, err := agent.GenerateAnswer(contextTexts, prompt)
	if err != nil {
		return err
	}
	return c.JSON(output)
}
