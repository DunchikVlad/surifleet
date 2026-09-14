-- Откат 000003_discovery.
ALTER TABLE hosts
    DROP COLUMN IF EXISTS discovery,
    DROP COLUMN IF EXISTS discovered_at;
