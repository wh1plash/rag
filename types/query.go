package types

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-playground/validator/v10"
)

type Validater interface {
	Validate() map[string]string
}

type QueryParams struct {
	Prompt string `json:"prompt" validate:"required"`
}

type ConfigParams struct {
	EmbeddingUrl   string `db:"embedding_url" json:"embedding_url,omitempty"`
	EmbeddingModel string `db:"embedding_model" json:"embedding_model,omitempty"`
	LLMUrl         string `db:"llm_ulr" json:"llm_ulr,omitempty"`
	LLMModel       string `db:"llm_model" json:"llm_model,omitempty"`
	PromptStr      string `db:"prompt_str" json:"prompt_str,omitempty"`
}

func Validate(v Validater) map[string]string {
	return v.Validate()
}

func (params *ConfigParams) Validate() map[string]string {
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

func (params *QueryParams) Validate() map[string]string {
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

type SearchResponse struct {
	Answer     string    `json:"answer"`
	Sources    []Source  `json:"sources"`
	Confidence float64   `json:"confidence"`
	Timestamp  time.Time `json:"timestamp"`
}
type Source struct {
	DocID     string `json:"doc_id"`
	Title     string `json:"title"`
	ChunkText string `json:"chunk_text"`
	Index     int    `json:"index"`
}
