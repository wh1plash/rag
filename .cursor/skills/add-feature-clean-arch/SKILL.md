---
name: add-feature-clean-arch
description: >-
  Adds a new feature to the RAG Go project following Clean Architecture workflow
  (domain → ports → usecase → adapters → delivery → cmd wiring → tests). Use when
  implementing a new endpoint, ingest path, LLM flow, config use case, or any
  product feature that must not grow fat handlers.
disable-model-invocation: true
---

# Add feature (Clean Architecture)

Следуй `.cursor/rules/clean-architecture.mdc`, `usecase-domain.mdc`, `handlers-transport.mdc`.

## Workflow

Скопируй прогресс:

```
- [ ] 1. Domain types (если нужны)
- [ ] 2. Ports (interfaces)
- [ ] 3. Use case
- [ ] 4. Adapter(s)
- [ ] 5. Thin delivery handler
- [ ] 6. Wire in cmd / server.Run
- [ ] 7. Tests (usecase с fake ports)
```

### 1. Domain

Файлы: `internal/domain/` (или временно `types/` **без** sql/http).

### 2. Ports

Файлы: `internal/port/` (или интерфейс рядом с usecase).

Примеры портов в этом продукте: `ChunkRepository`, `Embedder`, `AnswerGenerator`, `DocumentSource`, `PDFConverter`.

### 3. Use case

`internal/usecase/<feature>/` — входной DTO usecase (не Fiber), метод `Execute(ctx, in) (out, error)`.

Пока layout не мигрирован:
- query/RAG → не класть в `app/api`; вынести пакет usecase и вызвать из handler
- ingest → расширять `loader/service`, зависимости через интерфейсы

### 4. Adapters

Реализация в `store` / `model` / `app/agent` / `loader/internal` **или** сразу `internal/adapter/<name>`.

### 5. Delivery

`app/api` или `telegram`: только parse/validate/call/map.

### 6. Wiring

Только `app/cmd`, `loader/cmd`, `app/server.Run` создают конкретные типы.

## Антипаттерны

- Логика в `HandleRequest` / `ProcessFile` / `HandleSetConfig`
- `NewX()` внутри handler, который читает env и создаёт Ollama
- Импорт `rag/store` в domain

## Мини-тест

Unit-тест usecase с fake `port` — обязателен для нетривиальной логики (filter/extend chunks, ShouldUpdate).
