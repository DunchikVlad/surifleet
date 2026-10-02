-- 000009: OIDC-SSO (чанк 35) — одноразовые state/nonce для flow
-- Authorization Code + PKCE. Сами провайдеры — существующая таблица
-- sso_providers (000001), JIT-пользователи — users.external_id/provider_id.
CREATE TABLE oidc_states (
    state      text PRIMARY KEY,
    provider_id uuid NOT NULL REFERENCES sso_providers(id) ON DELETE CASCADE,
    nonce      text NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE oidc_states IS 'Одноразовые state+nonce OIDC-flow (защита от CSRF/replay); consume — DELETE … RETURNING';
CREATE INDEX idx_oidc_states_expiry ON oidc_states (expires_at);
