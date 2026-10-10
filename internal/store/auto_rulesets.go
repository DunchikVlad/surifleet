package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AutoRuleset — постоянно обновляемый ruleset (миграция 000018, чанк 91):
// состав по происхождению правил (origin) + фильтры тегов/категорий,
// exclude_sids — запрет на деплой, targeting — агенты. Пересборка =
// новая версия ruleset + волновой деплой (internal/autoruleset).
type AutoRuleset struct {
	ID               uuid.UUID  `json:"id"`
	OrganizationID   uuid.UUID  `json:"organization_id"`
	Name             string     `json:"name"`
	Description      *string    `json:"description"`
	Enabled          bool       `json:"enabled"`
	IncludeSuriupdate bool      `json:"include_suriupdate"`
	IncludeIoc       bool       `json:"include_ioc"`
	IncludeManual    bool       `json:"include_manual"`
	IncludeFeeds     bool       `json:"include_feeds"`
	IncludeTags      []string   `json:"include_tags"`
	IncludeCategories []string  `json:"include_categories"`
	ExcludeSids      []int64    `json:"exclude_sids"`
	IncludeSources   []string   `json:"include_sources"`
	ScheduleEnabled  bool       `json:"schedule_enabled"`
	ScheduleTime     *string    `json:"schedule_time"`
	Targeting        json.RawMessage `json:"targeting"`
	BatchSize        int        `json:"batch_size"`
	CanarySize       int        `json:"canary_size"`
	LastBuiltAt      *time.Time `json:"last_built_at"`
	LastRulesetVersionID *uuid.UUID `json:"last_ruleset_version_id"`
	LastDeploymentID *uuid.UUID `json:"last_deployment_id"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// AutoRulesetInput — создание/замена определения авто-ruleset'а.
type AutoRulesetInput struct {
	Name              string
	Description       *string
	Enabled           bool
	IncludeSuriupdate bool
	IncludeIoc        bool
	IncludeManual     bool
	IncludeFeeds      bool
	IncludeTags       []string
	IncludeCategories []string
	ExcludeSids       []int64
	IncludeSources    []string
	ScheduleEnabled   bool
	ScheduleTime      *string
	Targeting         json.RawMessage
	BatchSize         int
	CanarySize        int
}

const autoRulesetColumns = `id, organization_id, name, description, enabled,
	include_suriupdate, include_ioc, include_manual, include_feeds,
	include_tags, include_categories, exclude_sids, include_sources, schedule_enabled, schedule_time, targeting,
	batch_size, canary_size, last_built_at, last_ruleset_version_id, last_deployment_id,
	created_at, updated_at`

func scanAutoRuleset(row pgx.Row) (AutoRuleset, error) {
	var a AutoRuleset
	err := row.Scan(&a.ID, &a.OrganizationID, &a.Name, &a.Description, &a.Enabled,
		&a.IncludeSuriupdate, &a.IncludeIoc, &a.IncludeManual, &a.IncludeFeeds,
		&a.IncludeTags, &a.IncludeCategories, &a.ExcludeSids, &a.IncludeSources, &a.ScheduleEnabled, &a.ScheduleTime, &a.Targeting,
		&a.BatchSize, &a.CanarySize, &a.LastBuiltAt, &a.LastRulesetVersionID, &a.LastDeploymentID,
		&a.CreatedAt, &a.UpdatedAt)
	return a, err
}

// AutoRulesetsRepo — авто-обновляемые ruleset'ы.
type AutoRulesetsRepo struct {
	pool *pgxpool.Pool
}

// Create — новое определение. Дубль (org, name) → ErrConflict.
func (r *AutoRulesetsRepo) Create(ctx context.Context, orgID uuid.UUID, in AutoRulesetInput) (AutoRuleset, error) {
	if in.IncludeSources == nil {
		in.IncludeSources = []string{}
	}
	a, err := scanAutoRuleset(r.pool.QueryRow(ctx,
		`INSERT INTO auto_rulesets (organization_id, name, description, enabled,
			include_suriupdate, include_ioc, include_manual, include_feeds,
			include_tags, include_categories, exclude_sids, include_sources, schedule_enabled, schedule_time, targeting, batch_size, canary_size)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		 RETURNING `+autoRulesetColumns,
		orgID, in.Name, in.Description, in.Enabled,
		in.IncludeSuriupdate, in.IncludeIoc, in.IncludeManual, in.IncludeFeeds,
		in.IncludeTags, in.IncludeCategories, in.ExcludeSids, in.IncludeSources,
		in.ScheduleEnabled, in.ScheduleTime, in.Targeting,
		in.BatchSize, in.CanarySize))
	if err != nil {
		return a, translate(err)
	}
	return a, nil
}

// Get — определение по id. Нет → ErrNotFound.
func (r *AutoRulesetsRepo) Get(ctx context.Context, id uuid.UUID) (AutoRuleset, error) {
	a, err := scanAutoRuleset(r.pool.QueryRow(ctx,
		`SELECT `+autoRulesetColumns+` FROM auto_rulesets WHERE id = $1`, id))
	if err != nil {
		return a, translate(err)
	}
	return a, nil
}

// List — определения организации (сначала новые).
func (r *AutoRulesetsRepo) List(ctx context.Context, orgID uuid.UUID, limit int) ([]AutoRuleset, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+autoRulesetColumns+` FROM auto_rulesets WHERE organization_id = $1
		 ORDER BY created_at DESC LIMIT $2`, orgID, limit)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []AutoRuleset{}
	for rows.Next() {
		var a AutoRuleset
		if err := rows.Scan(&a.ID, &a.OrganizationID, &a.Name, &a.Description, &a.Enabled,
			&a.IncludeSuriupdate, &a.IncludeIoc, &a.IncludeManual, &a.IncludeFeeds,
			&a.IncludeTags, &a.IncludeCategories, &a.ExcludeSids, &a.IncludeSources, &a.ScheduleEnabled, &a.ScheduleTime, &a.Targeting,
			&a.BatchSize, &a.CanarySize, &a.LastBuiltAt, &a.LastRulesetVersionID, &a.LastDeploymentID,
			&a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, translate(err)
		}
		out = append(out, a)
	}
	return out, translate(rows.Err())
}

// Update — замена определения (полная).
func (r *AutoRulesetsRepo) Update(ctx context.Context, id uuid.UUID, in AutoRulesetInput) (AutoRuleset, error) {
	if in.IncludeSources == nil {
		in.IncludeSources = []string{}
	}
	a, err := scanAutoRuleset(r.pool.QueryRow(ctx,
		`UPDATE auto_rulesets SET name=$2, description=$3, enabled=$4,
			include_suriupdate=$5, include_ioc=$6, include_manual=$7, include_feeds=$8,
			include_tags=$9, include_categories=$10, exclude_sids=$11, include_sources=$12, schedule_enabled=$13, schedule_time=$14, targeting=$15,
			batch_size=$16, canary_size=$17, updated_at=now()
		 WHERE id=$1 RETURNING `+autoRulesetColumns,
		id, in.Name, in.Description, in.Enabled,
		in.IncludeSuriupdate, in.IncludeIoc, in.IncludeManual, in.IncludeFeeds,
		in.IncludeTags, in.IncludeCategories, in.ExcludeSids, in.IncludeSources,
		in.ScheduleEnabled, in.ScheduleTime, in.Targeting,
		in.BatchSize, in.CanarySize))
	if err != nil {
		return a, translate(err)
	}
	return a, nil
}

// Delete — удалить определение.
func (r *AutoRulesetsRepo) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM auto_rulesets WHERE id = $1`, id)
	return translate(err)
}

// SetLastBuild — фиксация последней пересборки (версия + деплой).
func (r *AutoRulesetsRepo) SetLastBuild(ctx context.Context, id uuid.UUID, versionID, deploymentID uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE auto_rulesets SET last_built_at=now(), last_ruleset_version_id=$2,
			last_deployment_id=$3, updated_at=now() WHERE id=$1`,
		id, versionID, deploymentID)
	return translate(err)
}

// ListScheduled — включённые определения с включённым расписанием.
func (r *AutoRulesetsRepo) ListScheduled(ctx context.Context) ([]AutoRuleset, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+autoRulesetColumns+` FROM auto_rulesets
		 WHERE enabled AND schedule_enabled AND schedule_time IS NOT NULL
		 ORDER BY created_at`)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []AutoRuleset{}
	for rows.Next() {
		var a AutoRuleset
		if err := rows.Scan(&a.ID, &a.OrganizationID, &a.Name, &a.Description, &a.Enabled,
			&a.IncludeSuriupdate, &a.IncludeIoc, &a.IncludeManual, &a.IncludeFeeds,
			&a.IncludeTags, &a.IncludeCategories, &a.ExcludeSids, &a.IncludeSources, &a.ScheduleEnabled, &a.ScheduleTime, &a.Targeting,
			&a.BatchSize, &a.CanarySize, &a.LastBuiltAt, &a.LastRulesetVersionID, &a.LastDeploymentID,
			&a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, translate(err)
		}
		out = append(out, a)
	}
	return out, translate(rows.Err())
}

// ListEnabledForRebuild — включённые определения с флагом include_suriupdate
// (пересборка после импорта suricata-update).
func (r *AutoRulesetsRepo) ListEnabledForRebuild(ctx context.Context, orgID uuid.UUID) ([]AutoRuleset, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+autoRulesetColumns+` FROM auto_rulesets
		 WHERE organization_id=$1 AND enabled AND include_suriupdate
		 ORDER BY created_at`, orgID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []AutoRuleset{}
	for rows.Next() {
		var a AutoRuleset
		if err := rows.Scan(&a.ID, &a.OrganizationID, &a.Name, &a.Description, &a.Enabled,
			&a.IncludeSuriupdate, &a.IncludeIoc, &a.IncludeManual, &a.IncludeFeeds,
			&a.IncludeTags, &a.IncludeCategories, &a.ExcludeSids, &a.IncludeSources, &a.ScheduleEnabled, &a.ScheduleTime, &a.Targeting,
			&a.BatchSize, &a.CanarySize, &a.LastBuiltAt, &a.LastRulesetVersionID, &a.LastDeploymentID,
			&a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, translate(err)
		}
		out = append(out, a)
	}
	return out, translate(rows.Err())
}

// LastRevisionHashes — sid → sha256 последней ревизии по всем правилам
// организации (одна выборка; фильтр «без изменений» импорта suriupdate —
// иначе 50k+ транзакций на каждый прогон).
func (r *RulesRepo) LastRevisionHashes(ctx context.Context, orgID uuid.UUID) (map[int64]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT r.sid, COALESCE((SELECT rr.hash FROM rule_revisions rr
			WHERE rr.rule_id = r.id ORDER BY rr.revision DESC LIMIT 1), '')
		 FROM rules r WHERE r.organization_id = $1`, orgID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var sid int64
		var h string
		if err := rows.Scan(&sid, &h); err != nil {
			return nil, translate(err)
		}
		out[sid] = h
	}
	return out, translate(rows.Err())
}

// SetSourceNames — массовая простановка source_name по карте sid → источник
// (чанк 95): обновляет даже правила без изменения raw.
func (r *RulesRepo) SetSourceNames(ctx context.Context, orgID uuid.UUID, m map[int64]string) (int64, error) {
	sids := make([]int64, 0, len(m))
	srcs := make([]string, 0, len(m))
	for sid, src := range m {
		sids = append(sids, sid)
		srcs = append(srcs, src)
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE rules SET source_name = u.src
		 FROM (SELECT unnest($2::bigint[]) AS sid, unnest($3::text[]) AS src) u
		 WHERE rules.organization_id = $1 AND rules.sid = u.sid`,
		orgID, sids, srcs)
	if err != nil {
		return 0, translate(err)
	}
	return tag.RowsAffected(), nil
}

// SelectRawByOrigins — raw-правила для сборки авто-ruleset'а: включённые,
// origin в списке, (опционально) теги/категории/источники suricata-update
// совпадают (пустые массивы = без ограничения), sid вне exclude.
func (r *RulesRepo) SelectRawByOrigins(ctx context.Context, orgID uuid.UUID, origins []string, tags, categories, sources []string, exclude []int64) ([]RuleRawForBuild, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT r.sid, COALESCE((SELECT raw FROM rule_revisions rr
			WHERE rr.rule_id = r.id ORDER BY rr.revision DESC LIMIT 1), ''),
			COALESCE((SELECT rr2.revision FROM rule_revisions rr2
			WHERE rr2.rule_id = r.id ORDER BY rr2.revision DESC LIMIT 1), 1)
		 FROM rules r
		 WHERE r.organization_id=$1 AND r.status='enabled'
		   AND r.origin = ANY ($2)
		   AND (cardinality($3::text[])=0 OR r.tags && $3)
		   AND (cardinality($4::text[])=0 OR r.category = ANY ($4))
		   AND (cardinality($6::text[])=0 OR r.source_name = ANY ($6))
		   AND NOT (r.sid = ANY ($5))
		 ORDER BY r.sid`,
		orgID, origins, tags, categories, exclude, sources)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []RuleRawForBuild{}
	for rows.Next() {
		var rr RuleRawForBuild
		if err := rows.Scan(&rr.SID, &rr.Raw, &rr.Rev); err != nil {
			return nil, translate(err)
		}
		out = append(out, rr)
	}
	return out, translate(rows.Err())
}
