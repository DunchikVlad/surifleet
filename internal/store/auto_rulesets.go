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
	Targeting         json.RawMessage
	BatchSize         int
	CanarySize        int
}

const autoRulesetColumns = `id, organization_id, name, description, enabled,
	include_suriupdate, include_ioc, include_manual, include_feeds,
	include_tags, include_categories, exclude_sids, targeting,
	batch_size, canary_size, last_built_at, last_ruleset_version_id, last_deployment_id,
	created_at, updated_at`

func scanAutoRuleset(row pgx.Row) (AutoRuleset, error) {
	var a AutoRuleset
	err := row.Scan(&a.ID, &a.OrganizationID, &a.Name, &a.Description, &a.Enabled,
		&a.IncludeSuriupdate, &a.IncludeIoc, &a.IncludeManual, &a.IncludeFeeds,
		&a.IncludeTags, &a.IncludeCategories, &a.ExcludeSids, &a.Targeting,
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
	a, err := scanAutoRuleset(r.pool.QueryRow(ctx,
		`INSERT INTO auto_rulesets (organization_id, name, description, enabled,
			include_suriupdate, include_ioc, include_manual, include_feeds,
			include_tags, include_categories, exclude_sids, targeting, batch_size, canary_size)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		 RETURNING `+autoRulesetColumns,
		orgID, in.Name, in.Description, in.Enabled,
		in.IncludeSuriupdate, in.IncludeIoc, in.IncludeManual, in.IncludeFeeds,
		in.IncludeTags, in.IncludeCategories, in.ExcludeSids, in.Targeting,
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
			&a.IncludeTags, &a.IncludeCategories, &a.ExcludeSids, &a.Targeting,
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
	a, err := scanAutoRuleset(r.pool.QueryRow(ctx,
		`UPDATE auto_rulesets SET name=$2, description=$3, enabled=$4,
			include_suriupdate=$5, include_ioc=$6, include_manual=$7, include_feeds=$8,
			include_tags=$9, include_categories=$10, exclude_sids=$11, targeting=$12,
			batch_size=$13, canary_size=$14, updated_at=now()
		 WHERE id=$1 RETURNING `+autoRulesetColumns,
		id, in.Name, in.Description, in.Enabled,
		in.IncludeSuriupdate, in.IncludeIoc, in.IncludeManual, in.IncludeFeeds,
		in.IncludeTags, in.IncludeCategories, in.ExcludeSids, in.Targeting,
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
			&a.IncludeTags, &a.IncludeCategories, &a.ExcludeSids, &a.Targeting,
			&a.BatchSize, &a.CanarySize, &a.LastBuiltAt, &a.LastRulesetVersionID, &a.LastDeploymentID,
			&a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, translate(err)
		}
		out = append(out, a)
	}
	return out, translate(rows.Err())
}

// SelectRawByOrigins — raw-правила для сборки авто-ruleset'а: включённые,
// origin в списке, (опционально) теги/категории совпадают, sid вне exclude.
func (r *RulesRepo) SelectRawByOrigins(ctx context.Context, orgID uuid.UUID, origins []string, tags, categories []string, exclude []int64) ([]RuleRawForBuild, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT r.sid, COALESCE((SELECT raw FROM rule_revisions rr
			WHERE rr.rule_id = r.id ORDER BY rr.revision DESC LIMIT 1), ''), r.rev
		 FROM rules r
		 WHERE r.organization_id=$1 AND r.status='enabled'
		   AND r.origin = ANY ($2)
		   AND (cardinality($3::text[])=0 OR r.tags && $3)
		   AND (cardinality($4::text[])=0 OR r.category = ANY ($4))
		   AND NOT (r.sid = ANY ($5))
		 ORDER BY r.sid`,
		orgID, origins, tags, categories, exclude)
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
