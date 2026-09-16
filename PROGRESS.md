# PROGRESS — состояние работы

> Обновляется после КАЖДОГО чанка. Правило возобновления: новая сессия начинается
> с чтения этого файла и `git log` — продолжаем с записанного следующего шага.

## Текущая фаза

**Фаза 2 — MVP** (п. 11 ТЗ, шаг 4): сервер (Go) + агент (Go) + UI.
Фаза 1 (архитектура, модель данных, proto, OpenAPI) завершена.

## Следующий шаг (конкретно)

**Чанк 13d** (после 13c, 2026-09-16):
1. Матрица правила×хосты (требование А, UI-часть).
2. React-фронтенд в web/ — когда MVP упрётся в пределы ванильного JS.
3. **Аномалия (не блокер)**: из ssh `sudo rm` в /etc/suricata → Permission
   denied при работающем touch; обход — агент от root правит сам (12a).

Чанк 13c ГОТОВ (2026-09-16): доставка логов агента на сервер и просмотр
в UI. Агент: slog captureHandler → ring buffer 500 (переживает reconnect)
→ LogBatch по стриму каждые 30 с. Сервер: `internal/chlogs` (ClickHouse
по HTTP :8123, DSN `server.clickhouse_dsn`, clickhouse://9900 → :8123),
таблица surifleet.agent_logs создаётся при старте; hub пишет батчи.
API: GET /api/v1/agents, GET /api/v1/agents/{id}/logs?limit=N (≤1000).
UI: вкладка «Логи» (выбор агента, лимит, автообновление 10 с).
Infra: ClickHouse на .28 — default без пароля из LAN через маунт
`zz_allow_network.xml` в users.d (docker-compose.yml на .28 изменён:
entrypoint официального образа без кредами сам ограничивает default
localhost'ом → HTTP 403 AUTHENTICATION_FAILED, несмотря на комментарий
«без пароля» в compose).
Живой e2e: после переката агента первый LogBatch принёс 30 записей
(включая накопленный за разрыв warn — доставка при reconnect работает),
SELECT count() = 30 > 0, SELECT … LIMIT 5 — свежие записи агента
97885671-36c1-46aa-b597-e0f1aa59c2af; API /agents/…/logs?limit=5 — 200
с JSON (ts RFC3339); /api/v1/agents — 200; /ui/app.js — 200, «logs»
встречается 13 раз; go build/vet/test + node --check чисто.
ВАЖНО (как и раньше): UI в реальном браузере не открывался — вкладку
«Логи» проверить руками при первом открытии.

Чанк 13b ГОТОВ (2026-09-16): управление из UI — создание деплоя
(выбор ruleset+инстанс), pause/resume/cancel, вкл/откл правил (bulk),
сборка ruleset из UI. Живой e2e: ruleset (идемпотентный возврат 245) →
деплой 102c688b → pause → resume → completed 1/1; bulk affected:1.
UI визуально в браузере так и не проверен (InAppBrowser недоступен в
ходе) — проверить руками при первом открытии.

Чанк 13 ГОТОВ (2026-09-16): MVP Web UI встроен в сервер (go:embed,
internal/httpapi/webui: index.html + app.js + style.css), раздаётся с
`/` и `/ui/*` на :8080. Вкладки: Обзор (compliance флота, активные
деплои, автообновление), Инстансы (+state/diff по клику), Правила
(фильтр+поиск+пагинация), Ruleset'ы, Деплои (+задачи по клику).
Проверено с Windows-хоста: все страницы и эндпоинты 200, app.js —
node --check. Баг при раздаче: FileServer 301 index.html → ./ —
исправлено прямой отдачей index.html.
ВАЖНО: UI на момент проверки НЕ открыт в реальном браузере (in-app
browser был недоступен) — при первом открытии пользователем проверить
вкладки вручную.

Чанк 12c-2 ГОТОВ И ПРОВЕРЕН (2026-09-16): watchdog автоотката после
деплоя. Через 90 с после успешного деплоя агент проверяет живость движка
(systemctl is-active по systemd_unit, fallback — suricatasc uptime); если
не жив — откат managed-файла из бэкапа + systemctl restart + повторная
проверка. Живой e2e: деплой e3df40cf (ruleset 2.0) → успех → движок
остановлен наблюдателем → watchdog в +90 с: «движок НЕ жив — откат» →
за 6 с откат + рестарт → «движок поднят», is-active=active, managed-файл
откачен на 245-правильную версию. Побочно выяснено: systemctl stop
Suricata — graceful, занимает до ~55 с (учитывать в тестах).

Чанк 12b ГОТОВ И ПРОВЕРЕН (2026-09-16): починен resume failed-деплоев
end-to-end. Кореневая причина была на агенте: failed-результаты писались
в журнал идемпотентности processed_tasks.jsonl и повторная доставка
проигрывала УСТАРЕВШИЙ провал (ошибка «Duplicate signature» от давно
исправленного окружения — ложный след; диагностировано ручным
suricata -T на том же файле: EXIT=0). Теперь журналируется только
succeeded; сервер принимает resume и из финального failed. Живой e2e:
деплой 1b8fbcf5 → resume → задача перевыполнена (attempts=3) →
succeeded (245/0), деплой completed, fleet in_sync:1.

Чанк 12a ГОТОВ И ПРОВЕРЕН (2026-09-16): агент при capability 'rules'
полностью берёт rule-files под управление — ensureRuleFiles отключает
чужие источники комментарием "# surifleet-disabled:" (идемпотентно,
бэкап yaml), оставляя только managed-файл. Живой e2e: деплой 335b6069
ET-сабсета d44182a6 (245 правил) → succeeded за 6 с, loaded=245/failed=0,
fleet/compliance in_sync:1, движок ruleset-stats 245 loaded/0 failed.
Побочный эффект: suricata -T стал <1 с (было ~48 с) — движок больше не
грузит 52k штатных правил. Закрыт вопрос по sid 2045706: правило валидно,
его прошлая ошибка была каскадом от Duplicate signature.
Документация по подключению: docs/access.md (карта портов, DevAuth,
все эндпоинты, сквозной сценарий; UI ещё не реализован — зафиксировано).

Верификация чанка 11 ПРОЙДЕНА (2026-09-16): ruleset smoke2 (2 кастомных
правила sid 9000020/9000021, version 2.0, id 9e3f67f5) → деплой
feeba40d → задача succeeded (loaded=2, failed=0) → /instances/{id}/state:
compliance in_sync, desired==actual hash 4813c308… → /fleet/compliance:
in_sync:1 → движок: ruleset-stats 52743 loaded / 0 failed. Попутно
исправлены: баг игнора rule_ids в POST /rulesets (400 при частичном
mismatch requested/found) и таймаут reload-rules 30с→240с (на стенде
reload занимает ~70 с — движок отвечает только после фактической
перезагрузки).

Состояние стенда: сервер .28 (пересобран с rule_ids, в deploy/config/
server.example.yaml перенесены s3 access_key/secret_key/public_endpoint);
агент .67 — /home/test/surifleet/start-agent.sh (лежит в ~/surifleet,
не в /tmp), запуск от root через `python .tools/ssh.py 67 sudo
"bash -c 'cd /home/test/surifleet && setsid ./start-agent.sh >/dev/null
2>&1 </dev/null &'"`. ВНИМАНИЕ: ssh.py sudo оборачивает команду как
`sudo -S <cmd>` без bash — для составных команд всегда `bash -c '...'`;
pkill на .67 НЕ установлен (использовать kill $(pgrep -x …)).
В БД: ruleset'ы 6ca65f0f (243 ET), 9e3f67f5 (2), d44182a6 (245 ET,
задеплоен); деплои e7ac5424/951d91bf/7ad065b6/1b8fbcf5/dc6a460d failed,
feeba40d и 335b6069 — succeeded. На сенсоре штатный suricata.rules
отключён агентом (# surifleet-disabled:), движок работает только на
managed-файле (245 правил).

## Сделано (продолжение)

| Чанк | Содержание | Коммит |
|---|---|---|
| 6 | Тестовое окружение: Docker 29.1.3 + Compose v2.40.3 на .28; `deploy/docker-compose.yml` (postgres:16-alpine, redis:7-alpine, nats:2.10-alpine -js, clickhouse:24.8-alpine, minio с quay.io + init-бакет surifleet-rulesets); стек healthy (~325 МиБ RAM); миграция 000001 прогнана up/down/up на живом PG (43 отношения: 28 таблиц + 15 партиций); append-only триггер audit_log проверен (UPDATE → ошибка); все сервисы доступны с Windows | 2940139 |
| 7 | Сервер: `internal/store` (pgx/v5 пул, миграции golang-migrate из embed.FS при старте + --migrate-only, репозитории organizations/clusters/hosts с keyset-пагинацией, маппинг 23505→409/23503→400) + `internal/httpapi` (chi /api/v1 CRUD флота, формат Error по openapi, limit/cursor, middleware request-id/recover/access-log/DevAuth-заглушка X-Dev-User); pgx 5.7.2, migrate 4.18.2, uuid 1.6.0; build/vet/test зелёные (go test прошёл под Windows); живой CRUD проверен curl-ом с Windows на .28 (201/409/400/404/204, next_cursor, health с checks.postgres). Dev-стенд запущен на .28 (PID в ~/surifleet/server.pid, API http://192.168.31.28:8080/api/v1), в БД тестовые org acme/кластер DC-1/хост sensor-01-dc1 | 0447e1f |
| 8 | gRPC Hub + агент end-to-end: миграция 000002 join_tokens; `internal/pki` (встроенный CA ECDSA P-256, SignCSR CN=agent_id 90 дней, серверный сертификат с SAN); `internal/enroll` (Enroll: проверка токена, атомарный расход в tx, создание host+agent, AlreadyExists при повторе без расхода токена); `internal/hub` (mTLS-сверка CN↔agent_id, Hello timeout, HelloAck 30/300/60, presence Redis stream:{agent_id} TTL 120 + hub:{id}:agents, seq replay-защита, clock-skew warn, offline в defer, agent_state_history при сменах); API POST/GET /clusters/{id}/join_tokens (токен один раз, в БД хэш); агент: enrollment (ключи/CSR, сохранение 0600) + mTLS-стрим + heartbeat-горутина. Живой e2e: enrollment с .67 → heartbeat → online в PG/Redis; kill → offline; рестарт → online без повторного enrollment | b6ecad4 |
| 9 | Suricata 8.0.3 на .67 (apt, сервис active/enabled, конфиг на enp0s3, ET Open 45 МБ через suricata-update); миграция 000003 (hosts.discovery jsonb + discovered_at); агент: discovery (бинарь/yaml/юнит/интерфейсы, свой лёгкий парсер yaml), DiscoveryReport после HelloAck, heartbeat с ResourceSummary (/proc) и статусами сервисов; сервер: сохранение discovery в PG, GET /hosts/{id}/discovery, POST confirm_discovery (идемпотентный upsert в instances), полный CRUD /instances. Живой e2e: discovery → confirm → инстанс с реальными путями в БД. Запущено: сервер .28 PID 64741, агент .67 PID 5404 | c07b8c4 |
| 10 | Репозиторий правил: `internal/rules` парсер (без зависимостей, ~430k правил/с, заголовок+опции с кавычками/экранированием, continuation-строки, action-набор + rejectsrc/dst/both, ошибки по строкам без остановки импорта); `internal/store` RulesRepo (upsert по (org,sid) в tx, sha256 raw → imported/updated/unchanged, тюнинг аналитика status/priority/threshold/tags НИКОГДА не перетирается импортом, keyset-ревизии по номеру); API /rules: import (multipart/text, source), CRUD (soft-delete, список скрывает deleted), bulk (enable/disable/delete/set_priority/add_tag по ids или фильтру, защита от пустой цели → 400), revisions. Живьём: 2000 строк ET Open → 1209 imported, повтор → unchanged, битый файл → errors со строками, bulk по категории 241 affected, тюнинг пережил реимпорт. Исправлен баг ANY-плейсхолдера в Bulk по ids. Сервер .28 PID 83802 | 68c3c74 |
| 11 | Волновой деплой (требование А) — **ГОТОВО И ПРОВЕРЕНО ЖИВЬЁМ**: `internal/blob` (MinIO, content-addressed по SHA-256, presigned GET); `internal/ruleset` (детерминированный рендер ruleset из выбранных правил); `internal/store` rulesets/deploy/capabilities; `internal/orchestrator` (волны canary+batch, Recover при рестарте, DispatchPending при Hello, HandleTaskResult, auto-pause при провале canary, deploy_events); `internal/hub/tasks.go` (in-process реестр стримов, SendTask); `internal/compliance` (расчёт in_sync/pending/partial/drift/stale по событиям); `cmd/agent/deploy.go` (скачивание блоба + проверка SHA-256, бэкап, атомарная запись, suricata -T с откатом, reload-rules через сокет, верификация ruleset-failed-rules, журнал обработанных task_id для идемпотентности); API: /rulesets, /deployments (+pause/resume/cancel/tasks), /instances/{id}/state, /instances/{id}/deploy_history, /fleet/compliance, capabilities. Живой e2e (2026-09-16): негатив (битые правила → suricata -T → откат → failed с текстом парсера) и позитив (ruleset из 2 кастомных правил → succeeded, loaded=2/failed=0, compliance in_sync, движок 52743 loaded/0 failed). Исправлены: игнор rule_ids в POST /rulesets, таймаут reload-rules 30→240 с, s3.public_endpoint в example-конфиге | 8dea190 |
| 12a | Агент управляет rule-files + документация доступа: `ensureRuleFiles` при capability 'rules' отключает чужие источники правил комментарием `# surifleet-disabled:` (идемпотентно, бэкап yaml .surifleet-bak-TS), оставляя только zz-surifleet-managed.rules — закрыта причина Duplicate signature при деплое ET-подмножеств. Живой e2e: деплой 335b6069 (245 ET-правил) → succeeded за 6 с, loaded=245/failed=0, in_sync; suricata -T ускорился 48 с → <1 с; подтверждено, что ошибка sid 2045706 была каскадом от дубликатов. Новый документ `docs/access.md`: карта портов/доступов стенда, DevAuth, полная карта эндпоинтов /api/v1, сквозной сценарий «импорт → ruleset → деплой → compliance», статус UI (не реализован) | f18b822 |
| 12b | Фикс resume failed-деплоев end-to-end: сервер — resume допустим из финального failed (раньше 409); агент — failed-результаты больше не пишутся в журнал идемпотентности processed_tasks.jsonl (раньше повторная доставка проигрывала устаревший закэшированный провал — кореневая причина бага; журналируется только succeeded). Живой e2e: деплой 1b8fbcf5 → resume → задача перевыполнена (attempts=3) → succeeded (245/0), деплой completed, fleet in_sync | bdad386 |
| 12c-1 | instance_id агенту при подключении: proto HelloAck.bound_instances (InstanceBinding: instance_id/name/config_path/rules_dir/log_dir), сервер — instanceBindings (инстансы хоста агента), агент — лог привязки + data_dir/bound_instances.json (0600, атомарно). Регенерация proto через scripts/gen-proto.sh. Живой e2e: при Hello агент получил instance_id 468c9c71…, файл записан | 660caf5 |
| 12c-2 | Watchdog автоотката после деплоя: +90 с после успеха агент проверяет живость движка (systemctl is-active / suricatasc uptime), при смерти — откат managed-файла + systemctl restart + перепроверка. Живой e2e: движок остановлен сразу после деплоя → watchdog детектировал → за 6 с откат + рестарт → active, managed-файл откачен (245 правил вместо 2) | 18f449b |
| 13 | MVP Web UI в сервере (`internal/httpapi/webui`, go:embed; index.html+app.js+style.css; ванильный JS поверх /api/v1): Обзор (compliance-карточки, активные деплои, автообновление 15 с), Инстансы (+state/diff), Правила (фильтр/поиск/пагинация), Ruleset'ы, Деплои (+задачи). Раздача с / и /ui/* на :8080. Проверено с Windows: все эндпоинты 200, node --check app.js; фикс зацикливания FileServer на index.html | 431f345 |
| 13b | Управление из UI: кнопки pause/resume/cancel у деплоев (dep-act), вкл/откл правил (rule-toggle → POST /rules/bulk), форма сборки ruleset (rs-build → POST /rulesets с rule_filter status=enabled), форма нового деплоя (dep-create). apiPOST-хелпер. Живой e2e через API как из UI: сборка ruleset 200 (идемпотентный 245 правил), деплой 102c688b create→pause→resume→completed 1/1, bulk enable/disable affected:1 туда-обратно; node --check app.js; сервер перекачен, app.js отдаётся с новыми функциями | c046466 |
| 13c | Логи агента на сервер и в UI: агент — captureHandler поверх slog → ring buffer 500 (переживает reconnect) → LogBatch каждые 30 с по стриму (`cmd/agent/logship.go`); сервер — `internal/chlogs` (ClickHouse по HTTP; native-DSN clickhouse://host:9900 конвертируется в http :8123), таблица surifleet.agent_logs (CREATE IF NOT EXISTS при старте), запись батчей в hub.handleLogBatch; API: GET /api/v1/agents (список с hostname), GET /api/v1/agents/{id}/logs?limit=200 (≤1000, ts DESC, ts→RFC3339); UI — вкладка «Логи» (селектор агента, лимит, обновить, авто 10 с). Infra-фикс: ClickHouse default без пароля из LAN через маунт zz_allow_network.xml в users.d (entrypoint без кредами сам резал default до localhost → 403). Живой e2e: 30 записей за первый батч (включая warn о разрыве — буфер дождался reconnect), SELECT count()>0, API 200 с записями, app.js содержит logs, go build/vet/test/node --check чисто | (этот коммит) |

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
