package api

import (
	"context"
	"rag/store"
	"rag/types"
	"reflect"
	"strings"

	"github.com/gofiber/fiber/v2"
)

type ConfigHandler struct {
	configStore store.DBStorer
}

func NewConfigHandler(cfgStore store.DBStorer) *ConfigHandler {
	return &ConfigHandler{
		configStore: cfgStore,
	}
}

func (h *ConfigHandler) HandleSetConfig(c *fiber.Ctx) error {
	var params types.ConfigParams
	if c.BodyParser(&params) != nil {
		return ErrBadRequest()
	}

	if errors := types.Validate(&params); len(errors) > 0 {
		return NewValidationError(errors)
	}

	v := reflect.ValueOf(params)
	t := reflect.TypeOf(params)
	querySet := make(map[string]any)
	for i := range v.NumField() {
		//fieldName := t.Field(i).Name
		jsonTag := t.Field(i).Tag.Get("db")
		fieldValue := v.Field(i).Interface()
		//fmt.Printf("Field: %s (json: %s), Value: %v\n", fieldName, jsonTag, fieldValue)

		key := strings.Split(jsonTag, ",")[0]
		if value, ok := fieldValue.(string); ok && value != "" {
			querySet[key] = value
		}
	}
	if len(querySet) == 0 {
		return ErrBadRequest()
	}

	resp, err := h.configStore.SetConfig(context.Background(), querySet)
	if err != nil {
		return err
	}

	return c.JSON(resp)
}
