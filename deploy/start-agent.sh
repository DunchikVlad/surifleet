#!/bin/bash
# Обёртка запуска агента SuriFleet на сенсоре (чанк 23).
# Логирует код выхода / сигнал завершения агента в data/agent-exit.log —
# диагностика «тихой» смерти процесса (инцидент 2026-09-16: агент умер без
# паники в логах, причина не найдена). Копия на сенсоре:
# /home/test/surifleet/start-agent.sh (запуск от root).
cd /home/test/surifleet || exit 1

./surifleet-agent --config agent.yaml >> data/agent-console.log 2>&1
code=$?

ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)
if [ "$code" -gt 128 ]; then
  sig=$((code - 128))
  echo "$ts agent завершился: exit_code=$code (signal $sig $(kill -l "$sig" 2>/dev/null))" >> data/agent-exit.log
else
  echo "$ts agent завершился: exit_code=$code" >> data/agent-exit.log
fi
exit "$code"
