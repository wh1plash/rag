package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v2"
)

func PlugStatic(staticPrefix string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		path := c.Path()

		// Проверяем, что путь предназначен для статики
		if strings.HasPrefix(path, staticPrefix) {

			// Ловим .well-known только для статики
			if strings.HasPrefix(path, "/.well-known/") {
				return c.JSON(fiber.Map{
					"status": "ignored dynamic-static",
				})
			}
		}

		return c.Next()
	}
}
