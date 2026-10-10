-- откат 000019.
ALTER TABLE auto_rulesets DROP COLUMN IF EXISTS schedule_time, DROP COLUMN IF EXISTS schedule_enabled, DROP COLUMN IF EXISTS include_sources;
ALTER TABLE rules DROP COLUMN IF EXISTS source_name;
