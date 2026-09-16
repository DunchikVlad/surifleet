# Changelog

Формат — [Keep a Changelog](https://keepachangelog.com/ru/1.1.0/).
Компоненты: server / agent / ui / db / api / proto / docs / infra.

## [Unreleased]

### Added

- Чанк 22 (2026-09-16): коннектор et_pro — фиды ПРАВИЛ Emerging Threats
  Pro. Тот же .rules-коннектор, что et_open (`syncRules` теперь
  принимает sourceType = тип фида), но код подписки ET Pro берётся из
  поля `credentials` фида (просто код; формат "user:pass" для et_pro не
  подходит — sync завершится failed с пояснением). Пустой `url` при
  создании et_pro-фида теперь допустим: URL строится при синке по
  шаблону `https://rules.emergingthreatspro.com/<code>/suricata/rules/etpro-all.rules`;
  явно заданный url используется как есть. Для et_pro auth-заголовок
  при загрузке не выставляется (код уже в URL). Миграция 000007
  расширяет CHECK rules.source_type значением 'et_pro' (+зеркало в
  internal/store/migrations; применена на .28 — version 7). Без
  credentials sync — failed «для et_pro укажите код подписки в поле
  credentials (просто код, без user:pass)». GET /rules source-фильтр
  + et_pro. Юнит-тест `TestETProURL` (ошибка без кода и при user:pass,
  построение URL из кода, явный URL как есть). OpenAPI приведён под
  факт (et_pro поддержан, смысл credentials для et_pro, дефолтный URL).
  React-вкладка «Фиды»: подсказки про et_pro (credentials = код
  подписки, URL можно не задавать).

- Чанк 21 (2026-09-16): коннектор et_open — фиды ПРАВИЛ Emerging Threats
  Open. В отличие от generic (IOC), фид type=et_open при sync скачивает
  .rules-файл и импортирует правила в мастер-репозиторий rules (НЕ в iocs).
  `internal/feedsync/rulesfeed.go`: `ParseRules` — разбор .rules-тела
  (комментарии «# ...» пропускаются; выключенные в фиде правила ET
  «#alert ...»/«#drop ...» — комментарий без пробела перед action —
  распознаются и импортируются со status=disabled; если остаток после '#'
  не парсится как правило — это обычный комментарий; continuation-строки
  '\\' склеиваются; битые строки — в ошибки, разбор не прерывается) и
  `syncRules` — upsert по (org, sid) через `Rules.UpsertImport` с
  source_type='et_open' и feed_id фида. Миграция 000006 расширяет CHECK
  rules.source_type значением 'et_open' (+зеркало в
  internal/store/migrations). `store.ImportItem` +FeedID/InitialStatus:
  новое правило создаётся в InitialStatus (по умолчанию under_review,
  для выключенных в фиде — disabled), feed_id фиксируется при создании;
  на существующие правила импорт по-прежнему не трогает тюнинг аналитика
  (status/priority/threshold/tags) и первичные source_type/feed_id.
  Счётчики run для et_open: imported — новые sid, updated — изменившийся
  raw (новая ревизия), skipped — битые строки/ошибки БД; правила без
  изменений в счётчики не входят, их число — в error-сообщении
  («без изменений N; ...»). URL по умолчанию (пустой url при создании
  et_open-фида) — emerging-all.rules; поддерживается любой URL на
  .rules-файл (категории ET, свой файл). et_pro/taxii/stix/misp по-прежнему
  failed с пояснением. Автопрогон генерации IOC-правил после ручного синка
  теперь только для generic (et_open сам импортирует правила). GET /rules
  source-фильтр принимает et_open. OpenAPI приведён под факт. Юнит-тесты
  ParseRules (активные/disabled/комментарии/мусор/continuation/BOM/без
  финального \n). Живой e2e на .28: тестовый feed.rules (5 правил:
  3 активных + 2 выключенных + мусорная строка) через локальный
  http.server → sync: imported=5, skipped=1; правила с source_type=et_open,
  feed_id, активные under_review, выключенные disabled; повторный sync —
  imported=0/updated=0, «без изменений 5», дублей нет; изменение
  msg+rev в фиде → sync: updated=1, новая ревизия rev 3 (rev 2 в истории),
  статус нетронут; дефолтный URL подставляется; et_pro → failed с
  понятным текстом. Существующие 245 ET-правил (source_type='file')
  не мигрированы — повторный импорт тех же sid через фид обновит их
  по (org, sid) без дублей (первичный source_type сохранится). Реальный
  URL ET Open с .28 доступен (HTTP 200, emerging-dns.rules). Деплоев на
  инстанс не было, compliance in_sync 1/1. (server, db, api)

- Чанк 20 (2026-09-16): исправлена ошибка «column reference "status" is
  ambiguous» (SQLSTATE 42702) в подхвате pending-задач. Кореневая причина:
  в `PendingTasksForAgent` (`internal/store/deploy.go`) список колонок
  собирался как `SELECT t.`+taskColumns — префикс `t.` получала только
  ПЕРВАЯ колонка, остальные (status и др.) были неоднозначны в JOIN с
  deployments/instances/agents. Ошибка проявлялась при каждом
  переподключении агента (hub.OnAgentOnline → оркестратор DispatchPending).
  Фикс: новая константа `taskColumnsT` — все колонки с префиксом `t.`
  (по образцу instanceColumnsI/hostColumnsH); регрессионный юнит-тест
  `TestTaskColumnsTQualified` (`internal/store/deploy_test.go`) проверяет,
  что taskColumnsT — это taskColumns с префиксом у каждой колонки. Остальные
  JOIN-запросы проверены — квалификация в порядке. Проверено живьём на .28:
  до фикса — 5 повторов ошибки в server.log при каждом подключении агента;
  после переката — переподключение агента без ошибки (подхват отработал
  молча, pending-задач не было), прямой прогон исправленного SQL в PG — 0
  строк без ошибки; health ok, compliance in_sync 1/1. Полный e2e подхвата
  с реальной pending-задачей пропущен — потребовал бы деплоя на инстанс
  468c9c71. (server, db)

- Чанк 19 (2026-09-16): отзыв IOC-правил при revoke/delete/expire
  источника. `internal/iocrules/revoke.go`: `RevokeForIoc(ctx, orgID,
  type, value, RuleStore)` — поиск правила тем же пробингом слотов sid,
  что и генератор (свободный слот = правила нет), отзыв в
  status='disabled' + тег `ioc-revoked` (msg не меняется — это ключ
  владения слотом при повторной генерации; disabled обратим: вернувшийся
  в active IOC снова включит правило). Вызовы: PATCH /iocs/{id} со
  сменой status→revoked, DELETE /iocs/{id} (IOC читается до удаления —
  нужны type/value), свип просроченных — и фоновый свипер
  (cmd/server/main.go), и свип внутри POST /iocs/generate (в ответе
  новый счётчик `revoked`; SweepExpired теперь возвращает погашенные
  IOC через RETURNING, IocForGeneration +OrganizationID). Ошибка отзыва
  не валит основной запрос (логируется). Юнит-тесты RevokeForIoc на
  поддельном RuleStore: disable+тег, идемпотентность, нет правила,
  пробинг коллизии, msg нетронут. OpenAPI: IocGenerateResult + revoked,
  описания update_ioc/delete_ioc. Живой e2e на .28: PATCH revoked
  (198.51.100.23) → правило 8891280 disabled + [ioc-revoked]; DELETE
  (url evil.example.com/payload) → правило 8836534 disabled; повторный
  generate — оба остаются disabled, ruleset пересобран без них
  (ioc-current-1eccbd2c, 7 правил); свип: IOC с expires_at в прошлом →
  generate: swept=1, revoked=1, правило 8813164 disabled, ruleset
  вернулся к тому же составу. Деплоев не было, compliance in_sync 1/1.
  Тестовые IOC оставлены: 198.51.100.23 revoked, 203.0.113.99 expired.
  (server, api)

- Чанк 18 (2026-09-16): фиды IOC — CRUD API, синхронизация и React-вкладка
  «Фиды». Store `internal/store/feeds.go` (FeedsRepo: Create/Get/Update/
  Delete/keyset-List с фильтром type, MarkSync, feed_runs CreateRun/
  FinishRun/ListRuns с композитным keyset-курсором (started_at,id) DESC);
  IocInput/IocPatch + feed_id (json:"-", проставляет только синк фида,
  UpsertImport его сохраняет). Миграция 000005: `feeds.last_error`,
  таблица `feed_runs` (status running/success/failed, счётчики
  imported/updated/skipped, error). Пакет `internal/feedsync`: HTTP GET
  (таймаут 30 с, лимит 32 МБ, креды из credentials_ref — "user:pass" →
  Basic, иначе Bearer), разбор plain text (# // — комментарии), CSV
  ("value,type" / "type,value") и JSON (массив строк или
  {type,value,score}), угадывание типа (ip/CIDR, domain, url,
  md5/sha1/sha256, email) с лёгкой валидацией; импорт через
  UpsertImport (source = имя фида, feed_id = id фида, score 50);
  ошибки строк не прерывают импорт (skipped + детали в error);
  ошибки загрузки — run failed + last_error фида, HTTP 200 (не 5xx).
  API: GET/POST /feeds, GET/PATCH/DELETE /feeds/{id}, POST
  /feeds/{id}/sync (синхронно; ответ FeedRun + счётчики автопрогона
  rules_created/updated/unchanged + ruleset_version), GET
  /feeds/{id}/runs. После успешного ручного синка — автопрогон
  генерации правил из активных IOC (generateIocRulesCore, выделен из
  хендлера; без деплоя). Фоновый планировщик авто-синка (роль api|all,
  `server.feed_sync_interval` default 60s): enabled-фиды с schedule —
  длительностью Go ("1h", "30m"; cron — следующие чанки), синк при
  last_sync_at + schedule <= now; плановый синк только импортирует IOC
  (без автогенерации правил). Синхронизируются только фиды
  type=generic — остальные типы получают понятный failed-запуск.
  OpenAPI: sync 202 → 200 с расширенным описанием, FeedRun дополнен
  skipped/rules_*/ruleset_version, Feed + last_error, schedule —
  «длительность Go». React: вкладка «Фиды» (таблица с
  enabled-переключателем, last sync/статус/ошибка, форма добавления,
  кнопка «Синхронизировать» со строкой результата imported/updated/
  skipped + итог автогенерации правил, удаление с confirm; apiPatch в
  api.ts). Живой e2e на .28: тестовый фид (python3 http.server 8899,
  9 строк: 8 IOC + мусор) → sync: imported=7, skipped=1,
  rules_created=6 (md5 не маппится — skipped в генерации), ruleset
  ioc-current-04161035; повторный sync — imported=0, updated=7, тот же
  ruleset (идемпотентно, без дублей); фид с битым URL → failed,
  last_error «загрузка фида: HTTP 404», сервер жив; PATCH enabled,
  дубль имени 409, мусорный url 400, DELETE 204 → 404; GET
  /feeds/{id}/runs — история обоих запусков свежими первыми.
  Тестовый http.server остановлен; фид chunk18-e2e
  (26329b8e-4d9d-4b23-b705-3eeff632457f) и его 7 IOC оставлены в БД.
  (server, api, db, ui)

- Чанк 17 (2026-09-16): генерация Suricata-правил из IOC + свипер
  expires. Новый пакет `internal/iocrules` — детерминированная
  генерация: sid = 8800000 + FNV-1a(type, value) % 100000 (диапазон
  8800000..8899999, не пересекается с ET 2xxxxx и локальными 9000xxx),
  rev = FormatRev (версия формата генератора — ключ новой ревизии при
  изменении шаблона). Маппинг: ip/CIDR → `alert ip`, domain →
  `dns.query; content` (nocase), url → `http.host` + `http.uri`
  (nocase снят с http.host — Suricata 8 нормализует буфер и считает
  nocase ошибкой парсинга); md5/sha1/sha256 (нужен файл хэшей) и email
  пропускаются с причиной в `skipped[]`. Правила пишутся в репозиторий
  как source_type='ioc' (миграция 000004 расширяет CHECK source_type)
  через UpsertImport и сразу enabled; хэш-коллизии sid разрешаются
  линейным пробингом (GetBySid). Эндпоинт `POST /api/v1/iocs/generate`
  (`internal/httpapi/iocs_generate.go`, bulk-вариант спекового
  POST /iocs/{id}/deploy): перед генерацией — свип просроченных IOC,
  затем генерация, затем сборка content-addressed ruleset
  `ioc-current-<sha8>` (суффикс sha256, т.к. version уникальна в
  пределах орг) и, только при `deploy: true` с явным `targeting`, —
  деплой через общий волновой конвейер. Ответ: swept_expired/active/
  created/updated/unchanged/skipped + ruleset_id/version/created +
  deployment_id. Свипер expires — фоновая горутина сервера
  (роль api|all), интервал `server.ioc_sweep_interval` (default 60s,
  0 — выкл): active с expires_at < now() → expired; тот же свип
  выполняется внутри /iocs/generate. Фильтр `source` в GET /rules и
  /rules/bulk допускает `ioc`. OpenAPI: /iocs/{id}/deploy заменён на
  /iocs/generate (IocGenerateInput/IocGenerateResult), enum source
  дополнен ioc. React: во вкладке «IOC» кнопка «Сгенерировать правила»
  с отчётом о результате (api.ts + IocGenerateResult, стиль
  .btn.primary). (server, api, db, ui)

- Чанк 16 (2026-09-16): IOC / Threat Intel — вертикальный срез.
  Таблица `iocs` уже существовала (миграция 000001), добавлен слой
  доступа и API: `internal/store/iocs.go` (IocsRepo — Create с
  конфликтом 409 по (org, type, value), Get, Update, жёсткий Delete,
  keyset-List с фильтрами type/status/source/q, UpsertImport — импорт
  обновляет score/source/expires_at, status аналитика не перетирается),
  модели Ioc/IocInput/IocPatch в `internal/store/model.go`;
  `internal/httpapi/iocs.go` — GET /iocs (keyset, фильтры),
  POST /iocs, GET/PATCH/DELETE /iocs/{id}, POST /iocs/import
  (JSON-массив, ошибочные элементы не прерывают импорт, ответ
  ImportResult). Валидация значений по типу (ip/CIDR, domain, url,
  md5/sha1/sha256 hex, email), score 0..100. OpenAPI приведён под факт:
  IocInput/IocUpdateInput дополнены `source`, исправлен битый $ref в
  IocPage, из /iocs/import убран неподдерживаемый text/csv.
  React UI — вкладка «IOC» (`web/src/pages/Iocs.tsx`): форма добавления
  (тип, значение, score, источник, истечение datetime-local), таблица
  (тип, значение + ссылка VirusTotal, score, статус, источник, сроки),
  фильтры по типу/статусу, поиск по значению, удаление, дозагрузка;
  в api.ts добавлен `apiDelete`. Проверено живьём на .28: POST 201 →
  GET/q-поиск находит → PATCH → дубль 409 → мусор 400 → import
  (2 imported + 1 ошибка по строке) → DELETE 204 → GET 404. (server, api, ui)

- Чанк 15 (2026-09-16): развитие React UI. Конструктор ruleset'ов во
  вкладке «Ruleset'ы»: список правил с чекбоксами (фильтр по статусу,
  поиск по sid/msg, keyset-дозагрузка по 50), выбранные правила
  накапливаются между страницами/поиском (счётчик, чипы со снятием,
  «снять выбор»), сборка `POST /rulesets` с явными `rule_ids`
  (приоритет над rule_filter); различает 201 «создан» / 200 «уже
  существует» (идемпотентность по содержимому) — в api.ts добавлен
  `apiPostEx` со статусом ответа. Страница инстанса (требование А):
  drill-down из списка инстансов (`pages/InstanceDetail.tsx`) —
  параметры, compliance, desired/actual хэши ruleset, loaded/failed,
  последний reload, failed-правила (sid/rev/ошибка), diff
  missing/extra, история деплоев инстанса
  (`GET /instances/{id}/deploy_history` — существующий эндпоинт чанка
  11, новый server-side не потребовался), последние 50 записей логов
  агента хоста (агент ищется по host_id инстанса). Проверено:
  `npm run build` чисто (tsc + vite, bundle 171 КБ), go
  build/vet/test зелёные, перекат на .28 — health ok, /app/ 200,
  новый js-бандл отдаётся, deploy_history инстанса 468c9c71 — 200
  (деплои 09200803, 102c688b и др.), e2e конструктора через API:
  3 sid → 201 ruleset 8ce1b381 (chunk15-e2e, 3 правила), повтор →
  200 (идемпотентность); DELETE /rulesets в API нет — тестовый
  ruleset оставлен. Браузер недоступен — только HTTP-проверки. (ui)

- Чанк 14 (2026-09-16): React-фронтенд в `web/` (Vite + React 18 +
  TypeScript, без UI-китов; стили портированы из ванильного MVP).
  Паритет экранов с MVP + мелкие улучшения: Обзор (compliance-карточки,
  активные деплои, автообновление 15 с), Инстансы (+state/diff по клику),
  Правила (фильтр/поиск/keyset-дозагрузка, вкл/откл с оптимистичным
  обновлением строки), Ruleset'ы (+форма сборки), Деплои (+задачи по
  клику, pause/resume/cancel, форма создания), Логи (селектор агента,
  лимит, авто 10 с), Матрица (цветные ячейки loaded/failed/missing/extra,
  легенда, фильтры, сводка по ячейкам страницы, keyset-дозагрузка).
  Вкладки не размонтируются при переключении — фильтры и пагинация
  сохраняются. Vite: `base=/app/`, dev-proxy `/api` →
  `http://192.168.31.28:8080` (переопределяется `VITE_API_TARGET`).
  Раздача: сервер монтирует `/app/*` (internal/httpapi/reactui.go) из
  `go:embed web/dist` (пакет `web`, embed.go) с SPA-fallback на
  index.html; если dist не собран (закоммичен только
  `web/dist/placeholder.txt`) — `/app/` отдаёт страницу-заглушку, а
  `go build` всегда работает на чистом клоне. Ванильный MVP UI остаётся
  на `/` без изменений. `npm run build` = `tsc --noEmit` + `vite build`;
  `postbuild` восстанавливает placeholder.txt после очистки dist.
  Проверено: npm install/build чисто, dev-сервер стартует (VITE ready,
  Local: http://localhost:7100/app/), go build/vet/test зелёные, перекат
  на .28 — /health ok, /app/ отдаёт React index.html, JS/CSS-ассеты 200,
  SPA-fallback 200, старый UI на / 200, /api/v1/fleet/compliance 200.
  Браузер недоступен — проверки только по HTTP, визуально проверить
  руками при первом открытии. (ui, server)

- Чанк 13d (2026-09-16): матрица «правила × инстансы» (требование А).
  API `GET /api/v1/matrix/rules` (openapi get_rules_matrix): две
  независимые keyset-оси — `rule_cursor` (по sid) и `instance_cursor`
  (по id), фильтры `rule_status` / `category` / `sid` / `cluster_id` /
  `cell_status`, `limit` (default 100, max 1000). Ячейка считается из
  `desired_state.computed_rules` и `actual_state` (loaded_rules /
  failed_rules последнего StateReport): failed > loaded (desired∩actual)
  > missing (desired без actual) > extra (actual вне desired); правило
  вне контекста инстанса — ячейки нет. Слой store: `internal/store/matrix.go`
  (страницы осей + батч-чтение состояний двумя запросами). UI: вкладка
  «Матрица» — строки sid+msg, столбцы-инстансы (hostname вертикально),
  цветные ячейки (loaded зелёный / failed красный / missing жёлтый /
  extra серый), легенда, фильтр по статусу ячейки, поиск по sid,
  дозагрузка правил по 50. Ограничение MVP: `cell_status` фильтрует
  ячейки внутри текущей страницы оси правил, а не подтягивает подходящие
  правила вперёд. Живой e2e: кейс missing (битое правило 9999991 в
  desired после failed-деплоя — агент отклонил его suricata -T до
  перезагрузки движка) и исчезновение ячейки при выводе правила из
  ruleset; стенд восстановлен (245/245 loaded). (server, api, ui)
- Чанк 13c (2026-09-16): доставка логов агента на сервер и просмотр в UI.
  Агент: captureHandler поверх slog складывает записи в ring buffer 500
  (переживает reconnect), shipper шлёт LogBatch каждые 30 с по стриму.
  Hub пишет батчи в ClickHouse (HTTP, `internal/chlogs`, таблица
  `surifleet.agent_logs`, CREATE TABLE IF NOT EXISTS при старте сервера;
  DSN `server.clickhouse_dsn`, native `clickhouse://` конвертируется в
  HTTP 8123). API: `GET /api/v1/agents` (список агентов с hostname),
  `GET /api/v1/agents/{id}/logs?limit=200` (≤1000, ts DESC).
  UI: вкладка «Логи» — выбор агента, лимит, ручное обновление и
  автообновление 10 с. (agent, server, api, ui)
- infra: ClickHouse на стенде — persistent-фикс доступа default без
  пароля из LAN (compose маунтит `zz_allow_network.xml` в users.d;
  официальный entrypoint без кредами сам режет default до localhost,
  из-за чего HTTP был 403). (infra)
- Чанк 13b (2026-09-16): управление из UI. Деплои: создание (выбор
  ruleset+инстанс), pause / resume / cancel кнопками. Правила: вкл/откл
  кнопкой (bulk). Ruleset'ы: сборка из enabled-правил из UI. Живой e2e
  полного цикла: сборка ruleset (идемпотентный возврат 245) → создание
  деплоя → pause → resume → completed (succeeded 1/1); вкл/откл правила
  (bulk affected:1). (ui)
- Чанк 13 (2026-09-16): **MVP Web UI** — одностраничный интерфейс,
  встроенный в серверный бинарь (`internal/httpapi/webui`, go:embed,
  ванильный JS поверх /api/v1): Обзор (карточки compliance флота,
  активные деплои, автообновление 15 с), Инстансы (+ desired/actual state
  и diff по клику), Правила (фильтр по статусу, поиск, пагинация),
  Ruleset'ы, Деплои (прогресс + задачи по клику). Раздаётся с `/` и
  `/ui/*` тем же HTTP-сервером (:8080), auth не требуется (dev).
  Проверено: index/css/js и все используемые эндпоинты — 200 с
  Windows-хоста; `node --check` для app.js. (server, ui)
- Чанк 12c-2 (2026-09-16): watchdog автоотката после успешного деплоя.
  Через 90 с агент проверяет живость движка (systemctl is-active по
  systemd_unit из задачи; fallback — suricatasc uptime); если движок не
  жив — откат managed-файла из бэкапа, systemctl restart, повторная
  проверка. Живой e2e: движок остановлен сразу после деплоя → watchdog
  детектировал, за 6 с откатил ruleset и поднял сервис (is-active=active,
  managed-файл откачен на прежнюю версию). Сервер видит откат косвенно:
  heartbeat + drift compliance. (agent)
- Чанк 12c-1 (2026-09-16): сервер в HelloAck отдаёт агенту привязку к
  зарегистрированным инстансам Suricata (`bound_instances`: instance_id,
  name, config_path, rules_dir, log_dir) — агент знает свои instance_id
  сразу при подключении, не дожидаясь первой задачи. Агент логирует
  привязку и сохраняет её в data_dir/bound_instances.json (0600,
  атомарно). Живой e2e: агент получил instance_id 468c9c71… при Hello.
  (proto, server, agent)
- Чанк 12a (2026-09-16): агент при capability 'rules' полностью берёт
  секцию rule-files под управление — ensureRuleFiles отключает чужие
  источники правил комментарием "# surifleet-disabled:" (идемпотентно,
  с бэкапом yaml), активным остаётся только zz-surifleet-managed.rules.
  Живой e2e: деплой ET-сабсета (245 правил) → succeeded за 6 с,
  loaded=245/failed=0, compliance in_sync; движок ruleset-stats
  245 loaded / 0 failed. Закрыта причина Duplicate signature; ошибка
  sid 2045706 подтверждена как каскад от дубликатов. (agent)
- docs/access.md — как подключиться к стенду: карта портов и доступов,
  DevAuth, полная карта эндпоинтов /api/v1 с примерами curl, сквозной
  сценарий «импорт → ruleset → деплой → compliance», статус UI.
  (docs)
- Верификация чанка 11, заход 3 (2026-09-16): **позитивный сценарий пройден
  полностью** — ruleset smoke2 (2 кастомных правила sid 9000020/9000021)
  задеплоен волной на инстанс: задача succeeded (loaded=2, failed=0),
  /instances/{id}/state → compliance in_sync (desired == actual, hash
  4813c308…), /fleet/compliance → in_sync:1, движок ruleset-stats
  52743 loaded / 0 failed. (server, agent)
- POST /rulesets принимает rule_ids — явный список id правил с приоритетом
  над rule_filter; при частичном mismatch → 400 requested/found.
  Поле добавлено в openapi RulesetBuildInput. (api, server)
- Верификация чанка 11, заход 2 (2026-09-15): **негативный сценарий пройден
  полностью** — бэкап suricata.yaml → запись managed-файла → `suricata -T`
  поймал ошибки → автоматический откат (Suricata не пострадала) → TaskResult
  failed с текстами парсера Suricata. Агент переведён на запуск от root
  (тестовый стенд). (agent, server)

### Fixed

- Чанк 12b (2026-09-16): **resume деплоя чинит failed-задачи end-to-end**.
  Сервер: resume теперь допустим и из финального статуса failed (раньше
  только из paused → 409). Агент: failed-результаты больше НЕ пишутся в
  журнал идемпотентности processed_tasks.jsonl — раньше повторная доставка
  проигрывала закэшированный устаревший провал без выполнения (кореневая
  причина «resume не перезапускает failed-задачи»); журналируется только
  succeeded. Живой e2e: деплой 1b8fbcf5 (2 failed-попытки) → resume →
  задача перевыполнена (attempts=3) → succeeded, деплой completed,
  fleet in_sync. (server, agent)
- БАГ: POST /rulesets с rule_ids игнорировал список и собирал ruleset по
  фильтру (245 правил вместо 2). (server)
- Таймаут reload-rules 30 с → 240 с (агент): на нагруженном сенсоре движок
  отвечает на команду только после фактической перезагрузки (~70 с на
  стенде); раньше suricatasc убивался по таймауту, reload при этом
  завершался успешно, но задача уходила в failed. (agent)
- s3 access_key/secret_key/public_endpoint перенесены в
  deploy/config/server.example.yaml (dev-стенд). (infra)
- Presigned URL для скачивания ruleset генерировался с внутренним endpoint
  (localhost:9000) вместо публичного — на стенде добавлен
  `s3.public_endpoint: 192.168.31.28:9000` в server.yaml на .28. (server)

### Known issues (выявлены верификацией, заход 2)

- ~~Деплой подмножества ET Open конфликтует со штатным suricata.rules
  (Duplicate signature)~~ — РЕШЕНО в чанке 12a: агент отключает штатные
  источники правил при capability 'rules'.
- POST /deployments/{id}/resume не перезапускает failed-задачи. (server)

### Added (ранее)

- Чанк 11 (2026-09-14): волновой деплой ruleset (требование А) — **код
  готов, живая проверка не выполнена** (прервано по лимиту шагов; верификация
  — следующий шаг по PROGRESS.md). `internal/blob` — MinIO content-addressed
  блобы (SHA-256, presigned GET). `internal/ruleset` — детерминированный
  рендер ruleset. `internal/orchestrator` — волны (canary + batch),
  Recover при рестарте сервера, DispatchPending при Hello агента,
  auto-pause при провале canary, deploy_events. `internal/hub/tasks.go` —
  in-process реестр стримов и отправка задач. `internal/compliance` —
  статусы in_sync/pending/partial/drift/stale по событиям.
  `cmd/agent/deploy.go` — выполнение DeployRulesTask: скачивание + проверка
  SHA-256, бэкап, атомарная запись, `suricata -T` с откатом, reload-rules
  через unix-сокет, верификация ruleset-failed-rules, журнал task_id
  (идемпотентность). API: /rulesets, /deployments (pause/resume/cancel/
  tasks), /instances/{id}/state, /instances/{id}/deploy_history,
  /fleet/compliance, capabilities. build/vet/gofmt/test зелёные.
  (server, agent, api, db)
- Чанк 10 (2026-09-14): репозиторий правил. `internal/rules` — парсер
  Suricata-правил без зависимостей (~430k правил/с): заголовок 7 полей,
  опции с кавычками/экранированием, continuation-строки, sid/rev/msg/
  classtype/priority/metadata/reference, ошибки по номерам строк без
  остановки импорта. `internal/store` RulesRepo: upsert по (org, sid) в
  транзакции, sha256 raw → imported/updated/unchanged, ревизии
  (keyset по номеру), тюнинг аналитика (status/priority/threshold/tags)
  никогда не перетирается импортом. API /api/v1/rules: import
  (multipart/text, source=file|feed), update CRUD с soft-delete, bulk
  (enable/disable/delete/set_priority/add_tag по ids или фильтру, пустая
  цель → 400), revisions. Живьём: 2000 строк ET Open → 1209 imported,
  повтор идемпотентен, тюнинг пережил реимпорт. (server, api, db)
- Чанк 9 (2026-09-14): Suricata 8.0.3 на сенсоре .67 (apt, сервис
  active/enabled, конфиг на enp0s3, ET Open 45 МБ через suricata-update).
  Миграция 000003 (hosts.discovery jsonb + discovered_at). Агент: discovery
  (бинарь, `suricata --build-info`, suricata.yaml, каталоги правил/логов,
  интерфейсы af-packet→pcap→default route, systemd-юнит — свой лёгкий
  парсер yaml без зависимостей), DiscoveryReport после HelloAck, heartbeat
  с ResourceSummary (/proc) и статусами сервисов инстансов. Сервер:
  сохранение discovery в PG, GET /api/v1/hosts/{id}/discovery, POST
  /api/v1/hosts/{id}/confirm_discovery (идемпотентный upsert в instances),
  полный CRUD /api/v1/instances. Живой e2e: discovery → confirm → инстанс
  с реальными путями (/etc/suricata/suricata.yaml, enp0s3, suricata.service)
  в БД. (agent, server, db, api)
- Чанк 8 (2026-09-14): gRPC Hub + агент end-to-end. Миграция 000002
  (join_tokens). `internal/pki` — встроенный CA (ECDSA P-256, 0700/0600),
  SignCSR (CN=agent_id, 90 дней, clientAuth), серверный сертификат с SAN,
  TLS-конфиги Hub (mTLS) / Enrollment (TLS без client cert). `internal/enroll`
  — Enroll по join token (атомарный расход в tx, создание host+agent,
  AlreadyExists при повторе без расхода токена). `internal/hub` — Channel:
  сверка CN↔agent_id, Hello timeout 10 с, HelloAck (30/300/60, log_level),
  presence Redis (stream:{agent_id} TTL 120 с, продление heartbeat'ами),
  seq replay-защита, clock-skew warn >60 с, статус offline при разрыве,
  agent_state_history при сменах статуса. API: POST/GET
  /api/v1/clusters/{id}/join_tokens (токен показывается один раз, в БД —
  SHA-256 хэш). Агент: enrollment (генерация ключей, CSR, сохранение
  идентичности 0600) + mTLS-стрим + heartbeat-горутина. redis/go-redis/v9
  9.7.3. Живой e2e: enrollment с .67 → heartbeat → online в PG/Redis,
  kill → offline, рестарт → online. (server, agent, db, api, proto)
- Чанк 7 (2026-09-14): сервер — `internal/store` (pgx/v5 пул, миграции
  golang-migrate из embed.FS при старте, флаг --migrate-only, репозитории
  organizations/clusters/hosts с keyset-пагинацией base64-курсором,
  маппинг ошибок PG 23505→409 conflict / 23503→400) и `internal/httpapi`
  (chi-роутер /api/v1: CRUD организаций/кластеров/хостов по openapi.yaml,
  единый формат Error, limit/cursor-пагинация, middleware request-id/
  recover/access-log/DevAuth-заглушка X-Dev-User с TODO на OIDC).
  Зависимости: pgx 5.7.2, golang-migrate 4.18.2, google/uuid 1.6.0.
  build/vet/gofmt/test зелёные; живой CRUD проверен curl-ом с Windows на
  .28 (201/409/400/404/204, next_cursor, /health с checks.postgres).
  Dev-стенд запущен на .28. (server, db, api)
- Чанк 6 (2026-09-14): тестовое окружение на 192.168.31.28 — Docker 29.1.3 +
  Compose v2.40.3 (apt, пользователь test в группе docker);
  `deploy/docker-compose.yml` + `deploy/.env.example`: postgres:16-alpine,
  redis:7-alpine, nats:2.10-alpine (JetStream), clickhouse:24.8-alpine,
  minio (quay.io) + init-контейнер бакета surifleet-rulesets; healthcheck'и,
  named volumes, лимиты памяти (стек ~325 МиБ). Миграция 000001 прогнана
  up/down/up на живом PostgreSQL (43 отношения), append-only триггер
  audit_log проверен ошибкой на UPDATE. Все сервисы доступны с машины
  разработки. (infra, db)
- ТЗ дополнено разделом 13 «Тестовая среда»: 192.168.31.28 — серверный
  контур, 192.168.31.67 — сенсор (Suricata + агент); SSH test/test (+sudo);
  оба хоста Ubuntu 26.04.1 LTS x86_64. Доступность проверена (ping, SSH,
  параметры ВМ зафиксированы в PROGRESS.md). SSH-хелпер `.tools/ssh.py`
  (paramiko). (docs, infra)
- Чанк 5 (2026-09-13): каркас Go-монорепозитория — `go.mod`
  (`github.com/surifleet/surifleet`, go 1.22; grpc 1.69.4, protobuf 1.36.5,
  chi 5.2.1, lumberjack 2.2.1, client_golang 1.20.5, yaml 3.0.1);
  сгенерированный код из proto в `internal/gen/agent/v1` (коммитится,
  скрипт `scripts/gen-proto.sh`); `internal/config` (YAML + env-override
  SURIFLEET_*, Validate); `cmd/server` (флаги --config/--role=api|hub|all,
  slog JSON, /api/v1/health + /api/v1/version, /metrics promhttp,
  graceful shutdown); `cmd/agent` (connectLoop с backoff 1s→60s + full
  jitter, lumberjack-ротация логов в файл + stdout); примеры конфигов
  `deploy/config/server.example.yaml` и `agent.example.yaml`.
  go build/vet/gofmt чисто. (server, agent, infra)
- Чанк 4 (2026-09-13): OpenAPI 3.0.3 спецификация REST API —
  `api/openapi/openapi.yaml` (85 путей, 126 операций, 114 схем): auth+SSO,
  флот и онбординг, правила/фиды/IOC, шаблоны/ruleset/волновой деплой,
  сводка соответствия флота и матрица «правила × инстансы», агенты/логи/
  инциденты, конфиг-профили, пользователи/роли/SSO/API-токены/аудит.
  Keyset-пагинация, единый формат ошибки, bearerAuth + apiKeyAuth.
  `docs/api.md` — конвенции (пагинация, идемпотентность, эволюция).
  Валидация redocly lint: 0 errors. (api, docs)
- Чанк 3 (2026-09-13): gRPC-контракт агент↔сервер —
  `api/proto/agent/v1/agent.proto` (сервис AgentChannel с bidi-стримом
  Channel, конверты AgentMessage/ServerMessage с oneof: 8 типов сообщений
  агента, 5 сервера, 7 типов задач, детали TaskResult), `enrollment.proto`
  (unary Enroll по join token + CSR), `docs/protocol.md` (семантика
  seq/session_id, идемпотентность, версионирование, правила эволюции).
  Компиляция проверена protoc 36.1. (proto, docs)
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
