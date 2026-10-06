# Подключение к SuriFleet: API и интерфейсы

> Документ для аналитика/инженера: как подключиться к развёрнутому стенду
> и что через него можно делать. Актуально на 2026-09-16 (после чанка 21).
> Тестовая среда описана в ТЗ п. 13: сервер — 192.168.31.28, сенсор —
> 192.168.31.67 (ssh/sudo: test/test).

## 1. Карта доступов (dev-стенд)

| Что | Адрес | Как подключиться |
|---|---|---|
| **Web UI (MVP)** | `http://192.168.31.28:8080/` | просто открыть в браузере |
| REST API (основной интерфейс) | `http://192.168.31.28:8080/api/v1` | curl / Postman / Insomnia, см. ниже |
| OpenAPI-спецификация | `api/openapi/openapi.yaml` в репозитории | импорт в Postman/Insomnia/Swagger UI |
| Hub (gRPC, mTLS) | `192.168.31.28:8443` | только для агентов (клиентский сертификат) |
| Enrollment (выдача сертификатов) | `192.168.31.28:8444` | только для агентов (join-token) |
| MinIO S3 API (ruleset-блобы) | `http://192.168.31.28:9000` | `surifleet` / `surifleet_dev_minio` |
| MinIO веб-консоль | `http://192.168.31.28:9001` | root-доступ задаётся в `.env` стека (`MINIO_ROOT_USER`/`MINIO_ROOT_PASSWORD`) |
| PostgreSQL | `192.168.31.28:5432` | `surifleet` / `surifleet_dev`, БД `surifleet` |
| Redis | `192.168.31.28:6379` | без пароля (dev) |
| ClickHouse | `192.168.31.28:9900` (native), `http://192.168.31.28:8123` | default без пароля (LAN разрешён маунтом `zz_allow_network.xml` в users.d — чанк 13c); логи агентов: таблица `surifleet.agent_logs` |

## 2. Веб-интерфейс (UI)

**React UI: http://192.168.31.28:8080/app/** (корень `/` — редирект
сюда; ванильный MVP UI выпилен в чанке 31) — SPA на React 18 +
TypeScript (исходники в `web/`, сборка встраивается в бинарь через
go:embed). Вкладки: Обзор, Инстансы, Правила, Ruleset'ы, Деплои, Логи,
Матрица, IOC, Фиды, Пользователи, Роли, Токены, Аудит. Форма входа
(email+пароль → токен сессии); видимость вкладок и кнопок — по правам
пользователя (ТЗ: UI скрывает недоступное; авторизация — на backend);
вкладки не размонтируются при переключении (фильтры/пагинация
сохраняются).

Если бинарь собран без собранного `web/dist`, `/app/` отдаёт
страницу-заглушку с инструкцией.

Вкладки:

- **Обзор** — карточки compliance флота (in_sync / pending / partial /
  drift / stale), активные деплои, автообновление 15 с.
- **Инстансы** — список инстансов; клик по строке — страница инстанса
  (чанк 15, в React UI): параметры (хост, версия, пути, интерфейсы),
  compliance-статус, desired vs actual (версия и хэши ruleset,
  loaded/failed, последний reload), failed-правила с текстом ошибки,
  diff missing/extra, история деплоев инстанса (версия, статус,
  инициатор, время, результат — `GET /instances/{id}/deploy_history`),
  последние логи агента этого хоста.
- **Правила** — репозиторий: фильтр по статусу, поиск по msg/sid,
  дозагрузка страницами, включение/отключение правила кнопкой.
- **Ruleset'ы** — версии ruleset'ов + конструктор (чанк 15, в React UI):
  список правил с чекбоксами (фильтр по статусу, поиск по sid/msg,
  дозагрузка по 50), выбранные sid накапливаются между страницами
  (счётчик, чипы со снятием, «снять выбор»), кнопка «Собрать из
  выбранных» → `POST /rulesets` с явными `rule_ids`; результат
  различает «создан» (201) и «уже существует» (200 — идемпотентность
  по содержимому).
- **Деплои** — создание деплоя (выбор ruleset'а и инстанса), статусы,
  прогресс, задачи по клику; управление: pause / resume (в т.ч. из
  failed) / cancel.
- **Матрица** — «правила × инстансы» (чанк 13d): строки — правила
  (sid + сообщение), столбцы — инстансы (hostname), цветные ячейки
  (loaded — зелёный, failed — красный, missing — жёлтый, extra — серый),
  легенда; фильтр по статусу ячейки, поиск по sid, дозагрузка правил
  по 50 (keyset `rule_cursor`).
- **IOC** (чанк 16, только React UI) — индикаторы компрометации: форма
  добавления (тип ip/domain/url/md5/sha1/sha256/email, значение, score
  0..100, источник, срок жизни), таблица (тип, значение со ссылкой на
  VirusTotal, score, статус, источник, истечение), фильтры по типу и
  статусу, поиск по значению, удаление, дозагрузка по 50.

Проверено на живом стенде: сборка ruleset, создание деплоя,
pause → resume → completed, вкл/откл правила (bulk affected:1),
матрица (loaded/missing-ячейки, фильтры, пагинация).

Auth не требуется (dev-заглушка).

### Локальная разработка React UI

```bash
cd web
npm install
npm run dev          # Vite dev-сервер; /api проксируется на http://192.168.31.28:8080
                     # (цель переопределяется переменной VITE_API_TARGET)
npm run dev -- --host 0.0.0.0 --port 7100   # явный host/port
npm run build        # tsc --noEmit + vite build → web/dist (встраивается в бинарь)
```

Сборка `web/dist` НЕ коммитится (в git только `dist/placeholder.txt`,
чтобы `go build` с go:embed работал на чистом клоне). Для переката на
стенд: `npm run build`, затем кросс-компиляция и деплой сервера по
процедуре из PROGRESS.md — собранный dist попадёт в бинарь автоматически.

Дополнительно — консоль MinIO (порт 9001): бакет `surifleet-rulesets`
с собранными ruleset-блобами (имя объекта = SHA-256 содержимого).

## 3. Аутентификация API

Стенд работает в режиме `server.auth_mode: token` (чанк 28): локальные
пользователи + Bearer-токен сессии. Break-glass администратор стенда:
`admin@surifleet.local` / `admin12345` (создаётся автоматически при старте,
если нет ни одного активного break-glass; сменить пароль — PATCH /users/{id}).

```bash
# вход → токен
TOKEN=$(curl -s -X POST -H "Content-Type: application/json" \
  -d '{"email":"admin@surifleet.local","password":"admin12345"}' \
  http://192.168.31.28:8080/api/v1/auth/login | python -c "import sys,json;print(json.load(sys.stdin)['access_token'])")
curl -H "Authorization: Bearer $TOKEN" http://192.168.31.28:8080/api/v1/agents
```

- Публичные без токена: `/api/v1/health`, `/api/v1/version`,
  `/auth/login`, `/auth/refresh`. Всё остальное — 401 без токена.
- Токен — непрозрачный (opaque), сессия в БД (хэш), TTL `server.session_ttl`
  (default 12h); `/auth/refresh` ротирует токен (старый отзывается),
  `/auth/logout` отзывает сессию.
- RBAC: роли admin (`*`), operator, analyst, viewer (миграция 000008) +
  кастомные (`/roles`); проверка прав — middleware на каждом маршруте
  (каталог: fleet/hosts/agents/rules/ioc/feeds/users/roles/audit .read/.write,
  rules.deploy); отказ — 403 + запись `authz.denied` в аудит.
- Пользователи: CRUD `/users`, `POST /users/{id}/revoke_sessions`
  (принудительный logout); последний break-glass админ неудаляем (409);
  себя удалить нельзя (409). Пароль ≥ 8 символов (bcrypt).
- API-токены автоматизации (чанк 29): `GET/POST /api_tokens`,
  `DELETE /api_tokens/{id}` (права tokens.read/write). Выпуск:
  `{name, scopes (из каталога разрешений), expires_at?}` — значение
  токена показывается один раз в поле `token`. Аутентификация заголовком
  `X-API-Key: <token>` (приоритетнее Bearer); права запроса = scopes
  токена; `last_used_at` обновляется (не чаще раза в минуту); отзыв
  мягкий (revoked_at, из листинга скрываются; аудит actor_type=api_token).
- Аудит: auth.login (break-glass — отдельным action auth.login_break_glass),
  auth.logout/refresh, authz.denied, users.\*, roles.\*, auth.login_sso,
  sso.\*, rules.update, iocs.update, feeds.update — чтение
  `GET /audit_log?action=&limit=&cursor=` (право audit.read).
  Для ВСЕХ PATCH (users/roles/sso — чанк 37; rules/iocs/feeds — чанк 39)
  запись содержит **diff** «было→стало» (только изменённые поля; секреты
  не включаются) — вкладка «Аудит» показывает его сворачиваемым списком.
- **Цепочка хэшей аудита (чанк 38)**: при `server.audit_hash_chain=true`
  каждая запись audit_log связывается `prev_hash→hash` (SHA-256 полей +
  created_at; вставка под advisory-блокировкой БД — без вилок). Проверка
  целостности: `GET /audit_log/verify?limit=N` (право audit.read) —
  пересчёт hash (детект подделки полей) и связность звеньев (детект
  удаления/вставки); кнопка «проверить цепочку» во вкладке «Аудит».
- **Экспорт аудита (чанк 44)**: `GET /audit_log/export?from=<RFC3339>&to=
  <RFC3339>&format=csv|json` (право audit.read; ≤ 50000 строк, файл
  audit-YYYYMMDD-YYYYMMDD.{csv,json}).
- **SSO / OIDC (чанк 35)**: вход через корпоративный IdP (Authorization
  Code + PKCE). Провайдеры настраиваются на вкладке «SSO» (или API
  `/sso_providers`, права sso.read/write): issuer_url, client_id,
  client_secret (writeOnly), redirect_url (`<базовый URL сервера>/api/v1/auth/sso/callback`
  — его же регистрируют в IdP), scopes, маппинг групп IdP → role_id
  (`group_role_mapping: {"<группа IdP>": ["<role_uuid>"]}`). Публичные
  эндпоинты flow (без токена): `GET /auth/sso/providers` (включённые
  OIDC-провайдеры для формы входа), `GET /auth/sso/{id}/login` (302 на IdP),
  `GET /auth/sso/callback` (проверка → сессия → редирект в UI). На форме
  входа появляется кнопка «Войти через <name>». JIT-провижининг: пользователь
  создаётся при первом входе; если локальный пользователь с таким email уже
  есть — привязывается к IdP; роли назначаются по группам из IdP при каждом
  входе. Деактивированный пользователь входа не проходит (отказ).
- **SSO / LDAP/AD (чанк 40)**: bind-аутентификация. Провайдер type=ldap в
  `/sso_providers` (config: `url` ldap/ldaps, `start_tls`, `bind_dn`,
  `bind_password` (writeOnly), `base_dn`, `user_filter` (шаблон с %s →
  username, дефолт AD/POSIX-набор), `username_attr`, `email_attr`,
  `group_attr` (дефолт memberOf)). Вход: `POST /auth/ldap/login`
  `{provider_id, username, password}` (публичный) — ответ как у
  `/auth/login` (`access_token`…); каталог сам проверяет пароль (у нас он
  не хранится). Группы (memberOf → cn) → роли через тот же
  group_role_mapping; JIT — общий с OIDC механизм. Аудит auth.login_ldap.
- **SSO / SAML 2.0 (чанк 41)**: провайдер type=saml в `/sso_providers`
  (config: `idp_metadata_url` или `idp_metadata_xml` (метаданные IdP),
  `sp_entity_id`, `acs_url` (`<базовый URL>/api/v1/auth/saml/acs?provider_id=<uuid>`
  — регистрируют в IdP), `username/email/name/groups_attr` — дефолты
  email/displayName/groups). Эндпоинты: `GET /auth/saml/{id}/metadata` —
  SP-метаданные (импортировать в IdP), `GET /auth/saml/{id}/login` —
  редирект на IdP, `POST /auth/saml/acs` — колбэк (проверка assertion →
  сессия → редирект в UI). SP-ключ самоподписанный, хранится на диске
  (ca_dir/saml-sp, load-or-create 0600) — метаданные SP стабильны между
  рестартами сервера (чанк 42). JIT + маппинг групп → роли — общий
  механизм. Аудит auth.login_saml.
- Режим `auth_mode: dev` (по умолчанию в коде) — прежняя заглушка
  X-Dev-User, все права; только для локальной разработки.
- Ванильный MVP UI на `/` в token-режиме НЕ работает (не шлёт
  Authorization) — основной UI — React на `/app/` (форма входа).

Формат ошибок единый: `{"error": {"code": "...", "message": "...",
"fields": {...}?}}` (коды: validation_failed, unauthorized, forbidden,
not_found, conflict, internal). Пагинация — keyset: параметры `limit`
(1..200, default 50) и `cursor`; в ответе `next_cursor` (null — страниц
больше нет).

## 4. Что можно делать через API (карта эндпоинтов)

Все пути — под префиксом `/api/v1`.

### 4.0 Auth, пользователи, роли, аудит (чанк 28)
- `POST /auth/login` (публичный) — `{email, password}` → `{access_token,
  refresh_token, token_type, expires_in}` (MVP: access = refresh — один
  токен сессии; refresh ротирует). 401 — неверные креды (в аудит).
- `POST /auth/refresh` (публичный) — ротация токена (старый отзывается).
- `POST /auth/logout` — отзыв текущей сессии (идемпотентно, 204).
- `GET /auth/me` — профиль, роли, итоговые разрешения.
- `GET/POST /users`, `GET/PATCH/DELETE /users/{id}`,
  `POST /users/{id}/revoke_sessions` — права users.read/users.write.
  POST: `{email, display_name, password (≥8), is_break_glass?, roles:
  [{role_id, scope_type?, cluster_ids?}]}`; PATCH: display_name/is_active/
  password/roles (полная замена назначений).
- `GET/POST /roles`, `GET/PATCH/DELETE /roles/{id}` — встроенные (admin/
  operator/analyst/viewer) неизменяемы и неудаляемы (409); кастомные —
  `permissions` только из каталога (иначе 400).
- `GET/POST /api_tokens`, `DELETE /api_tokens/{id}` — API-токены
  автоматизации (tokens.read/write; значение — один раз при выпуске;
  auth — X-API-Key, права = scopes).
- `GET /audit_log?action=&limit=&cursor=` — аудит (audit.read), свежие
  первыми, курсор `<RFC3339Nano>,<uuid>`.
- **SSO (чанк 35)**: `GET/POST /sso_providers`, `GET/PATCH/DELETE
  /sso_providers/{id}` — OIDC-провайдеры (права sso.read/sso.write;
  client_secret — writeOnly). Публичный flow: `GET /auth/sso/providers`,
  `GET /auth/sso/{id}/login`, `GET /auth/sso/callback`.

### 4.1 Служебные
- `GET /health` — живость процесса и PostgreSQL.
- `GET /version` — версия и коммит сборки.

### 4.2 Топология флота: Организация → Кластеры → Хосты → Инстансы
- `GET/POST /organizations`, `GET/PATCH/DELETE /organizations/{id}`
- `GET/POST /clusters`, `GET/PATCH/DELETE /clusters/{id}`
- `GET/POST /clusters/{id}/join_tokens` — выпуск одноразового токена для
  подключения агента (в ответе токен виден один раз; `GET` — список
  выданных, без значений).
- `GET/POST /hosts`, `GET/PATCH/DELETE /hosts/{id}`
- `GET /hosts/{id}/discovery` — отчёт агента о хосте (бинарь Suricata,
  версия, конфиг, интерфейсы, юнит systemd).
- `POST /hosts/{id}/confirm_discovery` — подтвердить discovery → создаётся
  инстанс Suricata с реальными путями (идемпотентно).
- `GET/POST /instances`, `GET/PATCH/DELETE /instances/{id}`

### 4.3 Репозиторий правил
- `POST /rules/import` — импорт .rules (multipart или text/plain; параметр
  `source`). Upsert по (org, sid): ответ `{imported, updated, unchanged,
  errors[]}`; ручной тюнинг (status/priority/threshold/tags) импорт не
  затирает.
- `GET /rules` — список с фильтрами (`status`, `category`, `tag`, `source`,
  `sid`, свободный поиск `q`) и пагинацией; удалённые (soft-delete) скрыты.
- `POST /rules`, `GET/PATCH/DELETE /rules/{id}` — CRUD одиночного правила.
- `POST /rules/bulk` — массовые операции (`enable` / `disable` / `delete` /
  `set_priority` / `add_tag`) по `ids` или по фильтру; пустая цель → 400.
- `GET /rules/{id}/revisions` — история версий правила (keyset по номеру
  ревизии).

### 4.4 Ruleset'ы (требование А: desired state)
- `POST /rulesets` — детерминированная сборка ruleset: `version`
  (обязательно), `rule_filter` (по умолчанию — все enabled) **или**
  `rule_ids` (явный список UUID, приоритет над фильтром; частичный
  mismatch → 400 `requested/found`), `note`. Блоб уходит в MinIO
  (content-addressed, SHA-256); повтор той же выборки → 200 с
  существующей версией (идемпотентно).
- `GET /rulesets`, `GET /rulesets/{id}` — список и деталь с manifest
  (состав `[{sid, rev}]`).

### 4.5 Волновой деплой
- `POST /deployments` — запуск: `ruleset_id`, `targeting`
  (`mode`: `all` / `specific_instances` / `specific_hosts` / `clusters` +
  соответствующие `*_ids`), `wave` (`batch_size`, `canary`).
- `GET /deployments`, `GET /deployments/{id}` — статус и progress
  (`total/pending/running/succeeded/failed`, `current_wave`) + лента
  событий деплоя.
- `POST /deployments/{id}/pause` | `/resume` | `/cancel` — управление
  (resume работает из paused и из финального failed: failed-задачи
  переводятся в pending и прогоняются заново — проверено живьём).
- `GET /deployments/{id}/tasks` — задачи по инстансам (status, attempts,
  error, result с `loaded_count` и `ruleset_hash`).

### 4.6 Фактическое состояние и compliance (требование А: actual state)
- `GET /instances/{id}/state` — `desired` (ruleset_version_id, hash) vs
  `actual` (hash, loaded/failed по отчёту агента), `compliance.status`,
  `diff` (missing/extra/failed rules).
- `GET /instances/{id}/deploy_history` — история деплоев инстанса.
- `GET /fleet/compliance` — сводка по флоту: `total_instances` и разбивка
  `by_status` (`in_sync` / `pending` / `partial` / `drift` / `stale`).
- `GET /matrix/rules` — матрица «правила × инстансы» (чанк 13d): оси
  `rules` (sid/msg/status) и `instances` (instance_id/hostname/name) +
  `cells` (sid, instance_id, status = loaded/failed/missing/extra).
  Независимые keyset-курсоры `rule_cursor` (по sid) и `instance_cursor`
  (по id); фильтры `rule_status`, `category`, `sid`, `cluster_id`,
  `cell_status`; `limit` (default 100, max 1000) общий для обеих осей.
  Ячейка строится из desired_state + последнего StateReport агента;
  правило вне целевого/фактического набора инстанса ячейки не имеет.
  Ограничение MVP: `cell_status` фильтрует ячейки текущей страницы оси
  правил (не подтягивает подходящие правила с других страниц).

### 4.7 IOC / Threat Intel (чанк 16)
- `GET /iocs` — список IOC с фильтрами `type` (ip/domain/url/md5/sha1/
  sha256/email), `status` (active/under_review/expired/revoked),
  `source`, свободным поиском `q` (подстрока значения) и keyset-пагинацией.
- `POST /iocs` — ручное создание: `{type, value, score?, source?,
  expires_at?}`; значение валидируется по типу (IP/CIDR, домен, URL,
  hex-хэши, email), score 0..100; дубль (org, type, value) → 409.
- `GET/PATCH/DELETE /iocs/{id}` — карточка, изменение (score/status/
  source/expires_at), жёсткое удаление (204). Смена status на `revoked`
  и удаление отзывают сгенерированное из IOC правило: оно переводится
  в `status=disabled` с тегом `ioc-revoked` (чанк 19; disabled обратим —
  при возврате IOC в active повторный /iocs/generate снова включит
  правило).
- `POST /iocs/import` — массовый импорт JSON `{source?, items:
  [IocInput...]}`; идемпотентно по (org, type, value): новые —
  `imported`, существующие — `updated` (перетираются только score/
  source/expires_at, status аналитика сохраняется); ошибочные элементы
  не прерывают импорт, ответ — ImportResult с ошибками по строкам.
- `POST /iocs/generate` (чанк 17) — генерация Suricata-правил из всех
  активных IOC (bulk-реализация идеи `POST /iocs/{id}/deploy` из спеки).
  Перед генерацией выполняется свип просроченных (active с
  `expires_at` < now() → expired). Правила детерминированные: sid из
  диапазона **8800000..8899999** (= 8800000 + FNV-1a(type, value) %
  100000; не пересекается с ET 2xxxxx и локальными ручными 9000xxx),
  повторная генерация идемпотентна. Маппинг: ip/CIDR → `alert ip`,
  domain → `dns.query; content` (nocase), url → `http.host` +
  `http.uri`; md5/sha1/sha256 (нужен файл хэшей) и email пропускаются
  с причиной в `skipped[]`. Правила попадают в репозиторий с
  `source_type=ioc` и сразу enabled. Из всех enabled ioc-правил
  собирается content-addressed ruleset `ioc-current-<sha8>` (суффикс —
  префикс sha256 состава: version уникальна в пределах организации).
  Деплой на инстансы — только по явному `"deploy": true` с `targeting`
  (общий волновой конвейер POST /deployments). Ответ:
  `{swept_expired, revoked, active, created, updated, unchanged, skipped[],
  ruleset_id, ruleset_version, ruleset_created, rules_count,
  deployment_id?}` (`revoked` — правила, отключённые свипом
  просроченных IOC в этом прогоне, чанк 19).
- Свипер истёкших IOC — фоновая горутина сервера (роль api|all),
  интервал `server.ioc_sweep_interval` (default 60s, 0 — выключен);
  дублируется свипом внутри `/iocs/generate`. Правила погашенных
  (expired) IOC автоматически отзываются в `disabled` + тег
  `ioc-revoked` (чанк 19); повторная генерация отозванное правило не
  включает, пока IOC не вернулся в active.

### 4.8 Фиды IOC и правил (чанки 18, 21, 22, 24–26)
- `GET /feeds` — список фидов с фильтром `type` (et_open/et_pro/taxii/
  stix/misp/generic) и keyset-пагинацией.
- `POST /feeds` — подключение: `{name, type, url, schedule?,
  credentials?, enabled?}`; URL — абсолютный http(s), дубль (org, name)
  → 409. Для `type=et_open` пустой `url` → дефолт
  `https://rules.emergingthreats.net/open/suricata/rules/emerging-all.rules`
  (можно URL отдельной категории ET или свой .rules-файл). Для
  `type=et_pro` пустой `url` допустим — URL строится при синке из кода
  подписки: `https://rules.emergingthreatspro.com/<code>/suricata/rules/etpro-all.rules`.
  `credentials` — writeOnly (в ответах не возвращается;
  "user:pass" → Basic Auth, иначе Bearer-токен при загрузке фида;
  для `et_pro` — код подписки ET Pro, просто код без "user:pass").
- `GET/PATCH/DELETE /feeds/{id}` — карточка, частичное изменение
  (name/url/schedule/credentials/enabled), удаление (204;
  импортированные IOC/правила остаются — feed_id → NULL по FK, история
  feed_runs удаляется каскадом).
- `POST /feeds/{id}/sync` — СИНХРОННАЯ синхронизация: HTTP GET
  (таймаут 30 с, лимит 32 МБ) → разбор → идемпотентный импорт.
  Два коннектора:
  - `type=generic` — IOC-лист (plain text: один IOC на строку,
    `#`/`//` — комментарии; CSV `value,type`/`type,value`; JSON — массив
    строк или `{type,value,score}`; тип угадывается: ip/CIDR, домен, URL,
    md5/sha1/sha256, email) → upsert в iocs по (org, type, value),
    source = имя фида, feed_id = id фида, score 50. После успешного
    импорта — автопрогон генерации правил из IOC (счётчики rules_*,
    ruleset ioc-current-*, БЕЗ деплоя).
  - `type=et_open` (чанк 21) — фид ПРАВИЛ ET Open (.rules-файл) → upsert
    в репозиторий rules по (org, sid) с `source_type=et_open` и feed_id
    фида (НЕ в iocs; автопрогон IOC-генерации не выполняется).
    Выключенные в фиде правила ET («#alert ...» — комментарий без пробела
    перед action) создаются со status=disabled, активные — under_review.
    Тюнинг аналитика (status/priority/threshold/tags) и первичные
    source_type/feed_id уже существующих правил импорт НЕ перетирает —
    поэтому 245 ET-правил начального импорта (source_type='file') при
    синке того же sid обновятся по (org, sid) без дублей, сохранив
    source_type='file'. Счётчики run: imported — новые sid, updated —
    изменившийся raw (новая ревизия), skipped — битые строки; правила без
    изменений в счётчики не входят («без изменений N» — в error при
    success).
  - `type=et_pro` (чанк 22) — тот же коннектор фида правил, что et_open,
    но для ET Pro: код подписки берётся из `credentials` (просто код;
    формат "user:pass" не подходит — будет failed с пояснением). Пустой
    `url` → `https://rules.emergingthreatspro.com/<code>/suricata/rules/etpro-all.rules`;
    явно заданный url используется как есть. Правила — с
    `source_type=et_pro` (миграция 000007). Без credentials sync —
    failed «для et_pro укажите код подписки в поле credentials».
  - `type=taxii` (чанк 24) — TAXII 2.x/STIX: `url` — endpoint объектов
    коллекции (`.../collections/{id}/objects/`, так и коллекция без
    /objects) или API root сервера (тогда discovery:
    `GET {url}/collections/` → объекты всех коллекций с can_read);
    пагинация envelope `more`/`next` (предел 100 страниц);
    `credentials` — "user:pass" (Basic) или токен (Bearer). Разбираются
    только STIX-объекты `indicator` с `pattern_type` = stix (отсутствие —
    тоже stix); revoked и истёкшие по `valid_until` пропускаются молча,
    `valid_until` → `expires_at` IOC, `confidence` (0..100) → score.
    Паттерн разбирается упрощённо — извлекаются сравнения `lhs = 'value'`
    (составной OR/AND-паттерн даёт по IOC на сравнение): ipv4/ipv6-addr
    → ip, domain-name → domain, url → url, email-addr → email,
    file:hashes.MD5/SHA-1/SHA-256 → md5/sha1/sha256. Импорт — общий с
    generic (upsert в iocs, source = имя фида) + автопрогон генерации
    правил.
  - `type=stix` (чанк 25) — статический STIX 2.x bundle (`{"objects":…}`)
    или голый JSON-массив объектов по URL, БЕЗ TAXII-протокола; загрузка
    общая (fetch, Basic/Bearer по credentials), разбор — тот же ParseStix,
    что у taxii.
  - `type=misp` (чанк 26) — MISP core format feed: `url` — базовый адрес
    фида; загружаются `manifest.json` и файлы событий `{uuid}.json`
    (свежие первыми по timestamp манифеста, предел 2000 событий за синк,
    последовательно). Атрибуты с `to_ids=false` или `deleted` пропускаются;
    типы ip-src/ip-dst → ip, domain/hostname → domain, url → url,
    md5/sha1/sha256 → хэши, email-src/email-dst/email → email; составные:
    domain|ip → доменная часть, ip-*|port → IP-часть, filename|md5 →
    хэш-часть. Score — по `threat_level_id` события (1 High→80, 2→60,
    3→40, прочее→50). MISP Event Object не разбираются (MVP).
  Ошибки загрузки/разбора — не 5xx, а `status=failed` + `error` в теле
  (дублируются в `feeds.last_error`); мусорные строки не прерывают импорт.
  Ответ — FeedRun `{status, imported, updated, skipped, error}` плюс для
  IOC-фидов (generic, taxii, stix, misp) итог автопрогона (`rules_created/rules_updated/rules_unchanged`,
  `ruleset_version`).
- `GET /feeds/{id}/runs` — история запусков (feed_runs, миграция
  000005), свежие первыми, keyset по (started_at, id).
- Планировщик авто-синка — фоновая горутина (роль api|all), интервал
  `server.feed_sync_interval` (default 60s, 0 — выключен): enabled-фиды
  с `schedule` (длительность Go "1h"/"30m" — от last_sync_at, или
  5-полевой cron "*/15 * * * *"/@daily — локальное время сервера;
  чанк 27) синкаются при наступлении срока; плановый
  синк только импортирует (автогенерация правил — у ручного синка
  IOC-фида). Работает для всех коннекторов (generic, et_open, et_pro, taxii, stix, misp).
- React UI: вкладка «Фиды» (таблица, форма добавления, кнопка
  «Синхронизировать» со строкой результата, переключатель enabled,
  удаление).

## 5. Сквозной сценарий «от нуля до задеплоенных правил»

Проверен на живом стенде 2026-09-16 (деплой `335b6069`, 245 правил,
in_sync). Все команды — с Windows-хоста или с самого сервера:

```bash
API=http://192.168.31.28:8080/api/v1

# 1. Импорт правил (файл .rules)
curl -X POST "$API/rules/import?source=feed" \
  -H "Content-Type: text/plain" --data-binary @rules.rules

# 2. Сборка ruleset из всех enabled правил (или явным списком rule_ids)
curl -X POST "$API/rulesets" -H "Content-Type: application/json" -d '{
  "version": "1.2",
  "rule_filter": {"status": "enabled", "category": "malware"}
}'
# → запоминаем "id" и "rule_count"

# 3. Деплой на конкретный инстанс
curl -X POST "$API/deployments" -H "Content-Type: application/json" -d '{
  "ruleset_id": "<id из шага 2>",
  "targeting": {"mode": "specific_instances",
                "instance_ids": ["<instance_id>"]},
  "wave": {"batch_size": 10, "canary": false}
}'

# 4. Наблюдение за ходом
curl "$API/deployments/<deployment_id>/tasks"

# 5. Итог: фактическое состояние и compliance
curl "$API/instances/<instance_id>/state"
curl "$API/fleet/compliance"
```

Что при этом происходит на сенсоре (агент, capability `rules`):
скачивание блоба по presigned URL + сверка SHA-256 → перевод секции
`rule-files` под управление SuriFleet (штатный `suricata.rules`
отключается комментарием `# surifleet-disabled:`, yaml бэкапится) →
атомарная запись `/var/lib/suricata/rules/zz-surifleet-managed.rules` →
`suricata -T` (при ошибке — автоматический откат и failed-задача с
текстом парсера) → `reload-rules` через unix-сокет → верификация
`ruleset-failed-rules` → отчёт серверу (hash, loaded/failed) →
пересчёт compliance.

## 6. Подключение нового сенсора (onboarding)

1. `POST /clusters/{id}/join_tokens` → одноразовый токен.
2. На сенсоре: запустить агента с `server_addr`/`enroll_addr` и токеном —
   агент пройдет enrollment (получит mTLS-сертификат с CN=agent_id),
   подключится к Hub, пришлёт DiscoveryReport.
3. `GET /hosts/{id}/discovery` → `POST /hosts/{id}/confirm_discovery` →
   инстанс готов к деплоям.

## 7. Известные ограничения (на 2026-10-03)

- Аутентификация — локальные пользователи + сессионные токены + RBAC
  (чанк 28, стенд в `auth_mode: token`) и **OIDC-SSO** (чанк 35: Authorization
  Code + PKCE, JIT-провижининг, маппинг групп→роли, админ-CRUD провайдеров,
  вкладка «SSO»; живой e2e на стенде ещё не проводился — sandbox сессии
  блокирует SSH). SAML 2.0 / LDAP, scoping ролей по кластерам и аудит-diff/
  цепочка хэшей — следующие чанки. UI — React SPA на `/app/` (форма входа +
  кнопка «Войти через SSO»; `/` — редирект).
- Сервер и агент на стенде — под systemd (surifleet-server.service на
  .28, surifleet-agent.service на .67; enable+Restart=always): переживают
  ребут ВМ; перекат — `systemctl restart` (процедуры — в
  docs/handover-kimi-code.md §5).
- Логи агентов стекаются в ClickHouse (чанк 13c): вкладка «Логи» в UI,
  API `GET /api/v1/agents/{id}/logs?limit=200`.
- Метрики хоста агента и Suricata (чанки 33–34): MetricsBatch 60 с →
  ClickHouse agent_metrics (host.* + suricata.* из eve.json);
  вкладка «Метрики» в UI (спарклайны),
  API `GET /api/v1/agents/{id}/metrics?minutes=60&names=`.
- Пересылка EVE-алертов в SIEM (чанк 45): агент tail eve.json →
  alert-события → syslog (UDP/TCP, формат CEF или сырой JSON) —
  `agent.siem_addr`/`siem_protocol`/`siem_format` в agent.yaml.
- Автооткат при падении сервиса Suricata после деплоя (watchdog) — чанк 12b.
