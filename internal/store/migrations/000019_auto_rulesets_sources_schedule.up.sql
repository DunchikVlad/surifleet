-- 000019: авто-ruleset'ы — выбор по источникам suricata-update,
-- расписание пересборки (чанк 95).
ALTER TABLE rules ADD COLUMN source_name text; -- имя источника suricata-update (et/open и др.), NULL — не из suricata-update

ALTER TABLE auto_rulesets
    ADD COLUMN include_sources text[] NOT NULL DEFAULT '{}',  -- пусто = все источники suricata-update
    ADD COLUMN schedule_enabled boolean NOT NULL DEFAULT false,
    ADD COLUMN schedule_time text;                            -- 'HH:MM' локального времени сервера
