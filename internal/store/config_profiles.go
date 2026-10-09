package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ConfigProfile — профиль suricata.yaml (таблица config_profiles, чанк 64).
// Наследование кластер → хост → инстанс через ParentID; рендер переменных —
// следующие чанки.
type ConfigProfile struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"organization_id"`
	Name           string    `json:"name"`
	Description    *string   `json:"description"`
	ScopeType      string    `json:"scope_type"` // cluster | host | instance
	ScopeID        uuid.UUID `json:"scope_id"`
	ParentID       *uuid.UUID `json:"parent_id"`
	ContentYAML    string    `json:"content_yaml"`
	Version        int       `json:"version"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ConfigProfilesRepo — профили конфигурации Suricata.
type ConfigProfilesRepo struct {
	pool *pgxpool.Pool
}

const configProfileColumns = `id, organization_id, name, description, scope_type, scope_id, parent_id, content_yaml, version, created_at, updated_at`

func scanConfigProfile(row pgx.Row) (ConfigProfile, error) {
	var p ConfigProfile
	err := row.Scan(&p.ID, &p.OrganizationID, &p.Name, &p.Description, &p.ScopeType,
		&p.ScopeID, &p.ParentID, &p.ContentYAML, &p.Version, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

// Create — создать профиль. parent_id (если задан) должен существовать —
// иначе 23503 → translate → conflict.
func (r *ConfigProfilesRepo) Create(ctx context.Context, orgID uuid.UUID, name, description, scopeType string, scopeID uuid.UUID, parentID *uuid.UUID, content string) (ConfigProfile, error) {
	p, err := scanConfigProfile(r.pool.QueryRow(ctx,
		`INSERT INTO config_profiles (organization_id, name, description, scope_type, scope_id, parent_id, content_yaml)
		 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING `+configProfileColumns,
		orgID, name, description, scopeType, scopeID, parentID, content))
	if err != nil {
		return p, translate(err)
	}
	return p, nil
}

// Get — профиль по id. Нет записи → ErrNotFound.
func (r *ConfigProfilesRepo) Get(ctx context.Context, id uuid.UUID) (ConfigProfile, error) {
	p, err := scanConfigProfile(r.pool.QueryRow(ctx,
		`SELECT `+configProfileColumns+` FROM config_profiles WHERE id = $1`, id))
	if err != nil {
		return p, translate(err)
	}
	return p, nil
}

// List — keyset-листинг профилей организации с фильтром scope (пустые —
// без фильтра).
func (r *ConfigProfilesRepo) List(ctx context.Context, orgID uuid.UUID, scopeType string, scopeID uuid.UUID, cursor uuid.UUID, limit int) ([]ConfigProfile, *string, error) {
	var cursorArg *uuid.UUID
	if cursor != uuid.Nil {
		cursorArg = &cursor
	}
	var scopeIDArg *uuid.UUID
	if scopeType != "" && scopeID != uuid.Nil {
		scopeIDArg = &scopeID
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+configProfileColumns+` FROM config_profiles
		 WHERE organization_id = $1
		   AND ($2::text IS NULL OR scope_type = $2)
		   AND ($3::uuid IS NULL OR scope_id = $3)
		   AND ($4::uuid IS NULL OR id > $4)
		 ORDER BY id LIMIT $5`, orgID, nullIfEmpty(scopeType), scopeIDArg, cursorArg, limit+1)
	if err != nil {
		return nil, nil, translate(err)
	}
	defer rows.Close()
	items := []ConfigProfile{}
	for rows.Next() {
		var p ConfigProfile
		if err := rows.Scan(&p.ID, &p.OrganizationID, &p.Name, &p.Description, &p.ScopeType,
			&p.ScopeID, &p.ParentID, &p.ContentYAML, &p.Version, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, nil, translate(err)
		}
		items = append(items, p)
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

// Update — переименование/описание/содержимое. Смена content_yaml
// инкрементирует version (по спеке: «изменение содержимого создаёт новую
// версию»); история версий — следующие чанки.
func (r *ConfigProfilesRepo) Update(ctx context.Context, id uuid.UUID, name, description, content *string) (ConfigProfile, error) {
	p, err := scanConfigProfile(r.pool.QueryRow(ctx,
		`UPDATE config_profiles SET
		   name = COALESCE($2, name),
		   description = CASE WHEN $3::text IS NULL THEN description ELSE $3 END,
		   content_yaml = COALESCE($4, content_yaml),
		   version = version + (CASE WHEN $4 IS NULL THEN 0 ELSE 1 END),
		   updated_at = now()
		 WHERE id = $1 RETURNING `+configProfileColumns,
		id, name, description, content))
	if err != nil {
		return p, translate(err)
	}
	return p, nil
}

// Chain — цепочка наследования профиля от корня к самому профилю
// (root→tip; рендер мержит в этом порядке, чанк 65). Родители читаются
// по parent_id; все звенья обязаны принадлежать той же организации —
// иначе ErrNotFound (защита от утечки между оргами). Цикл parent_id →
// ErrCycle (ручные правки БД; FK-циклов создать нельзя, но страхуемся).
func (r *ConfigProfilesRepo) Chain(ctx context.Context, id uuid.UUID) ([]ConfigProfile, error) {
	const maxDepth = 16
	chain := []ConfigProfile{}
	seen := map[uuid.UUID]bool{}
	cur := id
	for i := 0; i < maxDepth; i++ {
		if seen[cur] {
			return nil, ErrCycle
		}
		seen[cur] = true
		p, err := r.Get(ctx, cur)
		if err != nil {
			return nil, err
		}
		if len(chain) > 0 && p.OrganizationID != chain[0].OrganizationID {
			// родитель из другой орги — не показываем
			return nil, ErrNotFound
		}
		chain = append([]ConfigProfile{p}, chain...) // root→tip
		if p.ParentID == nil {
			return chain, nil
		}
		cur = *p.ParentID
	}
	return nil, ErrCycle // глубже maxDepth — фактически цикл
}

// Delete — удалить профиль. Дочерние профили остаются (parent_id → NULL
	// по ON DELETE SET NULL).
func (r *ConfigProfilesRepo) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM config_profiles WHERE id = $1`, id)
	return translate(err)
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
