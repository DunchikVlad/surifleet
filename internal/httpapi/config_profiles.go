// Профили конфигурации Suricata (чанк 64, план 1B): CRUD
// /config_profiles по openapi-спеке. Наследование кластер → хост →
// инстанс через parent_id; рендер шаблона с переменными, история
// версий и валидация через агента — следующие чанки.
package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

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
