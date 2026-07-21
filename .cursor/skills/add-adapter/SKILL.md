---
name: add-adapter
description: >-
  Adds an infrastructure adapter (Postgres, Ollama, Cohere, Docling, FS, PDF)
  behind a port without leaking vendor types into domain or handlers. Use when
  integrating a new external service, replacing Ollama/Cohere/Docling clients,
  or extracting HTTP/SQL details out of usecase/delivery.
disable-model-invocation: true
---

# Add adapter

Rules: `.cursor/rules/persistence-adapters.mdc`, `clean-architecture.mdc`.

## Шаги

```
- [ ] 1. Уточнить/создать port в internal/port (или у usecase)
- [ ] 2. Создать пакет адаптера
- [ ] 3. Маппинг vendor/sql ↔ domain внутри адаптера
- [ ] 4. Конфиг через New(cfg), без os.Getenv внутри методов
- [ ] 5. Подключить в composition root
- [ ] 6. Убедиться, что delivery/usecase не импортят vendor SDK
```

## Куда класть (сейчас → цель)

| Адаптер | Сейчас | Цель |
|---------|--------|------|
| Postgres | `store/` | `internal/adapter/postgres` |
| Embeddings / VL | `model/` | `internal/adapter/ollama` |
| LLM generate | `app/agent` | `internal/adapter/ollama`, `cohere` |
| Docling | `loader/internal` | `internal/adapter/docling` |
| FS watch/archive | `loader/internal` | `internal/adapter/fs` |

## Контракт

```go
// port
type AnswerGenerator interface {
    Generate(ctx context.Context, system, context, question string) (string, error)
}

// adapter — ключ только из cfg
func NewCohere(cfg CohereConfig) *Client { ... }
```

## Запрещено

- Класть `DoclingResponse` / sql nullables в `types`
- Хардкодить API keys (запрещён паттерн из `GenerateAnswerCohere`)
- Менять сигнатуры портов под удобство одного vendor без нужды

После добавления: delivery по-прежнему вызывает usecase, не адаптер напрямую (кроме исключительного health-check).
