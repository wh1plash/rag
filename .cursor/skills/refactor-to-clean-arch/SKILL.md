---
name: refactor-to-clean-arch
description: >-
  Step-by-step refactor of existing RAG packages toward Clean Architecture
  without a big-bang rewrite. Use when extracting usecases from handlers,
  moving ports out of store, cleaning types from sql/http, or following
  ARCHITECTURE_REVIEW.md migration plan.
disable-model-invocation: true
---

# Refactor to Clean Architecture

Источник правды по приоритетам: `ARCHITECTURE_REVIEW.md` §6.  
Rules: `clean-architecture.mdc`, `project-agent.mdc`.

## Принципы

1. Один вертикальный срез за раз; публичный HTTP API не ломать.
2. После каждого шага: `go build ./...` (и точечные тесты).
3. Сначала извлечь интерфейс/usecase рядом, потом двигать пакеты.
4. Не смешивать rename layout и смену поведения.

## Рекомендуемый порядок

```
- [ ] A. Убрать секреты/хардкод → env
- [ ] B. Извлечь usecase/query из app/api/handler.go
- [ ] C. Перенести DBStorer в port (store реализует)
- [ ] D. port.AnswerGenerator + обернуть agent
- [ ] E. Инъекция embedder/vision из cmd в handler и PDFLoader
- [ ] F. Разделить types: domain vs dto vs docling
- [ ] G. Порт DocumentSource для loader/service
- [ ] H. migrations вместо createRagTables
- [ ] I. Переезд в internal/{domain,port,usecase,adapter,delivery}
```

## Паттерн извлечения usecase из fat handler

1. Создай `QueryService` с методами, скопированными из `RequestHandler` helpers.
2. Handler оставь тонким: parse/validate/`QueryService.Execute`.
3. Зависимости `QueryService`: интерфейсы embedder, repo, LLM — не concrete.
4. Прогони ручной smoke: `/api/v1/request`.

## Паттерн переноса порта

1. Объяви интерфейс в новом пакете `port` (те же методы).
2. `var _ port.X = (*store.PostgresStore)(nil)`.
3. Замени типы полей в api/service на `port.X`.
4. Удали/deprecated старый интерфейс в `store`, когда не останется ссылок.

## Стоп-условия

Остановись и спроси пользователя, если шаг требует:
- смены JSON API
- миграции БД с downtime
- удаления Cohere/Ollama поведения

Не делай шаг I (полный переезд layout), пока A–G не стабилизированы.
