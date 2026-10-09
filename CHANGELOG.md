# Changelog

Формат — [Keep a Changelog](https://keepachangelog.com/ru/1.1.0/).
Компоненты: server / agent / ui / db / api / proto / docs / infra.

## [Unreleased]

### Added

- Чанк 73 (2026-10-09): ручная ревизия правила (1E п.1, server) —
  POST /rules/{id}/revisions {raw} (rules.write): валидация парсером,
  sid обязан совпадать; AddRevision — revision=max+1, msg/category по
  разбору, тюнинг не тронут, идемпотентно по sha256 (created|
  unchanged); аудит rules.revision. OpenAPI: POST /rules/{id}/
  revisions. (server, api)

- Чанк 72 (2026-10-09): валидация правил (1E п.1, server) — POST
  /rules/validate (rules.read): разбор парсером internal/rules без
  записи в репозиторий; ответ ok/total_lines/rules[] (структурная
  логика) + errors[] (построчно, лимит 50). OpenAPI: путь
  /rules/validate, схемы ParsedRule/LineError. (server, api)

- Чанк 71 (2026-10-09): скачивание ruleset'ов (1E п.2) — GET
  /rulesets/{id}/download (rules.read): content-addressed блоб .rules
  (тот же, что у агентам при деплое), отдача attachment + ETag по
  образцу POST /rules/export. OpenAPI: путь /rulesets/{id}/download.
  UI: кнопка «скачать» у версии во вкладке «Ruleset'ы». (server, ui, api)

- Чанк 70 (2026-10-09): UI доводка 1B — parent_id в форме профиля
  (наследование цепочки кластер→хост→инстанс), волновой деплой из UI:
  кнопка «волна» у версии + настройки (таргетинг all_clusters/
  specific_instances, canary_size, batch_size) → deploy_wave.
  npm build чисто. (ui)

- Чанк 69 (2026-10-09): UI профилей конфигурации — ProfilesPanel на
  вкладке «Конфигурации»: список, создание (scope из списков
  кластеров/хостов/инстансов, подсказка синтаксиса {{var}}),
  предпросмотр рендера с цепочкой наследования, деплой профиля
  (validate_only). npm build чисто. (ui)

- Чанк 68 (2026-10-09): API волнового деплоя конфигураций — POST
  /config_versions/{id}/deploy_wave (config.write): targeting +
  canary/batch → Deployment kind=config → оркестратор (auto-pause при
  провале волны); desired_state не пишется. Аудит configs.deploy_wave.
  OpenAPI: путь deploy_wave, Deployment дополнен kind/
  config_version_id. Стенд выключен — e2e отложен. (server, api)

- Чанк 67 (2026-10-09): волновой деплой конфигураций — backend-
  фундамент. Миграция 000013: deployments.kind (rules|config) +
  config_version_id FK, ruleset_version_id перестал быть NOT NULL.
  Store: Deployment.Kind/ConfigVersionID, Create обновлён.
  Оркестратор: sendDeployTask ветвится по kind — config собирает
  DeployConfigTask из config_versions; HandleTaskResult фиксирует и
  DeployConfig-результаты. API создания config-деплоев — следующий
  чанк. (server, db)

- Чанк 66 (2026-10-09): деплой отрендеренного профиля —
  POST /config_profiles/{id}/deploy {instance_id, validate_only}
  (config.write): рендер → версия конфигурации (content-addressed,
  авто cfg-v<N>) → штатная задача deploy_config агенту; 202 +
  profile_id, аудит config_profiles.deploy. Рефакторинг configs.go:
  общие helpers storeConfigVersion (идемпотентность 200/201
  сохранена) и dispatchDeployConfigTask. OpenAPI: путь
  /config_profiles/{id}/deploy. Стенд выключен — e2e отложен.
  (server, api)

- Чанк 65 (2026-10-09): рендер профилей конфигурации —
  deep-мерж цепочки наследования + подстановка переменных
  `{{var}}`. Новый пакет `internal/cfgrender` (чистая функция,
  строгий режим: неизвестные переменные → ошибка со списком;
  значения — top-level `vars:` профилей цепочки и встроенные
  факты цели instance.*/host.*/cluster.*; полное совпадение строки
  с плейсхолдером → типизированное значение). Store:
  ConfigProfilesRepo.Chain (root→tip, ErrCycle) + ErrCycle → 409.
  API: GET /config_profiles/{id}/render?target=<instance_id>
  (config.read, RenderResult по спеке). Юнит-тесты рендера.
  Стенд выключен — живой e2e отложен до переката. (server, api)

- Чанк 63 (2026-10-09): OpenAPI приведён под факт чанков 54–62 —
  /config_versions (list/create/get/content/deploy 202+validate_only),
  /instances/{id}/config/current (X-Config-Sha256), /instances/{id}/
  config/history, /clusters/{id}/capabilities; схемы ConfigVersion,
  ConfigDeploy, CapabilitiesSet (GET /hosts/{id}/capabilities с фаза-1
  CapabilitiesView → CapabilitiesSet). (api)

- Чанк 62 (2026-10-09): SetCapabilitiesTask — живое применение
  capability без рестарта стрима. Агент: applyCapabilities (набор под
  mu, чтения hasCap). Сервер: после PUT capabilities эффективный набор
  пушится подключённым агентам (pushCapabilities/
  pushClusterCapabilities); офлайн-агенты — при следующем Hello.
  (agent, server, api)

- Чанк 61 (2026-10-09): UI capability хоста — панель «Capability хоста»
  на вкладке «Конфигурации» (HostCapsPanel): выбор хоста → GET
  /hosts/{id}/capabilities, чекбоксы каталога из 6 capability, сохранение
  PUT по праву hosts.write; apiPut в api.ts. (ui)

- Чанк 60 (2026-10-09): cluster-level capability — GET/PUT
  /clusters/{id}/capabilities (hosts.read/write, scoping, валидация
  каталогом, аудит clusters.capabilities). Store: ClusterCaps/
  SetClusterCaps (tx-замена cluster-level набора; наследование
  host→cluster→default monitoring неизменно). (server, api)

- Чанк 59 (2026-10-08): REST для capability хоста (п. 4 ТЗ, первый
  срез эпика поэтапной передачи контроля). GET/PUT
  /hosts/{id}/capabilities (hosts.read/write, scoping clusterAllowed):
  валидация по каталогу (monitoring/rules/log_rotation/service_mgmt/
  packages/config, дубли/мусор → 400), аудит hosts.capabilities.
  Store: CapabilitiesRepo.HostCaps/SetHostCaps (tx-замена host-level
  набора; пусто — наследование кластера/дефолта monitoring). Агент
  применяет набор при следующем Hello (HelloAck.Config.Capabilities).
  (server, api)

- Чанк 58 (2026-10-08): UI истории применений конфигураций + откат
  (план 1B, срез 5). Вкладка «Конфигурации»: панель истории по
  инстансу (GET /instances/{id}/config/history; таблица
  время/версия/статус-бейдж/вывод валидатора), кнопка «Откат к
  последней applied» — redeploy последней applied-версии на инстанс
  истории (deploy + опциональный target). (ui)

- Чанк 57 (2026-10-08): история применения конфигураций по инстансу
  (план 1B, срез 4; стенд выключен — e2e отложен). Proto: DeployConfigResult
  + instance_id, validate_only (BREAKING — обновлять совместно).
  Миграция 000011: instance_config_history. hub: запись истории из
  TaskResult (configDeployStatus validated/applied/validation_failed/
  deploy_failed + тест). Store: RecordDeploy/DeployHistory. API: GET
  /instances/{id}/config/history (config.read, scoping). (proto, server,
  agent, db, api)

- Чанк 56 (2026-10-08): редактор «как на хосте» во вкладке
  «Конфигурации» (план 1B, срез 3; только UI). «Загрузить с сенсора»:
  выбор инстанса-источника → GET /instances/{id}/config/current →
  textarea создания версии (note «с сенсора <host>»), 409 — понятный
  текст. Кнопка «в редактор» у версии: контент в форму (note «на основе
  <version>») — цикл «правка → новая версия» в UI. (ui)

- Чанк 55 (2026-10-08): чтение фактического suricata.yaml с сенсора
  (план 1B, срез 2; стенд выключен — e2e отложен). Proto: FetchConfigTask
  (Task oneof =17), FetchConfigResult content/sha256/size_bytes
  (TaskResult oneof =16). hub: реестр taskWaiters + SendTaskAndWait —
  синхронное ожидание результата задачи от агента. Агент: executeFetch —
  чтение config_path из bound_instances HelloAck, sha256, предел 4 МБ,
  capability config. API: GET /instances/{id}/config/current
  (config.read, scoping) — YAML с заголовком X-Config-Sha256; 409
  offline/таймаут, 502 ошибка чтения. (proto, server, agent, api)

- Чанк 54 (2026-10-08): конфигурации — версии suricata.yaml и деплой
  конфигурации (план 1B, срез 1; стенд выключен — e2e отложен).
  Миграция 000010: таблица config_versions (контент — content-addressed
  блоб S3 по sha256, версия cfg-v<N> автоинкремент per-org или тег,
  UNIQUE (org,version) и (org,sha256)); operator получает
  config.read/config.write. Store: ConfigsRepo (Create идемпотентен
  по sha256, List keyset, NextAutoVersion). API /config_versions:
  GET/POST (list/create), GET /{id}, GET /{id}/content (блоб из S3),
  POST /{id}/deploy — прямая hub-задача DeployConfigTask агенту
  (409 при offline-агенте; validate_only — только suricata -T;
  аудит configs.deploy). Агент: executeConfig — скачивание по signed
  URL, бэкап текущего yaml, запись, `suricata -T` с откатом при провале,
  restart/reload systemd-юнита (capability config). UI: вкладка
  «Конфигурации» (список, создание, просмотр контента, деплой на
  инстанс). (server, agent, ui, db, api)

- Чанк 53 (2026-10-08): таргетинг на уровне ruleset + сортировка в вебе
  (план 1A, срез 4 — закрытие 1A; стенд выключен — e2e отложен). POST
  /rulesets принимает targeting {cluster_ids, host_ids} (в manifest);
  createDeployment сужает цели до пересечения (инстанс входит, если его
  кластер или хост в списке ruleset; пусто → 400); таргетинг виден в
  GET /rulesets/{id}/rules. UI: кликабельные заголовки колонок
  (SortTh/sortBy) на вкладках «Правила», «Ruleset'ы», «Аудит».
  (server, ui, api)

- Чанк 52 (2026-10-07): экспорт правил (план 1A, срез 3; стенд
  выключен — e2e отложен). POST /rules/export?format=text|stix|dataset:
  text — .rules по фильтру; stix — STIX 2.1 bundle (indicator
  pattern_type="suricata", id детерминирован по sid); dataset —
  активные IOC в формате Suricata Dataset (type,value). Ответ-файл
  (attachment + ETag). UI «Правила»: кнопки экспорта. (server, ui, api)

- Чанк 51 (2026-10-07): клонирование правил и контроль дублей sid
  (план 1A, срез 2; стенд выключен — e2e отложен). POST
  /rules/{id}/clone — копия с новым sid из локального диапазона
  9000xxx (max+1, исчерпан → 409) и msg (по умолчанию «<msg> (копия)»),
  rev→1; клон создаётся under_review. Сборка ruleset'а: дубликат sid в
  наборе → 400. UI «Правила»: кнопки «ред.» (msg) и «клон».
  (server, ui, api)

- Чанк 50 (2026-10-07): версия ruleset'а автоинкремент + просмотр
  состава (план 1A, срез 1; стенд выключен — e2e отложен). POST
  /rulesets: version опциональна, пустая → v<N+1> по организации
  (произвольный тег — как раньше). GET /rulesets/{id}/rules — состав
  версии (порядок manifest, msg/status актуальные из репозитория).
  UI «Ruleset'ы»: drill-down состава по клику на версию, версия в
  конструкторе необязательна (авто vN). (server, ui, api)

- Чанк 47 (2026-10-07): scoping в матрице «правила × инстансы»
  (завершение эпика scoping, чанки 43/46). Ось инстансов для
  restricted-пользователя сужается до его ScopeClusters (фильтр в SQL,
  `h.cluster_id = ANY(scope)` — пагинация не ломается); явный
  ?cluster_id= вне scope → 404 (объект вне scope неотличим от
  несуществующего). Живой e2e: scoped analyst (scope=DC-1) видит только
  инстанс test1; чужой кластер → 404; admin → 200. (server)

- Чанк 46 (2026-10-03): scoping таргетинга деплоев (п. 8 ТЗ, завершение
  эпика scoping). `handlers.scopeTargets` в createDeployment: для
  cluster-restricted пользователя цели деплоя пересекаются с его кластерами.
  Явные списки (selected_clusters/specific_hosts/specific_instances) —
  каждый id проверяется: чужой (кластер/хост по кластеру/инстанс по
  кластеру хоста) → 404 (объект вне scope неотличим от несуществующего).
  Режимы all_clusters/all_except_clusters — молча сужаются до разрешённых
  кластеров (intersectIDs результата с IDsForClusters(scope); пусто → 400
  «не выбрал ни одного инстанса»). Юнит-тест intersectIDs (пересечение,
  порядок, пустые). Матрица «правила × инстансы» — следующий чанк.
  (server, api, docs)

- Чанк 45 (2026-10-03): пересылка EVE-алертов в SIEM (п. 5.4 ТЗ:
  «пересылка EVE-алертов … в SIEM»). Агент `cmd/agent/siem.go`: tail
  eve.json (offset, устойчив к ротации, ≤16 МБ/тик — как метрики чанка 34)
  → события event_type=alert → syslog: UDP (RFC 5426) или TCP (RFC 6587
  octet-counting, реконнект при обрыве). Формат: CEF (Common Event Format
  — `CEF:0|SuriFleet|Suricata|1.0|sid|signature|severity|src=… dst=…
  proto=… rt=… cs1=…`; экранирование, severity 1..3 → CEF 8/5/2) или сырой
  JSON alert-события. Конфиг агента: `agent.siem_addr` (пусто — выкл.),
  `siem_protocol` (udp|tcp), `siem_format` (cef|json); горутина в
  session.go (тик 5 с, ленивый forwarder по log_dir первого инстанса).
  Юнит-тесты: toCEF (префикс/extension/экранирование), newAlerts (фильтр
  alert, инкремент, ротация), forwardOnce по UDP round-trip. Живой e2e не
  проводился (sandbox блокирует SSH): при перекате агента с siem_addr —
  алерты приходят в SIEM. (agent, docs)

- Чанк 44 (2026-10-03): экспорт аудита (п. 8 ТЗ: «поиск и фильтрация в
  UI; экспорт»). `AuditRepo.ListRange` — записи за период [from,to)
  хронологически (ASC, ≤ предела). `GET /audit_log/export?from&to&format=
  csv|json` (право audit.read; from/to — обязательные RFC3339, format
  default csv; ≤ 50000 строк; Content-Disposition attachment audit-
  YYYYMMDD-YYYYMMDD.{csv,json}). CSV — encoding/csv (заголовок created_at/
  actor_type/…/diff; nil-поля → пусто; diff как JSON). Юнит-тест
  writeAuditCSV (парсинг обратно, квотинг запятых/кавычек, nil-поля).
  Эпик аудита (п. 8) полностью закрыт: diff + цепочка хэшей + экспорт.
  (server, api, docs)

- Чанк 43 (2026-10-03): scoping ролей по кластерам (п. 8 ТЗ: «аналитик
  видит/меняет только свои кластеры»). `UsersRepo.ClusterScope` — семантика:
  ≥1 назначение scope_type='organization' → вся org (restricted=false);
  иначе union cluster_ids из scope_type='clusters' (restricted=true;
  пусто — ничего не видит). Identity + ScopeRestricted/ScopeClusters +
  ClusterScopeAllowed; authMiddleware заполняет (dev/API-токен — без
  ограничений). Применено: clusters (list — фильтр; get/patch/delete —
  guard clusterAllowed), hosts (list — фильтр по ClusterID; get/create/
  patch/delete — guard), instances (list — SQL-фильтр Instances.ListScoped
  по кластерам хостов ANY($n), пустой scope → пусто; get/create/patch/
  delete — guard instanceAllowed через кластер хоста). Объект вне scope →
  404 (неотличим от несуществующего, п. 8). Юнит-тест ClusterScopeAllowed
  (dev/org-scope/restricted/пустой scope). Таргетинг деплоев и матрица —
  следующие чанки. (server, api, docs)

- Чанк 42 (2026-10-03): постоянный SAML SP-ключ (снятие MVP-ограничения
  чанка 41). Самоподписанный ключ/сертификат Service Provider теперь
  хранится на диске (`ca_dir/saml-sp/sp.key.pem` 0600 + `sp.crt.pem` 0644,
  load-or-create, атомарная запись tmp+rename) — метаданные SP стабильны
  между рестартами сервера (IdP не нужно переподключать). `NewService(keyDir)`;
  пустой keyDir — эфемерный ключ в памяти (тесты/дев). main.go:
  samlSPKeyDir = ca_dir/saml-sp. Юнит-тесты: стабильность serial/ключа
  между «процессами» (два Service на один каталог), права 0600, кэш в
  памяти при пустом keyDir. (server, docs)

- Чанк 41 (2026-10-03): SAML 2.0 SSO (п. 9 ТЗ: «SAML 2.0 — второй
  протокол для корпоративных IdP»). `internal/samlauth` (crewjam/saml по
  ТЗ — ServiceProvider, разбор/валидация assertion без самодельного XML):
  самоподписанный ключ/сертификат SP (в памяти на процесс — MVP, до
  рестарта), метаданные IdP (inline XML или по URL, кэш), SP сборка
  лениво на провайдера. Публичные эндпоинты: GET /auth/saml/{id}/metadata
  (SP-метаданные для импорта в IdP), GET /auth/saml/{id}/login
  (AuthnRequest → 302 на IdP, HTTP-Redirect binding), POST
  /auth/saml/acs?provider_id=<uuid> (проверка assertion — подпись/
  audience/сроки crewjam/saml; атрибуты email/displayName/groups с
  дефолтами и *_attr из конфига → JIT общим oidc.Service.Provision →
  сессия → HTML с токеном, как OIDC-callback). Провайдер type=saml в
  /sso_providers (config: idp_metadata_url/idp_metadata_xml, sp_entity_id,
  acs_url, username/email/name/groups_attr); валидация обязательных полей.
  Аудит auth.login_saml. Юнит-тесты: ConfigFrom/Validate (url/inline xml/
  обязательные поля), ProfileFromAssertion (NameID/username_attr, fallback
  displayName→email, дедуп групп, нет email/nil). Живой e2e с IdP не
  проводился (в sandbox нет IdP) — проверить при перекате против
  Keycloak/AD FS/Entra ID. (server, api, docs)

- Чанк 40 (2026-10-03): LDAP/AD-аутентификация (п. 9 ТЗ: «LDAP/AD —
  bind-аутентификация и чтение групп»). `internal/ldapauth`
  (go-ldap/ldap/v3): поиск пользователя под service-аккаунтом (bind_dn)
  по user_filter (шаблон %s → ldap.EscapeFilter — защита от инъекций;
  дефолт (|(sAMAccountName=)(userPrincipalName=)(uid=))), bind DN
  записи + её паролем (проверка кредов самим каталогом, пароль не
  оседает), чтение email (mail → userPrincipalName), имени
  (displayName → cn) и групп (memberOf → cn RDN, дедуп). JIT-провижининг
  и маппинг групп → роли — общий oidc.Service.Provision (экспортирован).
  REST: POST /auth/ldap/login {provider_id, username, password}
  (публичный; ответ authTokens как у локального входа); аудит
  auth.login_ldap (success/denied/error). Провайдер type=ldap в
  /sso_providers: config — url (ldap/ldaps), start_tls, bind_dn,
  bind_password (writeOnly), base_dn, user_filter, group_attr и др.;
  валидация url+base_dn. store.SsoProvider + ConfigRaw (сырой jsonb для
  не-OIDC типов). Юнит-тесты: фильтр (экранирование инъекций/wildcard,
  шаблон), профиль (mail/UPN/cn fallback, нет email), группы (memberOf→
  cn, дедуп, posix). Живой e2e с каталогом не проводился (в sandbox нет
  LDAP-сервера) — проверить при перекате против AD/OpenLDAP.
  (server, db, api, docs)

- Чанк 39 (2026-10-03): аудит diff для остальных PATCH (п. 8 ТЗ,
  завершение эпика аудит-diff). `rules.update`, `iocs.update`,
  `feeds.update` теперь пишут аудит с diff «было→стало» через
  h.auditDiff (как users/roles/sso из чанка 37): «было» читается
  Get-методом до Update (ошибка чтения не валит запрос), секреты
  исключаются auditdiff.Compute. Эти PATCH раньше вообще не аудировались.
  Эпик аудита закрыт: diff для всех PATCH + цепочка хэшей (чанк 38).
  (server, docs)

- Чанк 38 (2026-10-03): цепочка хэшей аудит-лога (п. 8 ТЗ: «защита от
  подделки — append-only, опционально цепочка хэшей записей»). Каждая
  запись audit_log: `prev_hash` = hash предыдущей, `hash` = SHA-256
  канонической формы (prev_hash + все поля, включая created_at — задаётся
  в коде, не DEFAULT now(), чтобы попасть в хэш). Вставка под
  `pg_advisory_xact_lock` — цепочка не раздваивается при параллельных
  писателях (несколько API-процессов, одна БД). Включение:
  `server.audit_hash_chain=true` (AuditRepo.HashChain; default false —
  обычная вставка). Проверка целостности: `AuditRepo.VerifyChain`
  (пересчёт hash — подделка полей; связность prev_hash_i == hash_(i-1) —
  удаление/вставка) + `GET /audit_log/verify?limit=N` (audit.read),
  кнопка «проверить цепочку» во вкладке «Аудит». Колонки prev_hash/hash
  были в схеме с 000001 — миграция не нужна. Юнит-тесты: детерминизм и
  чувствительность хэша к полям/времени/prev_hash/diff, моделирование
  цепочки (связность, детект подделки и удаления). (server, api, ui, docs)

- Чанк 37 (2026-10-03): аудит diff «было → стало» (п. 8 ТЗ: «diff
  „было → стало“ для изменений»). `internal/auditdiff` — Compute(before,
  after) → `{"before":{…},"after":{…}}` только по изменённым полям;
  поля-секреты (password/secret/credentials/token/hash) исключаются.
  audit_log.diff (колонка была с 000001) заполняется для `users.update`,
  `roles.update`, `sso.update` (store.AuditEntry.Diff, INSERT +diff);
  GET /audit_log отдаёт diff; React-вкладка «Аудит» — сворачиваемый
  просмотр изменённых полей. Юнит-тесты auditdiff (изменённые/без
  изменений/секреты/добавленные-удалённые поля/мапы). Остальные PATCH
  (rules, iocs, feeds) — следующие чанки; цепочка хэшей (prev_hash→hash)
  — колонки в схеме есть, заполнение позже. (server, api, ui, docs)

- Чанк 36 (2026-10-03): retention телеметрии в ClickHouse (п. 5.4,
  продолжение). TTL для таблиц `surifleet.agent_logs` и
  `surifleet.agent_metrics`: параметр `server.ch_retention_days`
  (default 30 дней; 0 — бессрочно). `EnsureTable` при старте применяет
  `ALTER TABLE … MODIFY TTL ts + INTERVAL N DAY` (идемпотентно; CREATE
  TABLE IF NOT EXISTS существующие таблицы не обновляет — TTL задаётся
  отдельным ALTER). Юнит-тесты retentionExpr/ALTER-запроса.
  `deploy/config/server.example.yaml` + `ch_retention_days`.
  (server, db, docs)

- Чанк 35 (2026-10-03): OIDC-SSO (п. 9 ТЗ; Authorization Code + PKCE).
  `internal/oidc` (coreos/go-oidc/v3 + x/oauth2 — проверенные библиотеки,
  без самодельной криптографии): flow с PKCE S256, одноразовые state+nonce
  (таблица `oidc_states`, миграция 000009 + зеркало internal/store/migrations,
  consume атомарно DELETE…RETURNING — защита от CSRF/replay; PKCE-верификатор
  детерминированно выводится из state, отдельно не хранится). Проверка
  id_token (подпись, issuer, audience, exp, nonce), claims из id_token +
  userinfo (fallback для groups/email). Маппинг групп IdP → роли по
  `sso_providers.group_role_mapping` (дедуп role_id, scope=organization).
  JIT-провижининг: по (provider,sub) → обновление ролей; по email →
  привязка локального пользователя к IdP (LinkExternal, дедуп); иначе —
  создание без пароля (CreateExternal); деактивированный — отказ входа.
  Store: `internal/store/sso.go` (SsoProvidersRepo CRUD по sso_providers,
  OidcStatesRepo), UsersRepo + GetByExternalID/LinkExternal/CreateExternal/
  SetRoles. REST: публичные `GET /auth/sso/providers`, `GET /auth/sso/{id}/login`
  (302 на IdP), `GET /auth/sso/callback` (проверка → сессия → HTML, кладущая
  токен в localStorage → /app/); админ `GET/POST /sso_providers`,
  `GET/PATCH/DELETE /sso_providers/{id}` (права sso.read/sso.write;
  client_secret — writeOnly, в ответах обнуляется; пустой в PATCH — «не менять»).
  Каталог разрешений + sso.read/sso.write. Аудит auth.login_sso (success/denied),
  sso.create/update/delete. React: кнопки «Войти через SSO» на форме входа
  (публичный список провайдеров), вкладка «SSO» (CRUD провайдеров, маппинг
  групп→роли JSON с подсказкой role_id). Юнит-тесты internal/oidc (PKCE,
  groupsFrom, roleIDsForGroups, oauth2Config). Живой e2e на стенде НЕ проводился
  (sandbox сессии блокирует SSH до .28/.67 и IdP недоступен) — проверить при
  перекате: пересобрать, перекатить сервер, миграция → version 9, создать
  OIDC-провайдер, пройти flow до сессии. (server, db, api, ui, docs)

- Чанк 34 (2026-10-02): метрики Suricata из eve.json (п. 5.4,
  продолжение). Агент `evemetrics.go`: tail eve.json с offset
  (устойчив к ротации), последнее stats-событие за тик →
  suricata.uptime_seconds, capture_kernel_packets/drops, decoder_pkts/
  bytes, flow_memuse_bytes, detect_alert; instance_id — из привязок
  HelloAck по log_dir. UI: вкладка «Метрики» — динамические ряды
  (host.* закреплены, suricata.* по факту). Живой e2e: все ряды в
  ClickHouse с instance_id инстанса, API отдаёт, in_sync 1/1.
  (agent, ui, docs)

- Чанк 33 (2026-10-02): мониторинг — метрики агента host.* end-to-end
  (п. 5.4, первый срез). Агент: MetricsBatch каждые 60 с
  (host.cpu_percent, host.mem_bytes, host.disk_used_percent).
  ClickHouse: таблица surifleet.agent_metrics (создаётся при старте),
  запись из hub.handleMetricsBatch. API: GET /agents/{id}/metrics
  (agents.read, окно minutes 1..1440, фильтр names) → {series}.
  React-вкладка «Метрики»: селектор агента, окно 15мин–24ч,
  SVG-спарклайны CPU/память/диск, авто 30 с. Живой e2e: точки в
  ClickHouse, API с реальными значениями, compliance in_sync 1/1.
  (agent, server, ui, docs)

- Чанк 32 (2026-10-02): React UI — скрытие пишущих действий по
  разрешениям пользователя (ТЗ: «UI скрывает недоступные действия»).
  `web/src/perms.ts` (PermsContext + хук useCan), App провайдит
  permissions из /auth/me. Скрыты: вкл/откл и импорт правил,
  конструктор ruleset'ов, создание/управление деплоями, формы IOC и
  фидов, управление пользователями/ролями/токенами — по соответствующим
  правам *.write / rules.deploy. (ui)

- Чанк 31 (2026-10-02): ванильный MVP UI выпилен (React — единственный
  UI; в token-режиме ванильный не мог работать). `/` → 301 на `/app/`;
  удалены internal/httpapi/webui. (server, ui, docs)

- Чанк 29 (2026-10-02): API-токены автоматизации со scopes (п. 8 ТЗ).
  `internal/store/tokens.go` — ApiTokensRepo (в БД SHA-256 хэш; листинг
  только активных; мягкий отзыв; last_used_at троттлингом раз в минуту).
  REST: GET/POST /api_tokens (tokens.read/write), DELETE /api_tokens/{id}
  (отзыв); значение токена — один раз в ответе POST; scopes — строки из
  общего каталога разрешений (валидация 400), expires_at. Middleware:
  заголовок X-API-Key (приоритетнее Bearer) → identity с правами =
  scopes; аудит actor_type=api_token. Живой e2e: токен [rules.read] →
  200 на rules, 403 на iocs/users (authz.denied в аудите), 200 → отзыв
  → 401; compliance in_sync 1/1. (server, docs)

- Чанк 30 (2026-10-02): React UI — вкладки «Пользователи», «Роли»,
  «Токены», «Аудит» (п. 8 ТЗ). Пользователи: список с поиском, создание
  с ролью и флагом break-glass, вкл/откл, revoke_sessions, удаление.
  Роли: builtin/custom, создание кастомной с мультивыбором разрешений.
  Токены: выпуск (значение показывается один раз, с копированием),
  листинг, отзыв. Аудит: таблица (актор/действие/объект/результат/ip),
  фильтр по префиксу action, дозагрузка. Видимость вкладок — по правам
  из /auth/me (ТЗ: UI скрывает недоступное). (ui)

- Чанк 28 (2026-10-02): auth/RBAC — локальные пользователи, сессии,
  роли (п. 8–9 ТЗ, без SSO). Миграция 000008: встроенные роли admin
  (`*`), operator, analyst, viewer с permissions. `internal/authn` —
  bcrypt-хэш паролей, непрозрачный токен сессии (32 байта, SHA-256-хэш
  в БД). Store: UsersRepo (CRUD с ролями в tx, последний break-glass
  неудаляем), RolesRepo (встроенные неизменяемы 409, кастомные org),
  SessionsRepo (refresh-ротация, отзыв, revoke всех сессий), AuditRepo
  (запись + keyset-листинг). REST: POST /auth/login (break-glass —
  отдельный action аудита), /auth/refresh (ротация), /auth/logout,
  /auth/me; /users CRUD + revoke_sessions; /roles CRUD; GET /audit_log.
  Middleware: authMiddleware (режимы server.auth_mode=dev|token),
  requirePerm на всех маршрутах (каталог разрешений fleet/hosts/agents/
  rules/ioc/feeds/users/roles/audit + rules.deploy; organizations CUD —
  admin-only), 401 unauthorized / 403 forbidden по openapi, аудит
  отказов authz.denied. resolveOrgID — org из identity. deploy:true в
  /iocs/generate требует rules.deploy. Конфиг: auth_mode, session_ttl
  (12h), bootstrap_admin_*; bootstrapBreakGlass — автоматический
  break-glass админ при старте в token-режиме (пароль из env/конфига
  или генерируется, один раз в лог). React UI: форма входа, токен в
  localStorage, Authorization во всех вызовах, 401 → повторный вход,
  email + «выйти» в шапке. Ванильный UI на `/` в token-режиме не
  работает. Попутный фикс hub: дубль-стрим — разрыв старой сессии не
  гасит агента (streamHandle.sessionID, Delete только своей сессии).
  Живой e2e на стенде: 401 без токена, login, права analyst
  (403 на deployments/users/audit), ротация и logout, кастомная роль,
  защита break-glass, аудит-цепочка в GET /audit_log; compliance
  in_sync 1/1. Стенд переведён на auth_mode: token
  (admin@surifleet.local / admin12345). (server, ui, db, docs)

### BREAKING

- Чанк 64 (2026-10-09): профили конфигураций — фундамент (план 1B).
  Миграция 000012 config_profiles (scope_type/scope_id, parent_id
  self-ref — наследование кластер→хост→инстанс, content_yaml, version
  с инкрементом при смене содержимого). Store ConfigProfilesRepo CRUD;
  API GET/POST /config_profiles + GET/PATCH/DELETE /{id} (config.read/
  write, проверка scope_id с scoping, аудит config_profiles.*).
  Рендер переменных, история версий, /render|/validate — следующие
  чанки. (server, db, api)

### BREAKING

- Миграция 000012 (db): таблица config_profiles. Применится при
  перекате сервера; откат — 000012 down.

- Миграция 000011 (db): таблица instance_config_history (история
  deploy_config по инстансам).

- Чанк 55 (2026-10-08): proto — FetchConfigTask (Task oneof =17) и
  FetchConfigResult (TaskResult oneof =16). Обновлять агент и сервер
  совместно: сторона на старом proto честно откажет («тип задачи не
  поддерживается»), новые задачи fetch_config до переката работать не
  будут.

- Миграция 000010 (db): новая таблица config_versions + UPDATE
  встроенной роли operator (config.read/config.write). Применится
  автоматически при старте сервера; откат — 000010 down.

### Fixed

- Чанк 49 (2026-10-07): retention ClickHouse не работал — TTL-выражение
  «ts + INTERVAL N DAY» отклоняется CH 24.8 (TTL не может быть
  DateTime64: BAD_TTL_EXPRESSION; ошибка на каждом старте сервера).
  Фикс: toDateTime(ts) + INTERVAL N DAY. Живой e2e: TTL 30 дней на
  agent_logs и agent_metrics (SHOW CREATE), просроченная точка
  удаляется на мерже (OPTIMIZE FINAL), свежая остаётся. (server)

- Чанк 48 (2026-10-07): цепочка хэшей аудита включена на стенде
  (audit_hash_chain: true) + фиксы VerifyChain, найденные первым живым
  запуском (до них GET /audit_log/verify отдавал 500, а после — ложный
  «подделка полей»): scan NULL actor_name → *string; ip из PG приходит
  CIDR-нотацией («192.168.31.50/32») — при чтении нормализуется к хосту
  (в канон при записи шёл чистый адрес); created_at в каноне усечён до
  микросекунд (timestamptz хранит µs, запись с ns давала другой хэш).
  verify → ok:true, цепь наращивается. (server)

- Регрессия чанка 28: в token-режиме статика UI (/app/, /ui/, /) была
  защищена токеном — форма входа не загружалась. authMiddleware теперь
  применяет аутентификацию только к путям /api/v1/*. (server)

- Инфра-инцидент ночи 01→02.10: перезагрузка обеих ВМ выявила три
  проблемы. (1) Сервер/агент молча умирали — корень: systemd
  session-scope SIGTERM при закрытии ssh-сессии (setsid не выходит из
  cgroup сессии) — вероятная причина и тихой смерти агента 16.09;
  лечение: systemd-юниты surifleet-server.service (.28) и
  surifleet-agent.service (.67), enable + Restart=always, перекат
  через systemctl restart (процедуры — handover §5). (2) Redis после
  unclean shutdown падал в crash-loop (AOF corrupt) —
  redis-check-aof --fix (данные presence эфемерны). (3) Часы .28 —
  chrony синхронизирует корректно, утренние скачки были boot-коррекцией.
  Аномалия sudo rm из ssh (16.09) не воспроизводится — снята. (infra)

- Чанк 27 (2026-10-01): cron-расписания авто-синка фидов.
  `internal/feedsync/schedule.go`: `ParseSchedule` — длительность Go
  ("1h") или 5-полевой cron ("*/15 * * * *", @daily и др.; новая
  зависимость robfig/cron/v3; локальное время сервера);
  `Schedule.Due(last_sync_at, now)` — для cron наступление = ближайшее
  cron-время после last_sync_at уже прошло. Планировщик переведён на
  ParseSchedule/Due (фид с исправленным schedule подхватывается снова);
  валидация schedule в POST/PATCH /feeds → 400. Юнит-тесты
  ParseSchedule/Due. Живой e2e: фид generic со schedule "* * * * *" →
  авто-запуски каждую минуту без ручного синка (imported=2 → updated=2),
  невалидный schedule → 400; compliance in_sync 1/1. Эпик фидов
  завершён: 6 коннекторов + расписания. (server, docs)

- Чанк 26 (2026-10-01): коннектор misp — MISP core format feed
  (`internal/feedsync/misp.go`): manifest.json → события {uuid}.json
  (свежие первыми по timestamp, предел 2000 за синк); атрибуты с
  to_ids=false/deleted пропускаются; типы ip-src/ip-dst, domain/hostname,
  url, md5/sha1/sha256, email-* (+ составные domain|ip, ip-*|port,
  filename|hash); score по threat_level_id события (1→80, 2→60, 3→40,
  прочее→50); дедуп (type,value) между событиями; ошибки событий не
  прерывают синк. Автопрогон генерации правил — и для misp. Юнит-тесты
  ParseMispEvent (маппинг, составные, to_ids/deleted, score) и
  httptest-тест manifest/дедупа. Живой e2e на стенде (mock):
  imported=4/updated=1/skipped=1, score 80/60, rules_created=4, ruleset
  ioc-current-4d1d5b99; повтор — imported=0/updated=5 (идемпотентно);
  compliance in_sync 1/1. Все 6 типов фидов (generic, et_open, et_pro,
  taxii, stix, misp) теперь синхронизируются. (server, docs)

- Чанк 25 (2026-10-01): коннектор stix — статический STIX 2.x bundle
  (`{"objects":[...]}`) или голый JSON-массив объектов по URL, без
  TAXII-протокола. `ParseStixBody` (bundle/массив/BOM/мусор), разбор
  индикаторов — общий ParseStix чанка 24; загрузка общая (fetch,
  Basic/Bearer). Автопрогон генерации правил после ручного синка —
  теперь generic|taxii|stix. Юнит-тест TestParseStixBody. Живой e2e
  (http.server на .28): imported=3/skipped=1 (неподдерживаемый паттерн —
  понятный текст), score из confidence, rules_created=3, ruleset
  ioc-current-4acbe140; повтор imported=0/updated=3 (идемпотентно);
  битый URL → failed «HTTP 404». Тестовые фиды удалены, compliance
  in_sync 1/1. (server, docs)

- Чанк 24 (2026-10-01): коннектор taxii — фиды IOC по TAXII 2.x/STIX
  (`internal/feedsync/taxii.go`). URL фида: endpoint объектов коллекции
  (`.../collections/{id}/objects/` или коллекция без /objects) либо
  API root (discovery: GET {url}/collections/ → все коллекции с
  can_read != false); пагинация envelope more/next (предел 100 страниц);
  Accept application/taxii+json;version=2.1; credentials — "user:pass"
  (Basic) или Bearer. STIX-разбор: только indicator (pattern_type stix
  или пустой), revoked/истёкшие valid_until пропускаются, confidence
  (0..100) → score, valid_until → expires_at; паттерн — извлечение
  сравнений lhs='value' (OR/AND-композиты дают по IOC на сравнение):
  ipv4/ipv6-addr→ip, domain-name→domain, url→url, email-addr→email,
  file:hashes.MD5/'SHA-1'/'SHA-256'→md5/sha1/sha256; дедуп (type,value)
  внутри выборки. Импорт IOC вынесен в общий `Syncer.importIocs`
  (generic и taxii); автопрогон генерации правил после ручного синка —
  теперь и для taxii. Юнит-тесты: ParseStix (маппинг, confidence,
  expires, revoked/истёкшие/не-indicator, дедуп, ошибки) и
  fetchTaxiiObjects на httptest (discovery, can_read=false, пагинация,
  Basic-auth, коллекция без /objects, пустой URL). OpenAPI/UI/access.md
  под факт. Живой e2e на стенде (mock TAXII на .28): API root →
  imported=4/skipped=1 (yara — понятная ошибка), rules_created=3,
  ruleset ioc-current-1eb1ceea; повтор — imported=0/updated=4
  (идемпотентно); прямая коллекция (STIX bundle) — imported=1 (email);
  score=85 из confidence, feed_id проставлен; runs-история ok; тестовые
  фиды удалены (IOC остались, feed_id → NULL), compliance in_sync 1/1.
  Инфра-нужда: сборка фронта переведена на project-local Node.js
  `.tools/node` (v24.15.0) — старый shim ссылался на рантайм Kimi
  Desktop, недоступный в CLI-сессиях; обновлён `.tools/bin/npm`.
  (server, ui, docs)

- Чанк 23 (2026-10-01): детект «тихой» смерти агента (инцидент
  2026-09-16 — мёртвый процесс 2 часа отображался online). Heartbeat
  теперь не только продлевает Redis-TTL, но и троттлингом (не чаще 30 с,
  `hub.touchLastSeenMin`) пишет `last_seen_at` в PostgreSQL через
  `AgentsRepo.HeartbeatPulse`; пульс также возвращает в online агента,
  ошибочно погашенного свипером, чей стрим на самом деле жив
  (reason heartbeat-resumed; статусы degraded/updating/error не
  трогаются). Фоновый свипер (роль hub|all,
  `server.offline_sweep_interval` default 30s) переводит online-агентов
  с протухшим `last_seen_at` (старше `server.agent_offline_after`,
  default 120s) в offline: `AgentsRepo.SweepStaleOnline` (last_seen_at
  не перезаписывается — остаётся фактическим временем последнего
  heartbeat), история `agent_state_history` (reason heartbeat-timeout),
  очистка presence в Redis, пересчёт compliance инстансов в stale —
  `hub.Server.SweepOfflineAgents`. Живой e2e на стенде: SIGSTOP агенту
  (замороженный процесс, стрим выглядит живым) → offline за ~2–2.5 мин
  с записью в истории; SIGCONT → heartbeat-resumed → online без
  переподключения; живой агент ложно не гаснет.
  Диагностика тихой смерти агента: `deploy/start-agent.sh` (каноничная
  копия в репозитории, развёрнута на .67) — обёртка вместо `exec`,
  логирует код выхода/сигнал завершения агента в
  `data/agent-exit.log` (SIGTERM → exit_code=0, агент завершается
  gracefully; проверено). Замечание по стенду: часы ВМ .28 скачут
  (RTC отстаёт на 5+ мин, timesyncd «not synchronized» после
  перезагрузки) — wall-шаги искажают наблюдаемые задержки свипера,
  самодельный тайминг в тестах трактовать с поправкой. (server, agent,
  docs)

- 2026-10-01 (фиксация и передача): `docs/handover-kimi-code.md` —
  полная инструкция по продолжению проекта в Kimi Code: правило
  возобновления (PROGRESS.md + git log), дисциплина чанков и живых
  документов, стенд и инструменты .tools/, процедуры переката
  сервера/агента, карта репозитория, текущие следующие шаги, первый
  промт для Kimi Code. Стенд оживлён после перезагрузки ВМ
  (docker-стек сам, сервер+агент вручную): health ok, агент online,
  compliance in_sync 1/1. (docs, infra)

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
