-- Откат 000007: et_pro-правила переводятся в 'feed' (история не теряется),
-- CHECK возвращается к набору значений 000006.

UPDATE rules SET source_type = 'feed' WHERE source_type = 'et_pro';

ALTER TABLE rules DROP CONSTRAINT rules_source_type_check;
ALTER TABLE rules ADD CONSTRAINT rules_source_type_check
    CHECK (source_type IN ('file', 'feed', 'ioc', 'et_open'));
