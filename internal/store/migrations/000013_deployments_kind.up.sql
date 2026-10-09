-- 000013: волновой деплой конфигураций — деплои получают вид (чанк 67,
-- план 1B). kind='rules' — прежнее поведение (ruleset_version_id),
-- kind='config' — деплой версии конфигурации (config_version_id,
-- задача deploy_config). API для создания config-деплоев — следующий чанк.
ALTER TABLE deployments
    ADD COLUMN kind text NOT NULL DEFAULT 'rules'
        CHECK (kind IN ('rules', 'config')),
    ADD COLUMN config_version_id uuid REFERENCES config_versions(id) ON DELETE RESTRICT;
-- ruleset_version_id обязателен только для rules-деплоев.
ALTER TABLE deployments ALTER COLUMN ruleset_version_id DROP NOT NULL;
COMMENT ON COLUMN deployments.kind IS 'Вид деплоя: rules | config (волновой; чанк 67)';
