-- Откат 000004: ioc-правила переводятся в 'feed' (история не теряется),
-- CHECK возвращается к исходному набору значений.

UPDATE rules SET source_type = 'feed' WHERE source_type = 'ioc';

ALTER TABLE rules DROP CONSTRAINT rules_source_type_check;
ALTER TABLE rules ADD CONSTRAINT rules_source_type_check
    CHECK (source_type IN ('file', 'feed'));
