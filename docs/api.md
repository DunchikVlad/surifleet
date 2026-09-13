# REST API SuriFleet — конвенции

Версия: 0.1 (чанк 4). Спецификация: `api/openapi/openapi.yaml` (OpenAPI 3.0.3).

## Базовые правила

- **Префикс**: все endpoint'ы — под `/api/v1` (единый `servers.url`).
  `/health` и `/version` тоже живут под `/api/v1`, но **без аутентификации**
  (`security: []`) — решение зафиксировано здесь: отдельный служебный порт не
  вводим, балансировщику достаточно одного префикса.
- **Аутентификация**: JWT access token (`Authorization: Bearer …`) или
  API-токен автоматизации (заголовок `X-API-Key`). Оба способа равноправны;
  авторизация — всегда на backend по итоговым разрешениям (`/auth/me`).
- **Форматы**: даты — RFC3339 (`date-time`, UTC), идентификаторы — UUID,
  деньги/байты — integer. Тела запросов и ответов — `application/json`,
  если не указано иное (импорт — multipart, экспорт — text/csv).

## Пагинация (keyset, offset запрещён)

- Параметры: `limit` (default 100, max 1000) и `cursor` (непрозрачная строка
  из `next_cursor` предыдущего ответа).
- Ответ: `{items: [...], next_cursor: string|null}`; `next_cursor = null` —
  страниц больше нет.
- Курсор инвалидируется при смене набора фильтров — клиент обязан начинать
  выборку заново.
- Матрица «правила × инстансы» (`GET /matrix/rules`) имеет **два независимых
  курсора** по осям: `rule_cursor` и `instance_cursor`.

## Ошибки

Единый конверт для всех не-2xx ответов:

```json
{"error": {"code": "validation_failed", "message": "…", "details": {"field": "…"}}}
```

Коды HTTP: 400 — валидация, 401 — нет/просрочен токен, 403 — нет права
(в `details.reason` — какого), 404 — объект не найден или вне scoping,
409 — конфликт (дубликат уникального ключа, нарушение состояния — например
pause уже завершённого деплоя), 422 — бизнес-ошибка (деплой на проблемный
хост), 429 — rate limit, 500 — внутренняя.

## Идемпотентность

- Мутирующие POST с побочными эффектами на флоте (`POST /deployments`,
  `POST /deployments/{id}/rollback`, `POST /rulesets`,
  `POST /agents/update_wave`) принимают заголовок **`Idempotency-Key`**
  (UUID): повторный запрос с тем же ключом в течение 24 ч возвращает исходный
  результат без повторного выполнения.
- GET/PUT/PATCH/DELETE идемпотентны по природе; PUT — полная замена
  представления (capabilities, log_level), PATCH — частичное обновление.

## Асинхронные операции

Действия над агентами (`/agents/{id}/actions/*`, валидация конфигурации,
деплой IOC) возвращают **202 + TaskInfo{task_id}**; результат опрашивается
через `GET /tasks/{id}`. Успех деплоя правил — только по подтверждению
агента о фактической загрузке движком (см. docs/protocol.md).

## Авторизация и scoping

- Требуемое разрешение указано в `description` каждой операции
  («Требуется право: rules.write»). Реестр разрешений:
  `organizations.*`, `clusters.*`, `hosts.*`, `rules.read/write`,
  `feeds.read/write`, `iocs.read/write/deploy`, `deployments.read/write/
  create/control`, `fleet.read`, `agents.read/actions/update/logs`,
  `incidents.read/write`, `config.read/write`, `users.*`, `roles.*`,
  `sso.*`, `tokens.*`, `audit.read`.
- Scoping роли (организация или набор кластеров) применяется на уровне
  строк: объекты вне scope неотличимы от несуществующих (404, не 403).

## Секреты

Поля-секреты (`credentials` фида, `secret` SSO-провайдера, `password`)
помечены `writeOnly`: принимаются, хранятся шифрованно (envelope) и никогда
не возвращаются. Значения join token и API-токена показываются **один раз**
в ответе на создание.

## Валидация спецификации

```bash
# lint (структура + ссылки + пути)
npx --yes @redocly/cli@latest lint api/openapi/openapi.yaml

# парсинг YAML
python -c "import yaml; yaml.safe_load(open('api/openapi/openapi.yaml', encoding='utf-8'))"
```

CI: lint обязателен, errors блокируют merge; warnings допустимы.

## Эволюция API

- Внутри `/api/v1` — только обратно-совместимые изменения: новые endpoint'ы,
  новые необязательные поля и query-параметры, новые значения enum.
- Удаление/переименование полей, смена семантики пагинации или формата
  ошибок — только через `/api/v2` (параллельная публикация).
- Клиент обязан игнорировать неизвестные поля ответов.
