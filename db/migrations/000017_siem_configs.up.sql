-- 000017: SIEM-конфигурация с сервера (чанк 89, пр. 2 «SIEM-конфиг через
-- ConfigPush»; п. 5.4 ТЗ). Куда агент пересылает EVE-алерты (syslog):
-- addr host:514, protocol udp|tcp, format cef|json. Резолв как у
-- capabilities: host-level приоритетнее cluster-level; нет записей —
-- сервер SIEM не задаёт (агент на локальном agent.yaml siem_*).
-- Ровно одна из колонок host_id/cluster_id заполнена (CHECK).
CREATE TABLE siem_configs (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    host_id     uuid REFERENCES hosts(id) ON DELETE CASCADE,
    cluster_id  uuid REFERENCES clusters(id) ON DELETE CASCADE,
    addr        text NOT NULL,
    protocol    text NOT NULL DEFAULT 'udp' CHECK (protocol IN ('udp', 'tcp')),
    format      text NOT NULL DEFAULT 'cef' CHECK (format IN ('cef', 'json')),
    updated_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    CHECK ((host_id IS NOT NULL)::int + (cluster_id IS NOT NULL)::int = 1)
);
CREATE UNIQUE INDEX idx_siem_configs_host ON siem_configs (host_id) WHERE host_id IS NOT NULL;
CREATE UNIQUE INDEX idx_siem_configs_cluster ON siem_configs (cluster_id) WHERE cluster_id IS NOT NULL;
COMMENT ON TABLE siem_configs IS 'SIEM-конфигурация пересылки EVE-алертов (чанк 89): host/cluster scope; пустой addr — явное выключение пересылки';
