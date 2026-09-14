package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/surifleet/surifleet/internal/store"
)

// getDiscovery — GET /api/v1/hosts/{id}/discovery: последний DiscoveryReport
// агента из hosts.discovery (jsonb). Ответ openapi DiscoveryReport:
// {host_id, received_at, instances[], binary{...}} — обёртка собирается
// из колонок, отчёт в БД хранится без неё.
func (h *handlers) getDiscovery(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	raw, at, err := h.d.Store.Hosts.GetDiscovery(r.Context(), id)
	if err != nil {
		writeStoreError(w, err) // хост не найден → 404
		return
	}
	if raw == nil {
		writeError(w, http.StatusNotFound, CodeNotFound,
			"discovery ещё не прислан агентом", nil)
		return
	}
	var report map[string]any
	if err := json.Unmarshal(raw, &report); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "повреждён discovery jsonb", nil)
		return
	}
	instances := report["instances"]
	if instances == nil {
		instances = []any{} // по схеме — массив, не null
	}
	// Нормализация под схему openapi DiscoveryReport.binary: proto3 с
	// omitempty опускает false — built_from_source тогда отсутствует в jsonb.
	if bin, ok := report["binary"].(map[string]any); ok && bin != nil {
		if _, present := bin["built_from_source"]; !present {
			bin["built_from_source"] = false
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"host_id":     id,
		"received_at": at,
		"instances":   instances,
		"binary":      report["binary"],
	})
}

// confirmDiscovery — POST /api/v1/hosts/{id}/confirm_discovery: подтверждение
// найденных инстансов. Идемпотентно: upsert по (host_id, name), повторный
// вызов не дублирует записи. Ответ 201 {items: []Instance}.
func (h *handlers) confirmDiscovery(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	// Хост должен существовать (404, а не ErrForeignKey на каждом инстансе).
	if _, err := h.d.Store.Hosts.Get(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	var in struct {
		Instances []store.InstanceUpsertInput `json:"instances"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	fe := fieldErrors{}
	if in.Instances == nil {
		fe.add("instances", "обязательное поле (массив подтверждаемых инстансов)")
	}
	for i, inst := range in.Instances {
		if !validName(inst.Name) {
			fe.add("instances", "instances["+strconv.Itoa(i)+"].name: обязательное поле, 1..200 символов")
			break
		}
	}
	if fe.any() {
		writeValidation(w, fe)
		return
	}
	items := []store.Instance{}
	for _, inst := range in.Instances {
		created, err := h.d.Store.Instances.Upsert(r.Context(), id, inst)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		items = append(items, created)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"items": items})
}
