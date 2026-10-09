-- 000014: история версий профилей конфигурации (чанк 82, спека
-- /config_profiles/{id}/versions + diff + rollback). Снимок content_yaml
-- создаётся при создании профиля (version=1) и при каждой смене
-- содержимого (PATCH с content_yaml → version+1); откат — новая версия
-- с содержимым целевой (история не переписывается).
CREATE TABLE config_profile_versions (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    profile_id  uuid NOT NULL REFERENCES config_profiles(id) ON DELETE CASCADE,
    version     integer NOT NULL,
    content_yaml text NOT NULL,
    created_by  text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (profile_id, version)
);
CREATE INDEX idx_config_profile_versions_profile ON config_profile_versions (profile_id, version DESC);
COMMENT ON TABLE config_profile_versions IS 'Снимки content_yaml профилей по версиям (чанк 82); created_by — email актора (NULL — до введения истории)';

-- Бэкфилл: текущее содержимое существующих профилей — их актуальная
-- версия (version >= 1); промежуточных версий до миграции не было.
INSERT INTO config_profile_versions (profile_id, version, content_yaml, created_by)
SELECT id, version, content_yaml, NULL FROM config_profiles;
