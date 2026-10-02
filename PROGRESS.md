# PROGRESS — состояние работы

> Обновляется после КАЖДОГО чанка. Правило возобновления: новая сессия начинается
> с чтения этого файла и `git log` — продолжаем с записанного следующего шага.

## Текущая фаза

**Фаза 2 — MVP** (п. 11 ТЗ, шаг 4): сервер (Go) + агент (Go) + UI.
Фаза 1 (архитектура, модель данных, proto, OpenAPI) завершена.

## Следующий шаг (конкретно)

**Деплой 2026-10-01 ~21:55 (Kimi Work)**: собран и задеплоен чанк 27
(HEAD 3fa1beb) — сервер .28 (PID-старт 18:53:38 UTC, health ok,
cron-валидация schedule → 400 подтверждена) и агент .67 (18:56:48,
online, compliance in_sync 1/1, Suricata active). Незавершённый WIP
чанка 28 (auth/RBAC, не собирался: internal/authn, httpapi/auth.go,
store/users.go, миграция 000008) на время деплоя убирался в stash и
возвращён в рабочую копию КАК БЫЛ (git stash pop) — продолжать с него.

**Повторная тихая смерть агента (инцидент №2)**: агент прожил
16:01→~18:40 UTC и умер молча; wrapper start-agent.sh (чанк 23)
НЕ записал exit — процесс умер вместе с обёрткой или запущен был
без неё. Детект чанка 23 сработал: сервер показал offline + compliance
stale (раньше висел бы online). ✅ РЕШЕНО в чанке 28: агент под systemd
(surifleet-agent.service, Restart=always; корень — systemd scope-kill
при закрытии ssh-сессии + ребут ВМ).

**Статус на 2026-10-02**: чанки 23–34 закрыты. Стенд в `auth_mode: token`
(admin@surifleet.local / admin12345), сервер и агент под systemd, UI —
React на `/app/` (`/` — редирект).

**Следующий шаг после 36**:
1. Scoping ролей по кластерам (применение user_roles.scope_type/clusters),
   аудит diff «было→стало» + цепочка хэшей; SAML 2.0 / LDAP (п. 9).
2. Мониторинг (п. 5.4, продолжение): пересылка EVE-алертов в SIEM,
   дашборды флот/кластер/хост.
3. OIDC — e2e на стенде с реальным/mock IdP (не проведён в чанке 35 —
   sandbox сессии блокирует SSH до .28/.67).

Чанк 36 ГОТОВ (2026-10-03, этот коммит): retention телеметрии в ClickHouse
(п. 5.4, продолжение — закрывает «retention agent_metrics» из next-steps).
TTL для `surifleet.agent_logs` и `surifleet.agent_metrics`: параметр
`server.ch_retention_days` (default 30 дней; 0 — бессрочно). `chlogs.
EnsureTable(ctx, retentionDays)` при старте применяет `ALTER TABLE …
MODIFY TTL ts + INTERVAL N DAY` (идемпотентно; CREATE TABLE IF NOT EXISTS
существующие таблицы не обновляет, поэтому TTL — отдельным ALTER;
hэлпер retentionExpr). main.go: EnsureTable(ensureCtx, cfg.CHRetentionDays),
лог + retention_days. deploy/config/server.example.yaml + ch_retention_days.
Юнит-тесты retentionExpr (30/90/0/отрицательное → REMOVE TTL) и
ALTER-запроса по обеим таблицам. Проверки: go build/vet зелёные,
chlogs/config тесты ok. Живой e2e не проводился (sandbox блокирует SSH —
см. статус среды чанка 35): при перекате сервер применит TTL при старте
(в логе «ClickHouse подключён … retention_days=30»); проверить
`SELECT engine_full FROM system.tables WHERE name='agent_metrics'`.

1. Scoping ролей по кластерам (применение user_roles.scope_type/clusters),
   аудит diff «было→стало» + цепочка хэшей; SAML 2.0 / LDAP (п. 9).
2. Мониторинг (п. 5.4, продолжение): retention agent_metrics (TTL в
   ClickHouse), пересылка EVE-алертов в SIEM.
3. OIDC — e2e на стенде с реальным/mock IdP (не проведён в чанке 35 —
   sandbox сессии блокирует SSH до .28/.67).

**Статус на 2026-10-03**: чанк 35 (OIDC-SSO) закрыт по коду и юнит-тестам.
Среда разработки переехала на macOS (arm64): Go 1.27.1 (Homebrew), node 26.
Окружение go — workspace-local через `./goenv.sh` (GOPATH/GOCACHE/GOTMPDIR
в .tools/, т.к. sandbox запрещает запись в ~/go и ~/Library/Caches). SSH до
стенда .28/.67 из этой сессии ЗАБЛОКИРОВАН sandbox (raw socket; HTTP-allowlist
только по hostname, не по IP) — живая верификация на стенде не проводилась,
нужен перекат при первой возможности. Хелперы `.tools/ssh.py`/`.tools/scp.py`
восстановлены (paramiko в `.tools/venv`). Два теста падают ТОЛЬКО из-за
sandbox: feedsync.TestFetchMispFeed (httptest listen loopback запрещён) и
pki.TestLoadOrCreateCA (umask даёт 0755 вместо 0700) — не регрессии.

Чанк 35 ГОТОВ (2026-10-03, 9f7203f): OIDC-SSO (п. 9 ТЗ; Authorization
Code + PKCE) — логин через корпоративный IdP с маппингом групп в роли и
JIT-провижинингом. `internal/oidc` (coreos/go-oidc/v3 + x/oauth2 —
проверенные библиотеки по ТЗ, без самодельной криптографии): flow с PKCE
S256; одноразовые state+nonce (миграция 000009 `oidc_states` + зеркало,
consume атомарно DELETE…RETURNING — защита от CSRF/replay; PKCE-верификатор
выводится из state детерминированно, отдельно не хранится). Проверка
id_token (подпись/issuer/audience/exp/nonce через go-oidc), claims из
id_token + userinfo-fallback (groups/email). Маппинг групп → роли по
`sso_providers.group_role_mapping` (`roleIDsForGroups`, дедуп role_id,
scope=organization). JIT-провижининг (`provision`): по (provider,sub) →
обновление ролей; по email → привязка локального к IdP (LinkExternal,
дедуп); иначе → создание без пароля (CreateExternal); деактивированный →
отказ (ErrUserInactive). Store: `internal/store/sso.go` (SsoProvidersRepo
CRUD, OidcStatesRepo), UsersRepo + GetByExternalID/LinkExternal/
CreateExternal/SetRoles. REST: публичные GET /auth/sso/providers (список
для формы входа), GET /auth/sso/{id}/login (302 на IdP), GET
/auth/sso/callback (проверка → сессия SuriFleet → HTML, кладущая токен в
localStorage → /app/); админ GET/POST /sso_providers + GET/PATCH/DELETE
/sso_providers/{id} (права sso.read/sso.write; client_secret writeOnly —
обнуляется в ответах, пустой в PATCH = «не менять»; валидация URL/role_id).
Каталог разрешений + sso.read/sso.write. Аудит auth.login_sso (success/
denied), sso.create/update/delete. authMiddleware: /api/v1/auth/sso/* —
публичные префиксы. cmd/server: OIDC-сервис в Deps. React: кнопки «Войти
через SSO» на форме входа (публичный список провайдеров), вкладка «SSO»
(CRUD провайдеров, маппинг групп→роли JSON с подсказкой role_id, вкл/откл,
удаление). Юнит-тесты internal/oidc (PKCE-верификатор, groupsFrom,
roleIDsForGroups, oauth2Config scopes). Проверки: go build/vet зелёные,
oidc/httpapi/store/authn тесты ok; npm build чисто (204 КБ, SSO в бандле).
Живой e2e на стенде НЕ проводился (см. статус среды выше) — при перекате:
пересобрать, systemctl restart surifleet-server, миграция → version 9,
создать OIDC-провайдер, пройти flow до сессии в /auth/me.

**Следующий шаг после 34**:
1. OIDC-SSO (п. 9 ТЗ), scoping ролей по кластерам, аудит diff
   «было→стало» + цепочка хэшей.
2. Мониторинг (п. 5.4, продолжение): retention agent_metrics (TTL в
   ClickHouse), пересылка EVE-алертов в SIEM.

Чанк 34 ГОТОВ (2026-10-02, этот коммит): метрики Suricata из eve.json
(п. 5.4, продолжение). Агент `cmd/agent/evemetrics.go`: eveTailer —
чтение eve.json с отслеживанием offset (при уменьшении файла — ротация —
сброс с нуля; предел чтения 16 МБ/тик), разбор stats-событий
(event_type=stats, берётся последнее за тик), извлечение:
suricata.uptime_seconds, capture_kernel_packets, capture_kernel_drops,
decoder_pkts, decoder_bytes, flow_memuse_bytes, detect_alert.
instance_id — из привязок HelloAck по log_dir (suricataInstanceID).
Включено в metrics-горутину (чанк 33): tailer создаётся лениво по
log_dir первого инстанса. Юнит-тесты: TestEveTailer (инкремент, новые
события, усечение-ротация) и TestSuricataInstanceID (trailing slash,
нет совпадения). UI: вкладка «Метрики» теперь динамическая — host.*
закреплены сверху, остальные ряды (suricata.*) рендерятся по факту.
Живой e2e (перекат агента и сервера): в agent_metrics все 7 suricata-рядов
(kernel_packets 31696, drops 0, detect_alert, flow memuse), instance_id
= 468c9c71 (реальный инстанс); API отдаёт все ряды; compliance
in_sync 1/1. Браузер недоступен — только HTTP-проверки.

Чанк 33 ГОТОВ (2026-10-02, a75b5f1): мониторинг — метрики агента
host.* end-to-end (п. 5.4, первый срез). Агент: metrics-горутина в
session.go — MetricsBatch каждые metrics_interval (HelloAck, default
60 с): host.cpu_percent, host.mem_bytes, host.disk_used_percent;
отдельный resourceSampler (CPU% между сэмплами — с heartbeat не делить).
chlogs: таблица surifleet.agent_metrics (MergeTree ORDER BY
(agent_id, name, ts); создаётся в EnsureTable), InsertMetrics,
AgentMetrics (minutes + фильтр имён). hub.handleMetricsBatch →
ClickHouse (best-effort). API: GET /agents/{id}/metrics?minutes=60&
names=a,b (agents.read; валидация minutes 1..1440; ответ {series:
{name: [{ts, value}]}}). React-вкладка «Метрики» (perm agents.read):
селектор агента, окно 15мин..24ч, SVG-спарклайны CPU/память/диск без
библиотек, авто 30 с. Проверки: go build/vet/test зелёные, npm build
чисто; перекат сервера И агента (systemd); живой e2e: через ~100 с в
agent_metrics по 2 точки на имя; API — series с реальными значениями
(cpu 8%, disk 30.7%, mem 548 МБ); bad minutes → 400; бандл содержит
«Метрики агента»; compliance in_sync 1/1. Метрики Suricata (eve) —
следующий срез. Браузер недоступен — только HTTP-проверки.

Чанк 32 ГОТОВ (2026-10-02, 560d7dd): React UI — скрытие пишущих
действий по разрешениям (ТЗ: «UI скрывает недоступные действия»;
авторизация — на backend). `web/src/perms.ts`: PermsContext (дефолт
["*"] — dev-режим) + хук useCan; App.tsx провайдит me.permissions.
Скрыто условным рендером: Rules — вкл/откл (rules.write); Rulesets —
конструктор и сборка (rules.write); Deployments — форма создания и
pause/resume/cancel (rules.deploy; на «Обзоре» таблица без действий —
как было); Iocs — форма/удаление/генерация (ioc.write); Feeds —
форма/sync/enabled/удаление (feeds.write); Users — форма/active/revoke/
удаление (users.write); Roles — создание/удаление кастомных
(roles.write); Tokens — выпуск/отзыв (tokens.write). Instances/
InstanceDetail/Overview/Logs/Matrix/Audit — только чтение, не тронуты.
npm build чисто; перекат .28: бандл index-Dk10DCWs.js отдаётся,
compliance in_sync 1/1, оба systemd-юнита active. Браузер недоступен —
только HTTP-проверки.

Чанк 31 ГОТОВ (2026-10-02, d75acef): ванильный MVP UI выпилен (React —
основной и единственный; в token-режиме ванильный не мог работать —
не шлёт Authorization). Удалены `internal/httpapi/webui.go` и
`internal/httpapi/webui/` (app.js/index.html/style.css), mountWebUI;
`/` → 301 на `/app/` (в reactui.go). Живьём: / → 301 → /app/ 200,
/ui/app.js → 404, compliance in_sync 1/1. Доки: access.md §2/§7,
handover.

Чанк 30 ГОТОВ (2026-10-02, 348a330): React UI — вкладки
«Пользователи», «Роли», «Токены», «Аудит» + видимость вкладок по правам
из /auth/me (ТЗ: UI скрывает недоступное; авторизация — на backend).
Страницы: Users.tsx (список с поиском, создание с ролью из GET /roles и
чекбоксом break-glass, вкл/откл active, revoke_sessions, удаление с
подтверждением), Roles.tsx (builtin/custom бейджи, permissions,
создание кастомной с мультивыбором PERMS, удаление), Tokens.tsx (форма
выпуска с мультивыбором scopes и expires, панель с одноразовым
показом токена + copy, листинг, отзыв), Audit.tsx (таблица
время/актор/action/объект/результат/ip, фильтр по префиксу action,
дозагрузка по 50 через составной курсор). api.ts + типы User/Role/
ApiToken/AuditEntry/PERMS. App.tsx: TABS с perm-полем, фильтр по
permissions ('*' — всё; dev-режим — всё). ПОПУТНЫЙ ФИКС (регрессия
чанка 28): статика UI (/app/, /ui/, /) была защищена токеном — форма
входа не грузилась; authMiddleware теперь применяет auth только к
/api/v1/*. Проверки: npm build чисто (195.77 КБ index-1hOqOPPj.js),
go build/vet/test зелёные; перекат .28: /app/ отдаёт новый бандл
(api_tokens в нём есть), API без токена 401, compliance in_sync 1/1,
агент online. Браузер недоступен — только HTTP-проверки.

Чанк 29 ГОТОВ (2026-10-02, c256cbb): API-токены автоматизации со
scopes (п. 8 ТЗ; таблица api_tokens была с 000001). `internal/store/
tokens.go` — ApiTokensRepo (Create с хэшем, List только активные
keyset, GetValidByHash — revoked/expired отсекаются, Revoke мягкий,
TouchUsed троттлингом раз в минуту). REST (openapi уже специфицировал):
GET /api_tokens (tokens.read), POST /api_tokens (tokens.write; name +
scopes из каталога + expires_at — валидация 400; значение токена в
ответе один раз), DELETE /api_tokens/{id} (tokens.write; отзыв 204, 404).
authMiddleware: заголовок X-API-Key (приоритетнее Bearer) → identity с
Perms=scopes токена и APITokenID; аудит actor_type=api_token
(actor_api_token_id). Каталог + tokens.read/tokens.write (только admin
через '*' и кастомные роли). Аудит tokens.create/tokens.revoke. Живой
e2e: выпуск ci-reader [rules.read] → GET /rules 200 по X-API-Key,
POST /iocs 403, GET /users 403 (authz.denied в аудите от
api-token:ci-reader), мусорный scope 400, expires_at в прошлом 400,
last_used_at проставлен; второй токен: 200 до отзыва → DELETE 204 →
401 после, листинг пуст. Compliance in_sync 1/1, агент online.

Чанк 28 ГОТОВ (2026-10-02, 1ad3764): auth/RBAC — локальные
пользователи + сессии + роли (п. 8–9 ТЗ, без SSO). Миграция 000008:
встроенные роли admin/operator/analyst/viewer с permissions (+зеркало).
`internal/authn`: bcrypt (x/crypto → direct), opaque-токен 32B
base64url, SHA-256-хэш в БД. `internal/store/users.go`: UsersRepo
(Create c ролями в tx, GetByID/ByEmail, List keyset+q+is_active, Update
с заменой ролей, Delete — последний активный break-glass неудаляем
ErrConflict, TouchLogin, CountActiveBreakGlass), RolesRepo (встроенные +
кастомные org; builtin неизменяемы/неудаляемы — ErrConflict;
PermissionsForUser — объединение по назначениям), SessionsRepo (Create/
GetValidByHash/Revoke/RevokeAllForUser), AuditRepo (Log + List keyset
(created_at,id) DESC). `internal/httpapi/auth.go`: Identity (Perms map,
'*'), authMiddleware (dev — прежний X-Dev-User; token — Bearer → сессия
→ user.is_active → права; публичные health/version/login/refresh),
requirePerm на всех маршрутах (каталог fleet/hosts/agents/rules/ioc/
feeds/users/roles/audit .read/.write + rules.deploy; organizations CUD —
admin-only), 401 unauthorized / 403 forbidden (openapi-коды), аудит
отказов authz.denied. Хендлеры: /auth/login (break-glass вход — action
auth.login_break_glass), /auth/refresh (ротация: старый токен отзывается),
/auth/logout (идемпотентный 204), /auth/me; /users CRUD +
revoke_sessions (самоудаление — 409); /roles CRUD кастомных
(permissions валидируются по каталогу); GET /audit_log (audit.read,
фильтр action-префиксом). resolveOrgID в token-режиме — org из identity
(чужой organization_id → 404). deploy:true в /iocs/generate требует
rules.deploy. Конфиг: server.auth_mode (dev|token, default dev),
session_ttl (12h), bootstrap_admin_email/password (пароль лучше env;
пусто → генерируется и один раз WARN в лог); bootstrapBreakGlass в
cmd/server (org First или создаётся Default). React: токен в
localStorage, Authorization во всех вызовах, 401 → сброс + форма входа
(Login.tsx), email + «выйти» в шапке. Попутные фиксы: hub — защита от
дубль-стрима (streamHandle.sessionID; разрыв старой сессии не гасит
агента, Delete только своей сессии) — вскрыто при двух агентах после
миграции на systemd. Юнит-тесты authn (пароль, токены). Проверки:
go build/vet/test зелёные, npm build чисто (185 КБ). Живой e2e на
стенде (token-режим): без токена 401; неверный пароль 401 (в аудите);
login → токен; me → права ['*']; создан analyst (роль analyst): GET
/rules 200, POST /iocs 201, POST /deployments 403, GET /users 403,
GET /audit_log 403 (все отказы в аудите); refresh-ротация (старый 401,
новый 200); logout 204 → токен 401; кастомная роль create/delete 204,
неизвестное разрешение 400, удаление встроенной admin 409; удаление
последнего break-glass 409; revoke_sessions 204; тестовые пользователь
и IOC удалены; GET /audit_log показывает всю цепочку; compliance
in_sync 1/1, агент online. ИНФРА (ночной инцидент 01→02.10): обе ВМ
перезагружались; Redis падал в crash-loop (AOF corrupt после unclean
shutdown — redis-check-aof --fix, данные presence эфемерны); сервер
молча умирал — КОРЕНЬ найден: systemd session-scope SIGTERM при закрытии
ssh-сессии (setsid не выходит из cgroup сессии) — лечение: systemd-юниты
surifleet-server.service (.28) и surifleet-agent.service (.67, root,
ExecStart=start-agent.sh), enable + Restart=always; процедуры переката
обновлены (handover §5 — systemctl restart). Это же вероятная причина
тихой смерти агента 16.09 (запускался из ssh-сессии). Часы .28: chrony
активен и синхронизирован (суб-мс); утренние скачки — boot-коррекция
после выключенной ночи.

Чанк 27 ГОТОВ (2026-10-01, 3fa1beb): cron-расписания авто-синка
фидов. `internal/feedsync/schedule.go`: ParseSchedule — длительность Go
("1h") ИЛИ 5-полевой cron ("*/15 * * * *", дескрипторы @daily и т.п.;
robfig/cron/v3 — новая зависимость; локальное время сервера);
Schedule.Due(lastSyncAt, now): длительность — last+dur<=now; cron —
Next(last)<=now; синка не было → сразу. Планировщик (runFeedScheduler)
переведён на ParseSchedule/Due; фид с исправленным schedule снова
начинает синкаться (delete из badSchedule). Валидация schedule при
POST/PATCH /feeds → 400 с пояснением. Юнит-тесты TestParseSchedule
(длительность/cron/@daily/мусор/6-полей/невалидные значения) и
TestScheduleDue (nil, граница ровно, кратные минуты, @daily).
OpenAPI/UI-подсказка/access.md под факт. Живой e2e: фид generic с
schedule "* * * * *" (mock http.server 8899 на .28) → без ручных синков
два авто-запуска с интервалом в минуту (18:04:37 imported=2, 18:05:34
updated=2), IOC в БД с source=имя фида; невалидный schedule → 400.
Фид удалён, mock остановлен, compliance in_sync 1/1.

Чанк 26 ГОТОВ (2026-10-01, 3b4a15b): коннектор misp — MISP core
format feed. `internal/feedsync/misp.go`: url фида — базовый адрес
фида; GET {url}/manifest.json → события (timestamp строкой или числом —
mispTimestamp), свежие первыми, предел 2000 событий за синк
(mispMaxEvents, warn в лог при обрезке), последовательная загрузка
{uuid}.json. ParseMispEvent: атрибуты с to_ids=false/deleted пропускаются
молча; маппинг ip-src/ip-dst→ip, domain/hostname→domain, url→url,
md5/sha1/sha256, email-src/dst/email→email; составные domain|ip → домен,
ip-*|port → IP, filename|hash → хэш; score по threat_level_id события
(1→80, 2→60, 3→40, прочее→50); MISP Event Object не разбираются (MVP).
Дедуп (type,value) между событиями; ошибки событий не прерывают синк.
Импорт — общий importIocs; автопрогон генерации правил — и для misp.
Юнит-тесты TestParseMispEvent (маппинг, составные, to_ids/deleted, score,
битый JSON) и TestFetchMispFeed (httptest: manifest, timestamp
строка/число, дедуп между событиями). Живой e2e (http.server 8899 на
.28, manifest + 2 события): sync success imported=4/updated=1 (sha256
уже был от chunk24-taxi — upsert по (org,type,value))/skipped=1 (snort —
понятный текст), score 80/60 по threat_level, to_ids=false и deleted
пропущены, rules_created=4, ruleset ioc-current-4d1d5b99; повтор —
imported=0/updated=5 (идемпотентно). Фид удалён (IOC остались), mock
остановлен, compliance in_sync 1/1. Реальный MISP-фид не проверялся
(нет доступа) — только mock по core format.

Чанк 25 ГОТОВ (2026-10-01, bc32381): коннектор stix — статический
STIX 2.x bundle (`{"objects":[...]}`) или голый JSON-массив объектов по
URL, без TAXII-протокола. `ParseStixBody` (taxii.go): bundle/массив/BOM/
мусор; разбор индикаторов — общий ParseStix чанка 24. Загрузка общая
(fetch: 30 с, 32 МБ, Basic/Bearer по credentials). Автопрогон генерации
правил после ручного синка — теперь и для stix (условие в syncFeed:
generic|taxii|stix). Юнит-тест TestParseStixBody (bundle/массив/BOM/
пусто/без objects/битый JSON). OpenAPI/UI-подсказки/access.md под факт.
Живой e2e (http.server 8899 на .28 с bundle: 2 валидных индикатора (ip с
confidence=70, OR domain+url) + revoked + dst_port): sync success
imported=3/skipped=1 (понятный текст про неподдерживаемый паттерн),
score=70 из confidence, rules_created=3, ruleset ioc-current-4acbe140;
повтор imported=0/updated=3 (идемпотентно); битый URL → failed «HTTP
404». Тестовые фиды удалены (IOC остались, feed_id → NULL), http.server
остановлен, compliance in_sync 1/1.

Чанк 24 ГОТОВ (2026-10-01, 248e13c): коннектор taxii — фиды IOC по
TAXII 2.x/STIX. `internal/feedsync/taxii.go`: URL фида — endpoint объектов
коллекции (`.../collections/{id}/objects/`, или коллекция — /objects
добавляется) либо API root (discovery: GET {url}/collections/ → объекты
всех коллекций с can_read != false); пагинация envelope more/next (предел
100 страниц); Accept application/taxii+json;version=2.1; credentials —
"user:pass" → Basic, иначе Bearer (как generic). STIX-разбор (ParseStix):
только indicator (pattern_type stix/пустой; yara/suricata — в skipped с
пояснением), revoked и истёкшие valid_until — молча пропускаются,
confidence (0..100) → score, valid_until → expires_at IOC; из паттерна
извлекаются сравнения lhs='value' (OR/AND-композит → по IOC на сравнение):
ipv4/ipv6-addr:value→ip, domain-name→domain, url→url, email-addr→email,
file:hashes.MD5/'SHA-1'/'SHA-256'→md5/sha1/sha256; дедуп (type,value)
внутри выборки. Импорт IOC вынесен в общий `Syncer.importIocs` (generic +
taxii); автопрогон генерации правил после ручного синка — и для taxii.
Юнит-тесты: TestParseStix (маппинг/confidence/expires/пропуски/дедуп/
ошибки), TestFetchTaxiiObjects на httptest (discovery, can_read=false,
пагинация, Basic-auth, коллекция без /objects, пустой URL). OpenAPI,
React-подсказки, docs/access.md под факт. Живой e2e (mock TAXII python
http.server на .28:8899): фид по API root → sync success imported=4/
skipped=1 (yara — понятный текст), rules_created=3, ruleset
ioc-current-1eb1ceea; IOC в БД с source/feed_id, score=85 из confidence;
повтор — imported=0/updated=4 (идемпотентно); прямая коллекция
(STIX bundle без пагинации) → imported=1 (email), ruleset не изменился
(email не маппится в правило); runs-история корректна. Тестовые фиды
удалены (IOC остались, feed_id → NULL), mock остановлен, compliance
in_sync 1/1. Реальный TAXII-сервер не проверялся (нет доступа) — только
mock по спецификации envelope. Инфра: сборка фронта переведена на
project-local Node.js `.tools/node` (v24.15.0, npm 11) — старый shim
.tools/bin/npm ссылался на npm.cmd рантайма Kimi Desktop
(%KIMI_DESKTOP_RUNTIME_NODE%), недоступный в CLI-сессии; shim переписан
на .tools/node, vite требует node в PATH (команда в handover §4).
Побочно замечено: один GET /iocs?source=... отвечал ~112 с (разово,
сеть/PG) — при повторении смотреть.

Чанк 23 ГОТОВ (2026-10-01, a94087e): детект «тихой» смерти агента +
логирование завершения агента. Сервер: heartbeat в hub теперь троттлингом
(`touchLastSeenMin` = 30 с — разрыв между пульсами ≤ ~60 с при heartbeat
30 с) пишет `last_seen_at` в PG через `AgentsRepo.HeartbeatPulse`; пульс
заодно возвращает online агента, погашенного свипером, но чей стрим жив
(разморозка/заживший TCP — reason heartbeat-resumed в истории; статусы
degraded/updating/error heartbeat не меняет). `AgentsRepo.SweepStaleOnline`
(tx, FOR UPDATE): online + (last_seen_at IS NULL ИЛИ старше порога) →
offline, last_seen_at НЕ перезаписывается (остаётся фактическим моментом
последнего heartbeat), история agent_state_history с reason
heartbeat-timeout + last_seen_at. `hub.Server.SweepOfflineAgents`: свип +
очистка presence в Redis + пересчёт compliance инстансов хоста в stale.
Фоновый свипер `runAgentOfflineSweeper` (роль hub|all) в cmd/server:
`server.offline_sweep_interval` (default 30s), `server.agent_offline_after`
(default 120s — ≥ 2× разрыва пульса; изначально взяли 90s при пульсе 60s →
ложное срабатывание на живом агенте в зазоре 0.02 с, параметры ужесточены).
Конфиги: `deploy/config/server.example.yaml` обновлён; server.yaml на .28
без этих ключей → действуют дефолты. Агент: `deploy/start-agent.sh`
(каноничная копия в репо, развёрнута на .67) — обёртка вместо `exec`:
код выхода/сигнал (kill -l) с UTC-меткой → `data/agent-exit.log`;
процедура переката агента не меняется (kill pgrep -x surifleet-agent →
обёртка пишет exit и завершается). Доки: architecture.md §5.2/§5.4,
FEATURES.md (статусы online/offline). Проверки: go build/vet/test
зелёные, gofmt чисто. Живой e2e (.28/.67): пульс last_seen_at обновляется
при живом агенте (15:59:45→16:00:45); graceful kill → agent-exit.log
«exit_code=0» (SIGTERM обрабатывается агентом чисто); SIGSTOP (заморозка,
стрим выглядит живым) → свипер: WARN «агент помечен offline:
heartbeat-timeout», статус offline, история reason=heartbeat-timeout,
compliance пересчитан; SIGCONT → heartbeat-resumed → online без
переподключения (2 раза); живой агент после ужесточения параметров 2.5+
мин ложно не гасится. Замечена квазианомалия стенда: часы .28 скачут
(см. следующий шаг) — видимые задержки срабатывания свипера (77с–2.5мин
«позже») объясняются wall-шагами часов, не кодом (тикер монотонный,
сравнение last_seen/now идёт в одних часах). Стенд финально: health ok,
агент online, compliance in_sync 1/1, suricata active.

Инцидент 2026-09-16 ~21:25–21:35 (без коммита кода): пользователь создал
деплой 5130b21f (ruleset 05d71563) из UI — задача повисла pending
attempts=0. Диагноз: процесс агента на .67 был мёртв с ~16:28 UTC
(после переката сервера чанка 20; логи обрываются на «discovery
завершён», паники нет), а сервер показывал online — диспетч не
отправлял задачу (агента реально нет в hub), но и offline не
фиксировал. Лечение: перезапуск агента → подхват pending-задач
(фикс чанка 20) сработал — задача succeeded attempts=1 за ~5 с,
деплой completed, compliance in_sync.

Чанк 22 ГОТОВ (2026-09-16, этот коммит): коннектор et_pro — фиды ПРАВИЛ
ET Pro. Тот же .rules-коннектор, что et_open: `syncRules` принимает
sourceType (= тип фида), upsert в rules по (org,sid) с source_type=
'et_pro' + feed_id. Код подписки — из поля credentials фида (просто
код; credentials с ':' — НЕ et_pro формат → failed). `etProURL`:
пустой url → шаблон https://rules.emergingthreatspro.com/<code>/
suricata/rules/etpro-all.rules; явный url — как есть; без credentials —
понятный failed «для et_pro укажите код подписки в поле credentials
(просто код, без user:pass)». Для et_pro auth-заголовок при загрузке
не выставляется (код уже в URL). Создание et_pro-фида с пустым url
разрешено (валидация в createFeed). Миграция 000007: rules.source_type
+ 'et_pro' (+ зеркало internal/store/migrations; применена на .28 —
version 7). GET /rules source-фильтр + et_pro. OpenAPI под факт
(et_pro поддержан, смысл credentials, дефолтный URL). React-вкладка
«Фиды»: подсказки про et_pro (credentials = код, url можно не
задавать — через API). Юнит-тест TestETProURL (нет кода / user:pass →
ошибка; URL из кода; явный URL). Проверки: go build/vet/test зелёные,
npm build чисто (бандл 182.14 КБ index-DlUJlw4j.js). Перекат .28:
health ok, миграции → version 7, CHECK содержит et_pro. Живой e2e:
фид et_pro БЕЗ credentials → sync failed с понятным текстом, last_error
в БД; фид с credentials=test-code и явным url=http://localhost:8899/
feed.rules (http.server на .28, 2 правила + мусор) → sync success,
imported=2/skipped=1, правила 9940001 (under_review) и 9940002
(disabled) с source_type='et_pro' и feed_id; фид без url с
credentials=test-code → запрос ушёл на rules.emergingthreatspro.com →
HTTP 404 (дефолтный URL строится из кода). Реального кода ET Pro нет —
настоящий синк с production-фидом не проверялся. Тестовые фиды удалены
(chunk22-e2e-nocred, chunk22-e2e-pro, chunk22-e2e-defurl); правила
9940001/9940002 оставлены в БД (feed_id → NULL по FK). http.server
8899 остановлен, /tmp/ch22 удалён. Деплоев на инстанс не было,
compliance in_sync 1/1.

**Следующий шаг после 22**: коннекторы taxii/stix/misp и
cron-расписания фидов; либо auth/RBAC (п. 8–9).

Чанк 21 ГОТОВ (2026-09-16, 4b81139): коннектор et_open — фиды ПРАВИЛ
ET Open. Фид type=et_open при sync скачивает .rules-файл и импортирует
правила в репозиторий rules (НЕ в iocs). `internal/feedsync/rulesfeed.go`:
ParseRules (комментарии пропускаются; выключенные ET-правила «#alert ...» —
комментарий без пробела перед action — импортируются со status=disabled;
нераспарсенный «#alert …» — обычный комментарий, не ошибка; continuation
'\\' склеивается; битые строки — в ошибки, разбор не прерывается) +
syncRules (upsert по (org,sid) через Rules.UpsertImport, source_type=
'et_open', feed_id фида). Миграция 000006: rules.source_type + 'et_open'
(+ зеркало internal/store/migrations; применена на .28 — version 6).
store.ImportItem +FeedID/InitialStatus: статус/feed_id фиксируются при
создании (новые активные — under_review, выключенные — disabled); у
существующих правил тюнинг аналитика и первичные source_type/feed_id
импорт НЕ перетирает. Счётчики run: imported/updated/skipped;
«без изменений N» — в error-сообщении (success). Дефолтный URL (пустой
url при создании et_open) — emerging-all.rules; любой .rules-URL
поддержан. et_pro/taxii/stix/misp — по-прежнему failed с пояснением.
Автопрогон генерации IOC-правил после sync — только для generic.
GET /rules source-фильтр + et_open. OpenAPI под факт. Юнит-тесты
ParseRules. Живой e2e: feed.rules (3 активных + 2 выключенных + мусор)
через http.server 8899 на .28 → sync imported=5/skipped=1, правила
et_open с feed_id, статусы under_review/disabled корректны; повтор —
imported=0/updated=0, «без изменений 5», дублей нет; правка msg+rev в
фиде → updated=1, ревизии rev 2→3, статус нетронут; runs-история ok;
дефолтный URL подставляется; et_pro → failed. Существующие 245 ET-правил
(source_type='file') не мигрированы: повторный импорт тех же sid через
фид обновит их по (org,sid) без дублей, source_type останется 'file'
(первичный источник сохраняется — зафиксировано в docs/access.md).
Реальный ET Open URL с .28 доступен (HTTP 200, emerging-dns.rules).
http.server остановлен; тестовый фид chunk21-e2e
(d083db0f-ab37-427f-85b9-f9f9564a6ff4) и его 5 правил (9930011..9930015)
оставлены. Деплоев на инстанс не было, compliance in_sync 1/1.

**Следующий шаг после 21**: коннекторы et_pro/taxii/stix/misp и
cron-расписания фидов; либо auth/RBAC (п. 8–9).

Чанк 20 ГОТОВ (2026-09-16, bee8248): фикс «column reference "status"
is ambiguous» (SQLSTATE 42702) в подхвате pending-задач. Причина:
`internal/store/deploy.go`, PendingTasksForAgent — `SELECT t.`+taskColumns
ставил префикс `t.` только на первую колонку списка, остальные (status
и др.) неоднозначны в JOIN deployments/instances/agents; ошибка стреляла
при каждом переподключении агента (OnAgentOnline → DispatchPending) и
значит при каждом рестарте сервера. Фикс: константа `taskColumnsT` (все
колонки с `t.`, по образцу instanceColumnsI/hostColumnsH); регрессионный
тест `TestTaskColumnsTQualified` (internal/store/deploy_test.go). Остальные
JOIN-запросы (rulesets.go, matrix.go, agents.go, hosts.go, instances.go,
deploy.go:450) проверены — квалификация корректна. Проверки: go build/
vet/test зелёные, gofmt чисто. Перекат .28: до фикса — 5 повторов ошибки
в server.log; после — health ok, переподключение агента без ошибки
(подхват отработал молча — pending-задач не было), исправленный SQL
прогнан напрямую в PG (0 строк, без ошибки), compliance in_sync 1/1.
Полный e2e подхвата с реальной pending-задачей пропущен: требовал бы
деплоя на инстанс 468c9c71 без необходимости.

**Следующий шаг после 20**: коннекторы et_open/et_pro/taxii/stix/misp
и cron-расписания фидов; либо auth/RBAC (п. 8–9).

Чанк 19 ГОТОВ (2026-09-16, 38f00e2): отзыв IOC-правил при
revoke/delete/expire источника. `internal/iocrules/revoke.go`:
RevokeForIoc(ctx, orgID, type, value, RuleStore — минимальный
интерфейс: GetBySid/Update, *store.RulesRepo удовлетворяет) — поиск
правила тем же пробингом слотов sid, что у генератора (свободный слот
→ правила нет дальше; чужой слот → сдвиг), отзыв в status='disabled' +
тег `ioc-revoked`. Пометка в тегах, не в msg: msg — ключ владения
слотом при повторной генерации. disabled обратим: IOC, вернувшийся в
active, снова включит правило при generate. Вызовы: PATCH /iocs/{id}
(status→revoked), DELETE /iocs/{id} (IOC читается до удаления ради
type/value), свип просроченных — фоновый свипер (cmd/server/main.go)
и свип внутри POST /iocs/generate (ответ + счётчик `revoked`).
SweepExpired теперь RETURNING погашенные IOC, IocForGeneration
+OrganizationID (свипер мультиorg-безопасен). Ошибка отзыва не валит
основной запрос (логируется). Юнит-тесты RevokeForIoc на поддельном
RuleStore: disable+тег, идемпотентность, отсутствие правила, пробинг
коллизии, нетронутый msg. OpenAPI: IocGenerateResult + revoked,
описания update_ioc/delete_ioc; docs/access.md — поведение отзыва.
Проверки: go build/vet/test зелёные (4 новых теста iocrules), gofmt
чисто; перекат .28: health ok. Живой e2e: PATCH revoked
(198.51.100.23) → правило 8891280 disabled + [ioc-revoked]; DELETE
(url http://evil.example.com/payload) → 204, правило 8836534 disabled;
повторный generate — оба остаются disabled (не воскресают), ruleset
пересобран без них (ioc-current-1eccbd2c, 7 правил; фактическое
поведение: при изменении состава enabled ioc-правил собирается новая
content-addressed версия). Свип: IOC 203.0.113.99 с expires_at в
прошлом (PATCH) → generate: swept=1, revoked=1, правило 8813164
disabled, ruleset вернулся к ioc-current-1eccbd2c (состав совпал).
Деплоев на инстанс не было, compliance in_sync 1/1. Тестовые IOC
оставлены: 198.51.100.23 revoked, 203.0.113.99 expired (source
chunk19-e2e), их правила disabled — так и зафиксировано.

**Следующий шаг после 19**: коннекторы et_open/et_pro/taxii/stix/misp
и cron-расписания фидов; либо auth/RBAC (п. 8–9).

Чанк 18 ГОТОВ (2026-09-16, 4598d05): фиды IOC — CRUD API + sync +
React-вкладка «Фиды». Store `internal/store/feeds.go` (CRUD, keyset-List
с фильтром type, MarkSync, feed_runs с композитным keyset-курсором
(started_at,id) DESC); IocInput/IocPatch + feed_id (json:"-", проставляет
только синк). Миграция 000005: feeds.last_error + таблица feed_runs
(status/imported/updated/skipped/error) — применена при перекате
(version 5). Пакет `internal/feedsync`: GET (30s, 32 МБ, credentials_ref
"user:pass"→Basic/иначе Bearer), разбор plain text (# // комментарии) /
CSV (value,type | type,value) / JSON (строки или {type,value,score}),
угадывание типа ip/CIDR/domain/url/md5/sha1/sha256/email + лёгкая
валидация; импорт через UpsertImport (source = имя фида, feed_id,
score 50); мусорные строки — skipped, не прерывают; ошибки загрузки —
run failed + last_error, HTTP 200. API: GET/POST /feeds,
GET/PATCH/DELETE /feeds/{id}, POST /feeds/{id}/sync (синхронно; в ответе
FeedRun + rules_created/updated/unchanged + ruleset_version), GET
/feeds/{id}/runs. Синк только type=generic (остальные — понятный
failed). После успешного ручного синка — автопрогон генерации правил
(generateIocRulesCore выделен из хендлера iocs_generate; без деплоя).
Планировщик авто-синка (api|all, `server.feed_sync_interval` default
60s): schedule = длительность Go ("1h"); плановый синк — только импорт,
без автогенерации. OpenAPI приведён под факт (sync 202→200, FeedRun
+skipped/rules_*/ruleset_version, Feed +last_error, schedule —
длительность Go). React: вкладка «Фиды» (таблица, форма, «Синхронизировать»
со строкой результата, enabled-переключатель, удаление; apiPatch в api.ts).
Проверки: npm run build чисто (бандл 181.88 КБ index-CemYYVPX.js),
go build/vet/test зелёные (feedsync — юнит-тесты GuessType/Parse);
перекат .28: health ok, миграции → version 5, планировщик в логе.
Живой e2e: http.server 8899 на .28 с feed-ch18.txt (9 строк: 8 IOC +
мусор) → POST /feeds 201 → sync: imported=7, skipped=1,
rules_created=6 (md5 не маппится), ruleset ioc-current-04161035;
GET /iocs?source=chunk18-e2e — 7 шт с feed_id; повторный sync —
imported=0, updated=7, тот же ruleset (идемпотентно, дублей нет);
битый URL → failed, last_error «загрузка фида: HTTP 404», сервер жив;
PATCH enabled, дубль 409, мусорный url 400, DELETE 204 → 404, runs —
история. Тестовый http.server остановлен; фид chunk18-e2e
(26329b8e-4d9d-4b23-b705-3eeff632457f) и его 7 IOC оставлены в БД.
Браузерный инструмент недоступен субагенту — только HTTP-проверки
(бандл в /app/ содержит «Фиды»/«Синхронизировать»).

**Следующий шаг после 18**: отзыв/отключение IOC-правил при revoke
источника (правило живёт, пока enabled); коннекторы et_open/et_pro/
taxii/stix/misp и cron-расписания фидов; либо auth/RBAC (п. 8–9).

Чанк 17 ГОТОВ (2026-09-16, 9e0694f): генерация Suricata-правил из
IOC + свипер expires. Пакет `internal/iocrules`: sid = 8800000 +
FNV-1a(type,value) % 100000 (диапазон 8800000..8899999; занятые —
ET 2xxxxx, локальные ручные 9000xxx), rev = FormatRev (2; rev:1 был с
nocase на http.host — Suricata 8 считает это ошибкой парсинга: буфер
нормализован в lowercase). Маппинг: ip/CIDR → alert ip; domain →
dns.query content nocase; url → http.host + http.uri; hash-типы и
email — skipped с причиной. Правила — source_type='ioc' (миграция
000004 расширяет CHECK), UpsertImport + принудительный enabled;
коллизии sid — линейный пробинг через GetBySid. POST /iocs/generate
(bulk-реализация спекового /iocs/{id}/deploy): свип просроченных →
генерация → content-addressed ruleset `ioc-current-<sha8>` (имя с
префиксом sha — UNIQUE (org, version) в ruleset_versions не даёт
переиспользовать «ioc-current» при новом составе) → деплой только при
deploy:true с явным targeting. Свипер — горутина (api|all),
`server.ioc_sweep_interval` default 60s; дублируется внутри generate.
Проверки: npm run build чисто (177 КБ js), go build/vet/test зелёные;
перекат .28: health ok, миграции → version 4, свипер в логе. Живой
e2e: 5 IOC (ip/domain/url/email + ip с expires_at в прошлом) →
generate: swept_expired=1, created=3, email skipped → правила
8891280/8890656/8879857 enabled source=ioc; повторная генерация —
unchanged=3, тот же ruleset (идемпотентно); истёкший IOC → expired и
в правила не попал. Деплой e2e: первая попытка упала на suricata -T
агента (nocase на http.host — валидация отработала как задумано,
откат), после исправления формата (rev:2) деплой ioc-current-f021c4ab
completed 1/1, compliance in_sync; стенд возвращён на рабочий ruleset
d44182a6 (деплой d0197c12 completed, in_sync). Браузер недоступен —
только HTTP-проверки.

Проверка UI в браузере (2026-09-16 ~16:50, InAppBrowser, без коммита кода):
React UI /app/ визуально проверен — рендер корректный: шапка
(health ok), все 8 вкладок; «Матрица» — легенда цветов, вертикальный
заголовок TEST1, зелёные ячейки loaded у enabled-правил, пустые у
under_review; «IOC» — форма добавления, фильтры, кнопка «Сгенерировать
правила», бейджи active/expired; «Логи» — селектор агента
(test1 · online), живые записи (watchdog, деплой 245/0). Гигиена
стенда: отменены 5 зависших paused-деплоев от тестов (4e5b2520,
7ad065b6, 951d91bf, dc6a460d, e7ac5424 — все cancel 200), «Обзор»
теперь «Активных деплоев нет», compliance in_sync 1/1.

Чанк 16 ГОТОВ (2026-09-16, 25de9f3): IOC / Threat Intel — вертикальный срез
server→UI. Разведка показала: таблица iocs существовала с миграции
000001 (type/value/score/feed_id/source/status/expires_at, UNIQUE
(org,type,value)), openapi-спека IOC/фидов описана, но store-слоя и
REST не было. Добавлено: `internal/store/iocs.go` (IocsRepo: Create
(дубль → 409), Get, Update, жёсткий Delete, keyset-List с фильтрами
type/status/source/q, UpsertImport — перетирает только score/source/
expires_at, status аналитика нетронут), модели Ioc/IocInput/IocPatch;
`internal/httpapi/iocs.go`: GET /iocs (keyset+фильтры), POST /iocs,
GET/PATCH/DELETE /iocs/{id}, POST /iocs/import (JSON, ошибки по строкам
не прерывают импорт, ответ ImportResult); валидация значения по типу
(ip/CIDR, domain, url, md5/sha1/sha256, email), score 0..100; роуты в
router.go. OpenAPI приведён под факт (source в IocInput/IocUpdateInput,
починен $ref IocPage, из import убран text/csv). React: вкладка «IOC»
(web/src/pages/Iocs.tsx) — форма добавления (тип/значение/score/
источник/истечение), таблица со ссылками VirusTotal, фильтры
тип/статус, поиск по значению, удаление, дозагрузка по 50; api.ts +
apiDelete; badge-стили active/expired/revoked. Проверки: npm run build
чисто (bundle 176 КБ), go build/vet/test зелёные; перекат .28: health
ok. Живой e2e: POST ip 203.0.113.77 → 201 (source=manual, score=80);
GET /iocs — запись есть; q=203.0.113 — находит; GET one — 200;
PATCH score=95 — ок; дубль POST → 409; мусорный ip → 400; import
3 шт → {imported:2, errors:[line 3 md5]}; DELETE → 204, GET → 404;
тестовые IOC вычищены (осталось 0). /app/ отдаёт новый бандл
index-DFOtvsxQ.js (grep «/iocs» и «IOC / Threat Intel» — есть).
НЕ сделано (следующие чанки): фиды (API /feeds), автогенерация правил
из IOC (POST /iocs/{id}/deploy), свипер expires_at→expired. Браузер
недоступен — только HTTP-проверки.

Чанк 15 ГОТОВ (2026-09-16): развитие React UI. Конструктор ruleset'ов
(вкладка «Ruleset'ы»): выбор правил чекбоксами (фильтр по статусу,
поиск по sid/msg, дозагрузка по 50), выбор накапливается между
страницами (Map id→rule), счётчик/чипы/«снять выбор», сборка
POST /rulesets с rule_ids, в ответе различаются 201/200 (apiPostEx).
Страница инстанса (pages/InstanceDetail.tsx): drill-down из списка —
параметры, compliance, desired/actual hash, loaded/failed, last_reload,
failed-правила, diff missing/extra, история деплоев
(/instances/{id}/deploy_history — эндпоинт уже был с чанка 11, новый
server-side НЕ понадобился), логи агента хоста (по host_id). Проверки:
npm run build чисто (171 КБ js), go build/vet/test зелёные; перекат
.28: health ok, /app/ 200, index-C4hUzcSx.js 200, deploy_history
инстанса 468c9c71 → 200 (09200803, 102c688b и др.), state → in_sync
245/0. Живой e2e конструктора через API: 3 sid → 201 ruleset
8ce1b381-c4a9-42f2-b886-89a3f73f7824 (version chunk15-e2e, 3 правила),
повтор → 200 (идемпотентность по содержимому); DELETE /rulesets нет —
ruleset оставлен в БД. Браузер недоступен — только HTTP-проверки.

Чанк 14 ГОТОВ (2026-09-16): React-фронтенд в `web/` (Vite 5 + React 18 +
TS strict, без UI-китов; стили портированы из webui/style.css). Экраны в
паритете с ванильным MVP: Обзор (compliance + активные деплои, авто 15 с),
Инстансы (+state/diff), Правила (фильтр/поиск/пагинация, вкл/откл),
Ruleset'ы (+сборка), Деплои (+задачи, pause/resume/cancel, создание),
Логи (агент, авто 10 с), Матрица (легенда, фильтры, сводка ячеек
страницы, дозагрузка по 50). Улучшение над MVP: вкладки не
размонтируются — состояние фильтров/пагинации живёт между переключениями.
Vite `base=/app/`, dev-proxy /api → 192.168.31.28:8080 (VITE_API_TARGET).
Раздача из бинаря: `web/embed.go` (go:embed dist) +
`internal/httpapi/reactui.go` — /app/* со SPA-fallback на index.html;
если dist не собран — страница-заглушка (go build всегда работает с
чистого клона: закоммичен web/dist/placeholder.txt, postbuild его
восстанавливает после vite build). Старый UI на / не тронут.
Проверки: npm install 68 пакетов (сеть флапает — ECONNRESET лечится
повтором), `npm run build` чисто (tsc --noEmit + vite, bundle
~162 КБ js / ~5 КБ css), `npm run dev -- --host 0.0.0.0 --port 7100`
стартовал (VITE ready 454 мс, остановлен по таймауту, в фоне не
оставлен); go build/vet/test зелёные. Перекат .28: health ok,
/app/ → React index.html (200), /app/assets/*.js 200 text/javascript
163 КБ, *.css 200, SPA-fallback /app/some/route 200, / (старый UI) 200,
/api/v1/fleet/compliance 200 (in_sync:1). Браузер недоступен — только
HTTP-проверки, UI проверить руками при первом открытии.
Нюанс среды: npm в Git Bash вызывается через shim .tools/bin/npm →
npm.cmd рантайма Kimi (системного node нет, node v24.15.0).

Чанк 13d ГОТОВ (2026-09-16): матрица «правила × инстансы» (требование А).
API GET /api/v1/matrix/rules по openapi (RulesMatrix): keyset-курсоры
rule_cursor (по sid, base64url) и instance_cursor (по id) независимы,
фильтры rule_status/category/sid/cluster_id/cell_status, limit ≤1000.
Ячейка из desired_state.computed_rules + actual_state (loaded/failed
последнего StateReport): failed > loaded > missing > extra; вне
контекста инстанса — ячейки нет. store: matrix.go (страницы осей,
батч desired/actual двумя запросами ANY); тесты: sid-курсор round-trip,
cellStatusOf. UI: вкладка «Матрица» (строки sid+msg, столбцы hostname
вертикально, цветные ячейки + легенда, фильтр по статусу, поиск по sid,
дозагрузка по 50). Живой e2e на стенде: ?limit=5 — 200 с ячейками
loaded для инстанса 468c9c71; пагинация по rule_cursor; sid-фильтр;
cell_status=loaded на странице 100 правил — 84 loaded; валидация 400
(bogus cell_status / битые курсоры / sid=abc); кейс missing —
битое правило 9999991: деплой отклонён агентом (suricata -T), desired
обновлён → ячейка missing, сводка 244 loaded + 1 missing; кейс
«вывод из ruleset» — disable 2000596 → ребилд → деплой → ячейка
исчезла (244 loaded). Стенд восстановлен: битое правило удалено,
2000596 включён, деплой 09200803 completed → 245/245 loaded.
UI-проверки только по HTTP (curl /ui/app.js | grep -c matrix = 23,
node --check чисто) — реальный браузер недоступен, вкладку «Матрица»
проверить руками при первом открытии. Известное MVP-ограничение:
cell_status фильтрует ячейки внутри страницы оси правил (при >1000
правил в репозитории подходящие правила могут быть на других страницах).
При перекате подтвердилась старая гонка: kill → старый сервер держит
:8080 до ~10 с (drain gRPC) → новый падает на bind; лечится повторным
запуском, в процедуру переката добавить ожидание порта.

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
| 36 | Retention телеметрии в ClickHouse (п. 5.4): TTL для agent_logs и agent_metrics — `server.ch_retention_days` (default 30 дней; 0 — бессрочно); `chlogs.EnsureTable(ctx, days)` применяет ALTER … MODIFY TTL при старте (идемпотентно); юнит-тесты retentionExpr/ALTER. Живой e2e не проведён (sandbox блокирует SSH) — TTL применится при перекате | (этот коммит) |
| 35 | OIDC-SSO (п. 9, Authorization Code + PKCE): `internal/oidc` (go-oidc/v3 + x/oauth2, PKCE S256, одноразовые state+nonce в oidc_states — миграция 000009; проверка id_token+nonce; claims id_token+userinfo; маппинг групп→роли по group_role_mapping; JIT-провижининг: по (provider,sub)→обновление ролей / по email→LinkExternal дедуп / CreateExternal без пароля; деактивированный→отказ), store `sso.go` (SsoProviders/OidcStates) + UsersRepo (GetByExternalID/LinkExternal/CreateExternal/SetRoles), REST публичные /auth/sso/{providers,login,callback} + админ /sso_providers (sso.read/write, client_secret writeOnly), каталог + sso.read/write, аудит auth.login_sso/sso.*; React кнопка «Войти через SSO» + вкладка «SSO». Живой e2e НЕ проведён (sandbox блокирует SSH до стенда) — проверить при перекате | (этот коммит) |
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
| 13c | Логи агента на сервер и в UI: агент — captureHandler поверх slog → ring buffer 500 (переживает reconnect) → LogBatch каждые 30 с по стриму (`cmd/agent/logship.go`); сервер — `internal/chlogs` (ClickHouse по HTTP; native-DSN clickhouse://host:9900 конвертируется в http :8123), таблица surifleet.agent_logs (CREATE IF NOT EXISTS при старте), запись батчей в hub.handleLogBatch; API: GET /api/v1/agents (список с hostname), GET /api/v1/agents/{id}/logs?limit=200 (≤1000, ts DESC, ts→RFC3339); UI — вкладка «Логи» (селектор агента, лимит, обновить, авто 10 с). Infra-фикс: ClickHouse default без пароля из LAN через маунт zz_allow_network.xml в users.d (entrypoint без кредами сам резал default до localhost → 403). Живой e2e: 30 записей за первый батч (включая warn о разрыве — буфер дождался reconnect), SELECT count()>0, API 200 с записями, app.js содержит logs, go build/vet/test/node --check чисто | 500fb50 |
| 13d | Матрица «правила × инстансы» (требование А): API GET /api/v1/matrix/rules (openapi get_rules_matrix) — независимые keyset-курсоры rule_cursor (по sid) / instance_cursor (по id), фильтры rule_status/category/sid/cluster_id/cell_status, limit ≤1000; ячейка loaded/failed/missing/extra из desired_state.computed_rules + actual_state (loaded/failed StateReport); store — `internal/store/matrix.go` (страницы осей + батч состояний 2 запросами), handler — `internal/httpapi/matrix.go`; тесты sid-курсора и cellStatusOf. UI — вкладка «Матрица» (строки sid+msg, столбцы hostname вертикально, цветные ячейки + легенда, фильтр по статусу ячейки, поиск по sid, дозагрузка по 50). Живой e2e: ?limit=5 → 200 с ячейками loaded для инстанса 468c9c71; пагинация/фильтры/400-валидация; кейс missing живьём (битое правило 9999991 отклонено агентом через suricata -T → desired без actual → missing, сводка 244 loaded + 1 missing); disable+ребилд+деплой → ячейка исчезла; стенд восстановлен (245/245 loaded). Ограничение MVP: cell_status фильтрует ячейки внутри текущей страницы оси правил | d868a9b |
| 14 | React-фронтенд в `web/`: Vite 5 + React 18 + TS strict, без UI-китов (стили из webui/style.css); 7 экранов в паритете с ванильным MVP (Обзор/Инстансы/Правила/Ruleset'ы/Деплои/Логи/Матрица) + улучшение (вкладки не размонтируются — фильтры/пагинация сохраняются, сводка ячеек матрицы). Vite base=/app/, dev-proxy /api → .28:8080. Раздача из бинаря: `web/embed.go` (go:embed dist) + `internal/httpapi/reactui.go` (/app/*, SPA-fallback, заглушка когда dist не собран; placeholder.txt в git, postbuild восстанавливает). Старый UI на / не тронут. Проверки: npm install/build/dev чисто, go build/vet/test зелёные, перекат .28 — /app/ + ассеты + SPA-fallback + /api/v1/fleet/compliance 200; браузер недоступен — только HTTP | 96c777e |
| 15 | Развитие React UI: конструктор ruleset'ов во вкладке «Ruleset'ы» (выбор правил чекбоксами с фильтром/поиском/дозагрузкой, накопление выбора между страницами, чипы/счётчик/снятие, сборка POST /rulesets с rule_ids, различение 201 «создан»/200 «уже существует» через новый apiPostEx); страница инстанса (требование А) — drill-down `pages/InstanceDetail.tsx`: параметры, compliance, desired/actual hash, loaded/failed, last_reload, failed-правила, diff missing/extra, история деплоев (существующий GET /instances/{id}/deploy_history), логи агента хоста. Проверки: npm build + go build/vet/test чисто, перекат .28 (health ok, /app/ 200, новый бандл отдаётся, deploy_history 200 с деплоями 09200803/102c688b), живой e2e конструктора: 3 sid → 201 ruleset 8ce1b381 (chunk15-e2e), повтор → 200 (идемпотентность); браузер недоступен — только HTTP | 0ccd7b2 |
| 34 | Метрики Suricata из eve.json (п. 5.4): агент `evemetrics.go` — eveTailer (offset, устойчив к ротации, ≤16 МБ/тик), stats-события → suricata.uptime/capture_kernel_packets/capture_kernel_drops/decoder_pkts/decoder_bytes/flow_memuse_bytes/detect_alert, instance_id из привязок HelloAck; юнит-тесты tailer/rotation/instance-id. UI «Метрики» — динамические ряды (host.* закреплены, suricata.* по факту). Живой e2e: все 7 рядов в CH, instance_id=468c9c71, API отдаёт, in_sync 1/1 | (этот коммит) |
| 33 | Мониторинг (п. 5.4, первый срез): агент шлёт MetricsBatch каждые 60 с (host.cpu_percent/mem_bytes/disk_used_percent; metrics-горутина в session.go, отдельный sampler); chlogs — таблица surifleet.agent_metrics + InsertMetrics/AgentMetrics; hub.handleMetricsBatch → ClickHouse; API GET /agents/{id}/metrics?minutes=&names= (agents.read); React-вкладка «Метрики» (SVG-спарклайны CPU/память/диск). Живой e2e: точки в CH через ~100 с, API отдаёт реальные значения, bad minutes → 400, compliance in_sync 1/1 | (этот коммит) |
| 31 | Ванильный MVP UI выпилен (`webui.go` + `webui/` удалены, mountWebUI убран), `/` → 301 на `/app/`; живьём: / → /app/ 200, /ui/app.js → 404, in_sync 1/1 | d75acef |
| 32 | React UI — скрытие пишущих действий по разрешениям: `web/src/perms.ts` (PermsContext + useCan), App провайдит me.permissions; скрыты вкл/откл правил (rules.write), конструктор ruleset (rules.write), форма деплоя и pause/resume/cancel (rules.deploy), IOC-форма/генерация (ioc.write), фиды (feeds.write), users/roles/tokens формы (users/roles/tokens.write) | 560d7dd |
| 30 | React UI: вкладки «Пользователи» (список/создание с ролью/active-toggle/revoke/удаление), «Роли» (builtin/custom, мультивыбор permissions), «Токены» (выпуск с одноразовым показом, отзыв), «Аудит» (таблица, фильтр action, дозагрузка); видимость вкладок — по правам /auth/me (hasPerm, '*' — всё). Попутный фикс регрессии чанка 28: статика UI (/app/, /ui/, /) была защищена токеном — authMiddleware теперь только на /api/v1/*. npm build чисто (index-1hOqOPPj.js 195.77 КБ), перекат: бандл отдаётся, API 401 без токена, in_sync 1/1 | 348a330 |
| 29 | API-токены автоматизации со scopes (п. 8): `internal/store/tokens.go` (Create/List активных keyset/GetValidByHash/Revoke мягкий/TouchUsed троттлинг 1 мин), REST /api_tokens (tokens.read/write; значение один раз; валидация scopes/expires 400), X-API-Key в authMiddleware (приоритетнее Bearer, Perms=scopes, APITokenID), аудит actor_type=api_token + tokens.create/revoke, каталог + tokens.read/write. Живой e2e: ci-reader [rules.read] → 200 rules / 403 iocs/users, 400 на мусор, last_used_at, 200 → отзыв 204 → 401; compliance in_sync 1/1 | c256cbb |
| 28 | Auth/RBAC (п. 8–9, без SSO): миграция 000008 (builtin роли admin/operator/analyst/viewer), `internal/authn` (bcrypt, opaque-токен, SHA-256 в БД), store users/roles/sessions/audit репозитории, authMiddleware (dev\|token) + requirePerm на всех маршрутах, /auth/login+refresh(ротация)+logout+me, /users CRUD + revoke_sessions (последний break-glass 409, себя 409), /roles кастомные (builtin 409, perms по каталогу 400), GET /audit_log, аудит auth.*/authz.denied/users.*/roles.*, bootstrapBreakGlass (env/конфиг или генерация пароля в лог), resolveOrgID из identity, React-логин + Bearer во всех вызовах. Попутно: фикс дубль-стрима в hub (sessionID-гард), systemd-юниты сервера/агента (корень тихих смертей — session-scope kill + ребут ВМ), Redis AOF fix после ребута. Живой e2e: 401 без токена, логин, права analyst (403 на deployments/users/audit), ротация, logout, break-glass защита, аудит-цепочка; compliance in_sync 1/1 | 1ad3764 |
| 27 | Cron-расписания авто-синка фидов: `feedsync.ParseSchedule` — длительность Go или 5-полевой cron (robfig/cron/v3, новая зависимость; @daily и др. дескрипторы, локальное время сервера), `Schedule.Due(last_sync_at, now)`; планировщик переведён на него (исправленный schedule снова подхватывается); валидация schedule в POST/PATCH /feeds → 400. Юнит-тесты ParseSchedule/Due. Живой e2e: фид со schedule "* * * * *" → авто-запуски каждую минуту без ручного синка (imported=2 → updated=2), невалидный schedule → 400; compliance in_sync 1/1. Эпик фидов завершён (6 коннекторов + расписания) | 3fa1beb |
| 26 | Коннектор misp — MISP core format feed (`internal/feedsync/misp.go`): manifest.json → события (свежие первыми, ≤2000 за синк, timestamp строка/число), атрибуты to_ids=false/deleted молча skip, маппинг ip-src/dst, domain/hostname, url, md5/sha1/sha256, email-* + составные (domain\|ip, ip-*\|port, filename\|hash), score по threat_level_id (1→80/2→60/3→40); дедуп между событиями, ошибки событий не прерывают; общий importIocs + автопрогон генерации. Юнит-тесты ParseMispEvent/fetch (httptest). Живой e2e (mock на .28): imported=4/updated=1/skipped=1 (snort — понятный текст), score 80/60, rules_created=4; повтор imported=0/updated=5; фид удалён, compliance in_sync 1/1. ВСЕ 6 коннекторов фидов готовы | (этот коммит) |
| 25 | Коннектор stix — статический STIX 2.x bundle/JSON-массив по URL без TAXII-протокола: `ParseStixBody` (bundle/массив/BOM), разбор — общий ParseStix чанка 24, загрузка общая (fetch, Basic/Bearer); автопрогон генерации правил и для stix. Юнит-тест TestParseStixBody. Живой e2e (http.server на .28): imported=3/skipped=1 (неподдерживаемый паттерн — понятный текст), score=70 из confidence, rules_created=3, ruleset ioc-current-4acbe140; повтор imported=0/updated=3; битый URL → failed «HTTP 404»; фиды удалены, compliance in_sync 1/1 | bc32381 |
| 24 | Коннектор taxii — фиды IOC по TAXII 2.x/STIX (`internal/feedsync/taxii.go`): url = API root (discovery /collections/, can_read) или .../collections/{id}/objects, пагинация more/next ≤100 стр, Accept taxii+json;version=2.1, credentials user:pass→Basic/Bearer; ParseStix — только indicator pattern_type stix, revoked/истёкшие valid_until молча skip, confidence→score, valid_until→expires_at, паттерн → сравнения lhs='value' (ipv4/ipv6-addr→ip, domain-name→domain, url→url, email-addr→email, file:hashes→md5/sha1/sha256), дедуп; общий `importIocs` с generic; автопрогон генерации правил и для taxii. Юнит-тесты ParseStix/fetchTaxiiObjects (httptest: discovery, пагинация, Basic). Живой e2e (mock TAXII на .28): API root → imported=4/skipped=1 (yara), rules_created=3, ruleset ioc-current-1eb1ceea; повтор imported=0/updated=4; прямая коллекция → imported=1 (email); фиды удалены, compliance in_sync 1/1. Инфра: node теперь project-local в .tools/node (v24.15.0), shim .tools/bin/npm переписан (старый ссылался на рантайм Kimi Desktop) | 248e13c |
| 23 | Детект «тихой» смерти агента (инцидент 16.09): heartbeat троттлингом (30 с) пишет last_seen_at в PG (`AgentsRepo.HeartbeatPulse`, заодно revive offline→online при живом стриме, reason heartbeat-resumed); свипер offline (роль hub\|all, `server.offline_sweep_interval` 30s / `agent_offline_after` 120s) — `SweepStaleOnline` (last_seen не перетирается, история reason heartbeat-timeout) + `hub.SweepOfflineAgents` (чистка presence в Redis, compliance → stale); диагностика завершения агента — обёртка `deploy/start-agent.sh` логирует exit_code/signal в data/agent-exit.log (развёрнута на .67). Живой e2e: SIGSTOP → offline (heartbeat-timeout в истории), SIGCONT → revive online без реконнекта, живой агент ложно не гаснет; architecture.md §5.2/§5.4 под факт. Заметка: часы ВМ .28 скачут (RTC −5 мин) — тайминги свиперов в тестах трактовать с поправкой | a94087e |
| 20 | Фикс «column reference status is ambiguous» в подхвате pending-задач: в PendingTasksForAgent (`internal/store/deploy.go`) `SELECT t.`+taskColumns квалифицировал только первую колонку — остальные неоднозначны в JOIN deployments/instances/agents; ошибка при каждом (пере)подключении агента (DispatchPending). Добавлена константа taskColumnsT (все колонки с t.) + регрессионный тест TestTaskColumnsTQualified; остальные JOIN-запросы проверены. Перекат .28: ошибка в server.log исчезла (было 5 повторов), health ok, SQL прогнан в PG напрямую, compliance in_sync 1/1 | bee8248 |
| 22 | Коннектор et_pro — фиды ПРАВИЛ ET Pro: тот же .rules-коннектор, что et_open (syncRules + параметр sourceType), код подписки из credentials (просто код, не user:pass — иначе failed с пояснением); пустой url → https://rules.emergingthreatspro.com/<code>/suricata/rules/etpro-all.rules (явный url — как есть); миграция 000007 (rules.source_type + 'et_pro'); создание et_pro-фида с пустым url разрешено; GET /rules source + et_pro; openapi/UI-подсказки под факт; юнит-тест TestETProURL. Живой e2e: без credentials → failed с понятным текстом (last_error в БД); credentials=test-code + явный url (http.server 8899) → imported=2/skipped=1, правила source_type='et_pro' under_review/disabled; без url → запрос на emergingthreatspro.com → 404 (URL из кода). Реального кода ET Pro нет — production-синк не проверен. Тестовые фиды удалены, правила 9940001/9940002 оставлены; compliance in_sync 1/1 | 486ddc3 |
| 21 | Коннектор et_open — фиды ПРАВИЛ ET Open: `internal/feedsync/rulesfeed.go` (ParseRules — активные + выключенные «#alert …» → status=disabled при создании; syncRules — upsert в rules по (org,sid), source_type='et_open' + feed_id); миграция 000006 (rules.source_type + 'et_open'); ImportItem +FeedID/InitialStatus (тюнинг и первичный источник существующих правил не перетираются); дефолтный URL emerging-all.rules; автопрогон IOC-генерации после sync — только generic; GET /rules source + et_open; openapi под факт. Живой e2e: imported=5/skipped=1 → повтор «без изменений 5» без дублей → правка фида updated=1 с новой ревизией; et_pro — failed; compliance in_sync 1/1 | 4b81139 |
| 19 | Отзыв IOC-правил при revoke/delete/expire источника: `internal/iocrules/revoke.go` (RevokeForIoc — пробинг слота sid как у генератора, правило → disabled + тег ioc-revoked, msg нетронут); вызовы из PATCH (status→revoked)/DELETE /iocs/{id} и свипа expires (фоновый свипер + внутри /iocs/generate, ответ + revoked); SweepExpired RETURNING погашенные, IocForGeneration +OrganizationID; юнит-тесты revoke на фейке RuleStore; openapi + revoked/описания. Живой e2e: revoke → правило 8891280 disabled, delete → 8836534 disabled, повторный generate не воскрешает (ruleset ioc-current-1eccbd2c), свип expires → swept=1/revoked=1; compliance in_sync; тестовые IOC оставлены revoked/expired | 38f00e2 |
| 18 | Фиды IOC: store `internal/store/feeds.go` (CRUD/keyset/MarkSync, feed_runs с композитным курсором), миграция 000005 (feeds.last_error + feed_runs), пакет `internal/feedsync` (HTTP GET 30s/32 МБ, plain/CSV/JSON, угадывание типа IOC, импорт через UpsertImport source=имя фида + feed_id), API GET/POST /feeds + GET/PATCH/DELETE /feeds/{id} + POST /feeds/{id}/sync (синхронно, failed — не 5xx) + GET /feeds/{id}/runs; автопрогон генерации правил после ручного синка (generateIocRulesCore, без деплоя); планировщик авто-синка по schedule-длительности Go (`server.feed_sync_interval`); React-вкладка «Фиды» (форма/таблица/синк с результатом/enabled/удаление, apiPatch). Живой e2e: imported=7/skipped=1 → повтор imported=0/updated=7 тот же ruleset; битый URL → failed + last_error; 409/400/404/204; runs-история. Вкладка «Фиды» проверена в браузере (чанк 19) | 4598d05 |
| 16 | IOC / Threat Intel — вертикальный срез: таблица iocs была с миграции 000001, добавлены store (`internal/store/iocs.go` — Create/Get/Update/Delete/keyset-List с фильтрами type/status/source/q, UpsertImport без перетирания status) и REST (`internal/httpapi/iocs.go` — GET/POST /iocs, GET/PATCH/DELETE /iocs/{id}, POST /iocs/import с ImportResult; валидация значения по типу, score 0..100, дубль → 409); openapi приведён под факт (source в IocInput/IocUpdateInput, фикс $ref IocPage, убран text/csv из import). React UI — вкладка «IOC» (форма добавления с expires datetime-local, таблица со ссылками VirusTotal, фильтры тип/статус, поиск, удаление; apiDelete в api.ts). Проверки: npm build чисто, go build/vet/test зелёные; живой e2e на .28 — POST 201 → список/q-поиск → PATCH → дубль 409 → мусор 400 → import {imported:2, errors:[1]} → DELETE 204 → 404; тестовые IOC вычищены. Фиды, автогенерация правил из IOC и свипер expires_at — следующие чанки | 25de9f3 |

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
- Часы ВМ .28: на 01.10 скачки после перезагрузки оказались boot-коррекцией
  chrony (к 02.10 — синхронизирован, offset < 1 мс). Тайминги свиперов в
  тестах того дня трактовались с поправкой — см. чанк 23.
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
