package api

import (
	"github.com/gofiber/fiber/v2"
)

type CheckHandler struct{}

func NewCheckHandler() *CheckHandler {
	return &CheckHandler{}
}

func (h CheckHandler) HandleHealthy(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"result": "ok"})
}

func (h CheckHandler) HandleDrop(c *fiber.Ctx) error {
	panic("Drop application")
}
