-- Миграция 000002: одноразовые join tokens для онбординга агентов (enrollment).
-- Сам токен НЕ хранится — только SHA-256 хэш (hex). Токен показывается
-- пользователю один раз в ответе POST /api/v1/clusters/{id}/join_tokens.

CREATE TABLE join_tokens (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    cluster_id      uuid NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    token_hash      text NOT NULL UNIQUE,        -- SHA-256 (hex) токена
    name            text NOT NULL DEFAULT '',    -- человекочитаемая метка (comment из API)
    expires_at      timestamptz NOT NULL,        -- TTL токена (по умолчанию 1 час)
    used_at         timestamptz,                 -- первое использование
    max_uses        int NOT NULL DEFAULT 1 CHECK (max_uses > 0),
    use_count       int NOT NULL DEFAULT 0 CHECK (use_count >= 0),
    created_by      uuid,                        -- user id; NULL в dev-режиме (заглушка auth)
    created_at      timestamptz NOT NULL DEFAULT now(),
    CHECK (use_count <= max_uses)
);
COMMENT ON TABLE join_tokens IS 'Одноразовые токены онбординга агентов кластера; хранится только SHA-256 хэш';

-- Выборка токенов кластера (GET list) и проверка по хэшу (UNIQUE выше).
CREATE INDEX idx_join_tokens_cluster ON join_tokens (cluster_id);
CREATE INDEX idx_join_tokens_expires ON join_tokens (expires_at) WHERE used_at IS NULL;
