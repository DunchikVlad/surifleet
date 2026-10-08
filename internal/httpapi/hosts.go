package httpapi

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/surifleet/surifleet/internal/store"
)

// listHosts — GET /api/v1/hosts[?cluster_id=][&q=].
// Scoping (чанк 43): cluster-restricted — только хосты своих кластеров.
func (h *handlers) listHosts(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := queryUUID(w, r, "cluster_id")
	if !ok {
		return
	}
	cursor, limit, ok := parsePage(w, r)
	if !ok {
		return
	}
	q := r.URL.Query().Get("q")
	items, next, err := h.d.Store.Hosts.List(r.Context(), clusterID, cursor, q, limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	id := identityFrom(r.Context())
	if id != nil && id.ScopeRestricted {
		filtered := make([]store.Host, 0, len(items))
		for _, it := range items {
			if id.ClusterScopeAllowed(it.ClusterID) {
				filtered = append(filtered, it)
			}
		}
		items = filtered
	}
	writeJSON(w, http.StatusOK, page[store.Host]{Items: items, NextCursor: next})
}

// createHost — POST /api/v1/hosts (ручная регистрация; обычно — enrollment).
func (h *handlers) createHost(w http.ResponseWriter, r *http.Request) {
	var in store.HostInput
	if !decodeJSON(w, r, &in) {
		return
	}
	fe := fieldErrors{}
	if in.ClusterID == uuid.Nil {
		fe.add("cluster_id", "обязательное поле (UUID кластера)")
	}
	if !validHostname(in.Hostname) {
		fe.add("hostname", "RFC 1123: буквы/цифры/дефис/точка, 1..253 символа")
	}
	for i, ip := range in.IPAddresses {
		if !validIP(ip) {
			fe.add("ip_addresses", "некорректный IP-адрес: "+ip)
			_ = i
			break
		}
	}
	if fe.any() {
		writeValidation(w, fe)
		return
	}
	if !h.clusterAllowed(w, r, in.ClusterID) { // scoping (чанк 43)
		return
	}
	host, err := h.d.Store.Hosts.Create(r.Context(), in)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, host)
}

// hostDetail — openapi HostDetail: хост + агент + инстансы.
// Агент и инстансы появятся в следующих чанках (enrollment/протокол) —
// пока agent = null, instances = [].
type hostDetail struct {
	store.Host
	Agent     any   `json:"agent"`
	Instances []any `json:"instances"`
}

// getHost — GET /api/v1/hosts/{id}.
func (h *handlers) getHost(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	host, err := h.d.Store.Hosts.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !h.clusterAllowed(w, r, host.ClusterID) { // scoping (чанк 43)
		return
	}
	// TODO(chunk 8+): подтянуть agents и instances реальными запросами.
	writeJSON(w, http.StatusOK, hostDetail{Host: host, Agent: nil, Instances: []any{}})
}

// updateHost — PATCH /api/v1/hosts/{id} (hostname, labels).
func (h *handlers) updateHost(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	host, err := h.d.Store.Hosts.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !h.clusterAllowed(w, r, host.ClusterID) { // scoping (чанк 43)
		return
	}
	var p store.HostPatch
	if !decodeJSON(w, r, &p) {
		return
	}
	fe := fieldErrors{}
	if p.Hostname != nil && !validHostname(*p.Hostname) {
		fe.add("hostname", "RFC 1123: буквы/цифры/дефис/точка, 1..253 символа")
	}
	if fe.any() {
		writeValidation(w, fe)
		return
	}
	host2, err := h.d.Store.Hosts.Update(r.Context(), id, p)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, host2)
}

// deleteHost — DELETE /api/v1/hosts/{id}.
func (h *handlers) deleteHost(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	host, err := h.d.Store.Hosts.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !h.clusterAllowed(w, r, host.ClusterID) { // scoping (чанк 43)
		return
	}
	if err := h.d.Store.Hosts.Delete(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// knownCapabilities — каталог capability поэтапной передачи контроля
// (ТЗ п.4; совпадает с proto SetCapabilitiesTask).
var knownCapabilities = map[string]bool{
	"monitoring": true, "rules": true, "log_rotation": true,
	"service_mgmt": true, "packages": true, "config": true,
}

// validateCapabilities — чистая валидация набора: известные, без дублей.
func validateCapabilities(caps []string) []string {
	var bad []string
	seen := map[string]bool{}
	for _, c := range caps {
		if !knownCapabilities[c] || seen[c] {
			bad = append(bad, c)
		}
		seen[c] = true
	}
	return bad
}

// getHostCapabilities — GET /hosts/{id}/capabilities (hosts.read):
// host-level записи (пусто — наследуется от кластера/дефолта monitoring).
func (h *handlers) getHostCapabilities(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	host, err := h.d.Store.Hosts.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !h.clusterAllowed(w, r, host.ClusterID) { // scoping (чанк 43)
		return
	}
	caps, err := h.d.Store.Capabilities.HostCaps(r.Context(), host.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"capabilities": caps, "known": []string{"monitoring", "rules", "log_rotation", "service_mgmt", "packages", "config"}})
}

// setHostCapabilities — PUT /hosts/{id}/capabilities (hosts.write):
// заменить host-level набор. Агент применит его при следующем Hello
// (HelloAck.Config.Capabilities); действующие задачи гейтятся на агенте.
func (h *handlers) setHostCapabilities(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	host, err := h.d.Store.Hosts.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !h.clusterAllowed(w, r, host.ClusterID) { // scoping (чанк 43)
		return
	}
	var in struct {
		Capabilities []string `json:"capabilities"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if bad := validateCapabilities(in.Capabilities); len(bad) > 0 {
		writeValidation(w, fieldErrors{"capabilities": "неизвестные или повторяющиеся: " + strings.Join(bad, ", ")})
		return
	}
	uid := identityFrom(r.Context()).UserID
	if err := h.d.Store.Capabilities.SetHostCaps(r.Context(), host.ID, in.Capabilities, &uid); err != nil {
		writeStoreError(w, err)
		return
	}
	objType := "host"
	reason := "capabilities = [" + strings.Join(in.Capabilities, ", ") + "]"
	h.audit(r, identityFrom(r.Context()), "hosts.capabilities", &objType, &host.ID, "success", reason)
	writeJSON(w, http.StatusOK, map[string]any{"capabilities": in.Capabilities})
}

// getClusterCapabilities — GET /clusters/{id}/capabilities (hosts.read):
// cluster-level записи (хосты без своих записей наследуют этот набор).
func (h *handlers) getClusterCapabilities(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	if !h.clusterAllowed(w, r, id) { // scoping (чанк 43)
		return
	}
	if _, err := h.d.Store.Clusters.Get(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	caps, err := h.d.Store.Capabilities.ClusterCaps(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"capabilities": caps, "known": []string{"monitoring", "rules", "log_rotation", "service_mgmt", "packages", "config"}})
}

// setClusterCapabilities — PUT /clusters/{id}/capabilities (hosts.write).
func (h *handlers) setClusterCapabilities(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	if !h.clusterAllowed(w, r, id) { // scoping (чанк 43)
		return
	}
	if _, err := h.d.Store.Clusters.Get(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	var in struct {
		Capabilities []string `json:"capabilities"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if bad := validateCapabilities(in.Capabilities); len(bad) > 0 {
		writeValidation(w, fieldErrors{"capabilities": "неизвестные или повторяющиеся: " + strings.Join(bad, ", ")})
		return
	}
	uid := identityFrom(r.Context()).UserID
	if err := h.d.Store.Capabilities.SetClusterCaps(r.Context(), id, in.Capabilities, &uid); err != nil {
		writeStoreError(w, err)
		return
	}
	objType := "cluster"
	reason := "capabilities = [" + strings.Join(in.Capabilities, ", ") + "]"
	h.audit(r, identityFrom(r.Context()), "clusters.capabilities", &objType, &id, "success", reason)
	writeJSON(w, http.StatusOK, map[string]any{"capabilities": in.Capabilities})
}
