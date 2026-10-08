-- 000012: профили конфигурации Suricata (чанк 64, план 1B).
-- Наследование кластер → хост → инстанс через parent_id (самоссылка);
-- рендер шаблона с переменными и версионирование изменений — следующие чанки.
CREATE TABLE config_profiles (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name            text NOT NULL,
    description     text,
    scope_type      text NOT NULL CHECK (scope_type IN ('cluster', 'host', 'instance')),
    scope_id        uuid NOT NULL,
    parent_id       uuid REFERENCES config_profiles(id) ON DELETE SET NULL,
    content_yaml    text NOT NULL,
    version         integer NOT NULL DEFAULT 1,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_config_profiles_org ON config_profiles (organization_id, id);
CREATE INDEX idx_config_profiles_scope ON config_profiles (scope_type, scope_id);
COMMENT ON TABLE config_profiles IS 'Профили suricata.yaml с наследованием кластер→хост→инстанс (план 1B; чанк 64 — CRUD, рендер переменных позже)';
