-- 000018: авто-обновляемые ruleset'ы (чанк 91) + происхождение правил.
-- origin: manual (UI/импорт файла), feed (фид-коннекторы), ioc (генератор
-- IOC), suriupdate (импорт suricata-update). Бэкфилл: feed у строк с
-- feed_id; остальные manual — исторические suriupdate-импорты на стендах
-- докручиваются разовым UPDATE (см. PROGRESS).
ALTER TABLE rules
    ADD COLUMN origin text NOT NULL DEFAULT 'manual'
        CHECK (origin IN ('manual', 'feed', 'ioc', 'suriupdate'));
UPDATE rules SET origin = 'feed' WHERE feed_id IS NOT NULL;

-- auto_rulesets — постоянно обновляемые наборы: состав по происхождению
-- (+фильтры тегов/категорий), запрет на деплой exclude_sids, таргетинг
-- агентов; пересборка = новая версия ruleset + волновой деплой.
CREATE TABLE auto_rulesets (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name            text NOT NULL,
    description     text,
    enabled         boolean NOT NULL DEFAULT true,
    include_suriupdate boolean NOT NULL DEFAULT true,
    include_ioc     boolean NOT NULL DEFAULT true,
    include_manual  boolean NOT NULL DEFAULT true,
    include_feeds   boolean NOT NULL DEFAULT false,
    include_tags    text[] NOT NULL DEFAULT '{}',
    include_categories text[] NOT NULL DEFAULT '{}',
    exclude_sids    bigint[] NOT NULL DEFAULT '{}',
    targeting       jsonb NOT NULL,
    batch_size      integer NOT NULL DEFAULT 50 CHECK (batch_size > 0),
    canary_size     integer NOT NULL DEFAULT 1 CHECK (canary_size >= 0),
    last_built_at   timestamptz,
    last_ruleset_version_id uuid REFERENCES ruleset_versions(id) ON DELETE SET NULL,
    last_deployment_id uuid REFERENCES deployments(id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name)
);
CREATE INDEX idx_auto_rulesets_org ON auto_rulesets (organization_id, id);
COMMENT ON TABLE auto_rulesets IS 'Авто-обновляемые ruleset''ы: suricata-update+IOC+manual, exclude_sids, таргетинг, пересборка по триггеру (чанк 91)';
