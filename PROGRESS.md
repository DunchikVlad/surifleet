# PROGRESS — состояние работы

> Обновляется после КАЖДОГО чанка. Правило возобновления: новая сессия начинается
> с чтения этого файла и `git log` — продолжаем с записанного следующего шага.

## Текущая фаза

**Фаза 2 — MVP** (п. 11 ТЗ, шаг 4): сервер (Go) + агент (Go) + UI.
Фаза 1 (архитектура, модель данных, proto, OpenAPI) завершена.

## Следующий шаг (конкретно)

Чанк 6: тестовое окружение на 192.168.31.28 — установить Docker
(`docker.io` + `docker compose` plugin через apt), написать
`deploy/docker-compose.yml` (PostgreSQL 16, Redis 7, NATS JetStream,
ClickHouse, MinIO), поднять стек, прогнать миграцию 000001 на живом
PostgreSQL (up + down + up). Критерий: все контейнеры healthy,
`migrate up` применяется без ошибок. Затем коммит.

Далее чанк 7: сервер — pgx + миграции при старте, CRUD флота по openapi.yaml
с keyset-пагинацией, скелет auth-middleware.

## Тестовая среда (добавлена в ТЗ п. 13)

| Хост | Назначение | Доступ |
|---|---|---|
| 192.168.31.28 | Серверный контур (PG, Redis, NATS, ClickHouse, MinIO, SuriFleet server) | SSH test/test (+sudo) |
| 192.168.31.67 | Сенсор: Suricata + агент SuriFleet | SSH test/test (+sudo) |

Оба: Ubuntu 26.04.1 LTS, x86_64, 2 vCPU, 3.3 ГБ RAM, 20 ГБ свободно,
systemd 259. Интерфейс захвата на сенсоре: enp0s3. Docker/СУБД на .28
НЕ установлены — чистая ОС. SSH-хелпер: `python .tools/ssh.py 28|67 [sudo] <cmd>`
(paramiko, парольная аутентификация).

## Сделано

| Чанк | Содержание | Коммит |
|---|---|---|
| 0 | git-репозиторий, структура каталогов, .gitignore, README, живые документы; локальный Go 1.27.1 в `.tools/` | 817cef6 |
| 1 | `docs/architecture.md`: компонентная диаграмма, протокол агент↔сервер (конверт, жизненный цикл, типы сообщений), масштабирование 10k (Hub-узлы, Redis-реестр, NATS fan-out), волновой деплой, desired/actual state, деградация, безопасность | 2066cfd |
| 2 | `docs/data-model.md` (ER mermaid, 27 таблиц) + миграция `db/migrations/000001_init.up/down.sql`: все сущности п. 11.2 ТЗ, партиционирование audit_log/deploy_events/agent_state_history по месяцам, append-only триггеры audit_log, индексы под матрицу «правила × хосты» и keyset-пагинацию | abd33ee |
| 3 | `api/proto/agent/v1/agent.proto` + `enrollment.proto` + `docs/protocol.md`: bidi-стрим Channel, конверты с oneof (8 типов агент→сервер, 5 сервер→агент), 7 типов задач, Enrollment по join token + CSR; компиляция проверена protoc 36.1 | 773b382 |
| 4 | `api/openapi/openapi.yaml` (85 путей, 126 операций, 114 схем; keyset-пагинация, единый Error, bearerAuth+apiKeyAuth, права в description каждой операции) + `docs/api.md` (конвенции); валидация redocly lint — 0 errors | 01eb922 |
| 5 | Go-каркас: `go.mod` (go 1.22; grpc 1.69.4, protobuf 1.36.5, chi 5.2.1, lumberjack 2.2.1, client_golang 1.20.5), codegen из proto в `internal/gen` (коммитим, скрипт `scripts/gen-proto.sh`), `internal/config` (YAML+env SURIFLEET_*, Validate), `cmd/server` (роли --role, /api/v1/health + /version, /metrics promhttp, graceful shutdown), `cmd/agent` (connectLoop backoff 1s→60s + full jitter, lumberjack-ротация логов), примеры конфигов `deploy/config/*.example.yaml`. go build/vet/gofmt чисто; /health проверен curl-ом | (этот коммит) |

## Ключевые архитектурные решения

- Имя проекта: **SuriFleet**; Go-модуль `github.com/surifleet/surifleet`.
- ОС разработки — Windows; Go-тулчейн локальный в `.tools/go` (вне git).
- REST API — chi; агент↔сервер — gRPC bidirectional stream поверх mTLS.
- БД — PostgreSQL (pgx, golang-migrate); Redis — presence/кэш/блокировки;
  NATS JetStream — задачи и события; ClickHouse — метрики и логи агентов;
  MinIO (S3) — content-addressed блобы ruleset'ов.
- Агент сам устанавливает исходящее соединение (работа за NAT), heartbeat —
  лёгкие сообщения внутри стрима каждые 30 с.
- Один бинарь сервера, роли процесса `--role=api|hub|all`: API stateless,
  Hub держит стримы; в docker-compose — `--role=all`.
- Аффинити агент→Hub не требуется; реестр стримов в Redis
  (`stream:{agent_id}` → hub_id, TTL 120 с), задачи через NATS subject
  `tasks.{hub_id}`.
- Ruleset — content-addressed блоб в S3 (SHA-256), агент кэширует по хэшу,
  дедупликация между кластерами.
- Статусы соответствия инстанса: in_sync / pending / partial / drift / stale;
  расчёт инкрементальный по событиям, сводка из `instance_compliance`.
- mTLS: встроенный CA в MVP, CN сертификата = agent_id, срок 90 дней,
  авторотация за 30 дней; enrollment по одноразовому join token + CSR.
- Модель данных: тенант-изоляция через organization_id во всех доменных
  сущностях; audit_log без FK (переживает удаление акторов) и append-only
  на уровне БД (триггеры); desired/actual state — JSONB-документы с PK
  instance_id (link-таблица «инстанс × правило» отвергнута — 500M строк);
  дедупликация инцидентов — частичный UNIQUE по fingerprint среди нерешённых;
  enum-подобные поля — text + CHECK, без CREATE TYPE.
- Протокол v1: StateReport.full=false шлёт только хэш ruleset без списка 50k
  правил (экономия трафика); failed_rules — всегда; ротация сертификата на
  MVP — перевыпуск через повторный Enroll; эволюция proto — только добавление
  полей, резервирование номеров.
- Тулчейн в `.tools/` (вне git): Go 1.27.1, protoc 36.1.
- API: /health и /version публичные под /api/v1; единый GET /tasks/{id} для
  асинхронных действий агентов; rollback деплоя возвращает НОВЫЙ деплой-откат
  (история append-only); объекты вне RBAC-scoping → 404, не 403; секреты
  writeOnly, токены показываются один раз; 4xx — единый default→Error
  (128 стилистических warnings redocly оставлены осознанно).

## Как поднять окружение

```bash
export PATH="$PWD/.tools/go/bin:$PATH"
# ВАЖНО на этой машине: temp/cache внутри workspace (см. проблему Defender ниже)
export GOTMPDIR="$PWD/.tools/tmp" GOCACHE="$PWD/.tools/gocache"
go build ./...
# Запуск сервера (нужен живой PG — пока нет; health/version работают без БД):
go run ./cmd/server --config deploy/config/server.example.yaml
curl http://localhost:8080/api/v1/health
# Перегенерация proto:
./scripts/gen-proto.sh
```

## Известные проблемы / отложенное

- **Windows Defender блокирует линковку server.exe под Windows** (ложное
  срабатывание). Обходной путь найден: кросс-компиляция `GOOS=linux GOARCH=amd64`
  и запуск на 192.168.31.28 — разработка не блокируется; исключение Defender
  на Windows-машине — на усмотрение пользователя, не требуется.
- Миграция 000001 не прогнана на живом PostgreSQL — решается чанком 6.
- TRUNCATE-триггер audit_log на родителе не сработает при TRUNCATE отдельной
  партиции напрямую — остаточный зазор append-only для ролей с DDL-правами.
- Suricata на сенсоре 192.168.31.67 ещё не установлена — отдельный чанк
  после поднятия серверного контура.
