package api

import (
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
)

func ErrorHandler(c *fiber.Ctx, err error) error {
	if ApiError, ok := err.(Error); ok {
		return c.Status(ApiError.Code).JSON(ApiError)
	} else {
		if ValError, ok := err.(ValidationError); ok {
			return c.Status(ValError.Status).JSON(ValError)
		}
	}

	ApiError := NewError(err.(*fiber.Error).Code, err.Error())
	curTime := time.Now()
	fmt.Printf("%s Request failed with code %d and message: %s\n", &curTime, ApiError.Code, ApiError.Message)
	return c.Status(ApiError.Code).JSON(ApiError)

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
