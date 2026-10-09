-- откат 000013: возврат deployments к rules-only.
ALTER TABLE deployments ALTER COLUMN ruleset_version_id SET NOT NULL;
ALTER TABLE deployments DROP COLUMN config_version_id, DROP COLUMN kind;
