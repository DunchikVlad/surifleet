# FEATURES — реестр функционала

> Пункт ТЗ → статус → где реализовано → чанк/коммит. Обновляется при каждом
> изменении статуса. Статусы: ⬜ не начато · 🚧 в работе · ✅ готово · 🧪 протестировано.

## 5. Базовый функционал (уровень IDSTower)

| Пункт ТЗ | Статус | Где реализовано | Чанк |
|---|---|---|---|
| 5.1 Правила: репозиторий, импорт, жизненный цикл, таргетинг, история, экспорт | 🚧 | `internal/rules` (парсер), `internal/store` RulesRepo, API /rules (import/CRUD/bulk/revisions); жизненный цикл и история ревизий работают; таргетинг и экспорт (STIX/dataset) позже | чанк 10 |
| 5.2 IOC/TI: жизненный цикл, коннекторы TAXII/STIX/MISP, автогенерация правил | 🚧 | `internal/store/iocs.go` (CRUD, keyset-листинг, upsert-импорт, свип expires→expired), REST `/iocs` (+`/iocs/{id}`, `/iocs/import`, `/iocs/generate`), автогенерация Suricata-правил из активных IOC (`internal/iocrules`: sid 8800000..8899999 = FNV-1a(type,value), source_type='ioc' — миграция 000004; ip→alert ip, domain→dns.query, url→http.host+http.uri; hash/email — skip), ruleset `ioc-current-<sha8>` + опциональный деплой (deploy:true), фоновый свипер expires (`server.ioc_sweep_interval`, 60s), React-вкладка «IOC» (форма/таблица/поиск/удаление, ссылки VirusTotal, кнопка «Сгенерировать правила» с отчётом). Фиды: REST `/feeds` (CRUD + POST /feeds/{id}/sync + GET /feeds/{id}/runs), `internal/feedsync` (HTTP GET, plain/CSV/JSON, угадывание типа, импорт через UpsertImport source=имя фида + feed_id), feed_runs (миграция 000005, +feeds.last_error), автопрогон генерации правил после ручного синка (без деплоя), планировщик авто-синка по schedule-длительности Go (`server.feed_sync_interval`, 60s); синк только type=generic; React-вкладка «Фиды»; осталось: коннекторы TAXII/STIX/MISP/ET, cron-расписания, отзыв правил при revoke | чанки 16–18 |
| 5.3 Конфигурации: редактор suricata.yaml, валидация `suricata -T`, профили, откат | ⬜ | — | — |
| 5.4 Мониторинг: метрики Suricata/хоста, пересылка EVE в SIEM, дашборды | ⬜ | — | — |
| 5.5 Платформа: REST API (OpenAPI), API-токены, аудит-лог, уведомления | 🚧 | OpenAPI-спека `api/openapi/openapi.yaml`; REST-каркас `/api/v1` с CRUD флота и join_tokens (`internal/httpapi`); токены/аудит/уведомления позже | чанки 4, 7–8 |

## 6. Ключевое требование А: actual state правил

| Пункт ТЗ | Статус | Где реализовано | Чанк |
|---|---|---|---|
| Desired state (версии, хэш ruleset) | 🧪 | `internal/ruleset`, `internal/store` rulesets/deploy, desired_state; проверено живьём: деплой feeba40d → desired hash записан | чанк 11 |
| Actual state (отчёты агента, кэш Redis, история PG) | 🧪 | `cmd/agent/deploy.go` (RuleLoadReport/StateReport), actual_state + кэш Redis; проверено: actual.ruleset_hash == desired, loaded=2/failed=0 | чанк 11 |
| Drift detection (In sync / Pending / Partial / Drift / Stale) | 🧪 | `internal/compliance`, таблица instance_compliance, GET /fleet/compliance, /instances/{id}/state; проверено: in_sync на живом стенде (остальные статусы — в чанке 12+) | чанк 11 |
| Подтверждение деплоя по факту загрузки движком | 🧪 | `cmd/agent/deploy.go`: suricata -T → reload-rules → ruleset-failed-rules через unix-сокет; проверено в обе стороны (откат при битых правилах, успех на валидных) | чанк 11 |
| UI: матрица правила×хосты, страница инстанса, сводка флота | 🧪 | React UI в `web/` (Vite+React 18+TS, embed, раздаётся с `/app/`; чанки 14–15): все 7 экранов MVP — compliance-обзор, инстансы, правила (фильтр/поиск/вкл-откл), ruleset'ы, деплои (+задачи, pause/resume/cancel), логи агентов, матрица «правила × инстансы» (ячейки loaded/failed/missing/extra, легенда, фильтры); страница инстанса (чанк 15, drill-down): параметры, compliance, desired/actual hash, failed-правила, diff, история деплоев, логи агента; конструктор ruleset'ов (чанк 15): выбор правил чекбоксами → POST /rulesets с rule_ids; ванильный MVP (`internal/httpapi/webui`) остаётся на `/`; проверено по HTTP (браузер — вручную при первом открытии) | чанки 13–15 |

## 7. Ключевое требование Б: проблемы с агентами

| Пункт ТЗ | Статус | Где реализовано | Чанк |
|---|---|---|---|
| Heartbeat, статусы Online/Degraded/Offline/Updating/Error | 🚧 | `internal/hub` (presence Redis, HelloAck), `cmd/agent` (heartbeat 30 с); работают online/offline, остальные статусы позже | чанк 8 |
| Автодетект инцидентов (10 типов) | ⬜ | — | — |
| Автооткат при падении сервиса после деплоя | ⬜ | — | — |
| Реагирование: уведомления, действия из UI, диагностический бандл | ⬜ | — | — |
| Передача состояния и логов агента на сервер | 🧪 | Логи: `cmd/agent/logship.go` (slog captureHandler, ring buffer 500, LogBatch 30 с), hub → `internal/chlogs` → ClickHouse surifleet.agent_logs; чтение: GET /api/v1/agents/{id}/logs, вкладка «Логи» в UI; проверено живьём (30 записей, буфер пережил reconnect) | чанк 13c |
| Локальное логирование агента с ротацией и досылкой | 🚧 | Ротация lumberjack (размер/бэкапы/возраст) работает с чанка 5; досылка = ring buffer 500, переживающий reconnect (чанк 13c); персистентная досылка с диска — нет | чанки 5, 13c |

## 8–9. RBAC и SSO

| Пункт ТЗ | Статус | Где реализовано | Чанк |
|---|---|---|---|
| Встроенные роли (Админ/Оператор/Аналитик/Наблюдатель) + кастомные | ⬜ | — | — |
| Scoping по организации/кластерам | ⬜ | — | — |
| Полный аудит-лог (append-only, цепочка хэшей) | ⬜ | — | — |
| OIDC (Authorization Code + PKCE) | ⬜ | — | — |
| SAML 2.0 | ⬜ | — | — |
| LDAP/AD | ⬜ | — | — |
| Маппинг групп IdP → роли, JIT-провижининг | ⬜ | — | — |
| Break-glass локальный администратор | ⬜ | — | — |
| Сессии, API-токены со scopes | ⬜ | — | — |

## 4. Модель флота

| Пункт ТЗ | Статус | Где реализовано | Чанк |
|---|---|---|---|
| Организация → Кластеры → Хосты → Инстансы | 🚧 | CRUD organizations/clusters/hosts: `internal/store`, `internal/httpapi`; instances — после discovery (чанк 9) | чанки 7–8 |
| Онбординг без переустановки (детект существующей Suricata) | ✅ | `cmd/agent/discovery.go` (бинарь/yaml/юнит/интерфейсы), GET /hosts/{id}/discovery, POST confirm_discovery → instances; проверено на Suricata 8.0.3 | чанк 9 |
| 6 capability поэтапной передачи контроля | ⬜ | — | — |

## 10. Нефункциональные

| Пункт ТЗ | Статус | Где реализовано | Чанк |
|---|---|---|---|
| mTLS с индивидуальными сертификатами агентов, ротация | 🚧 | `internal/pki` (CA, SignCSR, TLS-конфиги), `internal/enroll`; mTLS-стрим работает; ротация сертификатов отложена (перевыпуск через Enroll) | чанк 8 |
| Симулятор флота (1000/10000 агентов) | ⬜ | — | — |
| docker-compose для оценки | ⬜ | — | — |
| Helm-чарт / systemd-пакеты | ⬜ | — | — |
