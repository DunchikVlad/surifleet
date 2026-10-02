package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ApiToken — API-токен автоматизации (api_tokens, чанк 29): хранится
// только SHA-256 хэш; scopes — разрешения из общего каталога; отзыв —
// revoked_at (из листинга отозванные скрыты); user_id NULL — сервисный.
type ApiToken struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID uuid.UUID  `json:"organization_id"`
	UserID         *uuid.UUID `json:"user_id"`
	Name           string     `json:"name"`
	Scopes         []string   `json:"scopes"`
	ExpiresAt      *time.Time `json:"expires_at"`
	LastUsedAt     *time.Time `json:"last_used_at"`
	CreatedAt      time.Time  `json:"created_at"`
}

// ApiTokensRepo — операции с api_tokens.
type ApiTokensRepo struct {
	pool *pgxpool.Pool
}

const apiTokenColumns = `id, organization_id, user_id, name, scopes, expires_at, last_used_at, created_at`

func scanApiToken(row pgx.Row) (ApiToken, error) {
	var t ApiToken
	err := row.Scan(&t.ID, &t.OrganizationID, &t.UserID, &t.Name, &t.Scopes,
		&t.ExpiresAt, &t.LastUsedAt, &t.CreatedAt)
	return t, err
}

// Create — выпуск токена (в БД — хэш). Дубль (org, name) → ErrConflict.
func (r *ApiTokensRepo) Create(ctx context.Context, orgID uuid.UUID, userID *uuid.UUID, name, tokenHash string, scopes []string, expiresAt *time.Time) (ApiToken, error) {
	t, err := scanApiToken(r.pool.QueryRow(ctx,
		`INSERT INTO api_tokens (organization_id, user_id, name, token_hash, scopes, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING `+apiTokenColumns,
		orgID, userID, name, tokenHash, scopes, expiresAt))
	if err != nil {
		return t, translate(err)
	}
	return t, nil
}

// List — keyset-листинг активных (не отозванных) токенов организации.
func (r *ApiTokensRepo) List(ctx context.Context, orgID uuid.UUID, cursor uuid.UUID, limit int) ([]ApiToken, *string, error) {
	var cursorArg *uuid.UUID
	if cursor != uuid.Nil {
		cursorArg = &cursor
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+apiTokenColumns+` FROM api_tokens
		 WHERE organization_id = $1 AND revoked_at IS NULL
		   AND ($2::uuid IS NULL OR id > $2)
		 ORDER BY id LIMIT $3`, orgID, cursorArg, limit+1)
	if err != nil {
		return nil, nil, translate(err)
	}
	defer rows.Close()
	items := []ApiToken{}
	for rows.Next() {
		var t ApiToken
		if err := rows.Scan(&t.ID, &t.OrganizationID, &t.UserID, &t.Name, &t.Scopes,
			&t.ExpiresAt, &t.LastUsedAt, &t.CreatedAt); err != nil {
			return nil, nil, translate(err)
		}
		items = append(items, t)
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

// GetValidByHash — активный токен по хэшу: не отозван и не истёк.
// Нет записи → ErrNotFound.
func (r *ApiTokensRepo) GetValidByHash(ctx context.Context, tokenHash string) (ApiToken, error) {
	t, err := scanApiToken(r.pool.QueryRow(ctx,
		`SELECT `+apiTokenColumns+` FROM api_tokens
		 WHERE token_hash = $1 AND revoked_at IS NULL
		   AND (expires_at IS NULL OR expires_at > now())`, tokenHash))
	if err != nil {
		return t, translate(err)
	}
	return t, nil
}

// Revoke — отзыв токена (запись остаётся для аудита). Нет записи → ErrNotFound.
func (r *ApiTokensRepo) Revoke(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE api_tokens SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchUsed — last_used_at, не чаще раза в минуту (middleware на каждый запрос).
func (r *ApiTokensRepo) TouchUsed(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE api_tokens SET last_used_at = now()
		 WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute')`, id)
	return translate(err)
}
