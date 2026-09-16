-- Откат 000006: et_open-правила переводятся в 'feed' (история не теряется),
-- CHECK возвращается к набору значений 000004.

UPDATE rules SET source_type = 'feed' WHERE source_type = 'et_open';

ALTER TABLE rules DROP CONSTRAINT rules_source_type_check;
ALTER TABLE rules ADD CONSTRAINT rules_source_type_check
    CHECK (source_type IN ('file', 'feed', 'ioc'));
