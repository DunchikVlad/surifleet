// Авто-обновляемые ruleset'ы (чанк 92): CRUD /auto_rulesets + пересборка
// POST /auto_rulesets/{id}/rebuild. Определение: состав по origin
// (suriupdate/ioc/manual/feed), фильтры тегов/категорий, exclude_sids
// (запрет на деплой), targeting агентов.
package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/surifleet/surifleet/internal/autoruleset"
	"github.com/surifleet/surifleet/internal/store"
)

// autoRulesetInput — POST/PATCH /auto_rulesets.
type autoRulesetInput struct {
	Name              string         `json:"name"`
	Description       string         `json:"description"`
	Enabled           *bool          `json:"enabled"`
	IncludeSuriupdate *bool          `json:"include_suriupdate"`
	IncludeIoc        *bool          `json:"include_ioc"`
	IncludeManual     *bool          `json:"include_manual"`
	IncludeFeeds      *bool          `json:"include_feeds"`
	IncludeTags       []string       `json:"include_tags"`
	IncludeCategories []string       `json:"include_categories"`
	ExcludeSids       []int64        `json:"exclude_sids"`
	IncludeSources    []string       `json:"include_sources"`
	ScheduleEnabled   *bool          `json:"schedule_enabled"`
	ScheduleTime      string         `json:"schedule_time"`
	Targeting         targetingInput `json:"targeting"`
	BatchSize         int            `json:"batch_size"`
	CanarySize        int            `json:"canary_size"`
}

func (in *autoRulesetInput) toStore() store.AutoRulesetInput {
	out := store.AutoRulesetInput{
		Name:              strings.TrimSpace(in.Name),
		IncludeTags:       in.IncludeTags,
		IncludeCategories: in.IncludeCategories,
		ExcludeSids:       in.ExcludeSids,
		IncludeSources:    in.IncludeSources,
		ScheduleTime:      schedTime(in.ScheduleTime),
		BatchSize:         in.BatchSize,
		CanarySize:        in.CanarySize,
		Enabled:           true,
		IncludeSuriupdate: true,
		IncludeIoc:        true,
		IncludeManual:     true,
	}
	if in.Description != "" {
		out.Description = &in.Description
	}
	if in.Enabled != nil {
		out.Enabled = *in.Enabled
	}
	if in.IncludeSuriupdate != nil {
		out.IncludeSuriupdate = *in.IncludeSuriupdate
	}
	if in.IncludeIoc != nil {
		out.IncludeIoc = *in.IncludeIoc
	}
	if in.IncludeManual != nil {
		out.IncludeManual = *in.IncludeManual
	}
	if in.ScheduleEnabled != nil {
		out.ScheduleEnabled = *in.ScheduleEnabled
	}
	if in.IncludeFeeds != nil {
		out.IncludeFeeds = *in.IncludeFeeds
	}
	if in.BatchSize == 0 {
		out.BatchSize = 50
	}
	// nil → пустые массивы (NOT NULL в БД).
	if out.IncludeTags == nil {
		out.IncludeTags = []string{}
	}
	if out.IncludeCategories == nil {
		out.IncludeCategories = []string{}
	}
	if out.ExcludeSids == nil {
		out.ExcludeSids = []int64{}
	}
	tRaw, _ := json.Marshal(in.Targeting)
	out.Targeting = tRaw
	return out
}

// createAutoRuleset — POST /auto_rulesets (rules.write).
func (h *handlers) createAutoRuleset(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	var in autoRulesetInput
	if !decodeJSON(w, r, &in) {
		return
	}
	fe := fieldErrors{}
	if in.toStore().Name == "" {
		fe.add("name", "обязательное поле")
	}
	switch in.Targeting.Mode {
	case "all_clusters", "selected_clusters", "all_except_clusters", "specific_hosts", "specific_instances":
	case "":
		fe.add("targeting.mode", "обязательное поле")
	default:
		fe.add("targeting.mode", "недопустимый режим")
	}
	if fe.any() {
		writeValidation(w, fe)
		return
	}
	a, err := h.d.Store.AutoRulesets.Create(r.Context(), orgID, in.toStore())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	objType := "auto_ruleset"
	h.audit(r, identityFrom(r.Context()), "auto_rulesets.create", &objType, &a.ID, "success", a.Name)
	writeJSON(w, http.StatusCreated, a)
}

// listAutoRulesets — GET /auto_rulesets (rules.read).
func (h *handlers) listAutoRulesets(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	items, err := h.d.Store.AutoRulesets.List(r.Context(), orgID, 100)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// getAutoRuleset — GET /auto_rulesets/{id} (rules.read).
func (h *handlers) getAutoRuleset(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	a, err := h.d.Store.AutoRulesets.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// updateAutoRuleset — PATCH /auto_rulesets/{id} (rules.write).
func (h *handlers) updateAutoRuleset(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	var in autoRulesetInput
	if !decodeJSON(w, r, &in) {
		return
	}
	cur, err := h.d.Store.AutoRulesets.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	merge := in.toStore()
	if in.Name == "" {
		merge.Name = cur.Name
	}
	if in.Description == "" {
		merge.Description = cur.Description
	}
	if in.Targeting.Mode == "" {
		merge.Targeting = cur.Targeting
	}
	if in.BatchSize == 0 {
		merge.BatchSize = cur.BatchSize
	}
	if in.Enabled == nil {
		merge.Enabled = cur.Enabled
	}
	a, err := h.d.Store.AutoRulesets.Update(r.Context(), id, merge)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	objType := "auto_ruleset"
	h.audit(r, identityFrom(r.Context()), "auto_rulesets.update", &objType, &a.ID, "success", a.Name)
	writeJSON(w, http.StatusOK, a)
}

// deleteAutoRuleset — DELETE /auto_rulesets/{id} (rules.write).
func (h *handlers) deleteAutoRuleset(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	if err := h.d.Store.AutoRulesets.Delete(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	objType := "auto_ruleset"
	h.audit(r, identityFrom(r.Context()), "auto_rulesets.delete", &objType, &id, "success", "")
	w.WriteHeader(http.StatusNoContent)
}

// rebuildAutoRuleset — POST /auto_rulesets/{id}/rebuild (rules.write):
// немедленная пересборка (новая версия + волновой деплой на таргетинг).
func (h *handlers) rebuildAutoRuleset(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	def, err := h.d.Store.AutoRulesets.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	res, err := autoruleset.Rebuild(r.Context(), errLog, h.d.Store, h.d.Blob, h.d.Orch, def)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, err.Error(), nil)
		return
	}
	objType := "auto_ruleset"
	reason := "пересборка: версия " + res.Version.Version
	if res.Skipped && res.Version.ID == uuid.Nil {
		reason = "пересборка пропущена: пустой состав или нет целей"
	}
	h.audit(r, identityFrom(r.Context()), "auto_rulesets.rebuild", &objType, &id, "success", reason)
	writeJSON(w, http.StatusOK, map[string]any{
		"skipped":           res.Skipped,
		"ruleset_version":   res.Version.Version,
		"ruleset_version_id": res.Version.ID,
		"deployment_id":     res.Deployment,
		"instances":         res.Instances,
	})
}

// schedTime — пустая строка → nil (расписание не задано).
func schedTime(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}
