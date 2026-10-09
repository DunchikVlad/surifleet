-- 000016: дедупликация уведомлений (чанк 84, пр. 2 «уведомления с
-- дедупликацией», п. 7 ТЗ). Одна строка на (канал, fingerprint события):
-- fingerprint = «тип:object_id» (agent.offline:<agent_id>). Повторная
-- отправка того же события в окне дедупликации подавляется.
CREATE TABLE notification_deliveries (
    channel_id  uuid NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    fingerprint text NOT NULL,
    last_sent_at timestamptz NOT NULL DEFAULT now(),
    send_count  integer NOT NULL DEFAULT 1,
    last_error  text,
    PRIMARY KEY (channel_id, fingerprint)
);
COMMENT ON TABLE notification_deliveries IS 'Состояние дедупликации отправленных уведомлений (чанк 84): последняя отправка события fingerprint в канал';
