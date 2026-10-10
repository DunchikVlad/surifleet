# Обновление правил на агентах (suricata-update)

> Как устроено обновление правил Suricata на сенсорах через SuriFleet:
> цепочка UI → API → агент → suricata-update → S3 → импорт в
> мастер-репозиторий → авто-ruleset'ы. Актуально на 2026-10-11
> (после чанка 109 — ротация бэкапов и TMPDIR, см. KI-5).

## 1. Общая схема

```
UI (Правила / Ruleset'ы)
  │  GET /instances/{id}/suricata_update/sources   ← список источников (синхронно)
  │  POST /instances/{id}/suricata_update          ← запуск обновления (202, async)
  ▼
Сервер (internal/httpapi/suriupdate.go)
  │  presigned PUT в S3 (набор + карта sid→источник), задача SuricataUpdateTask
  ▼  gRPC hub (mTLS)
Агент (cmd/agent/update.go, capability rules)
  │  suricata-update enable/disable-source → suricata-update → заливка в S3
  │  → карта sid→источник → reload-rules через unix-сокет
  ▼
Сервер: hub OnTaskResult →
  ├─ internal/suriupdate (импорт блоба в мастер-репозиторий организации)
  └─ internal/autoruleset (пересборка авто-ruleset'ов с include_suriupdate)
```

## 2. Список источников (синхронно)

`GET /instances/{id}/suricata_update/sources` (право rules.read). Сервер
шлёт агенту `SuricataUpdateTask{list_sources: true, no_update: true}` и
ждёт до 60 с (`SendTaskAndWait`). Агент выполняет:

1. `suricata-update list-enabled-sources` — пометки enabled (разбор
   защитный: форматы «Name: x», «  - x», ANSI-цвета срезаются).
2. `suricata-update list-sources` — полный каталог. Если индекса нет
   (свежий сенсор, «Source index does not exist») — сначала
   `update-sources`, потом повтор list-sources.
3. Summary привязывается к последнему Name (блочный формат вывода).

Ответ — `{items: [{name, summary, enabled}]}`. В UI это таблица выбора
источников на вкладке «Правила» (и источники для авто-ruleset'ов на
вкладке «Ruleset'ы»).

## 3. Запуск обновления (async, 202)

`POST /instances/{id}/suricata_update` (rules.write), тело:
`{enable_sources: [], disable_sources: [], reload: bool, import?: bool}`
(import по умолчанию true). Сервер:

- генерирует task_id и presigned PUT URL'ы в S3 (TTL 20 мин):
  `suricata-update/<task_id>.rules` и `.../<task_id>.sources.json`;
- шлёт агенту задачу (SendTask, не дожидаясь); ответ `202` с task_id;
- пишет аудит `instances.suricata_update`.

Агент (`executeSuricataUpdate`, гейт capability `rules`, дедлайн задачи,
журнал идемпотентности по task_id — повтор возвращает сохранённый
результат):

1. **enable/disable источников** — `suricata-update enable-source|disable-source <name>`
   (правит update.yaml на сенсоре). Таймаут 60 с на шаг; ошибки шагов
   собираются и фейлят задачу в конце (opErrs).
2. **list-sources** — снимок каталога для результата (как в §2).
3. **Само обновление** — `suricata-update` (таймаут 10 мин, первая
   загрузка большая). Перед запуском (чанк 109):
   `pruneOldBackups` чистит старые `zz-surifleet-managed.rules.surifleet-bak-*`
   в выходном каталоге (оставляет 3 свежих — иначе каталог раздувается
   за пределы tmpfs /tmp и внутренний backup-проход suricata-update
   падает с ENOSPC, см. KI-5), процессу выставляется `TMPDIR=/var/tmp`
   (root-FS, не tmpfs).
4. **Чтение итогового набора** — `/var/lib/suricata/rules/suricata.rules`;
   если вывод не по дефолту — самый свежий непустой `.rules` рядом
   (`readNewestRules`). Считаются sid'ы (`parseSids`).
5. **Заливка на сервер** (если задан upload_url): PUT на presigned URL,
   таймаут 5 мин, контроль HTTP-кода.
6. **Карта sid → источник** (чанк 95, `cmd/agent/sidmap.go`): из
   состояния suricata-update на сенсоре (`/var/lib/suricata/update/`:
   `sources/*.yaml` дают имя источника, `cache/index.yaml` — md5(url) →
   имя тарболла, внутри тарболла .rules-файлы). Заливается вторым PUT —
   нужна UI для фильтрации по источникам в авто-ruleset'ах.
7. **Reload движка** (если `reload: true`): `suricatasc reload-rules`
   через unix-сокет (`/var/run/suricata/suricata-command.socket`).
   На стенде live-swap 53k правил занимает ~60 с — это норма.

Результат — `SuricataUpdateResult` (sources, rules_count, uploaded_bytes,
output — хвост вывода) → в hub.

## 4. Импорт в мастер-репозиторий (сервер)

По результату задачи хаб вызывает цепочку колбэков
(`cmd/server/main.go` → OnTaskResult):

- **`internal/suriupdate.HandleResult`** — если задача успешна и залит
  набор (`uploaded_bytes > 0`): читает блоб из S3 по upload_key,
  разбирает парсером `internal/rules` (лимит ошибок разбора 100 — не
  роняют импорт) и идемпотентно мержит в общий список правил
  организации агента через `Rules.UpsertImport` (source_type='file',
  ревизии — по изменениям raw). Контекст отсоединённый, 15 мин — импорт
  50k+ правил долог. Ошибки импорта — только в лог: набор на сенсоре
  уже применён.
- **`internal/autoruleset`** — пересобирает включённые авто-ruleset'ы
  организации с `include_suriupdate` (новые/обновлённые правила из
  источников попадают в версии ruleset'ов) и запускает волновой деплой
  по их targeting. Срабатывает только на реальное обновление — гейт
  `uploaded_bytes > 0` (чанк 110, KI-6: list-only вызовы /sources раньше
  тоже триггерили пересборку — каждое открытие вкладки UI плодило
  деплой). Если состав не изменился (версия совпала с прошлой сборкой) —
  деплой не создаётся (skipped_reason=unchanged). У авто-ruleset'ов есть
  и своё интервальное расписание (чанк 97).

## 5. Что с чем не путать

- **suricata-update (этот документ)** — подтягивание правил из внешних
  источников (ET Open и др.) на сенсор + импорт в мастер-репозиторий.
- **Деплой ruleset'ов** — доставка собранного в SuriFleet ruleset'а
  (версия в S3) на инстансы: `zz-surifleet-managed.rules` рядом с
  конфигом, бэкап `.surifleet-bak-TS` (ротация 3, чанк 109), атомарная
  запись, watchdog автоотката (чанки 12c-2, 108).
- **Фиды/IOC** — отдельные механизмы (feedsync, iocrules), тоже дают
  origin для авто-ruleset'ов.

## 6. Диагностика

| Симптом | Где смотреть | Типовая причина |
|---|---|---|
| 404 «маршрут не найден» в UI | `git log -S auto_rulesets -- internal/httpapi/router.go` | регресс маршрутов (KI-5, чанк 109) |
| Задача failed: ENOSPC, shutil.Error | df -h /tmp на сенсоре; размер `/var/lib/suricata/rules` | бэкапы раздули каталог за пределы tmpfs (до чанка 109) |
| Обновление «висит» ~60 с при reload | suricata.log: «rule reload starting/complete» | live-swap 53k правил — норма; ждать до 150 с |
| `systemctl list-jobs` — висит reload | journalctl -u suricata | зависший reload-джоб; новый reload его выталкивает |
| 409 «агент offline» | GET /agents — status | агент не подключён к hub |
| import=true, но в общем списке пусто | лог сервера: «suriupdate: …» | ошибка чтения блоба/импорта — на задачу не влияет, см. логи |

Логи агента: `/home/test/surifleet/data/agent.log` (структурные; задача
«suricata-update завершён» с rules/sources). Аудит: действие
`instances.suricata_update`.
