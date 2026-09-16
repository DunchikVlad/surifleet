-- Чанк 22: коннектор et_pro — фид ПРАВИЛ Emerging Threats Pro.
-- Правила, импортированные фидом et_pro, помечаются source_type='et_pro'
-- (до этого были 'file', 'feed', 'ioc', 'et_open').

ALTER TABLE rules DROP CONSTRAINT rules_source_type_check;
ALTER TABLE rules ADD CONSTRAINT rules_source_type_check
    CHECK (source_type IN ('file', 'feed', 'ioc', 'et_open', 'et_pro'));
