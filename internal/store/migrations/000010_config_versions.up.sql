-- 000010: версии конфигураций Suricata (чанк 54, план 1B).
-- Контент — content-addressed блоб в S3 (sha256/s3_key), как ruleset;
-- version — cfg-v<N> автоинкремент per-org или произвольный тег.
CREATE TABLE config_versions (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    version         text NOT NULL,
    sha256          text NOT NULL,
    s3_key          text NOT NULL,
    note            text,
    created_by      uuid,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, version),
    UNIQUE (organization_id, sha256)
);
COMMENT ON TABLE config_versions IS 'Версии suricata.yaml под управлением SuriFleet (capability config): контент — блоб S3, деплой — задача deploy_config агенту';
CREATE INDEX idx_config_versions_org ON config_versions (organization_id, id);

-- Роль operator получает права на конфиги (встроенные роли сидированы
-- миграцией 000008; обновление содержимого builtin-роли — только миграцией).
UPDATE roles SET permissions = permissions || ARRAY['config.read','config.write']::text[]
WHERE name = 'operator' AND is_builtin AND NOT (permissions @> ARRAY['config.read']::text[]);
