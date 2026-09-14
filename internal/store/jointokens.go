package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// JoinToken — одноразовый токен онбординга (таблица join_tokens).
// Сам токен не хранится и не показывается после создания — только хэш.
type JoinToken struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID uuid.UUID  `json:"organization_id"`
	ClusterID      uuid.UUID  `json:"cluster_id"`
	Name           string     `json:"name"`
	ExpiresAt      time.Time  `json:"expires_at"`
	UsedAt         *time.Time `json:"used_at"`
	MaxUses        int        `json:"max_uses"`
	UseCount       int        `json:"use_count"`
	CreatedBy      *uuid.UUID `json:"created_by"`
	CreatedAt      time.Time  `json:"created_at"`
}

// GenerateToken генерирует криптостойкий токен (32 байта, base64url).
// Возвращает сам токен (показать пользователю один раз) и его SHA-256 (hex).
func GenerateToken() (token, hash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, HashToken(token), nil
}

// HashToken — SHA-256 (hex) токена; по хэшу токен ищется при enrollment.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// JoinTokensRepo — операции с join tokens.
type JoinTokensRepo struct {
	pool *pgxpool.Pool
}

const joinTokenColumns = "id, organization_id, cluster_id, name, expires_at, used_at, max_uses, use_count, created_by, created_at"

// Create сохраняет токен по хэшу.
func (r *JoinTokensRepo) Create(ctx context.Context, hash string, orgID, clusterID uuid.UUID, name string, expiresAt time.Time, maxUses int, createdBy *uuid.UUID) (JoinToken, error) {
	var t JoinToken
	err := r.pool.QueryRow(ctx,
		`INSERT INTO join_tokens (token_hash, organization_id, cluster_id, name, expires_at, max_uses, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING `+joinTokenColumns,
		hash, orgID, clusterID, name, expiresAt, maxUses, createdBy,
	).Scan(&t.ID, &t.OrganizationID, &t.ClusterID, &t.Name, &t.ExpiresAt, &t.UsedAt, &t.MaxUses, &t.UseCount, &t.CreatedBy, &t.CreatedAt)
	if err != nil {
		return t, translate(err)
	}
	return t, nil
}

// ListByCluster — метаданные токенов кластера (без самих токенов).
func (r *JoinTokensRepo) ListByCluster(ctx context.Context, clusterID uuid.UUID) ([]JoinToken, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+joinTokenColumns+` FROM join_tokens
		 WHERE cluster_id = $1 ORDER BY created_at DESC, id DESC LIMIT 100`, clusterID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	items := []JoinToken{}
	for rows.Next() {
		var t JoinToken
		if err := rows.Scan(&t.ID, &t.OrganizationID, &t.ClusterID, &t.Name, &t.ExpiresAt, &t.UsedAt, &t.MaxUses, &t.UseCount, &t.CreatedBy, &t.CreatedAt); err != nil {
			return nil, translate(err)
		}
		items = append(items, t)
	}
	return items, rows.Err()
}

// Consume атомарно проверяет и расходует токен (один шаг, без гонок):
// токен существует, не истёк и лимит использований не исчерпан.
// Возвращает токен с увеличенным use_count. Невалиден/истёк/исчерпан →
// ErrNotFound (нарочно не различаем причины наружу).
func (r *JoinTokensRepo) Consume(ctx context.Context, tx pgx.Tx, hash string) (JoinToken, error) {
	var t JoinToken
	err := tx.QueryRow(ctx,
		`UPDATE join_tokens
		 SET use_count = use_count + 1,
		     used_at = COALESCE(used_at, now())
		 WHERE token_hash = $1
		   AND expires_at > now()
		   AND use_count < max_uses
		 RETURNING `+joinTokenColumns,
		hash,
	).Scan(&t.ID, &t.OrganizationID, &t.ClusterID, &t.Name, &t.ExpiresAt, &t.UsedAt, &t.MaxUses, &t.UseCount, &t.CreatedBy, &t.CreatedAt)
	if err != nil {
		return t, translate(err)
	}
	return t, nil
}
