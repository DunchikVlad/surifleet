# Передача проекта SuriFleet в Kimi Code

> Дата: 2026-10-01. Документ описывает, как продолжить работу над проектом
> в Kimi Code (CLI-агент) вместо Kimi Work. Всё необходимое уже лежит в
> репозитории — этот файл связывает это воедино.

## 1. Что за проект

**SuriFleet** — централизованное управление флотом Suricata IDS (сервер +
агент на Go, React UI). Полное ТЗ: `промт_suricata_manager_go_kimi.md` в
корне репозитория. Каталог проекта: `C:\Users\Professional\Documents\suricata`.

## 2. С чего начинать КАЖДУЮ сессию (правило возобновления)

1. Прочитать `PROGRESS.md` — там «Текущая фаза», блок «Следующий шаг
   (конкретно)» и журнал всех чанков.
2. `git log --oneline -10` — сверить с таблицей «Сделано» в PROGRESS.md.
3. Работать с записанного следующего шага.

Это главный контракт проекта. PROGRESS.md — единственный источник правды
о том, что сделано и что дальше.

## 3. Дисциплина работы (обязательная)

- Работа **чанками**: один чанк = одна законченная вертикальная фича/фикс.
- После каждого чанка: `go build ./... && go vet ./...` (+ `go test ./internal/...`),
  **живая верификация на стенде** (не только компиляция), обновление
  живых документов, коммит.
- Живые документы обновляются при КАЖДОМ коммите:
  - `PROGRESS.md` — блок «Чанк N ГОТОВ» + строка в таблице «Сделано»
    (с «(этот коммит)», хэш проставляется при следующем чанке) + новый
    «Следующий шаг»;
  - `FEATURES.md` — статус по пунктам ТЗ;
  - `CHANGELOG.md` — запись чанка;
  - `docs/access.md` — если менялись доступы/эндпоинты/ограничения.
- Коммиты на русском/английском в формате `chunk N: <суть>`.
- Кодогенерация proto: `GOBIN="$PWD/.tools/bin" bash scripts/gen-proto.sh`.
- OpenAPI (`api/openapi/openapi.yaml`) приводим к факту реализации (фаза MVP).

## 4. Среда и инструменты

### Тестовый стенд

| Хост | Назначение | Что там |
|---|---|---|
| 192.168.31.28 | сервер | docker-стек (PostgreSQL :5432, Redis :6379, NATS, ClickHouse native :9900 / HTTP :8123, MinIO :9000/:9001); dev-сервер `~/surifleet/surifleet-server --config server.yaml` (API+UI :8080, Hub :8443 mTLS, Enrollment :8444) |
| 192.168.31.67 | сенсор | Suricata 8.0.3 (systemd `suricata.service`), агент от root, старт `/home/test/surifleet/start-agent.sh` |

Логин/пароль везде `test`/`test` (ssh и sudo). После перезагрузки ВМ:
docker-стек поднимается сам (restart-политика); сервер и агент — вручную
(см. §5).

### Инструменты репозитория (.tools/)

- **Go 1.27.1 локальный**: перед go-командами всегда
  `export PATH="$PWD/.tools/go/bin:$PATH" GOTMPDIR="$PWD/.tools/tmp" GOCACHE="$PWD/.tools/gocache"`.
- **SSH**: `python .tools/ssh.py 28 "cmd"` / `python .tools/ssh.py 67 "cmd"`.
  - sudo: `python .tools/ssh.py 67 sudo "systemctl restart suricata"` —
    оборачивается в `echo test | sudo -S <cmd>` **без bash**, поэтому
    составные команды только через `sudo "bash -c '...'"`.
- **SFTP**: `export MSYS_NO_PATHCONV=1 && python .tools/scp.py 28 put <local> <remote>`
  (и `get`; для 67 аналогично). После scp бинаря — `chmod +x` (exec-бит теряется).
- **npm/node**: системного нет; shim `"$PWD/.tools/bin/npm"` (node v24).
  Сборка фронта: `cd web && ../.tools/bin/npm run build`.
- **protoc** — в .tools, регенерация через scripts/gen-proto.sh.

### Ключевые ID (живые в БД)

- org acme `3199f12b-c44c-4585-9d58-fe28507f5605`
- инстанс `468c9c71-6ed3-4a3e-be56-5f1f3af93874` (host test1),
  агент `97885671-36c1-46aa-b597-e0f1aa59c2af`
- рабочий ruleset `d44182a6-0fc0-4dcc-9c68-69d113248b4a` (v1.1, 245 правил)
- формат деплоя:
  `{"ruleset_id":"…","targeting":{"mode":"specific_instances","instance_ids":["468c9c71-…"]},"wave":{"batch_size":10,"canary":false}}`

## 5. Типовые процедуры

### Перекат сервера (.28)

```bash
export PATH="$PWD/.tools/go/bin:$PATH" GOTMPDIR="$PWD/.tools/tmp" GOCACHE="$PWD/.tools/gocache"
GOOS=linux GOARCH=amd64 go build -o .tools/tmp/surifleet-server ./cmd/server
export MSYS_NO_PATHCONV=1
python .tools/scp.py 28 put .tools/tmp/surifleet-server /home/test/surifleet/surifleet-server.new
python .tools/ssh.py 28 "bash -c 'chmod +x /home/test/surifleet/surifleet-server.new; kill \$(pgrep -f \"^\\./surifleet-server\"); sleep 12; cd /home/test/surifleet && mv surifleet-server.new surifleet-server && (setsid ./surifleet-server --config server.yaml >> server.log 2>&1 </dev/null &)'"
python .tools/ssh.py 28 "curl -s localhost:8080/api/v1/health"
```

Нюансы: старый сервер держит :8080 ~10 с (gRPC-drain) — `sleep 12`
обязателен; pgrep-паттерн `^\./surifleet-server` — чтобы не убить ssh-сессию.

### Перекат агента (.67)

```bash
GOOS=linux GOARCH=amd64 go build -o .tools/tmp/surifleet-agent ./cmd/agent
python .tools/scp.py 67 put .tools/tmp/surifleet-agent /home/test/surifleet/surifleet-agent.new
python .tools/ssh.py 67 sudo "bash -c 'kill \$(pgrep -x surifleet-agent); sleep 2; cd /home/test/surifleet && chmod +x surifleet-agent.new && mv surifleet-agent.new surifleet-agent && (setsid ./start-agent.sh >/dev/null 2>&1 </dev/null &)'"
python .tools/ssh.py 67 "pgrep -x surifleet-agent && tail -5 /home/test/surifleet/data/agent.log"
```

Нюансы: `pkill` на .67 НЕТ — только `kill $(pgrep -x ...)`; ssh-команда с
setsid висит до таймаута — это норма, проверять pgrep отдельным вызовом;
логи агента — `data/agent.log` (структурные) и `data/agent-console.log`
(stdout/stderr, паники); `agent.out` в корне — старый, не смотреть.

### Фронтенд

- React SPA: `web/` (Vite 5 + React 18 + TS strict), собирается
  `npm run build` → `web/dist` встраивается в бинарь (embed), раздаётся
  с `/app/`. `web/dist` не коммитится (кроме placeholder.txt).
- Ванильный MVP UI: `internal/httpapi/webui/` (go:embed) — на `/` и `/ui/*`.
- Dev-запуск фронта: `cd web && ../.tools/bin/npm run dev` (vite proxy
  /api → 192.168.31.28:8080).

### Проверки после любого деплоя на стенд

```bash
python .tools/ssh.py 28 "curl -s localhost:8080/api/v1/health"
python .tools/ssh.py 28 "curl -s localhost:8080/api/v1/fleet/compliance"   # цель: in_sync
python .tools/ssh.py 67 sudo "systemctl is-active suricata"                # active
```

## 6. Где что лежит (карта репозитория)

- `cmd/server`, `cmd/agent` — точки входа.
- `internal/httpapi/` — REST API + ванильный UI (webui) + reactui (embed).
- `internal/hub/` — gRPC hub (mTLS, стримы агентов, диспетч задач).
- `internal/store/` — PostgreSQL (sql-слой, миграции в `db/migrations/`
  + зеркало `internal/store/migrations/` — копировать обе!).
- `internal/compliance/` — desired/actual state, drift.
- `internal/ruleset/` — сборка ruleset'ов (content-addressed, идемпотентно).
- `internal/iocrules/`, `internal/feedsync/` — IOC→правила, фиды.
- `internal/chlogs/` — логи агентов в ClickHouse (HTTP :8123).
- `api/proto/agent/v1/` — протокол агент↔сервер; `api/openapi/` — REST-спека.
- `docs/` — architecture.md, data-model.md, protocol.md, api.md, access.md.

## 7. Текущее состояние (2026-10-01)

- Всё работает: стенд поднят, compliance in_sync 1/1, UI на
  `http://192.168.31.28:8080/app/` (React) и `/` (ванильный).
- 22 чанка + инциденты закоммичены; история — `git log` и PROGRESS.md.
- **Следующие шаги** (актуальные — в PROGRESS.md, дублирую):
  1. Детект offline-агентов (heartbeat-timeout, свипер протухших
     last_seen) — найдено инцидентом 16.09: мёртвый агент 2 часа
     отображался «online».
  2. Диагностика тихой смерти агента: логирование exit/signal в
     start-agent.sh или systemd-юнит агента.
  3. Коннекторы фидов taxii/stix/misp, cron-расписания фидов.
  4. auth/RBAC (DevAuth → токены, п. 8–9 ТЗ).

## 8. Известные аномалии (не блокеры)

- Из ssh `sudo rm` в /etc/suricata → Permission denied при работающем
  touch; обход — агент от root правит файлы сам.
- При перекате сервера первая попытка может упасть на bind :8080
  (gRPC-drain) — повторить после sleep.

## 9. Первый промт для Kimi Code (скопировать)

```
Проект SuriFleet — управление флотом Suricata IDS (Go + React).
Прочти PROGRESS.md и сделай git log --oneline -10 — продолжи с записанного
там следующего шага. ТЗ — промт_suricata_manager_go_kimi.md. Правила
работы и среда описаны в docs/handover-kimi-code.md — следуй им
(чанки, живые документы, коммит после каждого чанка, живая верификация
на стенде 192.168.31.28/.67 через .tools/ssh.py).
```
