# PROGRESS — состояние работы

> Обновляется после КАЖДОГО чанка. Правило возобновления: новая сессия начинается
> с чтения этого файла и `git log` — продолжаем с записанного следующего шага.

## Текущая фаза

**Фаза 1 — Архитектура и контракты** (п. 11 ТЗ, шаги 1–3).

## Следующий шаг (конкретно)

Чанк 2: модель данных — `docs/data-model.md` (ER-диаграмма mermaid + описание
таблиц) и первая миграция `db/migrations/0001_init.up.sql` / `.down.sql`:
organizations, clusters, hosts, instances, users, roles, rules, rule_revisions,
feeds, deploy_templates, ruleset_versions, desired_state, actual_state,
deployments, agents, incidents, sso_providers, audit_log (партиционирование
по времени для audit_log/deploy_events/agent_state_history). Затем коммит.

## Сделано

| Чанк | Содержание | Коммит |
|---|---|---|
| 0 | git-репозиторий, структура каталогов, .gitignore, README, живые документы; локальный Go 1.27.1 в `.tools/` | 817cef6 |
| 1 | `docs/architecture.md`: компонентная диаграмма, протокол агент↔сервер (конверт, жизненный цикл, типы сообщений), масштабирование 10k (Hub-узлы, Redis-реестр, NATS fan-out), волновой деплой, desired/actual state, деградация, безопасность | (этот коммит) |

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

## Как поднять окружение

Пока нечего поднимать — фаза документов. Go-тулчейн:

```bash
export PATH="$PWD/.tools/go/bin:$PATH"
go version
```

## Известные проблемы / отложенное

- Docker на машине разработки отсутствует — окружение (PostgreSQL/Redis/NATS/
  ClickHouse/MinIO) позже поднимем либо через docker-compose на другой машине,
  либо через нативные бинарники; решение отложено до фазы MVP.
- `suricata` не установлена на машине разработки — для тестов агента понадобится
  либо Linux-VM, либо мок исполнителя команд агента (предусмотреть интерфейс).
