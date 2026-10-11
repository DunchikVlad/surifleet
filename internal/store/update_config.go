// update_config.go — история применения конфигов suricata-update
// (update_config_history, миграция 000021, чанк 113). Запись — из hub при
// приёме TaskResult с DeployUpdateConfigResult; чтение — API истории по
// инстансу.
package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// UpdateConfigDeploy — запись истории применения конфига suricata-update.
type UpdateConfigDeploy struct {
	ID         uuid.UUID `json:"id"`
	InstanceID uuid.UUID `json:"instance_id"`
	File       string    `json:"file"`
	Path       string    `json:"path"`
	Sha256     string    `json:"sha256"`
	SizeBytes  int64     `json:"size_bytes"`
	Backup     *string   `json:"backup,omitempty"`
	Unchanged  bool      `json:"unchanged"`
	Status     string    `json:"status"` // applied | failed
	Error      *string   `json:"error,omitempty"`
	CreatedBy  *string   `json:"created_by,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// RecordUpdateConfig — записать итог deploy_update_config инстанса.
// instanceID — строка из proto (эхо задачи); мусор не пишем, пропускаем.
func (r *ConfigsRepo) RecordUpdateConfig(ctx context.Context, instanceID, file, path, sha string, size int64, backup *string, unchanged bool, status, errText, actor string) error {
	instID, err := uuid.Parse(instanceID)
	if err != nil {
		return nil
	}
	var actorV *string
	if actor != "" {
		actorV = &actor
	}
	var errV *string
	if errText != "" {
		errV = &errText
	}
	_, err = r.pool.Exec(ctx,
		`INSERT INTO update_config_history (instance_id, file, path, sha256, size_bytes, backup, unchanged, status, error, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		instID, file, path, sha, size, backup, unchanged, status, errV, actorV)
	return translate(err)
}

// UpdateConfigHistory — последние применения конфигов suricata-update на
// инстансе (новые первыми, ≤ limit).
func (r *ConfigsRepo) UpdateConfigHistory(ctx context.Context, instanceID uuid.UUID, limit int) ([]UpdateConfigDeploy, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, instance_id, file, path, sha256, size_bytes, backup, unchanged, status, error, created_by, created_at
		 FROM update_config_history WHERE instance_id = $1
		 ORDER BY created_at DESC, id DESC LIMIT $2`, instanceID, limit)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	var out []UpdateConfigDeploy
	for rows.Next() {
		var d UpdateConfigDeploy
		if err := rows.Scan(&d.ID, &d.InstanceID, &d.File, &d.Path, &d.Sha256,
			&d.SizeBytes, &d.Backup, &d.Unchanged, &d.Status, &d.Error, &d.CreatedBy, &d.CreatedAt); err != nil {
			return nil, translate(err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
