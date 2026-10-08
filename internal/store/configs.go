package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ConfigVersion — версия suricata.yaml (таблица config_versions, чанк 54).
type ConfigVersion struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID uuid.UUID  `json:"organization_id"`
	Version        string     `json:"version"`
	SHA256         string     `json:"sha256"`
	S3Key          string     `json:"s3_key"`
	Note           *string    `json:"note"`
	CreatedBy      *uuid.UUID `json:"created_by"`
	CreatedAt      time.Time  `json:"created_at"`
}

// ConfigsRepo — версии конфигураций Suricata.
type ConfigsRepo struct {
	pool *pgxpool.Pool
}

const configColumns = `id, organization_id, version, sha256, s3_key, note, created_by, created_at`

func scanConfig(row pgx.Row) (ConfigVersion, error) {
	var v ConfigVersion
	err := row.Scan(&v.ID, &v.OrganizationID, &v.Version, &v.SHA256, &v.S3Key,
		&v.Note, &v.CreatedBy, &v.CreatedAt)
	return v, err
}

// Create сохраняет версию конфигурации. Content-addressed идемпотентность:
// дубль (organization_id, sha256) → существующая запись, created=false.
func (r *ConfigsRepo) Create(ctx context.Context, orgID uuid.UUID, version, sha256, s3Key, note string, createdBy *uuid.UUID) (ConfigVersion, bool, error) {
	v, err := scanConfig(r.pool.QueryRow(ctx,
		`INSERT INTO config_versions (organization_id, version, sha256, s3_key, note, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (organization_id, sha256) DO NOTHING
		 RETURNING `+configColumns,
		orgID, version, sha256, s3Key, note, createdBy))
	if err != nil {
		if isNoRows(err) {
			existing, gerr := scanConfig(r.pool.QueryRow(ctx,
				`SELECT `+configColumns+` FROM config_versions WHERE organization_id = $1 AND sha256 = $2`,
				orgID, sha256))
			if gerr != nil {
				return v, false, translate(gerr)
			}
			return existing, false, nil
		}
		return v, false, translate(err)
	}
	return v, true, nil
}

// Get возвращает версию по id. Нет записи → ErrNotFound.
func (r *ConfigsRepo) Get(ctx context.Context, id uuid.UUID) (ConfigVersion, error) {
	v, err := scanConfig(r.pool.QueryRow(ctx,
		`SELECT `+configColumns+` FROM config_versions WHERE id = $1`, id))
	if err != nil {
		return v, translate(err)
	}
	return v, nil
}

// List — keyset-листинг версий организации (по id).
func (r *ConfigsRepo) List(ctx context.Context, orgID uuid.UUID, cursor uuid.UUID, limit int) ([]ConfigVersion, *string, error) {
	var cursorArg *uuid.UUID
	if cursor != uuid.Nil {
		cursorArg = &cursor
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+configColumns+` FROM config_versions
		 WHERE organization_id = $1 AND ($2::uuid IS NULL OR id > $2)
		 ORDER BY id LIMIT $3`, orgID, cursorArg, limit+1)
	if err != nil {
		return nil, nil, translate(err)
	}
	defer rows.Close()
	items := []ConfigVersion{}
	for rows.Next() {
		var v ConfigVersion
		if err := rows.Scan(&v.ID, &v.OrganizationID, &v.Version, &v.SHA256, &v.S3Key,
			&v.Note, &v.CreatedBy, &v.CreatedAt); err != nil {
			return nil, nil, translate(err)
		}
		items = append(items, v)
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

// NextAutoVersion — следующая авто-версия конфигурации организации:
// cfg-v<N+1> (max по версиям формата "cfg-v<цифры>"); нет таких → cfg-v1.
func (r *ConfigsRepo) NextAutoVersion(ctx context.Context, orgID uuid.UUID) (string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT version FROM config_versions
		 WHERE organization_id = $1 AND version ~ '^cfg-v[0-9]+$'`, orgID)
	if err != nil {
		return "", translate(err)
	}
	defer rows.Close()
	maxN := 0
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return "", translate(err)
		}
		var n int
		if _, err := fmt.Sscanf(v, "cfg-v%d", &n); err == nil && n > maxN {
			maxN = n
		}
	}
	if err := rows.Err(); err != nil {
		return "", translate(err)
	}
	return fmt.Sprintf("cfg-v%d", maxN+1), nil
}

// ConfigDeploy — запись истории применения конфигурации (instance_config_history, чанк 57).
type ConfigDeploy struct {
	ID              uuid.UUID `json:"id"`
	InstanceID      uuid.UUID `json:"instance_id"`
	ConfigVersion   string    `json:"config_version"`
	Status          string    `json:"status"` // validated | applied | validation_failed | deploy_failed
	ValidationOutput *string  `json:"validation_output,omitempty"`
	ReportedAt      time.Time `json:"reported_at"`
}

// RecordDeploy — записать итог deploy_config/validate_config инстанса.
// instanceID — строка из proto (эхо задачи); мусор не пишем, пропускаем.
func (r *ConfigsRepo) RecordDeploy(ctx context.Context, instanceID, version, status, output string) error {
	instID, err := uuid.Parse(instanceID)
	if err != nil {
		return nil
	}
	var out *string
	if output != "" {
		out = &output
	}
	_, err = r.pool.Exec(ctx,
		`INSERT INTO instance_config_history (instance_id, config_version, status, validation_output)
		 VALUES ($1, $2, $3, $4)`, instID, version, status, out)
	return translate(err)
}

// DeployHistory — последние применения конфигураций на инстансе (новые первыми, ≤ limit).
func (r *ConfigsRepo) DeployHistory(ctx context.Context, instanceID uuid.UUID, limit int) ([]ConfigDeploy, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, instance_id, config_version, status, validation_output, reported_at
		 FROM instance_config_history WHERE instance_id = $1
		 ORDER BY reported_at DESC, id DESC LIMIT $2`, instanceID, limit)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	items := []ConfigDeploy{}
	for rows.Next() {
		var d ConfigDeploy
		if err := rows.Scan(&d.ID, &d.InstanceID, &d.ConfigVersion, &d.Status, &d.ValidationOutput, &d.ReportedAt); err != nil {
			return nil, translate(err)
		}
		items = append(items, d)
	}
	return items, translate(rows.Err())
}
