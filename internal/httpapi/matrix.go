package httpapi

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/surifleet/surifleet/internal/store"
)

// --- GET /api/v1/matrix/rules (openapi RulesMatrix, ТЗ требование А) ---

// Статусы ячеек матрицы (enum openapi RulesMatrix.cells[].status).
const (
	cellLoaded  = "loaded"
	cellFailed  = "failed"
	cellMissing = "missing"
	cellExtra   = "extra"
)

var cellStatuses = map[string]bool{
	cellLoaded: true, cellFailed: true, cellMissing: true, cellExtra: true,
}

type matrixCellView struct {
	Sid        int64     `json:"sid"`
	InstanceID uuid.UUID `json:"instance_id"`
	Status     string    `json:"status"`
}

type rulesMatrixView struct {
	Rules              []store.MatrixRule     `json:"rules"`
	Instances          []store.MatrixInstance `json:"instances"`
	Cells              []matrixCellView       `json:"cells"`
	NextRuleCursor     *string                `json:"next_rule_cursor"`
	NextInstanceCursor *string                `json:"next_instance_cursor"`
}

// getRulesMatrix — GET /api/v1/matrix/rules: матрица «правила × инстансы».
// Две независимые keyset-оси: rule_cursor (по sid) и instance_cursor (по id).
// Ячейка строится из desired_state.computed_rules и actual_state
// (loaded_rules/failed_rules последнего StateReport):
//   - sid в failed_rules → failed;
//   - sid в desired и loaded → loaded;
//   - sid в desired, но не loaded/failed → missing;
//   - sid в loaded вне desired → extra;
//   - иначе ячейки нет (правило не из целевого ruleset инстанса).
func (h *handlers) getRulesMatrix(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()

	// Валидация фильтров.
	ruleStatus := q.Get("rule_status")
	if ruleStatus != "" && !ruleStatuses[ruleStatus] {
		writeError(w, http.StatusBadRequest, CodeValidation,
			"rule_status должен быть одним из: enabled, disabled, expired, under_review, deleted",
			map[string]any{"rule_status": ruleStatus})
		return
	}
	cellStatus := q.Get("cell_status")
	if cellStatus != "" && !cellStatuses[cellStatus] {
		writeError(w, http.StatusBadRequest, CodeValidation,
			"cell_status должен быть одним из: loaded, failed, missing, extra",
			map[string]any{"cell_status": cellStatus})
		return
	}
	var sidFilter int64
	if s := q.Get("sid"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, CodeValidation,
				"sid должен быть положительным целым", map[string]any{"sid": s})
			return
		}
		sidFilter = n
	}
	clusterID, ok := queryUUID(w, r, "cluster_id")
	if !ok {
		return
	}

	// limit общий для обеих осей (openapi: default 100, max 1000).
	limit := defaultLimit
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, CodeValidation,
				"limit должен быть целым числом от 1 до 1000", map[string]any{"limit": s})
			return
		}
		if n > maxLimit {
			n = maxLimit
		}
		limit = n
	}

	ruleAfter, err := store.DecodeSidCursor(q.Get("rule_cursor"))
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation,
			"некорректный rule_cursor", map[string]any{"rule_cursor": q.Get("rule_cursor")})
		return
	}
	instCursor, err := store.DecodeCursor(q.Get("instance_cursor"))
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation,
			"некорректный instance_cursor", map[string]any{"instance_cursor": q.Get("instance_cursor")})
		return
	}

	ctx := r.Context()
	rulesPage, nextRule, err := h.d.Store.Rules.MatrixRulesPage(ctx, orgID,
		ruleStatus, q.Get("category"), sidFilter, ruleAfter, limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	instances, nextInst, err := h.d.Store.Instances.MatrixInstancesPage(ctx, clusterID, instCursor, limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	ids := make([]uuid.UUID, 0, len(instances))
	for _, inst := range instances {
		ids = append(ids, inst.InstanceID)
	}
	states, err := store.MatrixStates(ctx, h.d.Store.Pool, ids)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	// Ячейки: правила страницы × инстансы страницы.
	cells := []matrixCellView{}
	for _, rule := range rulesPage {
		for _, inst := range instances {
			st := cellStatusOf(rule.SID, states[inst.InstanceID])
			if st == "" {
				continue // правило не из целевого/фактического набора инстанса
			}
			if cellStatus != "" && st != cellStatus {
				continue
			}
			cells = append(cells, matrixCellView{Sid: rule.SID, InstanceID: inst.InstanceID, Status: st})
		}
	}

	writeJSON(w, http.StatusOK, rulesMatrixView{
		Rules:              rulesPage,
		Instances:          instances,
		Cells:              cells,
		NextRuleCursor:     nextRule,
		NextInstanceCursor: nextInst,
	})
}

// cellStatusOf — статус ячейки (sid, инстанс) по наборам desired/actual;
// пустая строка — ячейки нет (правило вне контекста инстанса).
func cellStatusOf(sid int64, st *store.MatrixInstanceState) string {
	if st == nil {
		return ""
	}
	if st.Failed[sid] {
		return cellFailed
	}
	if st.Desired[sid] {
		if st.Loaded[sid] {
			return cellLoaded
		}
		return cellMissing
	}
	if st.Loaded[sid] {
		return cellExtra
	}
	return ""
}
