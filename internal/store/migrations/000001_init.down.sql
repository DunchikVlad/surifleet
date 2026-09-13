-- ============================================================================
-- SuriFleet — миграция 000001 (down): полный откат начальной схемы
-- Порядок — обратный up-миграции: триггеры/функция → партиционированные
-- журналы → доменные таблицы → флот → идентичность → расширение.
-- DROP TABLE по партиционированному родителю удаляет все партиции.
-- ============================================================================

BEGIN;

-- Append-only триггеры и функция аудита
DROP TRIGGER IF EXISTS audit_log_no_truncate ON audit_log;
DROP TRIGGER IF EXISTS audit_log_no_update_delete ON audit_log;
DROP FUNCTION IF EXISTS forbid_append_only_mutation();

-- Партиционированные журналы (партиции удаляются вместе с родителем)
DROP TABLE IF EXISTS agent_state_history;
DROP TABLE IF EXISTS deploy_events;
DROP TABLE IF EXISTS audit_log;

-- Инциденты и деплой
DROP TABLE IF EXISTS incidents;
DROP TABLE IF EXISTS deployment_tasks;
DROP TABLE IF EXISTS deployments;

-- Состояние инстансов
DROP TABLE IF EXISTS instance_compliance;
DROP TABLE IF EXISTS actual_state;
DROP TABLE IF EXISTS desired_state;

-- Правила, фиды, IOC, шаблоны, ruleset
DROP TABLE IF EXISTS ruleset_versions;
DROP TABLE IF EXISTS deploy_templates;
DROP TABLE IF EXISTS iocs;
DROP TABLE IF EXISTS rule_revisions;
DROP TABLE IF EXISTS rules;
DROP TABLE IF EXISTS feeds;

-- Флот
DROP TABLE IF EXISTS capabilities;
DROP TABLE IF EXISTS instances;
DROP TABLE IF EXISTS agents;
DROP TABLE IF EXISTS hosts;
DROP TABLE IF EXISTS clusters;

-- Идентичность и RBAC
DROP TABLE IF EXISTS api_tokens;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS user_roles;
DROP TABLE IF EXISTS roles;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS sso_providers;
DROP TABLE IF EXISTS organizations;

DROP EXTENSION IF EXISTS pgcrypto;

COMMIT;
