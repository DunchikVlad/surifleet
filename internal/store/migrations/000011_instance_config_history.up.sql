-- 000011: история применения конфигураций Suricata по инстансам (чанк 57, план 1B).
-- Запись — из hub при приёме TaskResult с DeployConfigResult (прямые
-- config-задачи); статусы: validated (validate_only ok), applied (ok),
-- validation_failed (suricata -T не пройден, откат), deploy_failed (иное).
CREATE TABLE instance_config_history (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    instance_id     uuid NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    config_version  text NOT NULL,
    status          text NOT NULL,
    validation_output text,
    reported_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_instance_config_history_inst ON instance_config_history (instance_id, reported_at DESC);
COMMENT ON TABLE instance_config_history IS 'История применения версий suricata.yaml по инстансам (deploy_config; чанк 57)';
