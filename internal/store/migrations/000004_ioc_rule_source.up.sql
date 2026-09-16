-- Чанк 17: генерация Suricata-правил из IOC.
-- Правила, сгенерированные из IOC, помечаются source_type='ioc'
-- (до этого были только 'file' и 'feed').

ALTER TABLE rules DROP CONSTRAINT rules_source_type_check;
ALTER TABLE rules ADD CONSTRAINT rules_source_type_check
    CHECK (source_type IN ('file', 'feed', 'ioc'));
