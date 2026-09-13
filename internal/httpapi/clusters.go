package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/surifleet/surifleet/internal/store"
)

// listClusters — GET /api/v1/clusters[?organization_id=].
func (h *handlers) listClusters(w http.ResponseWriter, r *http.Request) {
	orgID, ok := queryUUID(w, r, "organization_id")
	if !ok {
		return
	}
	cursor, limit, ok := parsePage(w, r)
	if !ok {
		return
	}
	items, next, err := h.d.Store.Clusters.List(r.Context(), orgID, cursor, limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page[store.Cluster]{Items: items, NextCursor: next})
}

// createCluster — POST /api/v1/clusters.
func (h *handlers) createCluster(w http.ResponseWriter, r *http.Request) {
	var in store.ClusterInput
	if !decodeJSON(w, r, &in) {
		return
	}
	fe := fieldErrors{}
	if in.OrganizationID.String() == "00000000-0000-0000-0000-000000000000" {
		fe.add("organization_id", "обязательное поле (UUID организации)")
	}
	if !validName(in.Name) {
		fe.add("name", "обязательное поле, 1..200 символов")
	}
	if fe.any() {
		writeValidation(w, fe)
		return
	}
	c, err := h.d.Store.Clusters.Create(r.Context(), in)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

// getCluster — GET /api/v1/clusters/{id}.
func (h *handlers) getCluster(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	c, err := h.d.Store.Clusters.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// updateCluster — PATCH /api/v1/clusters/{id}.
func (h *handlers) updateCluster(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	var p store.ClusterPatch
	if !decodeJSON(w, r, &p) {
		return
	}
	fe := fieldErrors{}
	if p.Name != nil && !validName(*p.Name) {
		fe.add("name", "1..200 символов")
	}
	if fe.any() {
		writeValidation(w, fe)
		return
	}
	c, err := h.d.Store.Clusters.Update(r.Context(), id, p)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// deleteCluster — DELETE /api/v1/clusters/{id}.
func (h *handlers) deleteCluster(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	if err := h.d.Store.Clusters.Delete(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
