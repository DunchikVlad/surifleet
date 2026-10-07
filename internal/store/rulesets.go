package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RulesetsRepo — версии ruleset (таблица ruleset_versions).
type RulesetsRepo struct {
	pool *pgxpool.Pool
}

const rulesetColumns = `id, organization_id, version, sha256, s3_key, manifest, rule_count, created_by, created_at`

func scanRuleset(row pgx.Row) (RulesetVersion, error) {
	var v RulesetVersion
	err := row.Scan(&v.ID, &v.OrganizationID, &v.Version, &v.SHA256, &v.S3Key,
		&v.Manifest, &v.RuleCount, &v.CreatedBy, &v.CreatedAt)
	return v, err
}

// Create сохраняет версию ruleset. Content-addressed идемпотентность:
// дубль (organization_id, sha256) → возвращается существующая запись,
// created=false. Дубль (organization_id, version) с другим хэшем → ErrConflict.
func (r *RulesetsRepo) Create(ctx context.Context, orgID uuid.UUID, version, sha256, s3Key string, manifest json.RawMessage, ruleCount int) (RulesetVersion, bool, error) {
	v, err := scanRuleset(r.pool.QueryRow(ctx,
		`INSERT INTO ruleset_versions (organization_id, version, sha256, s3_key, manifest, rule_count)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (organization_id, sha256) DO NOTHING
		 RETURNING `+rulesetColumns,
		orgID, version, sha256, s3Key, manifest, ruleCount))
	if err != nil {
		if isNoRows(err) {
			// Блоб с таким хэшем уже есть — отдаём существующую версию.
			existing, gerr := scanRuleset(r.pool.QueryRow(ctx,
				`SELECT `+rulesetColumns+` FROM ruleset_versions WHERE organization_id = $1 AND sha256 = $2`,
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
func (r *RulesetsRepo) Get(ctx context.Context, id uuid.UUID) (RulesetVersion, error) {
	v, err := scanRuleset(r.pool.QueryRow(ctx,
		`SELECT `+rulesetColumns+` FROM ruleset_versions WHERE id = $1`, id))
	if err != nil {
		return v, translate(err)
	}
	return v, nil
}

// List — keyset-листинг версий ruleset организации (новые последними по id).
func (r *RulesetsRepo) List(ctx context.Context, orgID, cursor uuid.UUID, limit int) ([]RulesetVersion, *string, error) {
	var cursorArg *uuid.UUID
	if cursor != uuid.Nil {
		cursorArg = &cursor
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+rulesetColumns+` FROM ruleset_versions
		 WHERE organization_id = $1 AND ($2::uuid IS NULL OR id > $2)
		 ORDER BY id LIMIT $3`,
		orgID, cursorArg, limit+1)
	if err != nil {
		return nil, nil, translate(err)
	}
	defer rows.Close()

	items := []RulesetVersion{}
	for rows.Next() {
		v, err := scanRuleset(rows)
		if err != nil {
			return nil, nil, translate(err)
		}
		items = append(items, v)
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

// RuleRawForBuild — сырой текст правила для сборки ruleset:
// sid + rev + raw последней ревизии (по номеру ревизии).
type RuleRawForBuild struct {
	SID int64
	Rev int
	Raw string
}

// SelectRawByIDs — выборка правил по явному списку id (для rule_ids в
// RulesetBuildInput); порядок — по sid для детерминизма рендера.
// Правила со status='deleted' исключаются.
func (r *RulesRepo) SelectRawByIDs(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) ([]RuleRawForBuild, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT r.sid, rv.revision, rv.raw
		 FROM rules r
		 JOIN LATERAL (
		     SELECT revision, raw FROM rule_revisions
		     WHERE rule_id = r.id
		     ORDER BY revision DESC, created_at DESC LIMIT 1
		 ) rv ON true
		 WHERE r.organization_id = $1 AND r.id = ANY ($2) AND r.status <> 'deleted'
		 ORDER BY r.sid`,
		orgID, ids)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	items := []RuleRawForBuild{}
	for rows.Next() {
		var it RuleRawForBuild
		if err := rows.Scan(&it.SID, &it.Rev, &it.Raw); err != nil {
			return nil, translate(err)
		}
		items = append(items, it)
	}
	return items, translate(rows.Err())
}

// SelectRawForBuild — выборка правил организации для сборки ruleset
// (последняя ревизия каждого правила, фильтры как в Rules.List, но без
// пагинации — ruleset собирается целиком).
func (r *RulesRepo) SelectRawForBuild(ctx context.Context, orgID uuid.UUID, f RuleFilter) ([]RuleRawForBuild, error) {
	where, args := ruleFilterWhere(orgID, f)
	rows, err := r.pool.Query(ctx,
		fmt.Sprintf(`SELECT r.sid, rv.revision, rv.raw
		 FROM rules r
		 JOIN LATERAL (
		     SELECT revision, raw FROM rule_revisions
		     WHERE rule_id = r.id
		     ORDER BY revision DESC, created_at DESC LIMIT 1
		 ) rv ON true
		 WHERE %s
		 ORDER BY r.sid`, where),
		args...)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	items := []RuleRawForBuild{}
	for rows.Next() {
		var it RuleRawForBuild
		if err := rows.Scan(&it.SID, &it.Rev, &it.Raw); err != nil {
			return nil, translate(err)
		}
		items = append(items, it)
	}
	return items, translate(rows.Err())
}

// DesiredStateRepo — целевые состояния инстансов (таблица desired_state).
type DesiredStateRepo struct {
	pool *pgxpool.Pool
}

// Upsert задаёт целевое состояние инстанса; calc_version монотонно растёт.
func (r *DesiredStateRepo) Upsert(ctx context.Context, instanceID, rulesetVersionID uuid.UUID, computedRules json.RawMessage) error {
	if computedRules == nil {
		computedRules = json.RawMessage(`[]`)
	}
	_, err := r.pool.Exec(ctx,
		`INSERT INTO desired_state (instance_id, ruleset_version_id, computed_rules)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (instance_id) DO UPDATE SET
		     ruleset_version_id = EXCLUDED.ruleset_version_id,
		     computed_rules = EXCLUDED.computed_rules,
		     calc_version = desired_state.calc_version + 1,
		     updated_at = now()`,
		instanceID, rulesetVersionID, computedRules)
	return translate(err)
}

// Get возвращает desired state инстанса. Нет записи → ErrNotFound.
func (r *DesiredStateRepo) Get(ctx context.Context, instanceID uuid.UUID) (DesiredState, error) {
	var d DesiredState
	err := r.pool.QueryRow(ctx,
		`SELECT instance_id, ruleset_version_id, computed_rules, calc_version, updated_at
		 FROM desired_state WHERE instance_id = $1`, instanceID,
	).Scan(&d.InstanceID, &d.RulesetVersionID, &d.ComputedRules, &d.CalcVersion, &d.UpdatedAt)
	if err != nil {
		return d, translate(err)
	}
	return d, nil
}

// ActualStateRepo — фактические состояния инстансов (таблица actual_state).
type ActualStateRepo struct {
	pool *pgxpool.Pool
}

// Upsert сохраняет последний отчёт агента по инстансу.
func (r *ActualStateRepo) Upsert(ctx context.Context, a ActualState) error {
	loaded := a.LoadedRules
	if loaded == nil {
		loaded = json.RawMessage(`[]`)
	}
	failed := a.FailedRules
	if failed == nil {
		failed = json.RawMessage(`[]`)
	}
	_, err := r.pool.Exec(ctx,
		`INSERT INTO actual_state (instance_id, ruleset_hash, loaded_rules, failed_rules, last_reload_result, reported_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (instance_id) DO UPDATE SET
		     ruleset_hash = EXCLUDED.ruleset_hash,
		     loaded_rules = EXCLUDED.loaded_rules,
		     failed_rules = EXCLUDED.failed_rules,
		     last_reload_result = EXCLUDED.last_reload_result,
		     reported_at = EXCLUDED.reported_at,
		     updated_at = now()`,
		a.InstanceID, a.RulesetHash, loaded, failed, a.LastReloadResult, a.ReportedAt)
	return translate(err)
}

// Get возвращает actual state инстанса. Нет записи → ErrNotFound.
func (r *ActualStateRepo) Get(ctx context.Context, instanceID uuid.UUID) (ActualState, error) {
	var a ActualState
	err := r.pool.QueryRow(ctx,
		`SELECT instance_id, ruleset_hash, loaded_rules, failed_rules, last_reload_result, reported_at, updated_at
		 FROM actual_state WHERE instance_id = $1`, instanceID,
	).Scan(&a.InstanceID, &a.RulesetHash, &a.LoadedRules, &a.FailedRules, &a.LastReloadResult, &a.ReportedAt, &a.UpdatedAt)
	if err != nil {
		return a, translate(err)
	}
	return a, nil
}

// ComplianceRepo — сводка соответствия инстансов (таблица instance_compliance).
type ComplianceRepo struct {
	pool *pgxpool.Pool
}

// Upsert фиксирует текущий статус соответствия инстанса.
func (r *ComplianceRepo) Upsert(ctx context.Context, instanceID uuid.UUID, status string, details json.RawMessage) error {
	if details == nil {
		details = json.RawMessage(`{}`)
	}
	_, err := r.pool.Exec(ctx,
		`INSERT INTO instance_compliance (instance_id, status, details)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (instance_id) DO UPDATE SET
		     status = EXCLUDED.status, details = EXCLUDED.details, updated_at = now()`,
		instanceID, status, details)
	return translate(err)
}

// Get возвращает сводку по инстансу. Нет записи → ErrNotFound.
func (r *ComplianceRepo) Get(ctx context.Context, instanceID uuid.UUID) (InstanceCompliance, error) {
	var c InstanceCompliance
	err := r.pool.QueryRow(ctx,
		`SELECT instance_id, status, details, updated_at FROM instance_compliance WHERE instance_id = $1`,
		instanceID,
	).Scan(&c.InstanceID, &c.Status, &c.Details, &c.UpdatedAt)
	if err != nil {
		return c, translate(err)
	}
	return c, nil
}

// ComplianceSummary — сводка флота по статусам (openapi ComplianceSummary).
type ComplianceSummary struct {
	TotalInstances int            `json:"total_instances"`
	ByStatus       map[string]int `json:"by_status"`
}

// Summary — глобальная сводка по статусам соответствия (LEFT JOIN:
// инстансы без строки compliance считаются pending). clusterID != uuid.Nil —
// фильтр по кластеру.
func (r *ComplianceRepo) Summary(ctx context.Context, clusterID uuid.UUID) (ComplianceSummary, error) {
	var clusterArg *uuid.UUID
	if clusterID != uuid.Nil {
		clusterArg = &clusterID
	}
	rows, err := r.pool.Query(ctx,
		`SELECT COALESCE(c.status, 'pending') AS st, count(*)
		 FROM instances i
		 JOIN hosts h ON h.id = i.host_id
		 LEFT JOIN instance_compliance c ON c.instance_id = i.id
		 WHERE ($1::uuid IS NULL OR h.cluster_id = $1)
		 GROUP BY st`,
		clusterArg)
	if err != nil {
		return ComplianceSummary{}, translate(err)
	}
	defer rows.Close()

	sum := ComplianceSummary{ByStatus: map[string]int{}}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return sum, translate(err)
		}
		sum.ByStatus[st] = n
		sum.TotalInstances += n
	}
	return sum, translate(rows.Err())
}

// ComplianceItem — строка drill-down (openapi ComplianceItem).
type ComplianceItem struct {
	InstanceID uuid.UUID `json:"instance_id"`
	HostID     uuid.UUID `json:"host_id"`
	Hostname   string    `json:"hostname"`
	ClusterID  uuid.UUID `json:"cluster_id"`
	Status     string    `json:"status"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// ListByStatus — drill-down: страница инстансов заданного статуса (keyset).
func (r *ComplianceRepo) ListByStatus(ctx context.Context, status string, clusterID, cursor uuid.UUID, limit int) ([]ComplianceItem, *string, error) {
	var clusterArg, cursorArg *uuid.UUID
	if clusterID != uuid.Nil {
		clusterArg = &clusterID
	}
	if cursor != uuid.Nil {
		cursorArg = &cursor
	}
	rows, err := r.pool.Query(ctx,
		`SELECT i.id, i.host_id, h.hostname, h.cluster_id,
		        COALESCE(c.status, 'pending'), COALESCE(c.updated_at, i.updated_at)
		 FROM instances i
		 JOIN hosts h ON h.id = i.host_id
		 LEFT JOIN instance_compliance c ON c.instance_id = i.id
		 WHERE COALESCE(c.status, 'pending') = $1
		   AND ($2::uuid IS NULL OR h.cluster_id = $2)
		   AND ($3::uuid IS NULL OR i.id > $3)
		 ORDER BY i.id LIMIT $4`,
		status, clusterArg, cursorArg, limit+1)
	if err != nil {
		return nil, nil, translate(err)
	}
	defer rows.Close()

	items := []ComplianceItem{}
	for rows.Next() {
		var it ComplianceItem
		if err := rows.Scan(&it.InstanceID, &it.HostID, &it.Hostname, &it.ClusterID, &it.Status, &it.UpdatedAt); err != nil {
			return nil, nil, translate(err)
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, translate(err)
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		c := EncodeCursor(items[len(items)-1].InstanceID)
		next = &c
	}
	return items, next, nil
}

// NextAutoVersion — следующая авто-версия ruleset'а организации (чанк 50):
// v<N+1>, где N — максимум среди версий формата "v<цифры>"; нет таких → "v1".
func (r *RulesetsRepo) NextAutoVersion(ctx context.Context, orgID uuid.UUID) (string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT version FROM ruleset_versions
		 WHERE organization_id = $1 AND version ~ '^v[0-9]+$'`, orgID)
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
		if _, err := fmt.Sscanf(v, "v%d", &n); err == nil && n > maxN {
			maxN = n
		}
	}
	if err := rows.Err(); err != nil {
		return "", translate(err)
	}
	return fmt.Sprintf("v%d", maxN+1), nil
}
