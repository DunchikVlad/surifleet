-- Чанк 18: синхронизация IOC-фидов.
-- feeds.last_error — текст последней ошибки синхронизации (NULL при успехе);
-- feed_runs — история запусков синхронизации (ручных и по расписанию).

ALTER TABLE feeds ADD COLUMN last_error text;

CREATE TABLE feed_runs (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    feed_id     uuid NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
    status      text NOT NULL CHECK (status IN ('running', 'success', 'failed')),
    started_at  timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    imported    integer NOT NULL DEFAULT 0,
    updated     integer NOT NULL DEFAULT 0,
    skipped     integer NOT NULL DEFAULT 0,
    error       text
);
COMMENT ON TABLE feed_runs IS 'История запусков синхронизации фидов: статус, счётчики imported/updated/skipped, текст ошибки';
CREATE INDEX idx_feed_runs_feed_started ON feed_runs (feed_id, started_at DESC, id DESC);
