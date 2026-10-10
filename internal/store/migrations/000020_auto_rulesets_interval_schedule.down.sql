-- 000020: откат — убрать интервальное расписание.
ALTER TABLE auto_rulesets
    DROP COLUMN schedule_interval_minutes;
