package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// InstancesRepo — CRUD и keyset-листинг инстансов Suricata (таблица instances).
type InstancesRepo struct {
	pool *pgxpool.Pool
}

const instanceColumns = `id, host_id, name, config_path, rules_dir, log_dir,
	capture_interfaces, suricata_version, systemd_unit, created_at, updated_at`

// instanceColumnsI — те же колонки с префиксом i. (для запросов с JOIN hosts,
// где id/created_at неоднозначны).
const instanceColumnsI = `i.id, i.host_id, i.name, i.config_path, i.rules_dir, i.log_dir,
	i.capture_interfaces, i.suricata_version, i.systemd_unit, i.created_at, i.updated_at`

// Create регистрирует инстанс вручную. Несуществующий хост → ErrForeignKey,
// дубль (host_id, name) → ErrConflict.
func (r *InstancesRepo) Create(ctx context.Context, in InstanceInput) (Instance, error) {
	if in.CaptureInterfaces == nil {
		in.CaptureInterfaces = []string{}
	}
	var inst Instance
	err := r.pool.QueryRow(ctx,
		`INSERT INTO instances (host_id, name, config_path, rules_dir, log_dir, capture_interfaces, systemd_unit)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING `+instanceColumns,
		in.HostID, in.Name, in.ConfigPath, in.RulesDir, in.LogDir, in.CaptureInterfaces, in.SystemdUnit,
	).Scan(&inst.ID, &inst.HostID, &inst.Name, &inst.ConfigPath, &inst.RulesDir, &inst.LogDir,
		&inst.CaptureInterfaces, &inst.SuricataVersion, &inst.SystemdUnit, &inst.CreatedAt, &inst.UpdatedAt)
	if err != nil {
		return inst, translate(err)
	}
	return inst, nil
}

// Upsert — confirm_discovery: идемпотентное создание/обновление инстанса
// по (host_id, name) из подтверждённого DiscoveryReport. Повторный confirm
// не дублирует запись, а освежает пути/версию/юнит.
func (r *InstancesRepo) Upsert(ctx context.Context, hostID uuid.UUID, in InstanceUpsertInput) (Instance, error) {
	if in.CaptureInterfaces == nil {
		in.CaptureInterfaces = []string{}
	}
	var version, unit *string
	if in.SuricataVersion != "" {
		version = &in.SuricataVersion
	}
	if in.SystemdUnit != "" {
		unit = &in.SystemdUnit
	}
	var inst Instance
	err := r.pool.QueryRow(ctx,
		`INSERT INTO instances (host_id, name, config_path, rules_dir, log_dir, capture_interfaces, suricata_version, systemd_unit)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT (host_id, name) DO UPDATE SET
		     config_path = EXCLUDED.config_path,
		     rules_dir = EXCLUDED.rules_dir,
		     log_dir = EXCLUDED.log_dir,
		     capture_interfaces = EXCLUDED.capture_interfaces,
		     suricata_version = EXCLUDED.suricata_version,
		     systemd_unit = EXCLUDED.systemd_unit,
		     updated_at = now()
		 RETURNING `+instanceColumns,
		hostID, in.Name, in.ConfigPath, in.RulesDir, in.LogDir, in.CaptureInterfaces, version, unit,
	).Scan(&inst.ID, &inst.HostID, &inst.Name, &inst.ConfigPath, &inst.RulesDir, &inst.LogDir,
		&inst.CaptureInterfaces, &inst.SuricataVersion, &inst.SystemdUnit, &inst.CreatedAt, &inst.UpdatedAt)
	if err != nil {
		return inst, translate(err)
	}
	return inst, nil
}

// Get возвращает инстанс по id. Нет записи → ErrNotFound.
func (r *InstancesRepo) Get(ctx context.Context, id uuid.UUID) (Instance, error) {
	var inst Instance
	err := r.pool.QueryRow(ctx,
		`SELECT `+instanceColumns+` FROM instances WHERE id = $1`, id,
	).Scan(&inst.ID, &inst.HostID, &inst.Name, &inst.ConfigPath, &inst.RulesDir, &inst.LogDir,
		&inst.CaptureInterfaces, &inst.SuricataVersion, &inst.SystemdUnit, &inst.CreatedAt, &inst.UpdatedAt)
	if err != nil {
		return inst, translate(err)
	}
	return inst, nil
}

// Update частично обновляет инстанс (nil-поле — «не изменять»).
// Нет записи → ErrNotFound, дубль (host_id, name) → ErrConflict.
func (r *InstancesRepo) Update(ctx context.Context, id uuid.UUID, p InstancePatch) (Instance, error) {
	var inst Instance
	err := r.pool.QueryRow(ctx,
		`UPDATE instances
		 SET name = COALESCE($2, name),
		     config_path = COALESCE($3, config_path),
		     rules_dir = COALESCE($4, rules_dir),
		     log_dir = COALESCE($5, log_dir),
		     capture_interfaces = COALESCE($6::text[], capture_interfaces),
		     systemd_unit = COALESCE($7, systemd_unit),
		     updated_at = now()
		 WHERE id = $1
		 RETURNING `+instanceColumns,
		id, p.Name, p.ConfigPath, p.RulesDir, p.LogDir, p.CaptureInterfaces, p.SystemdUnit,
	).Scan(&inst.ID, &inst.HostID, &inst.Name, &inst.ConfigPath, &inst.RulesDir, &inst.LogDir,
		&inst.CaptureInterfaces, &inst.SuricataVersion, &inst.SystemdUnit, &inst.CreatedAt, &inst.UpdatedAt)
	if err != nil {
		return inst, translate(err)
	}
	return inst, nil
}

// Delete удаляет инстанс. Нет записи → ErrNotFound.
func (r *InstancesRepo) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM instances WHERE id = $1`, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// List — keyset-листинг инстансов; hostID != uuid.Nil — фильтр ?host_id=,
// clusterID != uuid.Nil — фильтр ?cluster_id= (через join с hosts).
func (r *InstancesRepo) List(ctx context.Context, hostID, clusterID, cursor uuid.UUID, limit int) ([]Instance, *string, error) {
	var hostArg, clusterArg, cursorArg *uuid.UUID
	if hostID != uuid.Nil {
		hostArg = &hostID
	}
	if clusterID != uuid.Nil {
		clusterArg = &clusterID
	}
	if cursor != uuid.Nil {
		cursorArg = &cursor
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+instanceColumnsI+` FROM instances i
		 JOIN hosts h ON h.id = i.host_id
		 WHERE ($1::uuid IS NULL OR i.host_id = $1)
		   AND ($2::uuid IS NULL OR h.cluster_id = $2)
		   AND ($3::uuid IS NULL OR i.id > $3)
		 ORDER BY i.id
		 LIMIT $4`,
		hostArg, clusterArg, cursorArg, limit+1,
	)
	if err != nil {
		return nil, nil, translate(err)
	}
	defer rows.Close()

	items := []Instance{}
	for rows.Next() {
		var inst Instance
		if err := rows.Scan(&inst.ID, &inst.HostID, &inst.Name, &inst.ConfigPath, &inst.RulesDir, &inst.LogDir,
			&inst.CaptureInterfaces, &inst.SuricataVersion, &inst.SystemdUnit, &inst.CreatedAt, &inst.UpdatedAt); err != nil {
			return nil, nil, translate(err)
		}
		items = append(items, inst)
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

// --- Резолв таргетинга деплоев (chunk 11) ---

// IDsForOrg — id всех инстансов организации (mode all_clusters;
// excludeClusterIDs — mode all_except_clusters).
func (r *InstancesRepo) IDsForOrg(ctx context.Context, orgID uuid.UUID, excludeClusterIDs []uuid.UUID) ([]uuid.UUID, error) {
	return r.idQuery(ctx,
		`SELECT i.id FROM instances i
		 JOIN hosts h ON h.id = i.host_id
		 JOIN clusters c ON c.id = h.cluster_id
		 WHERE c.organization_id = $1 AND NOT (h.cluster_id = ANY ($2))
		 ORDER BY i.id`, orgID, excludeClusterIDs)
}

// IDsForClusters — id инстансов заданных кластеров (mode selected_clusters).
func (r *InstancesRepo) IDsForClusters(ctx context.Context, clusterIDs []uuid.UUID) ([]uuid.UUID, error) {
	return r.idQuery(ctx,
		`SELECT i.id FROM instances i
		 JOIN hosts h ON h.id = i.host_id
		 WHERE h.cluster_id = ANY ($1)
		 ORDER BY i.id`, clusterIDs)
}

// IDsForHosts — id инстансов заданных хостов (mode specific_hosts).
func (r *InstancesRepo) IDsForHosts(ctx context.Context, hostIDs []uuid.UUID) ([]uuid.UUID, error) {
	return r.idQuery(ctx,
		`SELECT id FROM instances WHERE host_id = ANY ($1) ORDER BY id`, hostIDs)
}

// ExistingIDs — подмножество переданных id инстансов, существующих
// в организации (валидация mode specific_instances).
func (r *InstancesRepo) ExistingIDs(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) ([]uuid.UUID, error) {
	return r.idQuery(ctx,
		`SELECT i.id FROM instances i
		 JOIN hosts h ON h.id = i.host_id
		 JOIN clusters c ON c.id = h.cluster_id
		 WHERE c.organization_id = $1 AND i.id = ANY ($2)
		 ORDER BY i.id`, orgID, ids)
}

func (r *InstancesRepo) idQuery(ctx context.Context, query string, args ...any) ([]uuid.UUID, error) {
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	ids := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, translate(err)
		}
		ids = append(ids, id)
	}
	return ids, translate(rows.Err())
}
