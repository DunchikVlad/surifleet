package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/surifleet/surifleet/internal/store"
)

// listHosts — GET /api/v1/hosts[?cluster_id=][&q=].
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
	// TODO(chunk 8+): подтянуть agents и instances реальными запросами.
	writeJSON(w, http.StatusOK, hostDetail{Host: host, Agent: nil, Instances: []any{}})
}

// updateHost — PATCH /api/v1/hosts/{id} (hostname, labels).
func (h *handlers) updateHost(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
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
	host, err := h.d.Store.Hosts.Update(r.Context(), id, p)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, host)
}

// deleteHost — DELETE /api/v1/hosts/{id}.
func (h *handlers) deleteHost(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	if err := h.d.Store.Hosts.Delete(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
