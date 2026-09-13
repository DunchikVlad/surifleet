package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/surifleet/surifleet/internal/store"
)

// listOrganizations — GET /api/v1/organizations.
func (h *handlers) listOrganizations(w http.ResponseWriter, r *http.Request) {
	cursor, limit, ok := parsePage(w, r)
	if !ok {
		return
	}
	items, next, err := h.d.Store.Organizations.List(r.Context(), cursor, limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page[store.Organization]{Items: items, NextCursor: next})
}

// createOrganization — POST /api/v1/organizations.
func (h *handlers) createOrganization(w http.ResponseWriter, r *http.Request) {
	var in store.OrganizationInput
	if !decodeJSON(w, r, &in) {
		return
	}
	fe := fieldErrors{}
	if !validName(in.Name) {
		fe.add("name", "обязательное поле, 1..200 символов")
	}
	if !validSlug(in.Slug) {
		fe.add("slug", "строчные буквы/цифры/дефис, 1..63 символа, начинается с буквы или цифры")
	}
	if fe.any() {
		writeValidation(w, fe)
		return
	}
	o, err := h.d.Store.Organizations.Create(r.Context(), in)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, o)
}

// getOrganization — GET /api/v1/organizations/{id}.
func (h *handlers) getOrganization(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	o, err := h.d.Store.Organizations.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

// updateOrganization — PATCH /api/v1/organizations/{id}.
func (h *handlers) updateOrganization(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	var p store.OrganizationPatch
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
	o, err := h.d.Store.Organizations.Update(r.Context(), id, p)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

// deleteOrganization — DELETE /api/v1/organizations/{id} (каскадно).
func (h *handlers) deleteOrganization(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	if err := h.d.Store.Organizations.Delete(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
