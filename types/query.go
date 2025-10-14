package types

import (
	"fmt"
	"net/http"

	"github.com/go-playground/validator/v10"
)

type QueryParams struct {
	Prompt string `json:"prompt" validate:"required"`
}

func Validate(params *QueryParams) map[string]string {
	validate := validator.New()
	if err := validate.Struct(params); err != nil {
		errs := err.(validator.ValidationErrors)
		errors := make(map[string]string)
		for _, e := range errs {
			errors[e.Field()] = fmt.Sprintf("failed on '%s' tag", e.Tag())
		}
		// Err := NewValidationError(errors)
		return errors
	}
	return nil
}

func NewValidationError(errors map[string]string) ValidationError {
	return ValidationError{
		Status: http.StatusUnprocessableEntity,
		Errors: errors,
	}
}

type ValidationError struct {
	Status int               `json:"status"`
	Errors map[string]string `json:"errors"`
}

func (e ValidationError) Error() string {
	return "validation failed"
}
