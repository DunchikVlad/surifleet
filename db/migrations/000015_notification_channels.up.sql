-- 000015: каналы уведомлений (чанк 83, пр. 2 roadmap; п. 5.5 ТЗ
-- «уведомления email/webhook/Telegram»). config jsonb по типу:
--   webhook:  {url, headers?}              (headers — доп. заголовки POST)
--   telegram: {bot_token, chat_id}         (bot_token — секрет, writeOnly)
-- Движок отправки с дедупликацией — следующий чанк.
CREATE TABLE notification_channels (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name            text NOT NULL,
    type            text NOT NULL CHECK (type IN ('webhook', 'telegram')),
    config          jsonb NOT NULL DEFAULT '{}',
    enabled         boolean NOT NULL DEFAULT true,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name)
);
CREATE INDEX idx_notification_channels_org ON notification_channels (organization_id, id);
COMMENT ON TABLE notification_channels IS 'Каналы уведомлений webhook/telegram (чанк 83); секреты в config (bot_token) — writeOnly в API';
