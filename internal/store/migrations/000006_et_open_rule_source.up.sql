-- Чанк 21: коннектор et_open — фид ПРАВИЛ Emerging Threats Open.
-- Правила, импортированные фидом et_open, помечаются source_type='et_open'
-- (до этого были 'file', 'feed', 'ioc').

ALTER TABLE rules DROP CONSTRAINT rules_source_type_check;
ALTER TABLE rules ADD CONSTRAINT rules_source_type_check
    CHECK (source_type IN ('file', 'feed', 'ioc', 'et_open'));
