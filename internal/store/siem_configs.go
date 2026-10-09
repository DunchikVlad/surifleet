package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SiemConfig — SIEM-конфигурация пересылки EVE-алертов (таблица
// siem_configs, миграция 000017, чанк 89). Ровно один из HostID/ClusterID
// задан. Пустой Addr — явное выключение пересылки.
type SiemConfig struct {
	ID        uuid.UUID  `json:"id"`
	HostID    *uuid.UUID `json:"host_id"`
	ClusterID *uuid.UUID `json:"cluster_id"`
	Addr      string     `json:"addr"`
	Protocol  string     `json:"protocol"`
	Format    string     `json:"format"`
	UpdatedBy *uuid.UUID `json:"updated_by"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// SiemConfigsRepo — SIEM-конфигурации host/cluster scope.
type SiemConfigsRepo struct {
	pool *pgxpool.Pool
}

const siemConfigColumns = `id, host_id, cluster_id, addr, protocol, format, updated_by, updated_at`

func scanSiemConfig(row pgx.Row) (SiemConfig, error) {
	var c SiemConfig
	err := row.Scan(&c.ID, &c.HostID, &c.ClusterID, &c.Addr, &c.Protocol, &c.Format, &c.UpdatedBy, &c.UpdatedAt)
	return c, err
}

// ForHost — эффективная SIEM-конфигурация хоста: host-level приоритетнее
// cluster-level (как capabilities). Нет записей → ErrNotFound (сервер
// SIEM не задаёт — агент остаётся на локальном agent.yaml).
func (r *SiemConfigsRepo) ForHost(ctx context.Context, hostID, clusterID uuid.UUID) (SiemConfig, error) {
	c, err := scanSiemConfig(r.pool.QueryRow(ctx,
		`SELECT `+siemConfigColumns+` FROM siem_configs WHERE host_id = $1`, hostID))
	if err == nil {
		return c, nil
	}
	if tr := translate(err); tr != ErrNotFound {
		return c, tr
	}
	c, err = scanSiemConfig(r.pool.QueryRow(ctx,
		`SELECT `+siemConfigColumns+` FROM siem_configs WHERE cluster_id = $1`, clusterID))
	if err != nil {
		return c, translate(err)
	}
	return c, nil
}

// GetHost — host-level запись (для GET /hosts/{id}/siem: показываем
// собственную, не унаследованную). Нет записи → ErrNotFound.
func (r *SiemConfigsRepo) GetHost(ctx context.Context, hostID uuid.UUID) (SiemConfig, error) {
	c, err := scanSiemConfig(r.pool.QueryRow(ctx,
		`SELECT `+siemConfigColumns+` FROM siem_configs WHERE host_id = $1`, hostID))
	if err != nil {
		return c, translate(err)
	}
	return c, nil
}

// GetCluster — cluster-level запись. Нет записи → ErrNotFound.
func (r *SiemConfigsRepo) GetCluster(ctx context.Context, clusterID uuid.UUID) (SiemConfig, error) {
	c, err := scanSiemConfig(r.pool.QueryRow(ctx,
		`SELECT `+siemConfigColumns+` FROM siem_configs WHERE cluster_id = $1`, clusterID))
	if err != nil {
		return c, translate(err)
	}
	return c, nil
}

// SetHost — upsert host-level записи (по частичному уникальному индексу).
func (r *SiemConfigsRepo) SetHost(ctx context.Context, hostID uuid.UUID, addr, protocol, format string, updatedBy *uuid.UUID) (SiemConfig, error) {
	c, err := scanSiemConfig(r.pool.QueryRow(ctx,
		`INSERT INTO siem_configs (host_id, addr, protocol, format, updated_by)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (host_id) WHERE host_id IS NOT NULL DO UPDATE SET
		   addr = EXCLUDED.addr, protocol = EXCLUDED.protocol, format = EXCLUDED.format,
		   updated_by = EXCLUDED.updated_by, updated_at = now()
		 RETURNING `+siemConfigColumns,
		hostID, addr, protocol, format, updatedBy))
	if err != nil {
		return c, translate(err)
	}
	return c, nil
}

// SetCluster — upsert cluster-level записи.
func (r *SiemConfigsRepo) SetCluster(ctx context.Context, clusterID uuid.UUID, addr, protocol, format string, updatedBy *uuid.UUID) (SiemConfig, error) {
	c, err := scanSiemConfig(r.pool.QueryRow(ctx,
		`INSERT INTO siem_configs (cluster_id, addr, protocol, format, updated_by)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (cluster_id) WHERE cluster_id IS NOT NULL DO UPDATE SET
		   addr = EXCLUDED.addr, protocol = EXCLUDED.protocol, format = EXCLUDED.format,
		   updated_by = EXCLUDED.updated_by, updated_at = now()
		 RETURNING `+siemConfigColumns,
		clusterID, addr, protocol, format, updatedBy))
	if err != nil {
		return c, translate(err)
	}
	return c, nil
}

// DeleteHost — удалить host-level запись (хост возвращается к кластерной).
// Нет записи → ErrNotFound.
func (r *SiemConfigsRepo) DeleteHost(ctx context.Context, hostID uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM siem_configs WHERE host_id = $1`, hostID)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteCluster — удалить cluster-level запись. Нет записи → ErrNotFound.
func (r *SiemConfigsRepo) DeleteCluster(ctx context.Context, clusterID uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM siem_configs WHERE cluster_id = $1`, clusterID)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// HostIDsForCluster — хосты кластера (для push SIEM-конфига всем агентам
// кластера после PUT /clusters/{id}/siem).
func (r *SiemConfigsRepo) HostIDsForCluster(ctx context.Context, clusterID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := r.pool.Query(ctx, `SELECT id FROM hosts WHERE cluster_id = $1 ORDER BY id`, clusterID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, translate(err)
		}
		out = append(out, id)
	}
	return out, translate(rows.Err())
}
