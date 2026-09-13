-- ============================================================================
-- SuriFleet — миграция 000001: начальная схема БД
-- PostgreSQL 15+, golang-migrate
-- Модель флота: organizations → clusters → hosts → instances (1..N на хост),
-- на хосте ровно один agent. Enum-подобные поля — text + CHECK (без CREATE TYPE).
-- ============================================================================

BEGIN;

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ---------------------------------------------------------------------------
-- 1. Тенанты, идентичность, RBAC
-- ---------------------------------------------------------------------------

CREATE TABLE organizations (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL,
    slug        text NOT NULL UNIQUE,
    description text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE organizations IS 'Организации (тенанты) — верхний уровень изоляции данных флота';

CREATE TABLE sso_providers (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id    uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name               text NOT NULL,
    type               text NOT NULL CHECK (type IN ('oidc', 'saml', 'ldap')),
    config             jsonb NOT NULL DEFAULT '{}',
    secret_ref         text,
    group_role_mapping jsonb NOT NULL DEFAULT '{}',
    enabled            boolean NOT NULL DEFAULT true,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name)
);
COMMENT ON TABLE sso_providers IS 'SSO-провайдеры организации (OIDC/SAML/LDAP); секреты — по ссылке secret_ref, не в config';
COMMENT ON COLUMN sso_providers.group_role_mapping IS 'Маппинг групп IdP → роли системы: {"<группа IdP>": ["<role_uuid>", ...], приоритеты — порядком}';

CREATE TABLE users (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    external_id     text,
    provider_id     uuid REFERENCES sso_providers(id) ON DELETE SET NULL,
    email           text NOT NULL,
    display_name    text NOT NULL,
    password_hash   text,
    is_break_glass  boolean NOT NULL DEFAULT false,
    is_active       boolean NOT NULL DEFAULT true,
    last_login_at   timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, email)
);
COMMENT ON TABLE users IS 'Пользователи: локальные (password_hash) и JIT из SSO (external_id + provider_id); break-glass — локальный админ вне SSO';
CREATE UNIQUE INDEX uq_users_provider_external ON users (provider_id, external_id)
    WHERE provider_id IS NOT NULL AND external_id IS NOT NULL;
CREATE INDEX idx_users_org_id ON users (organization_id, id);

CREATE TABLE roles (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid REFERENCES organizations(id) ON DELETE CASCADE,
    name            text NOT NULL,
    description     text,
    permissions     text[] NOT NULL DEFAULT '{}',
    is_builtin      boolean NOT NULL DEFAULT false,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE roles IS 'Роли: встроенные (organization_id IS NULL: admin/operator/analyst/viewer) и кастомные; permissions — строки вида rules.read';
-- Встроенные роли глобально уникальны по имени, кастомные — в рамках организации
CREATE UNIQUE INDEX uq_roles_builtin_name ON roles (name) WHERE organization_id IS NULL;
CREATE UNIQUE INDEX uq_roles_org_name ON roles (organization_id, name) WHERE organization_id IS NOT NULL;

CREATE TABLE user_roles (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id     uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    scope_type  text NOT NULL DEFAULT 'organization' CHECK (scope_type IN ('organization', 'clusters')),
    cluster_ids uuid[] NOT NULL DEFAULT '{}',
    granted_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, role_id, scope_type, cluster_ids),
    CHECK (scope_type = 'organization' OR cardinality(cluster_ids) > 0)
);
COMMENT ON TABLE user_roles IS 'Назначение ролей пользователям со scoping: вся организация или набор кластеров';
CREATE INDEX idx_user_roles_user ON user_roles (user_id);
CREATE INDEX idx_user_roles_role ON user_roles (role_id);

CREATE TABLE sessions (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id            uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    refresh_token_hash text NOT NULL UNIQUE,
    user_agent         text,
    ip                 inet,
    expires_at         timestamptz NOT NULL,
    revoked_at         timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE sessions IS 'Сессии: refresh-токены (хранится хэш), отзыв через revoked_at, контекст UA/IP';
CREATE INDEX idx_sessions_user_id ON sessions (user_id, id);
CREATE INDEX idx_sessions_active_expiry ON sessions (expires_at) WHERE revoked_at IS NULL;

CREATE TABLE api_tokens (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id         uuid REFERENCES users(id) ON DELETE CASCADE,
    name            text NOT NULL,
    token_hash      text NOT NULL UNIQUE,
    scopes          text[] NOT NULL DEFAULT '{}',
    expires_at      timestamptz,
    last_used_at    timestamptz,
    revoked_at      timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name)
);
COMMENT ON TABLE api_tokens IS 'API-токены для автоматизации: scopes, срок жизни, хранится только хэш токена; user_id NULL — сервисный токен';
CREATE INDEX idx_api_tokens_user ON api_tokens (user_id) WHERE user_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- 2. Флот: кластеры, хосты, агенты, инстансы, capabilities
-- ---------------------------------------------------------------------------

CREATE TABLE clusters (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name            text NOT NULL,
    description     text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name)
);
COMMENT ON TABLE clusters IS 'Кластеры — логические группы хостов организации (площадка, сегмент сети)';
CREATE INDEX idx_clusters_org_id ON clusters (organization_id, id);

CREATE TABLE hosts (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id   uuid NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    hostname     text NOT NULL,
    ip_addresses inet[] NOT NULL DEFAULT '{}',
    os           text,
    labels       jsonb NOT NULL DEFAULT '{}',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, hostname)
);
COMMENT ON TABLE hosts IS 'Хосты — машины с агентом SuriFleet; на хосте 1..N инстансов Suricata';
CREATE INDEX idx_hosts_cluster_id ON hosts (cluster_id, id);

CREATE TABLE agents (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    host_id          uuid NOT NULL UNIQUE REFERENCES hosts(id) ON DELETE CASCADE,
    agent_version    text,
    protocol_version text,
    status           text NOT NULL DEFAULT 'offline'
                     CHECK (status IN ('online', 'degraded', 'offline', 'updating', 'error')),
    last_seen_at     timestamptz,
    cert_serial      text,
    cert_expires_at  timestamptz,
    enrolled_at      timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE agents IS 'Агенты SuriFleet — ровно один на хост (UNIQUE host_id); идентичность — mTLS-сертификат (CN = id, cert_serial)';
CREATE INDEX idx_agents_status ON agents (status);
CREATE INDEX idx_agents_last_seen ON agents (last_seen_at);

CREATE TABLE instances (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    host_id            uuid NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    name               text NOT NULL,
    config_path        text NOT NULL,
    rules_dir          text NOT NULL,
    log_dir            text NOT NULL,
    capture_interfaces text[] NOT NULL DEFAULT '{}',
    suricata_version   text,
    systemd_unit       text,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (host_id, name)
);
COMMENT ON TABLE instances IS 'Инстансы Suricata на хосте (1..N): свой suricata.yaml, каталоги правил/логов, интерфейсы захвата, systemd-юнит';
CREATE INDEX idx_instances_host_id ON instances (host_id, id);

CREATE TABLE capabilities (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id uuid REFERENCES clusters(id) ON DELETE CASCADE,
    host_id    uuid REFERENCES hosts(id) ON DELETE CASCADE,
    capability text NOT NULL
               CHECK (capability IN ('monitoring', 'rules', 'log_rotation', 'service_mgmt', 'packages', 'config')),
    enabled    boolean NOT NULL DEFAULT true,
    updated_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (num_nonnulls(cluster_id, host_id) = 1)
);
COMMENT ON TABLE capabilities IS 'Поэтапная передача контроля: 6 capability с гранулярностью кластер/хост (ровно один из cluster_id/host_id); дефолт онбординга — только monitoring';
CREATE UNIQUE INDEX uq_capabilities_cluster ON capabilities (cluster_id, capability) WHERE cluster_id IS NOT NULL;
CREATE UNIQUE INDEX uq_capabilities_host ON capabilities (host_id, capability) WHERE host_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- 3. Правила, ревизии, фиды, IOC
-- ---------------------------------------------------------------------------

CREATE TABLE feeds (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name             text NOT NULL,
    type             text NOT NULL CHECK (type IN ('et_open', 'et_pro', 'taxii', 'stix', 'misp', 'generic')),
    url              text NOT NULL,
    schedule         text,
    credentials_ref  text,
    enabled          boolean NOT NULL DEFAULT true,
    last_sync_at     timestamptz,
    last_sync_status text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name)
);
COMMENT ON TABLE feeds IS 'Фиды правил и IOC (ET Open/Pro, TAXII, STIX, MISP, generic); schedule — cron; креды — ссылка на секрет credentials_ref';

CREATE TABLE rules (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    sid             bigint NOT NULL,
    msg             text,
    category        text,
    tags            text[] NOT NULL DEFAULT '{}',
    status          text NOT NULL DEFAULT 'under_review'
                    CHECK (status IN ('enabled', 'disabled', 'expired', 'under_review', 'deleted')),
    priority        integer,
    threshold       jsonb,
    source_type     text NOT NULL DEFAULT 'file' CHECK (source_type IN ('file', 'feed')),
    feed_id         uuid REFERENCES feeds(id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, sid)
);
COMMENT ON TABLE rules IS 'Мастер-репозиторий правил (по sid в рамках организации); priority/threshold — тюнинг аналитика, сохраняется между ревизиями фида';
CREATE INDEX idx_rules_org_status_id ON rules (organization_id, status, id);
CREATE INDEX idx_rules_org_category ON rules (organization_id, category);
CREATE INDEX idx_rules_tags ON rules USING gin (tags);

CREATE TABLE rule_revisions (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    rule_id     uuid NOT NULL REFERENCES rules(id) ON DELETE CASCADE,
    sid         bigint NOT NULL,
    revision    integer NOT NULL,
    raw         text NOT NULL,
    hash        text NOT NULL,
    parsed      jsonb NOT NULL DEFAULT '{}',
    imported_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (rule_id, revision)
);
COMMENT ON TABLE rule_revisions IS 'История версий правила: sid + revision + raw-текст + sha256-хэш; parsed — разобранные поля (classtype, reference, ...)';
CREATE INDEX idx_rule_revisions_sid_rev ON rule_revisions (sid, revision);
CREATE INDEX idx_rule_revisions_hash ON rule_revisions (hash);

CREATE TABLE iocs (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    type            text NOT NULL CHECK (type IN ('ip', 'domain', 'url', 'md5', 'sha1', 'sha256', 'email')),
    value           text NOT NULL,
    score           integer NOT NULL DEFAULT 0,
    feed_id         uuid REFERENCES feeds(id) ON DELETE SET NULL,
    source          text,
    status          text NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active', 'under_review', 'expired', 'revoked')),
    expires_at      timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, type, value)
);
COMMENT ON TABLE iocs IS 'IOC: тип, значение, скоринг, источник (фид), автоистечение по expires_at, жизненный цикл status';
CREATE INDEX idx_iocs_active_expiry ON iocs (expires_at) WHERE status = 'active' AND expires_at IS NOT NULL;

-- ---------------------------------------------------------------------------
-- 4. Деплой: шаблоны, версии ruleset, desired/actual state, compliance
-- ---------------------------------------------------------------------------

CREATE TABLE deploy_templates (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name            text NOT NULL,
    description     text,
    targeting       jsonb NOT NULL,
    created_by      uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name)
);
COMMENT ON TABLE deploy_templates IS 'Шаблоны деплоя: targeting — JSONB-спецификация (mode: all_clusters | selected_clusters | all_except_clusters | specific_hosts | specific_instances + списки id)';

CREATE TABLE ruleset_versions (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    version         text NOT NULL,
    sha256          text NOT NULL,
    s3_key          text NOT NULL,
    manifest        jsonb NOT NULL DEFAULT '{}',
    rule_count      integer NOT NULL DEFAULT 0,
    created_by      uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, version),
    UNIQUE (organization_id, sha256)
);
COMMENT ON TABLE ruleset_versions IS 'Версии ruleset: sha256 — content-addressed блоб в S3 (s3_key), manifest — состав (ссылки на правила/ревизии)';

CREATE TABLE desired_state (
    instance_id        uuid PRIMARY KEY REFERENCES instances(id) ON DELETE CASCADE,
    ruleset_version_id uuid NOT NULL REFERENCES ruleset_versions(id) ON DELETE RESTRICT,
    computed_rules     jsonb NOT NULL DEFAULT '[]',
    calc_version       bigint NOT NULL DEFAULT 1,
    updated_at         timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE desired_state IS 'Целевое состояние инстанса (одна строка на инстанс): ruleset_version + рассчитанный набор [{sid,rev,status}] + монотонная версия расчёта';
CREATE INDEX idx_desired_state_ruleset ON desired_state (ruleset_version_id);

CREATE TABLE actual_state (
    instance_id        uuid PRIMARY KEY REFERENCES instances(id) ON DELETE CASCADE,
    ruleset_hash       text,
    loaded_rules       jsonb NOT NULL DEFAULT '[]',
    failed_rules       jsonb NOT NULL DEFAULT '[]',
    last_reload_result jsonb,
    reported_at        timestamptz,
    updated_at         timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE actual_state IS 'Фактическое состояние инстанса (последний отчёт агента): хэш ruleset на диске, загруженные sid+rev, failed rules с текстом ошибки, результат reload/restart, reported_at — «данные актуальны на»';
CREATE INDEX idx_actual_state_reported ON actual_state (reported_at);

CREATE TABLE instance_compliance (
    instance_id uuid PRIMARY KEY REFERENCES instances(id) ON DELETE CASCADE,
    status      text NOT NULL DEFAULT 'pending'
                CHECK (status IN ('in_sync', 'pending', 'partial', 'drift', 'stale')),
    details     jsonb NOT NULL DEFAULT '{}',
    updated_at  timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE instance_compliance IS 'Денормализованная текущая сводка соответствия (одна строка на инстанс); пересчёт инкрементальный по событиям; details — diff/failed';
CREATE INDEX idx_instance_compliance_status ON instance_compliance (status, instance_id);

-- ---------------------------------------------------------------------------
-- 5. Волновой деплой
-- ---------------------------------------------------------------------------

CREATE TABLE deployments (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id    uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    ruleset_version_id uuid NOT NULL REFERENCES ruleset_versions(id) ON DELETE RESTRICT,
    deploy_template_id uuid REFERENCES deploy_templates(id) ON DELETE SET NULL,
    targeting          jsonb NOT NULL,
    batch_size         integer NOT NULL DEFAULT 50 CHECK (batch_size > 0),
    concurrency        integer NOT NULL DEFAULT 10 CHECK (concurrency > 0),
    canary_size        integer NOT NULL DEFAULT 1 CHECK (canary_size >= 0),
    status             text NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending', 'running', 'paused', 'completed', 'failed', 'cancelled')),
    initiated_by       uuid REFERENCES users(id) ON DELETE SET NULL,
    started_at         timestamptz,
    paused_at          timestamptz,
    finished_at        timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE deployments IS 'Волновой деплой ruleset: таргетинг, параметры волн (batch/concurrency/canary), статус, инициатор; pause/resume через status';
CREATE INDEX idx_deployments_org_id ON deployments (organization_id, id);
CREATE INDEX idx_deployments_active ON deployments (status) WHERE status IN ('pending', 'running', 'paused');

CREATE TABLE deployment_tasks (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    instance_id   uuid NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    wave          integer NOT NULL DEFAULT 0,
    status        text NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending', 'sent', 'running', 'succeeded', 'failed', 'cancelled', 'skipped')),
    attempts      integer NOT NULL DEFAULT 0,
    max_attempts  integer NOT NULL DEFAULT 3,
    result        jsonb,
    error         text,
    started_at    timestamptz,
    finished_at   timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (deployment_id, instance_id)
);
COMMENT ON TABLE deployment_tasks IS 'Задача деплоя на один инстанс (task_id = id); успех — только по подтверждению агента о фактической загрузке правил';
CREATE INDEX idx_deployment_tasks_deployment_status ON deployment_tasks (deployment_id, status);
CREATE INDEX idx_deployment_tasks_instance_id ON deployment_tasks (instance_id, id);

-- ---------------------------------------------------------------------------
-- 6. Инциденты
-- ---------------------------------------------------------------------------

CREATE TABLE incidents (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    cluster_id      uuid REFERENCES clusters(id) ON DELETE SET NULL,
    host_id         uuid REFERENCES hosts(id) ON DELETE SET NULL,
    instance_id     uuid REFERENCES instances(id) ON DELETE SET NULL,
    agent_id        uuid REFERENCES agents(id) ON DELETE SET NULL,
    type            text NOT NULL,
    severity        text NOT NULL CHECK (severity IN ('info', 'warning', 'critical')),
    status          text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'ack', 'resolved')),
    fingerprint     text NOT NULL,
    context         jsonb NOT NULL DEFAULT '{}',
    recommendation  text,
    first_seen_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at    timestamptz NOT NULL DEFAULT now(),
    resolved_at     timestamptz,
    resolved_by     uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE incidents IS 'Инциденты агентов/флота: тип, severity, контекст, рекомендация; дедупликация — fingerprint уникален среди нерешённых';
-- Дедупликация: один открытый/подтверждённый инцидент на fingerprint
CREATE UNIQUE INDEX uq_incidents_open_fingerprint ON incidents (fingerprint) WHERE status <> 'resolved';
CREATE INDEX idx_incidents_org_status_id ON incidents (organization_id, status, id);
CREATE INDEX idx_incidents_open_host ON incidents (host_id) WHERE status <> 'resolved';

-- ---------------------------------------------------------------------------
-- 7. Партиционированные журналы: audit_log, deploy_events, agent_state_history
--    Месячные RANGE-партиции по created_at; PK включает колонку партиционирования.
-- ---------------------------------------------------------------------------

CREATE TABLE audit_log (
    id                 uuid NOT NULL DEFAULT gen_random_uuid(),
    created_at         timestamptz NOT NULL DEFAULT now(),
    organization_id    uuid,
    actor_type         text NOT NULL CHECK (actor_type IN ('user', 'api_token', 'system', 'agent')),
    actor_user_id      uuid,
    actor_session_id   uuid,
    actor_api_token_id uuid,
    actor_name         text,
    ip                 inet,
    user_agent         text,
    action             text NOT NULL,
    object_type        text,
    object_id          uuid,
    object_name        text,
    params             jsonb,
    result             text NOT NULL CHECK (result IN ('success', 'denied', 'error')),
    reason             text,
    diff               jsonb,
    prev_hash          text,
    hash               text,
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);
COMMENT ON TABLE audit_log IS 'Append-only аудит всех действий (UI/API): кто/откуда/что/когда/результат/diff; цепочка хэшей prev_hash→hash; FK намеренно отсутствуют — записи переживают удаление акторов';
COMMENT ON COLUMN audit_log.diff IS 'Diff изменений: {"before": {...}, "after": {...}}';
COMMENT ON COLUMN audit_log.prev_hash IS 'SHA-256 хэш предыдущей записи цепочки (опция audit.hash_chain)';

CREATE TABLE audit_log_2026_09 PARTITION OF audit_log
    FOR VALUES FROM ('2026-09-01 00:00:00+00') TO ('2026-10-01 00:00:00+00');
CREATE TABLE audit_log_2026_10 PARTITION OF audit_log
    FOR VALUES FROM ('2026-10-01 00:00:00+00') TO ('2026-11-01 00:00:00+00');
CREATE TABLE audit_log_2026_11 PARTITION OF audit_log
    FOR VALUES FROM ('2026-11-01 00:00:00+00') TO ('2026-12-01 00:00:00+00');
CREATE TABLE audit_log_2026_12 PARTITION OF audit_log
    FOR VALUES FROM ('2026-12-01 00:00:00+00') TO ('2027-01-01 00:00:00+00');
CREATE TABLE audit_log_default PARTITION OF audit_log DEFAULT;

CREATE INDEX idx_audit_log_created_id ON audit_log (created_at, id);
CREATE INDEX idx_audit_log_actor ON audit_log (actor_user_id, created_at);
CREATE INDEX idx_audit_log_org_created ON audit_log (organization_id, created_at);
CREATE INDEX idx_audit_log_object ON audit_log (object_type, object_id, created_at);

-- Append-only: запрет UPDATE/DELETE/TRUNCATE на уровне БД (даже для роли приложения)
CREATE OR REPLACE FUNCTION forbid_append_only_mutation() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'table % is append-only: % is forbidden', TG_TABLE_NAME, TG_OP;
END;
$$;

CREATE TRIGGER audit_log_no_update_delete
    BEFORE UPDATE OR DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION forbid_append_only_mutation();

CREATE TRIGGER audit_log_no_truncate
    BEFORE TRUNCATE ON audit_log
    FOR EACH STATEMENT EXECUTE FUNCTION forbid_append_only_mutation();

CREATE TABLE deploy_events (
    id                 uuid NOT NULL DEFAULT gen_random_uuid(),
    created_at         timestamptz NOT NULL DEFAULT now(),
    deployment_id      uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    deployment_task_id uuid REFERENCES deployment_tasks(id) ON DELETE SET NULL,
    instance_id        uuid REFERENCES instances(id) ON DELETE SET NULL,
    event_type         text NOT NULL,
    message            text,
    details            jsonb NOT NULL DEFAULT '{}',
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);
COMMENT ON TABLE deploy_events IS 'События деплоев (создание, волны, pause/resume, результаты задач, откаты) — история для страницы деплоя';

CREATE TABLE deploy_events_2026_09 PARTITION OF deploy_events
    FOR VALUES FROM ('2026-09-01 00:00:00+00') TO ('2026-10-01 00:00:00+00');
CREATE TABLE deploy_events_2026_10 PARTITION OF deploy_events
    FOR VALUES FROM ('2026-10-01 00:00:00+00') TO ('2026-11-01 00:00:00+00');
CREATE TABLE deploy_events_2026_11 PARTITION OF deploy_events
    FOR VALUES FROM ('2026-11-01 00:00:00+00') TO ('2026-12-01 00:00:00+00');
CREATE TABLE deploy_events_2026_12 PARTITION OF deploy_events
    FOR VALUES FROM ('2026-12-01 00:00:00+00') TO ('2027-01-01 00:00:00+00');
CREATE TABLE deploy_events_default PARTITION OF deploy_events DEFAULT;

CREATE INDEX idx_deploy_events_deployment ON deploy_events (deployment_id, created_at);
CREATE INDEX idx_deploy_events_created_id ON deploy_events (created_at, id);

CREATE TABLE agent_state_history (
    id              uuid NOT NULL DEFAULT gen_random_uuid(),
    created_at      timestamptz NOT NULL DEFAULT now(),
    agent_id        uuid NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    previous_status text CHECK (previous_status IN ('online', 'degraded', 'offline', 'updating', 'error')),
    status          text NOT NULL CHECK (status IN ('online', 'degraded', 'offline', 'updating', 'error')),
    details         jsonb NOT NULL DEFAULT '{}',
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);
COMMENT ON TABLE agent_state_history IS 'История смен состояний агентов (online→offline и т.п.); heartbeat в PG не пишется — только смены состояния';

CREATE TABLE agent_state_history_2026_09 PARTITION OF agent_state_history
    FOR VALUES FROM ('2026-09-01 00:00:00+00') TO ('2026-10-01 00:00:00+00');
CREATE TABLE agent_state_history_2026_10 PARTITION OF agent_state_history
    FOR VALUES FROM ('2026-10-01 00:00:00+00') TO ('2026-11-01 00:00:00+00');
CREATE TABLE agent_state_history_2026_11 PARTITION OF agent_state_history
    FOR VALUES FROM ('2026-11-01 00:00:00+00') TO ('2026-12-01 00:00:00+00');
CREATE TABLE agent_state_history_2026_12 PARTITION OF agent_state_history
    FOR VALUES FROM ('2026-12-01 00:00:00+00') TO ('2027-01-01 00:00:00+00');
CREATE TABLE agent_state_history_default PARTITION OF agent_state_history DEFAULT;

CREATE INDEX idx_agent_state_history_agent ON agent_state_history (agent_id, created_at);
CREATE INDEX idx_agent_state_history_created_id ON agent_state_history (created_at, id);

COMMIT;
