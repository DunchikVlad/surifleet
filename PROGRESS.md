# PROGRESS — состояние работы

> Обновляется после КАЖДОГО чанка. Правило возобновления: новая сессия начинается
> с чтения этого файла и `git log` — продолжаем с записанного следующего шага.

## Текущая фаза

**Фаза 2 — MVP** (п. 11 ТЗ, шаг 4): сервер (Go) + агент (Go) + UI.
Фаза 1 (архитектура, модель данных, proto, OpenAPI) завершена.

## Следующий шаг (конкретно)

Доверификация чанка 11 (прервана по тайм-боксу 2026-09-15):
1. **Решить модель прав агента**: деплой падает на `permission denied` при
   бэкапе /etc/suricata/suricata.yaml — агент работает от test, каталоги
   Suricata принадлежат root (группы suricata в Ubuntu 26.04 нет, setfacl
   не установлен). Варианты: агент под root (systemd), polkit/sudo-обёртки,
   или управляемый rules-файл в каталоге, доступном агенту + include в
   suricata.yaml один раз при онбординге (через sudo). Выбрать и реализовать.
2. После решения прав: повторить деплой ruleset 6ca65f0f (243 правила) на
   инстанс 468c9c71 (.67) → ожидается suricata -T → reload → RuleLoadReport
   → compliance=in_sync; проверить GET /instances/{id}/state и
   /fleet/compliance.
3. **Баг**: POST /deployments/{id}/resume НЕ перезапускает failed-задачи
   (progress остаётся failed:1, paused) — нужен retry механизм для
   failed-задач при resume.
4. Негативы (не выполнены): битое правило → suricata -T fail → откат;
   деплой при офлайн-агенте → pending → подхват при Hello.

Уже проверено живьём (2026-09-15): сборка ruleset (SHA-256, блоб в MinIO,
запись в PG), создание деплоя, доставка DeployRulesTask агенту через стрим,
скачивание блоба агентом (после фикса public_endpoint). Найден и исправлен
баг: presigned URL генерировался с внутренним endpoint (localhost) вместо
публичного — в server.yaml на .28 добавлен s3.public_endpoint=192.168.31.28:9000
(в example-конфиг репозитория тоже добавить!).

Далее чанк 12: instance_id агенту (ConfigPush/HelloAck), автооткат при
падении сервиса после деплоя, логи агента на сервер (LogBatch → ClickHouse).

## Сделано (продолжение)

| Чанк | Содержание | Коммит |
|---|---|---|
| 6 | Тестовое окружение: Docker 29.1.3 + Compose v2.40.3 на .28; `deploy/docker-compose.yml` (postgres:16-alpine, redis:7-alpine, nats:2.10-alpine -js, clickhouse:24.8-alpine, minio с quay.io + init-бакет surifleet-rulesets); стек healthy (~325 МиБ RAM); миграция 000001 прогнана up/down/up на живом PG (43 отношения: 28 таблиц + 15 партиций); append-only триггер audit_log проверен (UPDATE → ошибка); все сервисы доступны с Windows | 2940139 |
| 7 | Сервер: `internal/store` (pgx/v5 пул, миграции golang-migrate из embed.FS при старте + --migrate-only, репозитории organizations/clusters/hosts с keyset-пагинацией, маппинг 23505→409/23503→400) + `internal/httpapi` (chi /api/v1 CRUD флота, формат Error по openapi, limit/cursor, middleware request-id/recover/access-log/DevAuth-заглушка X-Dev-User); pgx 5.7.2, migrate 4.18.2, uuid 1.6.0; build/vet/test зелёные (go test прошёл под Windows); живой CRUD проверен curl-ом с Windows на .28 (201/409/400/404/204, next_cursor, health с checks.postgres). Dev-стенд запущен на .28 (PID в ~/surifleet/server.pid, API http://192.168.31.28:8080/api/v1), в БД тестовые org acme/кластер DC-1/хост sensor-01-dc1 | 0447e1f |
| 8 | gRPC Hub + агент end-to-end: миграция 000002 join_tokens; `internal/pki` (встроенный CA ECDSA P-256, SignCSR CN=agent_id 90 дней, серверный сертификат с SAN); `internal/enroll` (Enroll: проверка токена, атомарный расход в tx, создание host+agent, AlreadyExists при повторе без расхода токена); `internal/hub` (mTLS-сверка CN↔agent_id, Hello timeout, HelloAck 30/300/60, presence Redis stream:{agent_id} TTL 120 + hub:{id}:agents, seq replay-защита, clock-skew warn, offline в defer, agent_state_history при сменах); API POST/GET /clusters/{id}/join_tokens (токен один раз, в БД хэш); агент: enrollment (ключи/CSR, сохранение 0600) + mTLS-стрим + heartbeat-горутина. Живой e2e: enrollment с .67 → heartbeat → online в PG/Redis; kill → offline; рестарт → online без повторного enrollment | b6ecad4 |
| 9 | Suricata 8.0.3 на .67 (apt, сервис active/enabled, конфиг на enp0s3, ET Open 45 МБ через suricata-update); миграция 000003 (hosts.discovery jsonb + discovered_at); агент: discovery (бинарь/yaml/юнит/интерфейсы, свой лёгкий парсер yaml), DiscoveryReport после HelloAck, heartbeat с ResourceSummary (/proc) и статусами сервисов; сервер: сохранение discovery в PG, GET /hosts/{id}/discovery, POST confirm_discovery (идемпотентный upsert в instances), полный CRUD /instances. Живой e2e: discovery → confirm → инстанс с реальными путями в БД. Запущено: сервер .28 PID 64741, агент .67 PID 5404 | c07b8c4 |
| 10 | Репозиторий правил: `internal/rules` парсер (без зависимостей, ~430k правил/с, заголовок+опции с кавычками/экранированием, continuation-строки, action-набор + rejectsrc/dst/both, ошибки по строкам без остановки импорта); `internal/store` RulesRepo (upsert по (org,sid) в tx, sha256 raw → imported/updated/unchanged, тюнинг аналитика status/priority/threshold/tags НИКОГДА не перетирается импортом, keyset-ревизии по номеру); API /rules: import (multipart/text, source), CRUD (soft-delete, список скрывает deleted), bulk (enable/disable/delete/set_priority/add_tag по ids или фильтру, защита от пустой цели → 400), revisions. Живьём: 2000 строк ET Open → 1209 imported, повтор → unchanged, битый файл → errors со строками, bulk по категории 241 affected, тюнинг пережил реимпорт. Исправлен баг ANY-плейсхолдера в Bulk по ids. Сервер .28 PID 83802 | 68c3c74 |
| 11 | Волновой деплой (требование А) — **КОД ГОТОВ, ЖИВАЯ ПРОВЕРКА НЕ ВЫПОЛНЕНА**: `internal/blob` (MinIO, content-addressed по SHA-256, presigned GET); `internal/ruleset` (детерминированный рендер ruleset из выбранных правил); `internal/store` rulesets/deploy/capabilities; `internal/orchestrator` (волны canary+batch, Recover при рестарте, DispatchPending при Hello, HandleTaskResult, auto-pause при провале canary, deploy_events); `internal/hub/tasks.go` (in-process реестр стримов, SendTask); `internal/compliance` (расчёт in_sync/pending/partial/drift/stale по событиям); `cmd/agent/deploy.go` (скачивание блоба + проверка SHA-256, бэкап, атомарная запись, suricata -T с откатом, reload-rules через сокет, верификация ruleset-failed-rules, журнал обработанных task_id для идемпотентности); API: /rulesets, /deployments (+pause/resume/cancel/tasks), /instances/{id}/state, /instances/{id}/deploy_history, /fleet/compliance, capabilities. build/vet/gofmt/test зелёные | (этот коммит) |

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
- Инфра-стек на .28: MinIO — образы quay.io (с Docker Hub удалены),
  ClickHouse — 24.8-alpine (latest требует AVX, у KVM-гостя его нет;
  на гипервизоре стоит host-passthrough — тогда вернуть latest),
  CH-native порт наружу 9900 (9000 занят MinIO).
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
- TLS-схема: два слушателя — Hub :8443 (mTLS, RequireAndVerifyClientCert) и
  Enrollment :8444 (TLS от того же CA без client cert); серверный сертификат
  от встроенного CA, SAN из server.cert_sans. Enrollment у агента —
  InsecureSkipVerify с TODO на TOFU-пиннинг (защита MVP: одноразовость+TTL
  токена). Повторный enrollment хоста → AlreadyExists, токен не сгорает
  (откат tx). agents.id = CN сертификата.

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
  срабатывание). Обходной путь: кросс-компиляция `GOOS=linux GOARCH=amd64`
  и запуск на 192.168.31.28 — разработка не блокируется.
- TRUNCATE-триггер audit_log на родителе не сработает при TRUNCATE отдельной
  партиции напрямую — остаточный зазор append-only для ролей с DDL-правами.
- ClickHouse 24.8 вместо latest — у KVM-гостя нет AVX; на гипервизоре стоит
  включить host-passthrough CPU, тогда вернуть latest.
- Suricata 8.0.3 установлена на .67 (чанк 9): сервис active/enabled,
  интерфейс enp0s3, правила ET Open скачаны suricata-update.
- Enrollment использует InsecureSkipVerify (проблема «нет CA до enrollment») —
  TODO: TOFU-пиннинг/отпечаток CA в токене.
- TTL-свипер offline по истечении ключа Redis не реализован — при «тихой»
  смерти агента статус в PG останется online до переподключения; last_seen_at
  пишется только на connect/disconnect (heartbeat в PG не пишется, §5.4).
- Heartbeat пока без ResourceSummary/статусов инстансов (чанк 9);
  TaskResult — заглушка not implemented (чанк 10+).
- В середине чанка 8 sshd на .28 ~15 минут не обслуживал подключения
  (banner/auth висли) — похоже на rate-limit при частых SSH; ssh.py/scp.py
  теперь имеют jump-фолбэк через .67.
- Гонка при перекате сервера: kill → старый процесс держит :8080 до 3+ с →
  новый падает на bind. В процедуру переката добавить ожидание освобождения
  порта (чанк 10+).
- Агент не знает instance_id до confirm_discovery — статусы сервисов в
  heartbeat идут с пустым id, сервер их пропускает; после confirm нужна
  доставка instance_id агенту (ConfigPush или в HelloAck — решить в чанке 11).
- Same-rev перевыпуск фида: изменённый raw при том же rev обновляет rules,
  но новая ревизия не создаётся (ON CONFLICT DO NOTHING) — если нужна полная
  история, при конфликте брать max(revision)+1 (отдельная задача).
- threshold в PATCH /rules: jsonb null очищает поле, отсутствие ключа — не
  менять; задокументировать для фронта.
- Разовый флап PG-пула наблюдался (health 503 ~1 мин, само восстановилось) —
  при повторении добавить HealthCheckPeriod в pgxpool.
