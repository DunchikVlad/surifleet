package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Матрица «правила × инстансы» (ТЗ требование А, openapi RulesMatrix):
// две независимые keyset-оси — правила (по sid) и инстансы (по id),
// ячейка — статус правила на инстансе (loaded/failed/missing/extra),
// считается из desired_state.computed_rules и actual_state
// (loaded_rules/failed_rules последнего StateReport агента).

// MatrixRule — строка оси правил матрицы.
type MatrixRule struct {
	ID     uuid.UUID `json:"id"` // для карточки логики (чанк 76)
	SID    int64     `json:"sid"`
	Msg    *string   `json:"msg"`
	Status string    `json:"status"`
}

// MatrixInstance — столбец оси инстансов матрицы.
type MatrixInstance struct {
	InstanceID uuid.UUID `json:"instance_id"`
	Hostname   string    `json:"hostname"`
	Name       string    `json:"name"`
}

// MatrixInstanceState — наборы sid инстанса для расчёта ячеек:
// desired (computed_rules), loaded и failed (actual_state).
type MatrixInstanceState struct {
	Desired map[int64]bool
	Loaded  map[int64]bool
	Failed  map[int64]bool
}

// EncodeSidCursor кодирует sid последнего правила страницы в keyset-курсор
// оси правил (base64url десятичного представления — непрозрачен для клиента).
func EncodeSidCursor(sid int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(sid, 10)))
}

// DecodeSidCursor раскодирует курсор оси правил. Пустая строка — первая
// страница (0), ошибкой не является.
func DecodeSidCursor(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return 0, fmt.Errorf("некорректный курсор: %w", err)
	}
	sid, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("некорректный курсор: %w", err)
	}
	return sid, nil
}

// MatrixCellFilter — фильтр cell_status (чанк 119): ось правил
// подтягивает правила, у которых ЕСТЬ ячейка заданного статуса хотя бы на
// одном инстансе ТЕКУЩЕЙ страницы оси инстансов (раньше фильтр резал
// ячейки только внутри уже выбранной страницы правил — MVP-ограничение
// чанка 13d). InstanceIDs — страница инстансов (пусто → ячеек нет →
// правил нет).
type MatrixCellFilter struct {
	Status      string   // loaded | failed | missing | extra (пусто — без фильтра)
	InstanceIDs []uuid.UUID
}

// matrixCellFilterSQL — IN-подзапрос сидов с ячейкой статуса $7 среди
// инстансов $8. Семантика ячеек — ровно cellStatusOf (failed приоритетнее
// прочих; loaded = desired∩loaded¬failed; missing = desired¬loaded¬failed;
// extra = loaded¬desired¬failed), пер-инстанс, затем объединение по ∃.
// Вынесен константой для читаемости и юнит-контроля состава запроса.
const matrixCellFilterSQL = `rules.sid IN (
  WITH d AS (SELECT (e->>'sid')::bigint sid, ds.instance_id
             FROM desired_state ds, jsonb_array_elements(ds.computed_rules) e
             WHERE ds.instance_id = ANY($8::uuid[])),
       l AS (SELECT (e->>'sid')::bigint sid, a.instance_id
             FROM actual_state a, jsonb_array_elements(a.loaded_rules) e
             WHERE a.instance_id = ANY($8::uuid[]) AND a.reported_at IS NOT NULL),
       f AS (SELECT (e->>'sid')::bigint sid, a.instance_id
             FROM actual_state a, jsonb_array_elements(a.failed_rules) e
             WHERE a.instance_id = ANY($8::uuid[]) AND a.reported_at IS NOT NULL)
  SELECT sid FROM (
    SELECT 'failed' AS st, sid FROM f
    UNION ALL
    SELECT 'loaded', d.sid FROM d JOIN l ON l.instance_id = d.instance_id AND l.sid = d.sid
     WHERE NOT EXISTS (SELECT 1 FROM f fa WHERE fa.instance_id = d.instance_id AND fa.sid = d.sid)
    UNION ALL
    SELECT 'missing', d.sid FROM d
     WHERE NOT EXISTS (SELECT 1 FROM l la WHERE la.instance_id = d.instance_id AND la.sid = d.sid)
       AND NOT EXISTS (SELECT 1 FROM f fa WHERE fa.instance_id = d.instance_id AND fa.sid = d.sid)
    UNION ALL
    SELECT 'extra', l.sid FROM l
     WHERE NOT EXISTS (SELECT 1 FROM d da WHERE da.instance_id = l.instance_id AND da.sid = l.sid)
       AND NOT EXISTS (SELECT 1 FROM f fa WHERE fa.instance_id = l.instance_id AND fa.sid = l.sid)
  ) cells WHERE st = $7::text
)`

// matrixRulesPageSQL — выборка оси правил (чанк 119: +фильтр cell_status
// через matrixCellFilterSQL; при $7 IS NULL подзапрос не вычисляется).
const matrixRulesPageSQL = `SELECT id, sid, msg, status FROM rules
 WHERE organization_id = $1
   AND ($2::text IS NULL AND status != 'deleted' OR status = $2)
   AND ($3::text IS NULL OR category = $3)
   AND ($4::bigint IS NULL OR sid = $4)
   AND ($7::text IS NULL OR ` + matrixCellFilterSQL + `)
   AND sid > $5
 ORDER BY sid LIMIT $6`

// MatrixRulesPage — страница оси правил: keyset по sid, фильтры
// status/category/sid (как в списке правил; без явного status deleted скрыты).
// cell — фильтр cell_status (пустой Status — без фильтра, прежнее
// поведение).
func (r *RulesRepo) MatrixRulesPage(ctx context.Context, orgID uuid.UUID, status, category string, sid, afterSid int64, limit int, cell MatrixCellFilter) ([]MatrixRule, *string, error) {
	var statusArg, categoryArg *string
	if status != "" {
		statusArg = &status
	}
	if category != "" {
		categoryArg = &category
	}
	var sidArg *int64
	if sid != 0 {
		sidArg = &sid
	}
	var cellStatusArg *string
	if cell.Status != "" {
		cellStatusArg = &cell.Status
	}
	cellInsts := cell.InstanceIDs
	if len(cellInsts) == 0 {
		cellInsts = nil // ANY(NULL) — пустое множество: ячеек нет
	}
	rows, err := r.pool.Query(ctx, matrixRulesPageSQL,
		orgID, statusArg, categoryArg, sidArg, afterSid, limit+1, cellStatusArg, cellInsts)
	if err != nil {
		return nil, nil, translate(err)
	}
	defer rows.Close()

	items := []MatrixRule{}
	for rows.Next() {
		var it MatrixRule
		if err := rows.Scan(&it.ID, &it.SID, &it.Msg, &it.Status); err != nil {
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
		c := EncodeSidCursor(items[len(items)-1].SID)
		next = &c
	}
	return items, next, nil
}

// MatrixInstancesPage — страница оси инстансов с hostname (keyset по id
// инстанса, фильтр по cluster_id). scope — scoping по кластерам
// (чанк 47): nil — без ограничений, иначе только инстансы этих кластеров.
func (r *InstancesRepo) MatrixInstancesPage(ctx context.Context, clusterID uuid.UUID, scope []uuid.UUID, cursor uuid.UUID, limit int) ([]MatrixInstance, *string, error) {
	var clusterArg, cursorArg *uuid.UUID
	if clusterID != uuid.Nil {
		clusterArg = &clusterID
	}
	if cursor != uuid.Nil {
		cursorArg = &cursor
	}
	rows, err := r.pool.Query(ctx,
		`SELECT i.id, h.hostname, i.name
		 FROM instances i
		 JOIN hosts h ON h.id = i.host_id
		 WHERE ($1::uuid IS NULL OR h.cluster_id = $1)
		   AND ($2::uuid IS NULL OR i.id > $2)
		   AND ($4::uuid[] IS NULL OR h.cluster_id = ANY($4))
		 ORDER BY i.id LIMIT $3`,
		clusterArg, cursorArg, limit+1, scope)
	if err != nil {
		return nil, nil, translate(err)
	}
	defer rows.Close()

	items := []MatrixInstance{}
	for rows.Next() {
		var it MatrixInstance
		if err := rows.Scan(&it.InstanceID, &it.Hostname, &it.Name); err != nil {
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

// MatrixStates — desired/actual наборы sid по пачке инстансов (два запроса
// вместо Get на инстанс). Инстанс без строки в desired/actual получает
// пустые наборы (его ячейки просто не строятся).
func MatrixStates(ctx context.Context, pool *pgxpool.Pool, instanceIDs []uuid.UUID) (map[uuid.UUID]*MatrixInstanceState, error) {
	states := make(map[uuid.UUID]*MatrixInstanceState, len(instanceIDs))
	for _, id := range instanceIDs {
		states[id] = &MatrixInstanceState{
			Desired: map[int64]bool{},
			Loaded:  map[int64]bool{},
			Failed:  map[int64]bool{},
		}
	}
	if len(instanceIDs) == 0 {
		return states, nil
	}

	// desired: computed_rules — [{sid,rev,status}].
	dRows, err := pool.Query(ctx,
		`SELECT instance_id, computed_rules FROM desired_state WHERE instance_id = ANY ($1)`,
		instanceIDs)
	if err != nil {
		return nil, translate(err)
	}
	defer dRows.Close()
	for dRows.Next() {
		var id uuid.UUID
		var raw json.RawMessage
		if err := dRows.Scan(&id, &raw); err != nil {
			return nil, translate(err)
		}
		for _, sid := range sidsFrom(raw) {
			states[id].Desired[sid] = true
		}
	}
	if err := dRows.Err(); err != nil {
		return nil, translate(err)
	}

	// actual: loaded_rules — [{sid,rev}], failed_rules — [{sid,rev,error_text}].
	aRows, err := pool.Query(ctx,
		`SELECT instance_id, loaded_rules, failed_rules FROM actual_state
		 WHERE instance_id = ANY ($1) AND reported_at IS NOT NULL`,
		instanceIDs)
	if err != nil {
		return nil, translate(err)
	}
	defer aRows.Close()
	for aRows.Next() {
		var id uuid.UUID
		var loaded, failed json.RawMessage
		if err := aRows.Scan(&id, &loaded, &failed); err != nil {
			return nil, translate(err)
		}
		for _, sid := range sidsFrom(loaded) {
			states[id].Loaded[sid] = true
		}
		for _, sid := range sidsFrom(failed) {
			states[id].Failed[sid] = true
		}
	}
	return states, translate(aRows.Err())
}

// sidsFrom — набор sid из jsonb-массива вида [{sid,...}] (computed_rules,
// loaded_rules, failed_rules — во всех первое поле sid).
func sidsFrom(raw json.RawMessage) []int64 {
	var items []struct {
		Sid int64 `json:"sid"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	out := make([]int64, 0, len(items))
	for _, it := range items {
		out = append(out, it.Sid)
	}
	return out
}
