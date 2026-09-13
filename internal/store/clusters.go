package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ClustersRepo — CRUD и keyset-листинг кластеров.
type ClustersRepo struct {
	pool *pgxpool.Pool
}

const clusterColumns = "id, organization_id, name, description, created_at, updated_at"

// Create создаёт кластер. Несуществующая организация → ErrForeignKey,
// дубль (organization_id, name) → ErrConflict.
func (r *ClustersRepo) Create(ctx context.Context, in ClusterInput) (Cluster, error) {
	var c Cluster
	err := r.pool.QueryRow(ctx,
		`INSERT INTO clusters (organization_id, name, description)
		 VALUES ($1, $2, $3)
		 RETURNING `+clusterColumns,
		in.OrganizationID, in.Name, in.Description,
	).Scan(&c.ID, &c.OrganizationID, &c.Name, &c.Description, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return c, translate(err)
	}
	return c, nil
}

// Get возвращает кластер по id. Нет записи → ErrNotFound.
func (r *ClustersRepo) Get(ctx context.Context, id uuid.UUID) (Cluster, error) {
	var c Cluster
	err := r.pool.QueryRow(ctx,
		`SELECT `+clusterColumns+` FROM clusters WHERE id = $1`, id,
	).Scan(&c.ID, &c.OrganizationID, &c.Name, &c.Description, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return c, translate(err)
	}
	return c, nil
}

// Update частично обновляет кластер. Нет записи → ErrNotFound,
// дубль имени в организации → ErrConflict.
func (r *ClustersRepo) Update(ctx context.Context, id uuid.UUID, p ClusterPatch) (Cluster, error) {
	var c Cluster
	err := r.pool.QueryRow(ctx,
		`UPDATE clusters
		 SET name = COALESCE($2, name),
		     description = COALESCE($3, description),
		     updated_at = now()
		 WHERE id = $1
		 RETURNING `+clusterColumns,
		id, p.Name, p.Description,
	).Scan(&c.ID, &c.OrganizationID, &c.Name, &c.Description, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return c, translate(err)
	}
	return c, nil
}

// Delete удаляет кластер (каскадно хосты и дочерние сущности).
// Нет записи → ErrNotFound.
func (r *ClustersRepo) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM clusters WHERE id = $1`, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// List — keyset-листинг кластеров; orgID != uuid.Nil ограничивает выборку
// организацией (фильтр ?organization_id= из openapi).
func (r *ClustersRepo) List(ctx context.Context, orgID, cursor uuid.UUID, limit int) ([]Cluster, *string, error) {
	var orgArg, cursorArg *uuid.UUID
	if orgID != uuid.Nil {
		orgArg = &orgID
	}
	if cursor != uuid.Nil {
		cursorArg = &cursor
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+clusterColumns+` FROM clusters
		 WHERE ($1::uuid IS NULL OR organization_id = $1)
		   AND ($2::uuid IS NULL OR id > $2)
		 ORDER BY id
		 LIMIT $3`,
		orgArg, cursorArg, limit+1,
	)
	if err != nil {
		return nil, nil, translate(err)
	}
	defer rows.Close()

	items := []Cluster{}
	for rows.Next() {
		var c Cluster
		if err := rows.Scan(&c.ID, &c.OrganizationID, &c.Name, &c.Description, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, nil, translate(err)
		}
		items = append(items, c)
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
