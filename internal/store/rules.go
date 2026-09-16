package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RulesRepo — мастер-репозиторий правил (таблицы rules, rule_revisions).
type RulesRepo struct {
	pool *pgxpool.Pool
}

const ruleColumns = `id, organization_id, sid, msg, category, tags, status, priority,
	threshold, source_type, feed_id, created_at, updated_at`

func scanRule(row pgx.Row) (Rule, error) {
	var r Rule
	err := row.Scan(&r.ID, &r.OrganizationID, &r.SID, &r.Msg, &r.Category, &r.Tags,
		&r.Status, &r.Priority, &r.Threshold, &r.SourceType, &r.FeedID, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

// UpsertOutcome — результат импорта одного правила.
type UpsertOutcome string

const (
	UpsertImported  UpsertOutcome = "imported"  // новое правило + первая ревизия
	UpsertUpdated   UpsertOutcome = "updated"   // raw изменился → новая ревизия
	UpsertUnchanged UpsertOutcome = "unchanged" // raw совпал с последней ревизией
)

// ImportItem — одно разобранное правило для импорта/создания.
type ImportItem struct {
	SID       int64
	Rev       int
	Msg       string
	Classtype string          // → category, если CategoryOverride пуст
	Raw       string          // исходная строка правила целиком
	Parsed    json.RawMessage // разобранные поля (classtype, reference, ...) → revisions.parsed
	// FeedID — фид-источник (проставляется только при создании правила;
	// при обновлении существующего первичный источник сохраняется).
	FeedID *uuid.UUID
	// InitialStatus — статус нового правила (пусто → 'under_review');
	// фид et_open передаёт 'disabled' для выключенных в фиде правил.
	// На существующие правила не влияет — статус аналитика не перетирается.
	InitialStatus string
}

// Hash — sha256(hex) от Raw (ключ сравнения ревизий).
func (it ImportItem) Hash() string {
	sum := sha256.Sum256([]byte(it.Raw))
	return hex.EncodeToString(sum[:])
}

// category — целевая категория: явный override импорта, иначе classtype.
func (it ImportItem) category(override string) *string {
	if override != "" {
		return &override
	}
	if it.Classtype != "" {
		return &it.Classtype
	}
	return nil
}

// UpsertImport — идемпотентный импорт одного правила по (organization_id, sid).
//
// Сохранение тюнинга аналитика (ТЗ 5.1): импорт пишет ТОЛЬКО msg/category
// (данные фида) и ревизии; status/priority/threshold/tags никогда не
// перетираются — их меняет только аналитик (PATCH/bulk). Новое правило
// создаётся в status=InitialStatus (по умолчанию 'under_review'); feed_id и
// source_type фиксируются при создании и при последующих импортах по тому же
// (org, sid) не меняются — первичный источник сохраняется.
//
// Raw изменился → UPDATE rules (msg, category) + новая строка в
// rule_revisions с rev из файла; если пара (rule_id, revision) уже есть
// (фид перевыпустил ту же rev с другим текстом) — ревизия не дублируется
// (ON CONFLICT DO NOTHING), но rules обновляется. Raw совпал (по sha256
// последней ревизии) → unchanged, в БД ничего не пишется.
func (r *RulesRepo) UpsertImport(ctx context.Context, orgID uuid.UUID, it ImportItem, categoryOverride, sourceType string) (Rule, UpsertOutcome, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Rule{}, "", translate(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rule, err := scanRule(tx.QueryRow(ctx,
		`SELECT `+ruleColumns+` FROM rules WHERE organization_id = $1 AND sid = $2 FOR UPDATE`,
		orgID, it.SID))
	if err != nil {
		if !isNoRows(err) {
			return Rule{}, "", translate(err)
		}
		// Новое правило + первая ревизия.
		initialStatus := it.InitialStatus
		if initialStatus == "" {
			initialStatus = "under_review"
		}
		rule, err = scanRule(tx.QueryRow(ctx,
			`INSERT INTO rules (organization_id, sid, msg, category, status, source_type, feed_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)
			 RETURNING `+ruleColumns,
			orgID, it.SID, it.Msg, it.category(categoryOverride), initialStatus, sourceType, it.FeedID))
		if err != nil {
			return Rule{}, "", translate(err)
		}
		if err := insertRevision(ctx, tx, rule.ID, it); err != nil {
			return Rule{}, "", err
		}
		return rule, UpsertImported, translate(tx.Commit(ctx))
	}

	// Сравнение с последней ревизией по sha256.
	var lastHash *string
	if err := tx.QueryRow(ctx,
		`SELECT hash FROM rule_revisions WHERE rule_id = $1
		 ORDER BY created_at DESC, revision DESC LIMIT 1`, rule.ID).Scan(&lastHash); err != nil {
		return Rule{}, "", translate(err)
	}
	if lastHash != nil && *lastHash == it.Hash() {
		return rule, UpsertUnchanged, translate(tx.Commit(ctx))
	}

	// Изменилось: обновляем только данные фида (msg/category) — тюнинг нетронут.
	rule, err = scanRule(tx.QueryRow(ctx,
		`UPDATE rules SET msg = $2, category = $3, updated_at = now()
		 WHERE id = $1 RETURNING `+ruleColumns,
		rule.ID, it.Msg, it.category(categoryOverride)))
	if err != nil {
		return Rule{}, "", translate(err)
	}
	if err := insertRevision(ctx, tx, rule.ID, it); err != nil {
		return Rule{}, "", err
	}
	return rule, UpsertUpdated, translate(tx.Commit(ctx))
}

// insertRevision — новая строка rule_revisions; дубль (rule_id, revision)
// молча пропускается (та же rev из файла при изменённом raw).
func insertRevision(ctx context.Context, tx pgx.Tx, ruleID uuid.UUID, it ImportItem) error {
	parsed := it.Parsed
	if parsed == nil {
		parsed = json.RawMessage(`{}`)
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO rule_revisions (rule_id, sid, revision, raw, hash, parsed)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (rule_id, revision) DO NOTHING`,
		ruleID, it.SID, it.Rev, it.Raw, it.Hash(), parsed)
	return translate(err)
}

// CreateManual — ручное создание правила (POST /rules): разобранный raw +
// поля аналитика. Дубль (organization_id, sid) → ErrConflict.
func (r *RulesRepo) CreateManual(ctx context.Context, orgID uuid.UUID, it ImportItem, in RulePatch, status string) (Rule, error) {
	tags := in.Tags
	if tags == nil {
		tags = []string{}
	}
	if status == "" {
		status = "under_review"
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Rule{}, translate(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	category := it.category("")
	if in.Category != nil {
		category = in.Category
	}
	rule, err := scanRule(tx.QueryRow(ctx,
		`INSERT INTO rules (organization_id, sid, msg, category, tags, status, priority, threshold, source_type)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'file')
		 RETURNING `+ruleColumns,
		orgID, it.SID, it.Msg, category, tags, status, in.Priority, nullableJSON(in.Threshold)))
	if err != nil {
		return Rule{}, translate(err)
	}
	if err := insertRevision(ctx, tx, rule.ID, it); err != nil {
		return Rule{}, err
	}
	return rule, translate(tx.Commit(ctx))
}

// GetBySid возвращает правило по (org, sid) — нужен генератору IOC-правил
// для пробинга хэш-коллизий sid. Нет записи → ErrNotFound.
func (r *RulesRepo) GetBySid(ctx context.Context, orgID uuid.UUID, sid int64) (Rule, error) {
	rule, err := scanRule(r.pool.QueryRow(ctx,
		`SELECT `+ruleColumns+` FROM rules WHERE organization_id = $1 AND sid = $2`, orgID, sid))
	if err != nil {
		return Rule{}, translate(err)
	}
	return rule, nil
}

// Get возвращает правило по id. Нет записи → ErrNotFound.
func (r *RulesRepo) Get(ctx context.Context, id uuid.UUID) (Rule, error) {
	rule, err := scanRule(r.pool.QueryRow(ctx,
		`SELECT `+ruleColumns+` FROM rules WHERE id = $1`, id))
	if err != nil {
		return rule, translate(err)
	}
	return rule, nil
}

// Update — тюнинг аналитика (category/tags/status/priority/threshold),
// nil-поле — «не менять». Нет записи → ErrNotFound.
func (r *RulesRepo) Update(ctx context.Context, id uuid.UUID, p RulePatch) (Rule, error) {
	rule, err := scanRule(r.pool.QueryRow(ctx,
		`UPDATE rules
		 SET category = COALESCE($2, category),
		     tags = COALESCE($3::text[], tags),
		     status = COALESCE($4, status),
		     priority = COALESCE($5, priority),
		     threshold = COALESCE($6, threshold),
		     updated_at = now()
		 WHERE id = $1
		 RETURNING `+ruleColumns,
		id, p.Category, p.Tags, p.Status, p.Priority, nullableJSON(p.Threshold)))
	if err != nil {
		return rule, translate(err)
	}
	return rule, nil
}

// SoftDelete переводит правило в status='deleted' (строка остаётся,
// ревизии доступны). Нет записи или уже deleted → ErrNotFound.
func (r *RulesRepo) SoftDelete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE rules SET status = 'deleted', updated_at = now()
		 WHERE id = $1 AND status != 'deleted'`, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ruleFilterWhere — общий WHERE по фильтру (org + status/category/tag/
// source/feed_id/sid/q). Без явного status в фильтре deleted скрываются.
func ruleFilterWhere(orgID uuid.UUID, f RuleFilter) (string, []any) {
	args := []any{orgID}
	where := "organization_id = $1"
	add := func(cond string, v any) {
		args = append(args, v)
		where += fmt.Sprintf(" AND "+cond, len(args))
	}
	if f.Status != "" {
		add("status = $%d", f.Status)
	} else {
		where += " AND status != 'deleted'"
	}
	if f.Category != "" {
		add("category = $%d", f.Category)
	}
	if f.Tag != "" {
		add("$%d = ANY (tags)", f.Tag)
	}
	if f.Source != "" {
		add("source_type = $%d", f.Source)
	}
	if f.FeedID != uuid.Nil {
		add("feed_id = $%d", f.FeedID)
	}
	if f.SID != 0 {
		add("sid = $%d", f.SID)
	}
	if f.Q != "" {
		add("msg ILIKE '%%' || $%d || '%%'", f.Q)
	}
	return where, args
}

// List — keyset-листинг правил организации с фильтрами (AND).
func (r *RulesRepo) List(ctx context.Context, orgID uuid.UUID, f RuleFilter, cursor uuid.UUID, limit int) ([]Rule, *string, error) {
	where, args := ruleFilterWhere(orgID, f)
	var cursorArg *uuid.UUID
	if cursor != uuid.Nil {
		cursorArg = &cursor
	}
	args = append(args, cursorArg, limit+1)
	rows, err := r.pool.Query(ctx,
		fmt.Sprintf(`SELECT %s FROM rules
		 WHERE %s AND ($%d::uuid IS NULL OR id > $%d)
		 ORDER BY id LIMIT $%d`,
			ruleColumns, where, len(args)-1, len(args)-1, len(args)),
		args...)
	if err != nil {
		return nil, nil, translate(err)
	}
	defer rows.Close()

	items := []Rule{}
	for rows.Next() {
		rule, err := scanRule(rows)
		if err != nil {
			return nil, nil, translate(err)
		}
		items = append(items, rule)
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

// Bulk — массовая операция: цель — явный список ids (приоритетно) или фильтр;
// действия enable/disable/delete/set_priority/add_tag/remove_tag.
// deleted-правила не затрагиваются (кроме повторного delete — no-op).
// Возвращает число реально изменённых строк.
func (r *RulesRepo) Bulk(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID, f RuleFilter, action string, priority *int, tag *string) (int64, error) {
	where, args := ruleFilterWhere(orgID, f)
	if len(ids) > 0 {
		args = append(args, ids)
		where += fmt.Sprintf(" AND id = ANY ($%d)", len(args))
	}
	// Bulk не воскрешает и не трогает удалённые (кроме delete, который
	// идемпотентно их пропускает за счёт NOT IN ниже).
	where += " AND status != 'deleted'"

	var query string
	switch action {
	case "enable", "disable", "delete":
		status := map[string]string{"enable": "enabled", "disable": "disabled", "delete": "deleted"}[action]
		args = append(args, status)
		query = fmt.Sprintf(`UPDATE rules SET status = $%d, updated_at = now() WHERE %s AND status != $%d`, len(args), where, len(args))
	case "set_priority":
		args = append(args, *priority)
		query = fmt.Sprintf(`UPDATE rules SET priority = $%d, updated_at = now() WHERE %s`, len(args), where)
	case "add_tag":
		args = append(args, *tag)
		query = fmt.Sprintf(`UPDATE rules SET tags = (SELECT array(SELECT DISTINCT unnest(tags || ARRAY[$%d]) ORDER BY 1)), updated_at = now()
			WHERE %s AND NOT ($%d = ANY (tags))`, len(args), where, len(args))
	case "remove_tag":
		args = append(args, *tag)
		query = fmt.Sprintf(`UPDATE rules SET tags = array_remove(tags, $%d), updated_at = now()
			WHERE %s AND $%d = ANY (tags)`, len(args), where, len(args))
	default:
		return 0, fmt.Errorf("неизвестное действие bulk: %s", action)
	}
	res, err := r.pool.Exec(ctx, query, args...)
	if err != nil {
		return 0, translate(err)
	}
	return res.RowsAffected(), nil
}

// ListRevisions — ревизии правила, новые первыми; keyset по номеру ревизии
// (afterRev > 0 → revision < afterRev). Правило не найдено → ErrNotFound.
func (r *RulesRepo) ListRevisions(ctx context.Context, ruleID uuid.UUID, afterRev int, limit int) ([]RuleRevision, *string, error) {
	// Отдельная проверка существования правила — иначе пустой список
	// неотличим от 404.
	if _, err := r.Get(ctx, ruleID); err != nil {
		return nil, nil, err
	}
	var afterArg *int
	if afterRev > 0 {
		afterArg = &afterRev
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, rule_id, sid, revision, raw, hash, parsed, created_at
		 FROM rule_revisions
		 WHERE rule_id = $1 AND ($2::int IS NULL OR revision < $2)
		 ORDER BY revision DESC LIMIT $3`,
		ruleID, afterArg, limit+1)
	if err != nil {
		return nil, nil, translate(err)
	}
	defer rows.Close()

	items := []RuleRevision{}
	for rows.Next() {
		var rev RuleRevision
		if err := rows.Scan(&rev.ID, &rev.RuleID, &rev.SID, &rev.Revision, &rev.Raw, &rev.Hash, &rev.Parsed, &rev.CreatedAt); err != nil {
			return nil, nil, translate(err)
		}
		items = append(items, rev)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, translate(err)
	}

	var next *string
	if len(items) > limit {
		items = items[:limit]
		c := fmt.Sprintf("%d", items[len(items)-1].Revision)
		next = &c
	}
	return items, next, nil
}

// nullableJSON — nil для пустого RawMessage (jsonb NULL в БД).
func nullableJSON(j json.RawMessage) json.RawMessage {
	if len(j) == 0 {
		return nil
	}
	return j
}

// isNoRows — pgx «записи нет» (без обёртки translate).
func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

// First возвращает старейшую организацию — dev-контур с одной организацией
// (правила org-scoped, а DevAuth не несёт org-контекста). Нет записей → ErrNotFound.
func (r *OrganizationsRepo) First(ctx context.Context) (Organization, error) {
	var o Organization
	err := r.pool.QueryRow(ctx,
		`SELECT `+orgColumns+` FROM organizations ORDER BY created_at, id LIMIT 1`,
	).Scan(&o.ID, &o.Name, &o.Slug, &o.Description, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		return o, translate(err)
	}
	return o, nil
}
