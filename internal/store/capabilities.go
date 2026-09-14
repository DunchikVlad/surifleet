package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CapabilitiesRepo — поэтапная передача контроля (таблица capabilities):
// включённые capability хоста или кластера. Гранулярность: записи уровня
// хоста приоритетнее кластерных; если записей нет вовсе — дефолт онбординга
// ["monitoring"].
type CapabilitiesRepo struct {
	pool *pgxpool.Pool
}

// ForHost — включённые capability хоста (host-level wins → cluster → default).
func (r *CapabilitiesRepo) ForHost(ctx context.Context, hostID, clusterID uuid.UUID) ([]string, error) {
	caps, err := r.list(ctx, `SELECT capability FROM capabilities WHERE host_id = $1 AND enabled ORDER BY capability`, hostID)
	if err != nil {
		return nil, translate(err)
	}
	if len(caps) > 0 {
		return caps, nil
	}
	caps, err = r.list(ctx, `SELECT capability FROM capabilities WHERE cluster_id = $1 AND enabled ORDER BY capability`, clusterID)
	if err != nil {
		return nil, translate(err)
	}
	if len(caps) > 0 {
		return caps, nil
	}
	return []string{"monitoring"}, nil
}

func (r *CapabilitiesRepo) list(ctx context.Context, query string, id uuid.UUID) ([]string, error) {
	rows, err := r.pool.Query(ctx, query, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	caps := []string{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		caps = append(caps, c)
	}
	return caps, rows.Err()
}
