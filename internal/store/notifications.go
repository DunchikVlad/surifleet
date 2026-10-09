package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NotificationChannel — канал уведомлений (таблица notification_channels,
// миграция 000015, чанк 83). Config — jsonb по типу:
//
//	webhook:  {url, headers?}
//	telegram: {bot_token, chat_id}   (bot_token — секрет, см. Public)
type NotificationChannel struct {
	ID             uuid.UUID       `json:"id"`
	OrganizationID uuid.UUID       `json:"organization_id"`
	Name           string          `json:"name"`
	Type           string          `json:"type"` // webhook | telegram
	Config         json.RawMessage `json:"config"`
	Enabled        bool            `json:"enabled"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// Public — представление наружу: секретные поля config (bot_token)
// обнуляются (writeOnly по образцу SSO client_secret).
func (c NotificationChannel) Public() NotificationChannel {
	var m map[string]any
	if err := json.Unmarshal(c.Config, &m); err == nil {
		delete(m, "bot_token")
		if raw, err := json.Marshal(m); err == nil {
			c.Config = raw
		}
	}
	return c
}

// BotToken — секретный токен telegram-канала (для отправки; наружу
// не отдаётся).
func (c NotificationChannel) BotToken() string {
	var v struct {
		BotToken string `json:"bot_token"`
	}
	_ = json.Unmarshal(c.Config, &v)
	return v.BotToken
}

// NotificationChannelsRepo — каналы уведомлений.
type NotificationChannelsRepo struct {
	pool *pgxpool.Pool
}

const notificationChannelColumns = `id, organization_id, name, type, config, enabled, created_at, updated_at`

func scanNotificationChannel(row pgx.Row) (NotificationChannel, error) {
	var c NotificationChannel
	err := row.Scan(&c.ID, &c.OrganizationID, &c.Name, &c.Type, &c.Config, &c.Enabled, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

// Create — новый канал. Дубль (org, name) → ErrConflict.
func (r *NotificationChannelsRepo) Create(ctx context.Context, orgID uuid.UUID, name, typ string, config json.RawMessage, enabled bool) (NotificationChannel, error) {
	c, err := scanNotificationChannel(r.pool.QueryRow(ctx,
		`INSERT INTO notification_channels (organization_id, name, type, config, enabled)
		 VALUES ($1, $2, $3, $4, $5) RETURNING `+notificationChannelColumns,
		orgID, name, typ, config, enabled))
	if err != nil {
		return c, translate(err)
	}
	return c, nil
}

// Get — канал по id. Нет записи → ErrNotFound.
func (r *NotificationChannelsRepo) Get(ctx context.Context, id uuid.UUID) (NotificationChannel, error) {
	c, err := scanNotificationChannel(r.pool.QueryRow(ctx,
		`SELECT `+notificationChannelColumns+` FROM notification_channels WHERE id = $1`, id))
	if err != nil {
		return c, translate(err)
	}
	return c, nil
}

// List — keyset-листинг каналов организации.
func (r *NotificationChannelsRepo) List(ctx context.Context, orgID uuid.UUID, cursor uuid.UUID, limit int) ([]NotificationChannel, *string, error) {
	var cursorArg *uuid.UUID
	if cursor != uuid.Nil {
		cursorArg = &cursor
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+notificationChannelColumns+` FROM notification_channels
		 WHERE organization_id = $1 AND ($2::uuid IS NULL OR id > $2)
		 ORDER BY id LIMIT $3`, orgID, cursorArg, limit+1)
	if err != nil {
		return nil, nil, translate(err)
	}
	defer rows.Close()
	items := []NotificationChannel{}
	for rows.Next() {
		var c NotificationChannel
		if err := rows.Scan(&c.ID, &c.OrganizationID, &c.Name, &c.Type, &c.Config, &c.Enabled, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, nil, translate(err)
		}
		items = append(items, c)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, translate(err)
	}
	var next *string
	if len(items) > limit {
		s := items[limit-1].ID.String()
		next = &s
		items = items[:limit]
	}
	return items, next, nil
}

// Update — частичное обновление (name/config/enabled); nil — «не менять».
// Нет записи → ErrNotFound.
func (r *NotificationChannelsRepo) Update(ctx context.Context, id uuid.UUID, name *string, config json.RawMessage, enabled *bool) (NotificationChannel, error) {
	var configArg any
	if len(config) > 0 {
		configArg = config
	}
	c, err := scanNotificationChannel(r.pool.QueryRow(ctx,
		`UPDATE notification_channels SET
		   name = COALESCE($2, name),
		   config = COALESCE($3, config),
		   enabled = COALESCE($4, enabled),
		   updated_at = now()
		 WHERE id = $1 RETURNING `+notificationChannelColumns,
		id, name, configArg, enabled))
	if err != nil {
		return c, translate(err)
	}
	return c, nil
}

// Delete — жёсткое удаление канала.
func (r *NotificationChannelsRepo) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM notification_channels WHERE id = $1`, id)
	return translate(err)
}

// ListEnabled — все включённые каналы организации (движок уведомлений,
// чанк 84). Порядок детерминирован (по id).
func (r *NotificationChannelsRepo) ListEnabled(ctx context.Context, orgID uuid.UUID) ([]NotificationChannel, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+notificationChannelColumns+` FROM notification_channels
		 WHERE organization_id = $1 AND enabled ORDER BY id`, orgID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	items := []NotificationChannel{}
	for rows.Next() {
		var c NotificationChannel
		if err := rows.Scan(&c.ID, &c.OrganizationID, &c.Name, &c.Type, &c.Config, &c.Enabled, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, translate(err)
		}
		items = append(items, c)
	}
	return items, translate(rows.Err())
}

// TryDelivery — атомарная попытка отправки события fingerprint в канал с
// дедупликацией (чанк 84): если последняя отправка была в пределах
// window — возвращает false (подавить); иначе фиксирует отправку
// (upsert: last_sent_at=now(), send_count++, last_error=NULL) и
// возвращает true. Фиксация ДО фактической отправки — при гонке двух
// эмиттеров второй подавляется (атомарность INSERT .. ON CONFLICT
// гарантирует PG; потерянное при падении отправителя уведомление —
// приемлемая цена против дублей).
func (r *NotificationChannelsRepo) TryDelivery(ctx context.Context, channelID uuid.UUID, fingerprint string, window time.Duration) (bool, error) {
	var ok bool
	err := r.pool.QueryRow(ctx,
		`INSERT INTO notification_deliveries (channel_id, fingerprint, last_sent_at)
		 VALUES ($1, $2, now())
		 ON CONFLICT (channel_id, fingerprint) DO UPDATE
		   SET last_sent_at = now(), send_count = notification_deliveries.send_count + 1,
		       last_error = NULL
		   WHERE notification_deliveries.last_sent_at < now() - $3::interval
		 RETURNING true`, channelID, fingerprint, window.String()).Scan(&ok)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil // в окне дедупликации — подавлено
		}
		return false, translate(err)
	}
	return true, nil
}

// FailDelivery — фиксация ошибки отправки (last_error; last_sent_at не
// трогаем — дедуп-окно уже открыто TryDelivery).
func (r *NotificationChannelsRepo) FailDelivery(ctx context.Context, channelID uuid.UUID, fingerprint, errText string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE notification_deliveries SET last_error = $3
		 WHERE channel_id = $1 AND fingerprint = $2`, channelID, fingerprint, errText)
	return translate(err)
}
