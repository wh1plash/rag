---
name: clean-arch-review
description: >-
  Reviews Go code in this RAG repo against Clean Architecture and project rules.
  Use when reviewing PRs, large refactors, new packages/services, or when the user
  asks for architecture review, dependency-rule check, or CA audit.
disable-model-invocation: true
---

# Clean Architecture review (RAG)

Читай `.cursor/rules/clean-architecture.mdc` и `ARCHITECTURE_REVIEW.md` перед ревью.

## Когда применять

- PR / diff затрагивает `app/api`, `loader`, `store`, `types`, `model`, `agent`
- Добавляется новый сервис/пакет
- Пользователь просит architectural review

## Чеклист

Скопируй и отмечай:

```
- [ ] delivery не содержит RAG/ingest оркестрацию
- [ ] usecase не импортирует fiber/pgx/конкретных HTTP-клиентов
- [ ] domain без database/sql, net/http, Docling DTO
- [ ] порты не объявлены только в пакете реализации
- [ ] адаптеры создаются в cmd/composition root, не в NewHandler/NewLoader
- [ ] нет хардкода секретов/URL
- [ ] context.Context на I/O
- [ ] ошибки с %w
```

## Формат finding

```
### [Critical|Major|Minor|Nit] Заголовок
- Где: path:line
- Что не так
- Почему (dependency rule / тестируемость)
- Как исправить
- Effort: S/M/L
```

## Фокус по зонам репо

| Зона | Типичные нарушения |
|------|-------------------|
| `app/api` | fat handlers, прямые `agent.*` |
| `types` | sql/http/docling leak |
| `store` | порт внутри infra |
| `loader/service` | concrete `*PDFLoader` |
| `loader/internal` | self-wiring embedder/vision |
| `app/agent` | нет интерфейса, секреты |

Не рефакторь код в рамках этого skill, пока пользователь явно не попросит fix.
