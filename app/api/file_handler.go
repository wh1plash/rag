package api

import (
	"context"
	"io"
	"os"
	"rag/app/agent"
	"rag/store"

	"github.com/gofiber/fiber/v2"
)

type FileHandler struct {
	Store store.DBStorer
}

func NewFileHandler(s store.DBStorer) *FileHandler {
	return &FileHandler{
		Store: s,
	}
}

func (h *FileHandler) ProcessFile(c *fiber.Ctx) error {
	fileHeader, err := c.FormFile("file")
	if err != nil {
		return ErrBadRequest()
	}

	file, err := fileHeader.Open()
	if err != nil {
		return err
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		return err
	}

	cfg, err := h.Store.GetConfig(context.Background(), 3)
	if err != nil {
		return err
	}

	resp, err := agent.ProcessFile(string(data), cfg)
	if err != nil {
		return err
	}

	output := fileHeader.Filename
	if err := os.WriteFile(output, []byte(resp), 0644); err != nil {
		return err
	}
	defer os.Remove(output)

	return c.Download(output, output)
}
