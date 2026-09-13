package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// OrganizationsRepo — CRUD и keyset-листинг организаций.
type OrganizationsRepo struct {
	pool *pgxpool.Pool
}

const orgColumns = "id, name, slug, description, created_at, updated_at"

// Create создаёт организацию. Конфликт slug → ErrConflict.
func (r *OrganizationsRepo) Create(ctx context.Context, in OrganizationInput) (Organization, error) {
	var o Organization
	err := r.pool.QueryRow(ctx,
		`INSERT INTO organizations (name, slug, description)
		 VALUES ($1, $2, $3)
		 RETURNING `+orgColumns,
		in.Name, in.Slug, in.Description,
	).Scan(&o.ID, &o.Name, &o.Slug, &o.Description, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		return o, translate(err)
	}
	return o, nil
}

// Get возвращает организацию по id. Нет записи → ErrNotFound.
func (r *OrganizationsRepo) Get(ctx context.Context, id uuid.UUID) (Organization, error) {
	var o Organization
	err := r.pool.QueryRow(ctx,
		`SELECT `+orgColumns+` FROM organizations WHERE id = $1`, id,
	).Scan(&o.ID, &o.Name, &o.Slug, &o.Description, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		return o, translate(err)
	}
	return o, nil
}

// Update частично обновляет организацию (nil-поля не трогаем).
// Нет записи → ErrNotFound.
func (r *OrganizationsRepo) Update(ctx context.Context, id uuid.UUID, p OrganizationPatch) (Organization, error) {
	var o Organization
	err := r.pool.QueryRow(ctx,
		`UPDATE organizations
		 SET name = COALESCE($2, name),
		     description = COALESCE($3, description),
		     updated_at = now()
		 WHERE id = $1
		 RETURNING `+orgColumns,
		id, p.Name, p.Description,
	).Scan(&o.ID, &o.Name, &o.Slug, &o.Description, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		return o, translate(err)
	}
	return o, nil
}

// Delete удаляет организацию (каскадно всё содержимое — ON DELETE CASCADE).
// Нет записи → ErrNotFound.
func (r *OrganizationsRepo) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM organizations WHERE id = $1`, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// List — keyset-листинг по id. cursor == uuid.Nil — первая страница.
// Возвращает до limit записей и next_cursor (nil, если страница последняя).
func (r *OrganizationsRepo) List(ctx context.Context, cursor uuid.UUID, limit int) ([]Organization, *string, error) {
	var cursorArg *uuid.UUID
	if cursor != uuid.Nil {
		cursorArg = &cursor
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+orgColumns+` FROM organizations
		 WHERE ($1::uuid IS NULL OR id > $1)
		 ORDER BY id
		 LIMIT $2`,
		cursorArg, limit+1,
	)
	if err != nil {
		return nil, nil, translate(err)
	}
	defer rows.Close()

	items := []Organization{}
	for rows.Next() {
		var o Organization
		if err := rows.Scan(&o.ID, &o.Name, &o.Slug, &o.Description, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, nil, translate(err)
		}
		items = append(items, o)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, translate(err)
	}

	var next *string
	if len(items) > limit {
		items = items[:limit]
		c := EncodeCursor(items[len(items)-1].ID)
		next = &c
	}
	return items, next, nil
}
