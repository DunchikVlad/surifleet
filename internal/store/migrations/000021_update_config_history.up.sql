-- 000021: история применения конфигов suricata-update по инстансам
-- (чанк 113, backlog заказчика). Запись — из hub при приёме TaskResult с
-- DeployUpdateConfigResult; статусы: applied (ok/unchanged), failed.
-- Содержимое версий не дублируем — оно на сенсоре (текущее) и в бэкапах
-- .surifleet-bak-TS; здесь — кто/когда/какой файл/sha/какой бэкап.
CREATE TABLE update_config_history (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    instance_id uuid NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    file        text NOT NULL,              -- update_yaml | enable_conf | disable_conf | modify_conf | source_yaml:<name>
    path        text NOT NULL,              -- канонический путь на сенсоре
    sha256      text NOT NULL,              -- хэш записанного содержимого
    size_bytes  bigint NOT NULL,
    backup      text,                       -- имя файла бэкапа (NULL — исходника не было)
    unchanged   boolean NOT NULL DEFAULT false,
    status      text NOT NULL CHECK (status IN ('applied', 'failed')),
    error       text,
    created_by  text,                       -- email автора (из identity сервера)
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_update_config_history_inst ON update_config_history (instance_id, created_at DESC);
COMMENT ON TABLE update_config_history IS 'История применения конфигов suricata-update по инстансам (чанк 113)';
