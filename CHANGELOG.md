# Changelog

Формат — [Keep a Changelog](https://keepachangelog.com/ru/1.1.0/).
Компоненты: server / agent / ui / db / api / proto / docs / infra.

## [Unreleased]

### Added

- Чанк 12a (2026-09-16): агент при capability 'rules' полностью берёт
  секцию rule-files под управление — ensureRuleFiles отключает чужие
  источники правил комментарием "# surifleet-disabled:" (идемпотентно,
  с бэкапом yaml), активным остаётся только zz-surifleet-managed.rules.
  Живой e2e: деплой ET-сабсета (245 правил) → succeeded за 6 с,
  loaded=245/failed=0, compliance in_sync; движок ruleset-stats
  245 loaded / 0 failed. Закрыта причина Duplicate signature; ошибка
  sid 2045706 подтверждена как каскад от дубликатов. (agent)
- docs/access.md — как подключиться к стенду: карта портов и доступов,
  DevAuth, полная карта эндпоинтов /api/v1 с примерами curl, сквозной
  сценарий «импорт → ruleset → деплой → compliance», статус UI.
  (docs)
- Верификация чанка 11, заход 3 (2026-09-16): **позитивный сценарий пройден
  полностью** — ruleset smoke2 (2 кастомных правила sid 9000020/9000021)
  задеплоен волной на инстанс: задача succeeded (loaded=2, failed=0),
  /instances/{id}/state → compliance in_sync (desired == actual, hash
  4813c308…), /fleet/compliance → in_sync:1, движок ruleset-stats
  52743 loaded / 0 failed. (server, agent)
- POST /rulesets принимает rule_ids — явный список id правил с приоритетом
  над rule_filter; при частичном mismatch → 400 requested/found.
  Поле добавлено в openapi RulesetBuildInput. (api, server)
- Верификация чанка 11, заход 2 (2026-09-15): **негативный сценарий пройден
  полностью** — бэкап suricata.yaml → запись managed-файла → `suricata -T`
  поймал ошибки → автоматический откат (Suricata не пострадала) → TaskResult
  failed с текстами парсера Suricata. Агент переведён на запуск от root
  (тестовый стенд). (agent, server)

### Fixed

- Чанк 12b (2026-09-16): **resume деплоя чинит failed-задачи end-to-end**.
  Сервер: resume теперь допустим и из финального статуса failed (раньше
  только из paused → 409). Агент: failed-результаты больше НЕ пишутся в
  журнал идемпотентности processed_tasks.jsonl — раньше повторная доставка
  проигрывала закэшированный устаревший провал без выполнения (кореневая
  причина «resume не перезапускает failed-задачи»); журналируется только
  succeeded. Живой e2e: деплой 1b8fbcf5 (2 failed-попытки) → resume →
  задача перевыполнена (attempts=3) → succeeded, деплой completed,
  fleet in_sync. (server, agent)
- БАГ: POST /rulesets с rule_ids игнорировал список и собирал ruleset по
  фильтру (245 правил вместо 2). (server)
- Таймаут reload-rules 30 с → 240 с (агент): на нагруженном сенсоре движок
  отвечает на команду только после фактической перезагрузки (~70 с на
  стенде); раньше suricatasc убивался по таймауту, reload при этом
  завершался успешно, но задача уходила в failed. (agent)
- s3 access_key/secret_key/public_endpoint перенесены в
  deploy/config/server.example.yaml (dev-стенд). (infra)
- Presigned URL для скачивания ruleset генерировался с внутренним endpoint
  (localhost:9000) вместо публичного — на стенде добавлен
  `s3.public_endpoint: 192.168.31.28:9000` в server.yaml на .28. (server)

### Known issues (выявлены верификацией, заход 2)

- ~~Деплой подмножества ET Open конфликтует со штатным suricata.rules
  (Duplicate signature)~~ — РЕШЕНО в чанке 12a: агент отключает штатные
  источники правил при capability 'rules'.
- POST /deployments/{id}/resume не перезапускает failed-задачи. (server)

### Added (ранее)

- Чанк 11 (2026-09-14): волновой деплой ruleset (требование А) — **код
  готов, живая проверка не выполнена** (прервано по лимиту шагов; верификация
  — следующий шаг по PROGRESS.md). `internal/blob` — MinIO content-addressed
  блобы (SHA-256, presigned GET). `internal/ruleset` — детерминированный
  рендер ruleset. `internal/orchestrator` — волны (canary + batch),
  Recover при рестарте сервера, DispatchPending при Hello агента,
  auto-pause при провале canary, deploy_events. `internal/hub/tasks.go` —
  in-process реестр стримов и отправка задач. `internal/compliance` —
  статусы in_sync/pending/partial/drift/stale по событиям.
  `cmd/agent/deploy.go` — выполнение DeployRulesTask: скачивание + проверка
  SHA-256, бэкап, атомарная запись, `suricata -T` с откатом, reload-rules
  через unix-сокет, верификация ruleset-failed-rules, журнал task_id
  (идемпотентность). API: /rulesets, /deployments (pause/resume/cancel/
  tasks), /instances/{id}/state, /instances/{id}/deploy_history,
  /fleet/compliance, capabilities. build/vet/gofmt/test зелёные.
  (server, agent, api, db)
- Чанк 10 (2026-09-14): репозиторий правил. `internal/rules` — парсер
  Suricata-правил без зависимостей (~430k правил/с): заголовок 7 полей,
  опции с кавычками/экранированием, continuation-строки, sid/rev/msg/
  classtype/priority/metadata/reference, ошибки по номерам строк без
  остановки импорта. `internal/store` RulesRepo: upsert по (org, sid) в
  транзакции, sha256 raw → imported/updated/unchanged, ревизии
  (keyset по номеру), тюнинг аналитика (status/priority/threshold/tags)
  никогда не перетирается импортом. API /api/v1/rules: import
  (multipart/text, source=file|feed), update CRUD с soft-delete, bulk
  (enable/disable/delete/set_priority/add_tag по ids или фильтру, пустая
  цель → 400), revisions. Живьём: 2000 строк ET Open → 1209 imported,
  повтор идемпотентен, тюнинг пережил реимпорт. (server, api, db)
- Чанк 9 (2026-09-14): Suricata 8.0.3 на сенсоре .67 (apt, сервис
  active/enabled, конфиг на enp0s3, ET Open 45 МБ через suricata-update).
  Миграция 000003 (hosts.discovery jsonb + discovered_at). Агент: discovery
  (бинарь, `suricata --build-info`, suricata.yaml, каталоги правил/логов,
  интерфейсы af-packet→pcap→default route, systemd-юнит — свой лёгкий
  парсер yaml без зависимостей), DiscoveryReport после HelloAck, heartbeat
  с ResourceSummary (/proc) и статусами сервисов инстансов. Сервер:
  сохранение discovery в PG, GET /api/v1/hosts/{id}/discovery, POST
  /api/v1/hosts/{id}/confirm_discovery (идемпотентный upsert в instances),
  полный CRUD /api/v1/instances. Живой e2e: discovery → confirm → инстанс
  с реальными путями (/etc/suricata/suricata.yaml, enp0s3, suricata.service)
  в БД. (agent, server, db, api)
- Чанк 8 (2026-09-14): gRPC Hub + агент end-to-end. Миграция 000002
  (join_tokens). `internal/pki` — встроенный CA (ECDSA P-256, 0700/0600),
  SignCSR (CN=agent_id, 90 дней, clientAuth), серверный сертификат с SAN,
  TLS-конфиги Hub (mTLS) / Enrollment (TLS без client cert). `internal/enroll`
  — Enroll по join token (атомарный расход в tx, создание host+agent,
  AlreadyExists при повторе без расхода токена). `internal/hub` — Channel:
  сверка CN↔agent_id, Hello timeout 10 с, HelloAck (30/300/60, log_level),
  presence Redis (stream:{agent_id} TTL 120 с, продление heartbeat'ами),
  seq replay-защита, clock-skew warn >60 с, статус offline при разрыве,
  agent_state_history при сменах статуса. API: POST/GET
  /api/v1/clusters/{id}/join_tokens (токен показывается один раз, в БД —
  SHA-256 хэш). Агент: enrollment (генерация ключей, CSR, сохранение
  идентичности 0600) + mTLS-стрим + heartbeat-горутина. redis/go-redis/v9
  9.7.3. Живой e2e: enrollment с .67 → heartbeat → online в PG/Redis,
  kill → offline, рестарт → online. (server, agent, db, api, proto)
- Чанк 7 (2026-09-14): сервер — `internal/store` (pgx/v5 пул, миграции
  golang-migrate из embed.FS при старте, флаг --migrate-only, репозитории
  organizations/clusters/hosts с keyset-пагинацией base64-курсором,
  маппинг ошибок PG 23505→409 conflict / 23503→400) и `internal/httpapi`
  (chi-роутер /api/v1: CRUD организаций/кластеров/хостов по openapi.yaml,
  единый формат Error, limit/cursor-пагинация, middleware request-id/
  recover/access-log/DevAuth-заглушка X-Dev-User с TODO на OIDC).
  Зависимости: pgx 5.7.2, golang-migrate 4.18.2, google/uuid 1.6.0.
  build/vet/gofmt/test зелёные; живой CRUD проверен curl-ом с Windows на
  .28 (201/409/400/404/204, next_cursor, /health с checks.postgres).
  Dev-стенд запущен на .28. (server, db, api)
- Чанк 6 (2026-09-14): тестовое окружение на 192.168.31.28 — Docker 29.1.3 +
  Compose v2.40.3 (apt, пользователь test в группе docker);
  `deploy/docker-compose.yml` + `deploy/.env.example`: postgres:16-alpine,
  redis:7-alpine, nats:2.10-alpine (JetStream), clickhouse:24.8-alpine,
  minio (quay.io) + init-контейнер бакета surifleet-rulesets; healthcheck'и,
  named volumes, лимиты памяти (стек ~325 МиБ). Миграция 000001 прогнана
  up/down/up на живом PostgreSQL (43 отношения), append-only триггер
  audit_log проверен ошибкой на UPDATE. Все сервисы доступны с машины
  разработки. (infra, db)
- ТЗ дополнено разделом 13 «Тестовая среда»: 192.168.31.28 — серверный
  контур, 192.168.31.67 — сенсор (Suricata + агент); SSH test/test (+sudo);
  оба хоста Ubuntu 26.04.1 LTS x86_64. Доступность проверена (ping, SSH,
  параметры ВМ зафиксированы в PROGRESS.md). SSH-хелпер `.tools/ssh.py`
  (paramiko). (docs, infra)
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
