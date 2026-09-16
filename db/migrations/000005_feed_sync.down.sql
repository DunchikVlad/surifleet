-- Откат 000005: история запусков и last_error у фидов.

DROP TABLE IF EXISTS feed_runs;
ALTER TABLE feeds DROP COLUMN IF EXISTS last_error;
