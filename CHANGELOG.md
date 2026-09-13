# Changelog

Формат — [Keep a Changelog](https://keepachangelog.com/ru/1.1.0/).
Компоненты: server / agent / ui / db / api / proto / docs / infra.

## [Unreleased]

### Added

- Чанк 5 (2026-09-13): каркас Go-монорепозитория — `go.mod`
  (`github.com/surifleet/surifleet`, go 1.22; grpc 1.69.4, protobuf 1.36.5,
  chi 5.2.1, lumberjack 2.2.1, client_golang 1.20.5, yaml 3.0.1);
  сгенерированный код из proto в `internal/gen/agent/v1` (коммитится,
  скрипт `scripts/gen-proto.sh`); `internal/config` (YAML + env-override
  SURIFLEET_*, Validate); `cmd/server` (флаги --config/--role=api|hub|all,
  slog JSON, /api/v1/health + /api/v1/version, /metrics promhttp,
  graceful shutdown); `cmd/agent` (connectLoop с backoff 1s→60s + full
  jitter, lumberjack-ротация логов в файл + stdout); примеры конфигов
  `deploy/config/server.example.yaml` и `agent.example.yaml`.
  go build/vet/gofmt чисто. (server, agent, infra)
- Чанк 4 (2026-09-13): OpenAPI 3.0.3 спецификация REST API —
  `api/openapi/openapi.yaml` (85 путей, 126 операций, 114 схем): auth+SSO,
  флот и онбординг, правила/фиды/IOC, шаблоны/ruleset/волновой деплой,
  сводка соответствия флота и матрица «правила × инстансы», агенты/логи/
  инциденты, конфиг-профили, пользователи/роли/SSO/API-токены/аудит.
  Keyset-пагинация, единый формат ошибки, bearerAuth + apiKeyAuth.
  `docs/api.md` — конвенции (пагинация, идемпотентность, эволюция).
  Валидация redocly lint: 0 errors. (api, docs)
- Чанк 3 (2026-09-13): gRPC-контракт агент↔сервер —
  `api/proto/agent/v1/agent.proto` (сервис AgentChannel с bidi-стримом
  Channel, конверты AgentMessage/ServerMessage с oneof: 8 типов сообщений
  агента, 5 сервера, 7 типов задач, детали TaskResult), `enrollment.proto`
  (unary Enroll по join token + CSR), `docs/protocol.md` (семантика
  seq/session_id, идемпотентность, версионирование, правила эволюции).
  Компиляция проверена protoc 36.1. (proto, docs)
- Чанк 2 (2026-09-13): модель данных — `docs/data-model.md` (ER-диаграмма
  mermaid, 27 таблиц, разделы по партиционированию/индексам/retention) и
  миграция `db/migrations/000001_init.up.sql` / `000001_init.down.sql`:
  organizations, clusters, hosts, agents, instances, capabilities, rules,
  rule_revisions, feeds, iocs, deploy_templates, ruleset_versions,
  desired_state, actual_state, instance_compliance, deployments,
  deployment_tasks, incidents, users, roles, user_roles, sso_providers,
  sessions, api_tokens + партиционированные audit_log / deploy_events /
  agent_state_history (месячные партиции 2026-09…2026-12 + DEFAULT).
  Append-only audit_log с триггерами запрета UPDATE/DELETE/TRUNCATE.
  (db, docs)
- Чанк 1 (2026-09-13): `docs/architecture.md` — компонентная диаграмма
  (mermaid), протокол агент↔сервер (конверт Envelope, жизненный цикл
  соединения, таблицы сообщений, идемпотентность, backoff+jitter), схема
  масштабирования на 10k агентов (Hub-концентраторы, Redis-реестр стримов,
  fan-out задач через NATS JetStream), волновой деплой с canary,
  desired/actual state и статусы соответствия, матрица деградации,
  безопасность (mTLS, CA, enrollment). (docs)
- Чанк 0 (2026-09-13): инициализация репозитория SuriFleet — структура
  каталогов (cmd, internal, api, db/migrations, docs, web, deploy), .gitignore,
  README.md, живые документы PROGRESS.md / FEATURES.md / CHANGELOG.md.
  Локальный Go-тулчейн 1.27.1 в `.tools/` (вне git). (docs, infra)
