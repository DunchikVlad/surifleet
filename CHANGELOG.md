# Changelog

Формат — [Keep a Changelog](https://keepachangelog.com/ru/1.1.0/).
Компоненты: server / agent / ui / db / api / proto / docs / infra.

## [Unreleased]

### Added

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
