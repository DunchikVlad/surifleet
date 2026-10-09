// Профили конфигурации Suricata (чанк 64, план 1B): CRUD
// /config_profiles по openapi-спеке. Наследование кластер → хост →
// инстанс через parent_id; рендер шаблона с переменными, история
// версий и валидация через агента — следующие чанки.
package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/surifleet/surifleet/internal/cfgrender"
	"github.com/surifleet/surifleet/internal/store"
)

var profileScopes = map[string]bool{"cluster": true, "host": true, "instance": true}

// configProfileInput — POST /config_profiles.
type configProfileInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ScopeType   string `json:"scope_type"`
	ScopeID     string `json:"scope_id"`
	ParentID    string `json:"parent_id"`
	ContentYAML string `json:"content_yaml"`
}

func (in *configProfileInput) validate() fieldErrors {
	var fe fieldErrors
	if in.Name = strings.TrimSpace(in.Name); in.Name == "" {
		fe["name"] = "обязательное поле"
	}
	if !profileScopes[in.ScopeType] {
		fe["scope_type"] = "cluster | host | instance"
	}
	if _, err := uuid.Parse(in.ScopeID); err != nil {
		fe["scope_id"] = "обязательный UUID"
	}
	if in.ContentYAML = strings.TrimSpace(in.ContentYAML); in.ContentYAML == "" {
		fe["content_yaml"] = "обязательное поле"
	}
	return fe
}

// createConfigProfile — POST /config_profiles (config.write).
func (h *handlers) createConfigProfile(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	var in configProfileInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if fe := in.validate(); len(fe) > 0 {
		writeValidation(w, fe)
		return
	}
	scopeID := uuid.MustParse(in.ScopeID)
	// scope_id должен существовать — проверка по типу области.
	if !h.profileScopeExists(w, r, in.ScopeType, scopeID) {
		return
	}
	var parent *uuid.UUID
	if in.ParentID != "" {
		pid, err := uuid.Parse(in.ParentID)
		if err != nil {
			writeValidation(w, fieldErrors{"parent_id": "UUID"})
			return
		}
		if _, err := h.d.Store.ConfigProfiles.Get(r.Context(), pid); err != nil {
			writeStoreError(w, err)
			return
		}
		parent = &pid
	}
	p, err := h.d.Store.ConfigProfiles.Create(r.Context(), orgID, in.Name, in.Description, in.ScopeType, scopeID, parent, in.ContentYAML)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	objType := "config_profile"
	h.audit(r, identityFrom(r.Context()), "config_profiles.create", &objType, &p.ID, "success", "профиль "+p.Name+" ("+p.ScopeType+")")
	writeJSON(w, http.StatusCreated, p)
}

// profileScopeExists — проверка существования объекта области с учётом
// scoping (объект вне scope → 404, п. 8).
func (h *handlers) profileScopeExists(w http.ResponseWriter, r *http.Request, scopeType string, scopeID uuid.UUID) bool {
	ctx := r.Context()
	var err error
	switch scopeType {
	case "cluster":
		if !h.clusterAllowed(w, r, scopeID) {
			return false
		}
		_, err = h.d.Store.Clusters.Get(ctx, scopeID)
	case "host":
		var host store.Host
		host, err = h.d.Store.Hosts.Get(ctx, scopeID)
		if err == nil && !h.clusterAllowed(w, r, host.ClusterID) {
			return false
		}
	case "instance":
		var inst store.Instance
		inst, err = h.d.Store.Instances.Get(ctx, scopeID)
		if err == nil && !h.instanceAllowed(w, r, inst.HostID) {
			return false
		}
	}
	if err != nil {
		writeStoreError(w, err)
		return false
	}
	return true
}

// listConfigProfiles — GET /config_profiles (config.read).
func (h *handlers) listConfigProfiles(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	scopeType := r.URL.Query().Get("scope_type")
	if scopeType != "" && !profileScopes[scopeType] {
		writeValidation(w, fieldErrors{"scope_type": "cluster | host | instance"})
		return
	}
	var scopeID uuid.UUID
	if v := r.URL.Query().Get("scope_id"); v != "" {
		var err error
		scopeID, err = uuid.Parse(v)
		if err != nil {
			writeValidation(w, fieldErrors{"scope_id": "UUID"})
			return
		}
	}
	cursor, limit, ok := parsePage(w, r)
	if !ok {
		return
	}
	items, next, err := h.d.Store.ConfigProfiles.List(r.Context(), orgID, scopeType, scopeID, cursor, limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

// getConfigProfile — GET /config_profiles/{id} (config.read).
func (h *handlers) getConfigProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	p, err := h.d.Store.ConfigProfiles.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !h.profileScopeExists(w, r, p.ScopeType, p.ScopeID) {
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// updateConfigProfile — PATCH /config_profiles/{id} (config.write).
// Смена content_yaml инкрементирует version.
func (h *handlers) updateConfigProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	var in struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		ContentYAML *string `json:"content_yaml"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
		writeValidation(w, fieldErrors{"name": "не может быть пустым"})
		return
	}
	p, err := h.d.Store.ConfigProfiles.Update(r.Context(), id, in.Name, in.Description, in.ContentYAML)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	objType := "config_profile"
	h.audit(r, identityFrom(r.Context()), "config_profiles.update", &objType, &p.ID, "success", "версия "+strconv.Itoa(p.Version))
	writeJSON(w, http.StatusOK, p)
}

// deleteConfigProfile — DELETE /config_profiles/{id} (config.write).
func (h *handlers) deleteConfigProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	if err := h.d.Store.ConfigProfiles.Delete(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	objType := "config_profile"
	h.audit(r, identityFrom(r.Context()), "config_profiles.delete", &objType, &id, "success", "")
	w.WriteHeader(http.StatusNoContent)
}

// renderConfigProfile — GET /config_profiles/{id}/render?target=<instance_id>
// (config.read): отрендерить итоговый suricata.yaml для цели — цепочка
// наследования (store.Chain, root→tip) мержится, подставляются {{var}}
// (internal/cfgrender; синтаксис и источники значений — в доке пакета).
func (h *handlers) renderConfigProfile(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	targetParam := r.URL.Query().Get("target")
	targetID, err := uuid.Parse(targetParam)
	if targetParam == "" || err != nil {
		writeValidation(w, fieldErrors{"target": "обязательный UUID инстанса"})
		return
	}
	p, err := h.d.Store.ConfigProfiles.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if p.OrganizationID != orgID {
		writeError(w, http.StatusNotFound, CodeNotFound, "ресурс не найден", nil)
		return
	}
	if !h.profileScopeExists(w, r, p.ScopeType, p.ScopeID) {
		return
	}
	inst, err := h.d.Store.Instances.Get(r.Context(), targetID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !h.instanceAllowed(w, r, inst.HostID) { // scoping (чанк 43)
		return
	}

	yml, sources, err := h.renderProfile(r, id, inst)
	if err != nil {
		writeRenderError(w, err)
		return
	}
	objType := "config_profile"
	h.audit(r, identityFrom(r.Context()), "config_profiles.render", &objType, &p.ID, "success",
		"цель "+inst.Name+" ("+strconv.Itoa(len(sources))+" профилей в цепочке)")
	writeJSON(w, http.StatusOK, map[string]any{"rendered_yaml": yml, "sources": sources})
}

// deployConfigProfile — POST /config_profiles/{id}/deploy (config.write):
// интеграция рендера с deploy_config (чанк 66) — отрендерить профиль для
// инстанса, сохранить результат версией конфигурации (content-addressed,
// авто cfg-v<N>) и отправить агенту задачу deploy_config.
func (h *handlers) deployConfigProfile(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	var in deployConfigInput // instance_id + validate_only
	if !decodeJSON(w, r, &in) {
		return
	}
	instID, err := uuid.Parse(in.InstanceID)
	if err != nil {
		writeValidation(w, fieldErrors{"instance_id": "обязательный UUID"})
		return
	}
	p, err := h.d.Store.ConfigProfiles.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if p.OrganizationID != orgID {
		writeError(w, http.StatusNotFound, CodeNotFound, "ресурс не найден", nil)
		return
	}
	if !h.profileScopeExists(w, r, p.ScopeType, p.ScopeID) {
		return
	}
	inst, err := h.d.Store.Instances.Get(r.Context(), instID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !h.instanceAllowed(w, r, inst.HostID) { // scoping (чанк 43)
		return
	}

	yml, _, err := h.renderProfile(r, id, inst)
	if err != nil {
		writeRenderError(w, err)
		return
	}
	note := "render профиля " + p.Name + " v" + strconv.Itoa(p.Version) + " → " + inst.Name
	cv, _, err := h.storeConfigVersion(r.Context(), orgID, "", yml, note)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	taskID, agentID, err := h.dispatchDeployConfigTask(r.Context(), cv, inst, in.ValidateOnly)
	if errors.Is(err, errAgentOffline) {
		writeError(w, http.StatusConflict, CodeConflict, errAgentOffline.Error(), nil)
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	objType := "config_profile"
	reason := "деплой на инстанс " + inst.Name + ", версия " + cv.Version + " (задача " + taskID + ")"
	h.audit(r, identityFrom(r.Context()), "config_profiles.deploy", &objType, &p.ID, "success", reason)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"task_id": taskID, "config_version_id": cv.ID, "version": cv.Version,
		"instance_id": instID, "agent_id": agentID, "profile_id": p.ID,
	})
}

// renderProfile — цепочка наследования + факты цели + cfgrender.Render
// для пары (профиль, инстанс-цель); маппинг ошибок — в writeRenderError.
func (h *handlers) renderProfile(r *http.Request, profileID uuid.UUID, inst store.Instance) (string, []cfgrender.Source, error) {
	chainStore, err := h.d.Store.ConfigProfiles.Chain(r.Context(), profileID)
	if err != nil {
		return "", nil, err
	}
	facts, err := h.renderTarget(r, inst)
	if err != nil {
		return "", nil, err
	}
	chain := make([]cfgrender.Profile, len(chainStore))
	for i, cp := range chainStore {
		chain[i] = cfgrender.Profile{ID: cp.ID.String(), Name: cp.Name, ScopeType: cp.ScopeType, Content: cp.ContentYAML}
	}
	return cfgrender.Render(chain, facts)
}

// writeRenderError — маппинг ошибок рендера на HTTP: неизвестные
// переменные → 400 с деталями, прочее → 400 validation_failed.
func writeRenderError(w http.ResponseWriter, err error) {
	var uv *cfgrender.UnknownVarsError
	if errors.As(err, &uv) {
		writeError(w, http.StatusBadRequest, CodeValidation,
			"неизвестные переменные профиля", map[string]any{"variables": uv.Names})
		return
	}
	writeError(w, http.StatusBadRequest, CodeValidation, err.Error(), nil)
}

// renderTarget — факты об инстансе-цели для встроенных переменных
// (instance.*/host.*/cluster.*): инстанс, его хост и кластер хоста.
func (h *handlers) renderTarget(r *http.Request, inst store.Instance) (cfgrender.Target, error) {
	ctx := r.Context()
	t := cfgrender.Target{
		InstanceID:   inst.ID.String(),
		InstanceName: inst.Name,
		ConfigPath:   inst.ConfigPath,
		RulesDir:     inst.RulesDir,
		LogDir:       inst.LogDir,
	}
	if len(inst.CaptureInterfaces) > 0 {
		t.Interface = inst.CaptureInterfaces[0]
	}
	host, err := h.d.Store.Hosts.Get(ctx, inst.HostID)
	if err != nil {
		return t, err
	}
	t.HostID = host.ID.String()
	t.Hostname = host.Hostname
	if len(host.IPAddresses) > 0 {
		t.HostIP = host.IPAddresses[0]
	}
	cluster, err := h.d.Store.Clusters.Get(ctx, host.ClusterID)
	if err != nil {
		return t, err
	}
	t.ClusterID = cluster.ID.String()
	t.ClusterName = cluster.Name
	return t, nil
}
