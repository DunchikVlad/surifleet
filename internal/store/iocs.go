package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// IocsRepo — репозиторий IOC (таблица iocs, миграция 000001).
type IocsRepo struct {
	pool *pgxpool.Pool
}

const iocColumns = `id, organization_id, type, value, score, status, feed_id, source,
	expires_at, created_at, updated_at`

func scanIoc(row pgx.Row) (Ioc, error) {
	var i Ioc
	err := row.Scan(&i.ID, &i.OrganizationID, &i.Type, &i.Value, &i.Score,
		&i.Status, &i.FeedID, &i.Source, &i.ExpiresAt, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

// Create — ручное создание IOC (POST /iocs). Дубль (org, type, value) → ErrConflict.
func (r *IocsRepo) Create(ctx context.Context, orgID uuid.UUID, in IocInput) (Ioc, error) {
	ioc, err := scanIoc(r.pool.QueryRow(ctx,
		`INSERT INTO iocs (organization_id, type, value, score, source, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING `+iocColumns,
		orgID, in.Type, in.Value, in.Score, in.Source, in.ExpiresAt))
	if err != nil {
		return Ioc{}, translate(err)
	}
	return ioc, nil
}

// Get возвращает IOC по id. Нет записи → ErrNotFound.
func (r *IocsRepo) Get(ctx context.Context, id uuid.UUID) (Ioc, error) {
	ioc, err := scanIoc(r.pool.QueryRow(ctx,
		`SELECT `+iocColumns+` FROM iocs WHERE id = $1`, id))
	if err != nil {
		return Ioc{}, translate(err)
	}
	return ioc, nil
}

// Update — частичное обновление (score/status/expires_at/source);
// nil-поле — «не менять». Нет записи → ErrNotFound.
func (r *IocsRepo) Update(ctx context.Context, id uuid.UUID, p IocPatch) (Ioc, error) {
	ioc, err := scanIoc(r.pool.QueryRow(ctx,
		`UPDATE iocs
		 SET score = COALESCE($2, score),
		     status = COALESCE($3, status),
		     source = COALESCE($4, source),
		     expires_at = COALESCE($5, expires_at),
		     updated_at = now()
		 WHERE id = $1
		 RETURNING `+iocColumns,
		id, p.Score, p.Status, p.Source, p.ExpiresAt))
	if err != nil {
		return Ioc{}, translate(err)
	}
	return ioc, nil
}

// Delete — жёсткое удаление IOC (записи не нужны после истечения срока).
// Нет записи → ErrNotFound.
func (r *IocsRepo) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM iocs WHERE id = $1`, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// IocFilter — фильтры списка IOC (openapi list_iocs).
type IocFilter struct {
	Type   string
	Status string
	Source string
	Q      string // подстрока по value
}

// List — keyset-листинг IOC организации с фильтрами (AND).
func (r *IocsRepo) List(ctx context.Context, orgID uuid.UUID, f IocFilter, cursor uuid.UUID, limit int) ([]Ioc, *string, error) {
	args := []any{orgID}
	where := "organization_id = $1"
	add := func(cond string, v any) {
		args = append(args, v)
		where += fmt.Sprintf(" AND "+cond, len(args))
	}
	if f.Type != "" {
		add("type = $%d", f.Type)
	}
	if f.Status != "" {
		add("status = $%d", f.Status)
	}
	if f.Source != "" {
		add("source = $%d", f.Source)
	}
	if f.Q != "" {
		add("value ILIKE '%%' || $%d || '%%'", f.Q)
	}

	var cursorArg *uuid.UUID
	if cursor != uuid.Nil {
		cursorArg = &cursor
	}
	args = append(args, cursorArg, limit+1)
	rows, err := r.pool.Query(ctx,
		fmt.Sprintf(`SELECT %s FROM iocs
		 WHERE %s AND ($%d::uuid IS NULL OR id > $%d)
		 ORDER BY id LIMIT $%d`,
			iocColumns, where, len(args)-1, len(args)-1, len(args)),
		args...)
	if err != nil {
		return nil, nil, translate(err)
	}
	defer rows.Close()

	items := []Ioc{}
	for rows.Next() {
		ioc, err := scanIoc(rows)
		if err != nil {
			return nil, nil, translate(err)
		}
		items = append(items, ioc)
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

// UpsertImport — идемпотентный импорт одного IOC по (org, type, value):
// новый → inserted=true; существующий — обновляются score/source/expires_at
// (данные источника), status не перетирается (жизненный цикл — у аналитика).
func (r *IocsRepo) UpsertImport(ctx context.Context, orgID uuid.UUID, in IocInput) (Ioc, bool, error) {
	ioc, err := scanIoc(r.pool.QueryRow(ctx,
		`SELECT `+iocColumns+` FROM iocs
		 WHERE organization_id = $1 AND type = $2 AND value = $3 FOR UPDATE`,
		orgID, in.Type, in.Value))
	if err != nil {
		if !isNoRows(err) {
			return Ioc{}, false, translate(err)
		}
		ioc, err := r.Create(ctx, orgID, in)
		return ioc, true, err
	}
	ioc, err = r.Update(ctx, ioc.ID, IocPatch{Score: &in.Score, Source: in.Source, ExpiresAt: in.ExpiresAt})
	return ioc, false, err
}
