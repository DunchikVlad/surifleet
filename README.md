# SuriFleet — централизованное управление флотом Suricata IDS/IPS

Веб-платформа управления флотом Suricata IDS/IPS от 1 000 до 10 000 агентов.
Аналог IDSTower с двумя ключевыми преимуществами:

- **(А) Actual state в реальном времени** — точный ответ, какие правила реально
  загружены и работают на каждом инстансе Suricata прямо сейчас (desired vs actual,
  drift detection, failed rules с текстом ошибки парсера).
- **(Б) Полная диагностика агентов** — heartbeat в gRPC-стриме, автодетект
  инцидентов, автооткат при падении сервиса после деплоя, централизованные логи
  агентов, диагностический бандл одной кнопкой.

## Стек

| Слой | Технология |
|---|---|
| Backend / агент | Go 1.22+ (собрано на 1.27), REST — chi, агент↔сервер — gRPC bidi stream поверх mTLS |
| Основная БД | PostgreSQL (миграции golang-migrate, доступ pgx, без ORM) |
| Горячее состояние | Redis (presence, кэш actual state, блокировки, rate limit) |
| Шина задач | NATS JetStream (деплой-задачи, события, fan-out) |
| Метрики/телеметрия | ClickHouse (метрики флота, логи агентов) |
| Блобы ruleset'ов | S3-совместимое хранилище (MinIO), content-addressed по хэшу |
| Frontend | React + TypeScript + Tailwind + shadcn/ui, локализация ru/en |
| SSO | OIDC (основной), SAML 2.0, LDAP/AD (опционально) |

## Структура репозитория

```
api/openapi/    OpenAPI-спецификация REST API
api/proto/      gRPC-контракт агент↔сервер (.proto)
cmd/server/     серверный бинарь (API + концентратор стримов)
cmd/agent/      агент (единый статический бинарь)
cmd/fleetsim/   симулятор флота для нагрузочных тестов
db/migrations/  миграции PostgreSQL (golang-migrate)
deploy/         docker-compose, Helm
docs/           архитектура, модель данных, протокол — версионируются с кодом
internal/       общий код сервера и агента
web/            React-фронтенд
```

## Живые документы

- [PROGRESS.md](PROGRESS.md) — текущая фаза, что сделано, следующий шаг.
- [FEATURES.md](FEATURES.md) — реестр функционала: пункт ТЗ → статус → где реализовано.
- [CHANGELOG.md](CHANGELOG.md) — журнал изменений по чанкам (Keep a Changelog).
- [docs/access.md](docs/access.md) — как подключиться к стенду: API, порты, сценарии использования.
- [docs/known-issues.md](docs/known-issues.md) — реестр известных проблем на исправление.

## Требования к разработке

- Go 1.22+ (локальный тулчейн лежит в `.tools/go`, в git не попадает)
- Node.js 20+ для фронтенда
- Docker для окружения оценки (PostgreSQL, Redis, NATS, ClickHouse, MinIO)

Исходное ТЗ: [промт_suricata_manager_go_kimi.md](промт_suricata_manager_go_kimi.md)
