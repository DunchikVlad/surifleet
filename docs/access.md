# Подключение к SuriFleet: API и интерфейсы

> Документ для аналитика/инженера: как подключиться к развёрнутому стенду
> и что через него можно делать. Актуально на 2026-09-16 (после чанка 13d).
> Тестовая среда описана в ТЗ п. 13: сервер — 192.168.31.28, сенсор —
> 192.168.31.67 (ssh/sudo: test/test).

## 1. Карта доступов (dev-стенд)

| Что | Адрес | Как подключиться |
|---|---|---|
| **Web UI (MVP)** | `http://192.168.31.28:8080/` | просто открыть в браузере |
| REST API (основной интерфейс) | `http://192.168.31.28:8080/api/v1` | curl / Postman / Insomnia, см. ниже |
| OpenAPI-спецификация | `api/openapi/openapi.yaml` в репозитории | импорт в Postman/Insomnia/Swagger UI |
| Hub (gRPC, mTLS) | `192.168.31.28:8443` | только для агентов (клиентский сертификат) |
| Enrollment (выдача сертификатов) | `192.168.31.28:8444` | только для агентов (join-token) |
| MinIO S3 API (ruleset-блобы) | `http://192.168.31.28:9000` | `surifleet` / `surifleet_dev_minio` |
| MinIO веб-консоль | `http://192.168.31.28:9001` | root-доступ задаётся в `.env` стека (`MINIO_ROOT_USER`/`MINIO_ROOT_PASSWORD`) |
| PostgreSQL | `192.168.31.28:5432` | `surifleet` / `surifleet_dev`, БД `surifleet` |
| Redis | `192.168.31.28:6379` | без пароля (dev) |
| ClickHouse | `192.168.31.28:9900` (native), `http://192.168.31.28:8123` | default без пароля (LAN разрешён маунтом `zz_allow_network.xml` в users.d — чанк 13c); логи агентов: таблица `surifleet.agent_logs` |

## 2. Веб-интерфейс (UI)

**MVP Web UI доступен: http://192.168.31.28:8080/** — одностраничное
приложение, встроенное в серверный бинарь (embed, ванильный JS поверх
/api/v1, без отдельного фронтенд-стека). Вкладки:

- **Обзор** — карточки compliance флота (in_sync / pending / partial /
  drift / stale), активные деплои, автообновление 15 с.
- **Инстансы** — список инстансов; клик по строке — desired vs actual
  (хэши ruleset, loaded/failed), compliance-статус и diff.
- **Правила** — репозиторий: фильтр по статусу, поиск по msg/sid,
  дозагрузка страницами, включение/отключение правила кнопкой.
- **Ruleset'ы** — версии ruleset'ов + сборка нового ruleset из всех
  enabled-правил (версия + комментарий; дубль по содержимому вернёт
  существующую версию — идемпотентно).
- **Деплои** — создание деплоя (выбор ruleset'а и инстанса), статусы,
  прогресс, задачи по клику; управление: pause / resume (в т.ч. из
  failed) / cancel.
- **Матрица** — «правила × инстансы» (чанк 13d): строки — правила
  (sid + сообщение), столбцы — инстансы (hostname), цветные ячейки
  (loaded — зелёный, failed — красный, missing — жёлтый, extra — серый),
  легенда; фильтр по статусу ячейки, поиск по sid, дозагрузка правил
  по 50 (keyset `rule_cursor`).

Проверено на живом стенде: сборка ruleset, создание деплоя,
pause → resume → completed, вкл/откл правила (bulk affected:1),
матрица (loaded/missing-ячейки, фильтры, пагинация).

Auth не требуется (dev-заглушка). Полноценный React-фронтенд
(каталог `web/`) — следующий этап фазы 2: конструктор ruleset'ов,
расширенные представления матрицы и работа с IOC.

Дополнительно — консоль MinIO (порт 9001): бакет `surifleet-rulesets`
с собранными ruleset-блобами (имя объекта = SHA-256 содержимого).

## 3. Аутентификация API (dev-режим)

Сейчас работает **заглушка DevAuth**: настоящей auth нет, identity берётся
из заголовка `X-Dev-User` (если не передан — `dev-admin`). API-токены и
аудит-лог запланированы (FEATURES.md, п. 5.5).

```bash
curl -H "X-Dev-User: analyst1" http://192.168.31.28:8080/api/v1/health
```

Формат ошибок единый: `{"error": {"code": "...", "message": "...",
"fields": {...}?}}`. Пагинация — keyset: параметры `limit` (1..200,
default 50) и `cursor`; в ответе `next_cursor` (null — страниц больше нет).

## 4. Что можно делать через API (карта эндпоинтов)

Все пути — под префиксом `/api/v1`.

### 4.1 Служебные
- `GET /health` — живость процесса и PostgreSQL.
- `GET /version` — версия и коммит сборки.

### 4.2 Топология флота: Организация → Кластеры → Хосты → Инстансы
- `GET/POST /organizations`, `GET/PATCH/DELETE /organizations/{id}`
- `GET/POST /clusters`, `GET/PATCH/DELETE /clusters/{id}`
- `GET/POST /clusters/{id}/join_tokens` — выпуск одноразового токена для
  подключения агента (в ответе токен виден один раз; `GET` — список
  выданных, без значений).
- `GET/POST /hosts`, `GET/PATCH/DELETE /hosts/{id}`
- `GET /hosts/{id}/discovery` — отчёт агента о хосте (бинарь Suricata,
  версия, конфиг, интерфейсы, юнит systemd).
- `POST /hosts/{id}/confirm_discovery` — подтвердить discovery → создаётся
  инстанс Suricata с реальными путями (идемпотентно).
- `GET/POST /instances`, `GET/PATCH/DELETE /instances/{id}`

### 4.3 Репозиторий правил
- `POST /rules/import` — импорт .rules (multipart или text/plain; параметр
  `source`). Upsert по (org, sid): ответ `{imported, updated, unchanged,
  errors[]}`; ручной тюнинг (status/priority/threshold/tags) импорт не
  затирает.
- `GET /rules` — список с фильтрами (`status`, `category`, `tag`, `source`,
  `sid`, свободный поиск `q`) и пагинацией; удалённые (soft-delete) скрыты.
- `POST /rules`, `GET/PATCH/DELETE /rules/{id}` — CRUD одиночного правила.
- `POST /rules/bulk` — массовые операции (`enable` / `disable` / `delete` /
  `set_priority` / `add_tag`) по `ids` или по фильтру; пустая цель → 400.
- `GET /rules/{id}/revisions` — история версий правила (keyset по номеру
  ревизии).

### 4.4 Ruleset'ы (требование А: desired state)
- `POST /rulesets` — детерминированная сборка ruleset: `version`
  (обязательно), `rule_filter` (по умолчанию — все enabled) **или**
  `rule_ids` (явный список UUID, приоритет над фильтром; частичный
  mismatch → 400 `requested/found`), `note`. Блоб уходит в MinIO
  (content-addressed, SHA-256); повтор той же выборки → 200 с
  существующей версией (идемпотентно).
- `GET /rulesets`, `GET /rulesets/{id}` — список и деталь с manifest
  (состав `[{sid, rev}]`).

### 4.5 Волновой деплой
- `POST /deployments` — запуск: `ruleset_id`, `targeting`
  (`mode`: `all` / `specific_instances` / `specific_hosts` / `clusters` +
  соответствующие `*_ids`), `wave` (`batch_size`, `canary`).
- `GET /deployments`, `GET /deployments/{id}` — статус и progress
  (`total/pending/running/succeeded/failed`, `current_wave`) + лента
  событий деплоя.
- `POST /deployments/{id}/pause` | `/resume` | `/cancel` — управление
  (resume работает из paused и из финального failed: failed-задачи
  переводятся в pending и прогоняются заново — проверено живьём).
- `GET /deployments/{id}/tasks` — задачи по инстансам (status, attempts,
  error, result с `loaded_count` и `ruleset_hash`).

### 4.6 Фактическое состояние и compliance (требование А: actual state)
- `GET /instances/{id}/state` — `desired` (ruleset_version_id, hash) vs
  `actual` (hash, loaded/failed по отчёту агента), `compliance.status`,
  `diff` (missing/extra/failed rules).
- `GET /instances/{id}/deploy_history` — история деплоев инстанса.
- `GET /fleet/compliance` — сводка по флоту: `total_instances` и разбивка
  `by_status` (`in_sync` / `pending` / `partial` / `drift` / `stale`).
- `GET /matrix/rules` — матрица «правила × инстансы» (чанк 13d): оси
  `rules` (sid/msg/status) и `instances` (instance_id/hostname/name) +
  `cells` (sid, instance_id, status = loaded/failed/missing/extra).
  Независимые keyset-курсоры `rule_cursor` (по sid) и `instance_cursor`
  (по id); фильтры `rule_status`, `category`, `sid`, `cluster_id`,
  `cell_status`; `limit` (default 100, max 1000) общий для обеих осей.
  Ячейка строится из desired_state + последнего StateReport агента;
  правило вне целевого/фактического набора инстанса ячейки не имеет.
  Ограничение MVP: `cell_status` фильтрует ячейки текущей страницы оси
  правил (не подтягивает подходящие правила с других страниц).

## 5. Сквозной сценарий «от нуля до задеплоенных правил»

Проверен на живом стенде 2026-09-16 (деплой `335b6069`, 245 правил,
in_sync). Все команды — с Windows-хоста или с самого сервера:

```bash
API=http://192.168.31.28:8080/api/v1

# 1. Импорт правил (файл .rules)
curl -X POST "$API/rules/import?source=etopen" \
  -H "Content-Type: text/plain" --data-binary @rules.rules

# 2. Сборка ruleset из всех enabled правил (или явным списком rule_ids)
curl -X POST "$API/rulesets" -H "Content-Type: application/json" -d '{
  "version": "1.2",
  "rule_filter": {"status": "enabled", "category": "malware"}
}'
# → запоминаем "id" и "rule_count"

# 3. Деплой на конкретный инстанс
curl -X POST "$API/deployments" -H "Content-Type: application/json" -d '{
  "ruleset_id": "<id из шага 2>",
  "targeting": {"mode": "specific_instances",
                "instance_ids": ["<instance_id>"]},
  "wave": {"batch_size": 10, "canary": false}
}'

# 4. Наблюдение за ходом
curl "$API/deployments/<deployment_id>/tasks"

# 5. Итог: фактическое состояние и compliance
curl "$API/instances/<instance_id>/state"
curl "$API/fleet/compliance"
```

Что при этом происходит на сенсоре (агент, capability `rules`):
скачивание блоба по presigned URL + сверка SHA-256 → перевод секции
`rule-files` под управление SuriFleet (штатный `suricata.rules`
отключается комментарием `# surifleet-disabled:`, yaml бэкапится) →
атомарная запись `/var/lib/suricata/rules/zz-surifleet-managed.rules` →
`suricata -T` (при ошибке — автоматический откат и failed-задача с
текстом парсера) → `reload-rules` через unix-сокет → верификация
`ruleset-failed-rules` → отчёт серверу (hash, loaded/failed) →
пересчёт compliance.

## 6. Подключение нового сенсора (onboarding)

1. `POST /clusters/{id}/join_tokens` → одноразовый токен.
2. На сенсоре: запустить агента с `server_addr`/`enroll_addr` и токеном —
   агент пройдет enrollment (получит mTLS-сертификат с CN=agent_id),
   подключится к Hub, пришлёт DiscoveryReport.
3. `GET /hosts/{id}/discovery` → `POST /hosts/{id}/confirm_discovery` →
   инстанс готов к деплоям.

## 7. Известные ограничения (на 2026-09-16)

- Нет настоящей аутентификации (DevAuth-заглушка); UI — MVP: основные
  операции управления есть (деплои, вкл/откл правил, сборка ruleset),
  матрица «правила × инстансы» — вкладка «Матрица» (чанк 13d);
  работа с IOC — в React-фронтенде (web/).
- Логи агентов стекаются в ClickHouse (чанк 13c): вкладка «Логи» в UI,
  API `GET /api/v1/agents/{id}/logs?limit=200`.
- Автооткат при падении сервиса Suricata после деплоя (watchdog) — чанк 12b.
