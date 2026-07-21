package api

import (
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/gofiber/fiber/v2"
)

func ErrorHandler(c *fiber.Ctx, err error) error {
	var apiErr Error
	if errors.As(err, &apiErr) {
		log.Printf("%s request failed code=%d msg=%s", time.Now().Format(time.RFC3339), apiErr.Code, apiErr.Message)
		return c.Status(apiErr.Code).JSON(apiErr)
	}

	var valErr ValidationError
	if errors.As(err, &valErr) {
		return c.Status(valErr.Status).JSON(valErr)
	}

	var fiberErr *fiber.Error
	if errors.As(err, &fiberErr) {
		out := NewError(fiberErr.Code, fiberErr.Message)
		log.Printf("%s request failed code=%d msg=%s", time.Now().Format(time.RFC3339), out.Code, out.Message)
		return c.Status(out.Code).JSON(out)
	}

	// Обычные/wrapped ошибки (fmt.Errorf, LLM, DB, web search, …)
	out := NewError(fiber.StatusInternalServerError, err.Error())
	log.Printf("%s request failed code=%d msg=%s", time.Now().Format(time.RFC3339), out.Code, out.Message)
	return c.Status(out.Code).JSON(out)
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"error"`
}

type ValidationError struct {
	Status int               `json:"status"`
	Errors map[string]string `json:"errors"`
}

func (e ValidationError) Error() string {
	return "validation failed"
}

func NewValidationError(errors map[string]string) ValidationError {
	return ValidationError{
		Status: fiber.StatusUnprocessableEntity,
		Errors: errors,
	}
}

// Error implements the Error interface
func (e Error) Error() string {
	return e.Message
}

func NewError(code int, err string) Error {
	return Error{
		Code:    code,
		Message: err,
	}
}

func ErrBadRequest() Error {
	return Error{
		Code:    fiber.StatusBadRequest,
		Message: "invalid JSON request",
	}
}

func ErrInvalidID() Error {
	return Error{
		Code:    fiber.StatusBadRequest,
		Message: "invalid id given",
	}
}

func ErrUnAuthorized(msg string) Error {
	return Error{
		Code:    fiber.StatusUnauthorized,
		Message: msg,
	}
}

func ErrNotFound[T any](arg T, resource string) Error {
	return Error{
		Code:    fiber.StatusNotFound,
		Message: fmt.Sprintf("%s with %v not found", resource, arg),
	}
}

func ErrInvalidCredentials() Error {
	return Error{
		Code:    fiber.StatusBadRequest,
		Message: "invalid credentials",
	}
}
