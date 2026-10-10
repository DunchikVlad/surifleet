# Протокол агент↔сервер SuriFleet

Версия: 0.1 (чанк 3). Контракт: `api/proto/agent/v1/agent.proto` (основной
канал) и `api/proto/agent/v1/enrollment.proto` (первичная регистрация).
Реализует раздел 4 `docs/architecture.md`.

## 1. Каналы

| Канал | RPC | Транспорт | Назначение |
|---|---|---|---|
| Основной | `AgentChannel.Channel(stream AgentMessage) returns (stream ServerMessage)` | HTTP/2, **mTLS** (CN сертификата = agent_id) | Весь обмен после регистрации: heartbeat, отчёты, логи, метрики, задачи |
| Регистрация | `Enrollment.Enroll(EnrollRequest) returns (EnrollResponse)` | HTTP/2, обычный TLS (клиентского сертификата ещё нет) | Одноразовый обмен join token + CSR → сертификат агента |

Агент — всегда инициатор исходящего соединения (443/TCP), работает за NAT и
файрволом без входящих портов. Первое сообщение стрима — `Hello`; до получения
`HelloAck` другие сообщения не отправляются.

## 2. Конверты сообщений

Оба направления — конверт с общими полями и `oneof payload`:

| Поле | Тип | Семантика |
|---|---|---|
| `msg_id` | string (UUID) | Уникальный ID сообщения; основа ack и идемпотентности |
| `seq` | int64 | Монотонный номер **в рамках сессии** (см. §3) |
| `sent_at` | Timestamp | Часы отправителя; сервер сравнивает со своими — детект рассинхронизации (инцидент `clock_skew`) |
| `payload` | oneof | Типизированное тело сообщения |

## 3. Семантика session_id и seq

- `session_id` выдаётся сервером в `HelloAck` и живёт до разрыва стрима.
- `seq` — строго монотонный счётчик **каждого направления в рамках сессии**:
  агент нумерует свои сообщения 1, 2, 3…, сервер — свои независимо.
- Replay-защита: получатель отбрасывает сообщения с `seq` ≤ последнего
  виденного в этой сессии. После переподключения — новая сессия, счётчики
  сбрасываются; старые `seq` недействительны.
- `session_id` + `seq` не заменяют mTLS-идентичность, а дополняют её против
  повторной отправки внутри одного соединения.

## 4. Сообщения агент → сервер

| Сообщение | Периодичность | Ключевые поля |
|---|---|---|
| `Hello` | при подключении | `agent_id`, `agent_version`, `protocol_version` (=1), `boot_id`, `hostname`, `instances[]` (InstanceInfo), `capabilities[]` |
| `Heartbeat` | 30 с (из HelloAck) | `agent_id`, `uptime_seconds`, версии, `resources{cpu_percent, mem_bytes, disk_used_percent}`, `clock_offset_ms`, `instances[]{instance_id, state, pid}` |
| `StateReport` | после деплоя + каждые 300 с | `instance_id`, `ruleset_hash`, `loaded_rules[]{sid,rev}`, `failed_rules[]{sid,rev,error_text}`, `last_reload{action,success,message,finished_at}`, `reported_at`, `full` |
| `RuleLoadReport` | сразу после reload/restart | `instance_id`, `ruleset_hash`, `loaded_count`, `failed_rules[]`, `verified_at` |
| `MetricsBatch` | 60 с | `points[]{ts, instance_id (пусто = хост), name, value, labels{}}` |
| `LogBatch` | по накоплению/таймеру | `entries[]{seq, ts, level, msg, attrs{}}` |
| `TaskResult` | по завершении задачи | `task_id`, `status`, `error`, `details` (oneof по типу), `state_after` |
| `DiscoveryReport` | при онбординге | `instances[]` (DiscoveredInstance: пути, интерфейсы, версия, флаги сборки, systemd unit), `binary{path, version, built_from_source, build_info}` |

`StateReport.full`: при `true` поле `loaded_rules` содержит полный список;
при `false` — список не изменился с прошлого отчёта и опущен (экономия трафика
на 10k инстансов × 50k правил; `failed_rules` передаётся всегда полностью).
Сервер при `full = false` сверяет `ruleset_hash` с предыдущим снапшотом.

## 5. Сообщения сервер → агент

| Сообщение | Ключевые поля |
|---|---|
| `HelloAck` | `session_id`, `server_version`, интервалы (heartbeat 30 / state_report 300 / metrics 60 с), `log_level`, `config` (AgentConfig: capabilities, siem — чанк 89) |
| `Task` | `task_id` (UUID), `deadline`, `type` — oneof из 11 типов задач (ниже) |
| `TaskCancel` | `task_id`, `reason` |
| `LogLevelChange` | `level` (debug/info/warn/error) — на лету, без рестарта |
| `ConfigPush` | `config` (AgentConfig; нулевые поля — «не менять»; `siem` — чанк 89: пересылка EVE-алертов addr/protocol/format, пустой addr — выкл) |

Типы задач (`Task.type`):

| Задача | Поля | Идемпотентность |
|---|---|---|
| `DeployRulesTask` | `instance_id`, `ruleset_version`, `ruleset_hash` (sha256), `signed_url`, `validate_only` (чанк 75) | По `ruleset_hash`: хэш на диске совпадает → только reload + отчёт; `validate_only` — только `suricata -T` кандидата во временном окружении (без записи/рестарта) |
| `DeployConfigTask` | `instance_id`, `config_version`, `signed_url` или `inline_yaml`, `validate_only` | По `config_version`; `validate_only` — только `suricata -T` |
| `ServiceActionTask` | `instance_id`, `action` (reload/restart/stop/start) | Повторное выполнение безопасно по семантике systemd |
| `RollbackTask` | `instance_id`, цель: `ruleset_version` или `config_version` | По целевой версии |
| `CollectBundleTask` | флаги состава (логи агента/Suricata, конфиги, sysinfo), `upload_url` | Повтор перезаписывает бандл |
| `AgentUpdateTask` | `version`, `signed_url`, `sha256` | По `version`: уже на ней → сразу success |
| `SetCapabilitiesTask` | `capabilities[]` — полный целевой набор | По составу набора |
| `FetchConfigTask` | `instance_id` | Читающая задача (журнал не пишется; чанк 55) |
| `SuricataUpdateTask` | `instance_id`, enable/disable источников, `list_sources`, `no_update`, `reload`, `upload_url`/`upload_key`, `sources_url`/`sources_key` | Журнал обработанных task_id (чанк 82) |
| `LogRotationTask` (чанк 111) | `instance_id`, `report_only`, `min_size_kb`, `keep` | Порог размера: повтор после усечения — no-op; журнал не пишется |
| `PackageTask` (чанк 111) | `package` (suricata/suricata-update), `action` (check/install/remove/update) | apt идемпотентен по семантике; журнал не пишется |

## 6. Идемпотентность и надёжность

- **Задачи**: `task_id` (UUID) — агент хранит на диске журнал обработанных
  task_id; повторная доставка не выполняется дважды.
- **TaskResult**: доставляется at-least-once (ретраи до ack сервера);
  сервер дедуплицирует по `task_id`.
- **Логи**: `LogEntry.seq` монотонен на агенте; сервер дедуплицирует по
  `(agent_id, seq)`. Недоставленные записи переживают ротацию локального
  файла (журнал «последний подтверждённый seq») и досылаются после
  восстановления канала.
- **Успех деплоя** — только по факту: `DeployRulesResult` +
  `RuleLoadReport`/`state_after` с подтверждением загрузки правил движком,
  а не по коду возврата команды доставки файла.
- **Переподключение**: exponential backoff 1→2→4…60 с с полным jitter;
  при потере канала агент работает на последней конфигурации, буферизует
  логи/метрики на диск с ротацией.

## 7. Версионирование протокола

- Текущая версия — `protocol_version = 1` (передаётся в `Hello`).
- Версия **мажорная**: несовместимые изменения (удаление/переименование полей,
  смена семантики) требуют `agent.v2` с новым пакетом; внутри v1 — только
  обратимо-совместимые добавления.
- Совместимость: сервер обслуживает все версии ≥ минимально поддерживаемой
  (публикуется в матрице совместимости); `HelloAck.server_version` позволяет
  агенту зафиксировать пару версий для диагностики и инцидента
  `agent_outdated`. Агент с неподдерживаемой версией получает отказ на уровне
  gRPC-статуса при `Hello` с указанием минимальной версии.
- Правило обновления флота: сервер обновляется **раньше** агентов (сервер
  понимает старых агентов); автообновление агентов — волнами через
  `AgentUpdateTask`.

## 8. Правила эволюции proto-файлов

1. **Только добавление**: новые поля — с новыми номерами; новые сообщения —
  свободно; новые ветки oneof — свободно.
2. **Номера полей после публикации не менять и не переиспользовать.**
3. Удаляемое поле: сначала перестаёт использоваться кодом, затем помечается
  `reserved <номер>; reserved "<имя>";` — имя и номер навсегда выводятся из
  обращения.
4. Значения enum не удалять и не менять номера; новые значения — в конец;
  нулевое значение `*_UNSPECIFIED` обязательно.
5. Диапазоны, помеченные в файлах комментарием «зарезервировано», — под
  будущие сообщения той же категории (payload агента 18–99, payload сервера
  15–99, типы задач 17–49, детали результатов 16–49).
6. Поля `map<string, string>` (labels, attrs) — только для разреженных
  необязательных атрибутов; всё, что участвует в логике протокола, — явными
  полями.
7. Время — `google.protobuf.Timestamp`; длительности —
  `google.protobuf.Duration`; интервалы в секундах, заданные изначально как
  int32 (совместимость с HelloAck), не переводить на Duration внутри v1.

## 9. Enrollment (первичная регистрация)

По §10 архитектуры: join token (одноразовый, TTL, привязан к кластеру) →
агент генерирует ключи → отправляет CSR → получает сертификат.

| Сообщение | Поля |
|---|---|
| `EnrollRequest` | `join_token`, `csr` (PEM PKCS#10), `host` (HostMeta: hostname, os, arch, ip_addresses, agent_version) |
| `EnrollResponse` | `agent_id`, `certificate` (PEM, 90 дней, CN = agent_id), `ca_chain` (PEM), `hub_endpoints[]`, `config` (AgentConfig) |

- `Enroll` идемпотентен по join token до его погашения: повторный запрос с тем
  же токеном возвращает тот же `agent_id` (защита от потери ответа сетью).
- CN в CSR игнорируется — сервер подписывает сертификат с CN = выданный
  `agent_id`.
- Ротация сертификата — через основной стрим за 30 дней до истечения
  (отдельный тип задачи появится в следующих ревизиях протокола; на MVP —
  перевыпуск через повторный Enroll по новому join token).
