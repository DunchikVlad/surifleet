package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// HostsRepo — CRUD и keyset-листинг хостов.
type HostsRepo struct {
	pool *pgxpool.Pool
}

// ip_addresses хранится как inet[]; наружу отдаём адреса без маски хоста
// (host(ip) срезает /32), на вход принимаем []string с явным приведением
// (::inet[]). WITH ORDINALITY сохраняет исходный порядок адресов.
const hostIPs = `(SELECT COALESCE(array_agg(host(ip) ORDER BY ord), '{}')
	FROM unnest(ip_addresses) WITH ORDINALITY AS t(ip, ord)) AS ip_addresses`

const hostColumns = "id, cluster_id, hostname, " + hostIPs + ", os, labels, created_at, updated_at"

// Create регистрирует хост. Несуществующий кластер → ErrForeignKey,
// дубль (cluster_id, hostname) → ErrConflict.
func (r *HostsRepo) Create(ctx context.Context, in HostInput) (Host, error) {
	if in.IPAddresses == nil {
		in.IPAddresses = []string{}
	}
	if in.Labels == nil {
		in.Labels = map[string]string{}
	}
	var h Host
	err := r.pool.QueryRow(ctx,
		`INSERT INTO hosts (cluster_id, hostname, ip_addresses, os, labels)
		 VALUES ($1, $2, $3::inet[], $4, $5)
		 RETURNING `+hostColumns,
		in.ClusterID, in.Hostname, in.IPAddresses, in.OS, in.Labels,
	).Scan(&h.ID, &h.ClusterID, &h.Hostname, &h.IPAddresses, &h.OS, &h.Labels, &h.CreatedAt, &h.UpdatedAt)
	if err != nil {
		return h, translate(err)
	}
	return h, nil
}

// Get возвращает хост по id. Нет записи → ErrNotFound.
func (r *HostsRepo) Get(ctx context.Context, id uuid.UUID) (Host, error) {
	var h Host
	err := r.pool.QueryRow(ctx,
		`SELECT `+hostColumns+` FROM hosts WHERE id = $1`, id,
	).Scan(&h.ID, &h.ClusterID, &h.Hostname, &h.IPAddresses, &h.OS, &h.Labels, &h.CreatedAt, &h.UpdatedAt)
	if err != nil {
		return h, translate(err)
	}
	return h, nil
}

// Update частично обновляет хост (hostname, labels). Нет записи → ErrNotFound,
// дубль hostname в кластере → ErrConflict.
func (r *HostsRepo) Update(ctx context.Context, id uuid.UUID, p HostPatch) (Host, error) {
	var h Host
	err := r.pool.QueryRow(ctx,
		`UPDATE hosts
		 SET hostname = COALESCE($2, hostname),
		     labels = COALESCE($3, labels),
		     updated_at = now()
		 WHERE id = $1
		 RETURNING `+hostColumns,
		id, p.Hostname, p.Labels,
	).Scan(&h.ID, &h.ClusterID, &h.Hostname, &h.IPAddresses, &h.OS, &h.Labels, &h.CreatedAt, &h.UpdatedAt)
	if err != nil {
		return h, translate(err)
	}
	return h, nil
}

// Delete удаляет хост (агент отвязывается, инстансы удаляются каскадом).
// Нет записи → ErrNotFound.
func (r *HostsRepo) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM hosts WHERE id = $1`, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// List — keyset-листинг хостов; clusterID != uuid.Nil — фильтр по кластеру
// (?cluster_id=), q — подстрока по hostname или IP (?q=).
func (r *HostsRepo) List(ctx context.Context, clusterID, cursor uuid.UUID, q string, limit int) ([]Host, *string, error) {
	var clusterArg, cursorArg *uuid.UUID
	if clusterID != uuid.Nil {
		clusterArg = &clusterID
	}
	if cursor != uuid.Nil {
		cursorArg = &cursor
	}
	var qArg *string
	if q != "" {
		pat := "%" + q + "%"
		qArg = &pat
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+hostColumns+` FROM hosts
		 WHERE ($1::uuid IS NULL OR cluster_id = $1)
		   AND ($2::uuid IS NULL OR id > $2)
		   AND ($3::text IS NULL OR hostname ILIKE $3 OR ip_addresses::text ILIKE $3)
		 ORDER BY id
		 LIMIT $4`,
		clusterArg, cursorArg, qArg, limit+1,
	)
	if err != nil {
		return nil, nil, translate(err)
	}
	defer rows.Close()

	items := []Host{}
	for rows.Next() {
		var h Host
		if err := rows.Scan(&h.ID, &h.ClusterID, &h.Hostname, &h.IPAddresses, &h.OS, &h.Labels, &h.CreatedAt, &h.UpdatedAt); err != nil {
			return nil, nil, translate(err)
		}
		items = append(items, h)
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
