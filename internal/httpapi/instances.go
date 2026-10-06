package httpapi

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/surifleet/surifleet/internal/store"
)

// listInstances — GET /api/v1/instances[?host_id=][&cluster_id=].
// Scoping (чанк 43): cluster-restricted — только инстансы своих кластеров.
func (h *handlers) listInstances(w http.ResponseWriter, r *http.Request) {
	hostID, ok := queryUUID(w, r, "host_id")
	if !ok {
		return
	}
	clusterID, ok := queryUUID(w, r, "cluster_id")
	if !ok {
		return
	}
	cursor, limit, ok := parsePage(w, r)
	if !ok {
		return
	}
	// Scoping: restricted — ограничиваем выборку своими кластерами.
	var allowed []uuid.UUID
	if id := identityFrom(r.Context()); id != nil && id.ScopeRestricted {
		allowed = id.ScopeClusters // пусто → ничего не видит (ListScoped вернёт [])
		// Явный ?cluster_id= вне scope → пустой результат (вне scope несуществует).
		if clusterID != uuid.Nil && !id.ClusterScopeAllowed(clusterID) {
			writeJSON(w, http.StatusOK, page[store.Instance]{Items: []store.Instance{}, NextCursor: nil})
			return
		}
	}
	items, next, err := h.d.Store.Instances.ListScoped(r.Context(), hostID, clusterID, allowed, cursor, limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page[store.Instance]{Items: items, NextCursor: next})
}

// createInstance — POST /api/v1/instances (ручное добавление; обычно инстансы
// появляются через discovery + confirm_discovery).
func (h *handlers) createInstance(w http.ResponseWriter, r *http.Request) {
	var in store.InstanceInput
	if !decodeJSON(w, r, &in) {
		return
	}
	fe := fieldErrors{}
	if in.HostID == uuid.Nil {
		fe.add("host_id", "обязательное поле (UUID хоста)")
	}
	if !validName(in.Name) {
		fe.add("name", "обязательное поле, 1..200 символов")
	}
	if strings.TrimSpace(in.ConfigPath) == "" {
		fe.add("config_path", "обязательное поле (путь к suricata.yaml)")
	}
	if strings.TrimSpace(in.RulesDir) == "" {
		fe.add("rules_dir", "обязательное поле (каталог правил)")
	}
	if strings.TrimSpace(in.LogDir) == "" {
		fe.add("log_dir", "обязательное поле (каталог логов)")
	}
	if fe.any() {
		writeValidation(w, fe)
		return
	}
	if !h.instanceAllowed(w, r, in.HostID) { // scoping (чанк 43): хост вне scope
		return
	}
	inst, err := h.d.Store.Instances.Create(r.Context(), in)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, inst)
}

// getInstance — GET /api/v1/instances/{id}.
func (h *handlers) getInstance(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	inst, err := h.d.Store.Instances.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !h.instanceAllowed(w, r, inst.HostID) { // scoping (чанк 43)
		return
	}
	writeJSON(w, http.StatusOK, inst)
}

// updateInstance — PATCH /api/v1/instances/{id} (пути, интерфейсы, юнит).
func (h *handlers) updateInstance(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	cur, err := h.d.Store.Instances.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !h.instanceAllowed(w, r, cur.HostID) { // scoping (чанк 43)
		return
	}
	var p store.InstancePatch
	if !decodeJSON(w, r, &p) {
		return
	}
	fe := fieldErrors{}
	if p.Name != nil && !validName(*p.Name) {
		fe.add("name", "1..200 символов")
	}
	if p.ConfigPath != nil && strings.TrimSpace(*p.ConfigPath) == "" {
		fe.add("config_path", "не может быть пустым")
	}
	if p.RulesDir != nil && strings.TrimSpace(*p.RulesDir) == "" {
		fe.add("rules_dir", "не может быть пустым")
	}
	if p.LogDir != nil && strings.TrimSpace(*p.LogDir) == "" {
		fe.add("log_dir", "не может быть пустым")
	}
	if fe.any() {
		writeValidation(w, fe)
		return
	}
	inst, err := h.d.Store.Instances.Update(r.Context(), id, p)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, inst)
}

// deleteInstance — DELETE /api/v1/instances/{id} (из-под управления).
func (h *handlers) deleteInstance(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	cur, err := h.d.Store.Instances.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !h.instanceAllowed(w, r, cur.HostID) { // scoping (чанк 43)
		return
	}
	if err := h.d.Store.Instances.Delete(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
