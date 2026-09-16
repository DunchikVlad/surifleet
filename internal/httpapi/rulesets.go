package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/surifleet/surifleet/internal/ruleset"
	"github.com/surifleet/surifleet/internal/store"
)

// rulesetBuildInput — POST /api/v1/rulesets (openapi RulesetBuildInput).
type rulesetBuildInput struct {
	Version    string          `json:"version"`
	RuleFilter ruleFilterInput `json:"rule_filter"`
	RuleIDs    []string        `json:"rule_ids"`
	Note       string          `json:"note"`
}

// ruleFilterInput — фильтр правил для сборки (подмножество openapi RuleFilter).
type ruleFilterInput struct {
	Status   string `json:"status"`
	Category string `json:"category"`
	Tag      string `json:"tag"`
	Source   string `json:"source"`
	Sid      int64  `json:"sid"`
}

// manifestRule — состав ruleset в manifest ([{sid,rev}]) — основа
// computed_rules desired_state при деплое.
type manifestRule struct {
	Sid int64 `json:"sid"`
	Rev int   `json:"rev"`
}

// buildRuleset — POST /api/v1/rulesets: детерминированная сборка ruleset
// из выбранных правил, блоб в S3 (content-addressed), запись версии.
// Дубль по содержимому (тот же sha256) → 200 с существующей версией.
func (h *handlers) buildRuleset(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	var in rulesetBuildInput
	if !decodeJSON(w, r, &in) {
		return
	}
	fe := fieldErrors{}
	if !validName(in.Version) {
		fe.add("version", "обязательное поле, 1..200 символов")
	}
	if fe.any() {
		writeValidation(w, fe)
		return
	}

	// Дефолт фильтра — только включённые правила (деплой under_review/disabled
	// на флот не имеет смысла).
	filter := store.RuleFilter{
		Status:   in.RuleFilter.Status,
		Category: in.RuleFilter.Category,
		Tag:      in.RuleFilter.Tag,
		Source:   in.RuleFilter.Source,
		SID:      in.RuleFilter.Sid,
	}
	if filter.Status == "" {
		filter.Status = "enabled"
	}

	var raw []store.RuleRawForBuild
	var err error
	if len(in.RuleIDs) > 0 {
		// Явный список id имеет приоритет над фильтром; фильтр при этом
		// не применяется (манифест фиксирует rule_ids).
		ids := make([]uuid.UUID, 0, len(in.RuleIDs))
		for i, s := range in.RuleIDs {
			id, err := uuid.Parse(s)
			if err != nil {
				fe.add("rule_ids", "невалидный uuid на позиции "+strconv.Itoa(i))
				break
			}
			ids = append(ids, id)
		}
		if fe.any() {
			writeValidation(w, fe)
			return
		}
		raw, err = h.d.Store.Rules.SelectRawByIDs(r.Context(), orgID, ids)
		if err == nil && len(raw) != len(ids) {
			err = nil // не ошибка БД, а неполная выборка — сообщим клиенту
			writeError(w, http.StatusBadRequest, CodeValidation,
				"часть правил не найдена или удалена", map[string]any{
					"requested": len(ids), "found": len(raw)})
			return
		}
	} else {
		raw, err = h.d.Store.Rules.SelectRawForBuild(r.Context(), orgID, filter)
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if len(raw) == 0 {
		writeError(w, http.StatusBadRequest, CodeValidation,
			"по фильтру не выбрано ни одного правила", nil)
		return
	}

	rules := make([]ruleset.RawRule, len(raw))
	manifestRules := make([]manifestRule, len(raw))
	for i, rr := range raw {
		rules[i] = ruleset.RawRule{SID: rr.SID, Raw: rr.Raw}
		manifestRules[i] = manifestRule{Sid: rr.SID, Rev: rr.Rev}
	}
	blob := ruleset.Render(rules)
	sha := ruleset.SHA256(blob)
	key := ruleset.BlobKey(sha)

	if _, err := h.d.Blob.PutIfAbsent(r.Context(), key, blob); err != nil {
		errLog.Error("загрузка ruleset-блоба в S3", "err", err)
		writeError(w, http.StatusInternalServerError, CodeInternal, "загрузка блоба в хранилище", nil)
		return
	}

	sample := make([]int64, 0, 10)
	for i, mr := range manifestRules {
		if i >= 10 {
			break
		}
		sample = append(sample, mr.Sid)
	}
	manifest, _ := json.Marshal(map[string]any{
		"count":       len(manifestRules),
		"filter":      in.RuleFilter,
		"note":        in.Note,
		"rules":       manifestRules,
		"sids_sample": sample,
		"built_at":    time.Now().UTC().Format(time.RFC3339),
	})

	v, created, err := h.d.Store.Rulesets.Create(r.Context(), orgID, in.Version, sha, key, manifest, len(manifestRules))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Content-addressed идемпотентность: тот же состав → существующая версия.
	// В list-ответе manifest не отдаём (большой), в detail — отдаём.
	code := http.StatusOK
	if created {
		code = http.StatusCreated
	}
	v.Manifest = nil
	writeJSON(w, code, v)
}

// listRulesets — GET /api/v1/rulesets: keyset-листинг версий организации.
func (h *handlers) listRulesets(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	cursor, limit, ok := parsePage(w, r)
	if !ok {
		return
	}
	items, next, err := h.d.Store.Rulesets.List(r.Context(), orgID, cursor, limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	for i := range items {
		items[i].Manifest = nil
	}
	writeJSON(w, http.StatusOK, page[store.RulesetVersion]{Items: items, NextCursor: next})
}

// getRuleset — GET /api/v1/rulesets/{id}: деталь с manifest.
func (h *handlers) getRuleset(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	v, err := h.d.Store.Rulesets.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// computedRulesFromManifest — [{sid,rev,status:"enabled"}] из manifest
// ruleset'а для desired_state.computed_rules.
func computedRulesFromManifest(manifest json.RawMessage) json.RawMessage {
	var m struct {
		Rules []manifestRule `json:"rules"`
	}
	if err := json.Unmarshal(manifest, &m); err != nil || len(m.Rules) == 0 {
		return json.RawMessage(`[]`)
	}
	type computed struct {
		Sid    int64  `json:"sid"`
		Rev    int    `json:"rev"`
		Status string `json:"status"`
	}
	out := make([]computed, len(m.Rules))
	for i, r := range m.Rules {
		out[i] = computed{Sid: r.Sid, Rev: r.Rev, Status: "enabled"}
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return json.RawMessage(`[]`)
	}
	return raw
}

// uuidStrings — разбор списка UUID из JSON-строк (targeting.*_ids).
func uuidStrings(ss []string) ([]uuid.UUID, string) {
	ids := make([]uuid.UUID, 0, len(ss))
	for _, s := range ss {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, s
		}
		ids = append(ids, id)
	}
	return ids, ""
}
