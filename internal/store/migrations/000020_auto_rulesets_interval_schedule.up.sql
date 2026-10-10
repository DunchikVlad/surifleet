-- 000020: авто-ruleset'ы — интервальное расписание пересборки (чанк 97):
-- каждые N минут (N из schedule_interval_minutes). NULL — прежний режим
-- «ежедневно в schedule_time (HH:MM)».
ALTER TABLE auto_rulesets
    ADD COLUMN schedule_interval_minutes integer; -- NULL = ежедневно по schedule_time
