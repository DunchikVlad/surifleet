package httpapi

import (
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/surifleet/surifleet/internal/iocrules"
	"github.com/surifleet/surifleet/internal/rules"
	"github.com/surifleet/surifleet/internal/store"
)

// Допустимые значения enum (CHECK в DDL + enum openapi).
var (
	iocTypes = map[string]bool{
		"ip": true, "domain": true, "url": true,
		"md5": true, "sha1": true, "sha256": true, "email": true,
	}
	iocStatuses = map[string]bool{
		"active": true, "under_review": true, "expired": true, "revoked": true,
	}
)

// hexRe — hex-строка заданной длины (хэши md5/sha1/sha256).
var hexRe = regexp.MustCompile(`^[0-9a-fA-F]+$`)

// validIocValue — облегчённая валидация значения по типу IOC.
// Не RFC-строгая: цель — отсеять явный мусор до записи в репозиторий.
func validIocValue(typ, value string) bool {
	v := strings.TrimSpace(value)
	switch typ {
	case "ip":
		if net.ParseIP(v) != nil {
			return true
		}
		_, _, err := net.ParseCIDR(v) // iprep-фиды несут подсети
		return err == nil
	case "domain":
		return validHostname(v)
	case "url":
		u, err := url.Parse(v)
		return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
	case "md5":
		return len(v) == 32 && hexRe.MatchString(v)
	case "sha1":
		return len(v) == 40 && hexRe.MatchString(v)
	case "sha256":
		return len(v) == 64 && hexRe.MatchString(v)
	case "email":
		return strings.Contains(v, "@") && validHostname(v[strings.LastIndex(v, "@")+1:])
	}
	return false
}

// validateIocInput — общая валидация IocInput (POST /iocs и /iocs/import).
// Пишет ошибки в fe с префиксом поля (для import — items[i].field).
func validateIocInput(fe fieldErrors, prefix string, in store.IocInput) {
	if !iocTypes[in.Type] {
		fe.add(prefix+"type", "ip|domain|url|md5|sha1|sha256|email")
	}
	if strings.TrimSpace(in.Value) == "" {
		fe.add(prefix+"value", "обязательное поле")
	} else if iocTypes[in.Type] && !validIocValue(in.Type, in.Value) {
		fe.add(prefix+"value", "значение не соответствует типу "+in.Type)
	}
	if in.Score < 0 || in.Score > 100 {
		fe.add(prefix+"score", "целое 0..100")
	}
}

// listIocs — GET /api/v1/iocs[?type&status&source&q].
func (h *handlers) listIocs(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	cursor, limit, ok := parsePage(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := store.IocFilter{
		Type:   q.Get("type"),
		Status: q.Get("status"),
		Source: q.Get("source"),
		Q:      q.Get("q"),
	}
	fe := fieldErrors{}
	if f.Type != "" && !iocTypes[f.Type] {
		fe.add("type", "ip|domain|url|md5|sha1|sha256|email")
	}
	if f.Status != "" && !iocStatuses[f.Status] {
		fe.add("status", "active|under_review|expired|revoked")
	}
	if fe.any() {
		writeValidation(w, fe)
		return
	}
	items, next, err := h.d.Store.Iocs.List(r.Context(), orgID, f, cursor, limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page[store.Ioc]{Items: items, NextCursor: next})
}

// createIoc — POST /api/v1/iocs (ручное создание одного IOC).
func (h *handlers) createIoc(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	var in store.IocInput
	if !decodeJSON(w, r, &in) {
		return
	}
	fe := fieldErrors{}
	validateIocInput(fe, "", in)
	if fe.any() {
		writeValidation(w, fe)
		return
	}
	ioc, err := h.d.Store.Iocs.Create(r.Context(), orgID, in)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ioc)
}

// getIoc — GET /api/v1/iocs/{id}.
func (h *handlers) getIoc(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	ioc, err := h.d.Store.Iocs.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ioc)
}

// updateIoc — PATCH /api/v1/iocs/{id} (скоринг, статус, срок жизни, источник).
func (h *handlers) updateIoc(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	var p store.IocPatch
	if !decodeJSON(w, r, &p) {
		return
	}
	fe := fieldErrors{}
	if p.Score != nil && (*p.Score < 0 || *p.Score > 100) {
		fe.add("score", "целое 0..100")
	}
	if p.Status != nil && !iocStatuses[*p.Status] {
		fe.add("status", "active|under_review|expired|revoked")
	}
	if fe.any() {
		writeValidation(w, fe)
		return
	}
	oldIoc, _ := h.d.Store.Iocs.Get(r.Context(), id) // «было» для аудит-diff (чанк 39)
	ioc, err := h.d.Store.Iocs.Update(r.Context(), id, p)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	objType := "ioc"
	h.auditDiff(r, identityFrom(r.Context()), "iocs.update", &objType, &id, oldIoc, ioc)
	// Отзыв сгенерированного правила при revoke IOC (чанк 19): ошибка
	// отзыва не валит запрос — IOC уже переведён, правило догонит свип
	// или повторный вызов.
	if p.Status != nil && *p.Status == "revoked" {
		if _, err := iocrules.RevokeForIoc(r.Context(), ioc.OrganizationID, ioc.Type, ioc.Value, h.d.Store.Rules); err != nil {
			errLog.Error("отзыв IOC-правила после revoke", "ioc_id", ioc.ID, "err", err)
		}
	}
	writeJSON(w, http.StatusOK, ioc)
}

// deleteIoc — DELETE /api/v1/iocs/{id} (жёсткое удаление).
// Перед удалением читаем IOC — его type/value нужны для отзыва
// сгенерированного правила (чанк 19); Get же даёт 404 до DELETE.
func (h *handlers) deleteIoc(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	ioc, err := h.d.Store.Iocs.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if err := h.d.Store.Iocs.Delete(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	if _, err := iocrules.RevokeForIoc(r.Context(), ioc.OrganizationID, ioc.Type, ioc.Value, h.d.Store.Rules); err != nil {
		errLog.Error("отзыв IOC-правила после delete", "ioc_id", ioc.ID, "err", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

// importIocs — POST /api/v1/iocs/import: JSON {source?, items:[IocInput...]}.
// Идемпотентно по (org, type, value): новые — imported, существующие —
// updated (score/source/expires_at; status не перетирается). Ошибочные
// элементы не прерывают импорт остальных (первые 100 ошибок в ответе).
func (h *handlers) importIocs(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, importMaxBytes)
	var in struct {
		Source *string          `json:"source"`
		Items  []store.IocInput `json:"items"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if len(in.Items) == 0 {
		writeError(w, http.StatusBadRequest, CodeValidation,
			"items: обязательное непустое поле (массив IocInput)", nil)
		return
	}

	res := importResult{Errors: []rules.LineError{}}
	for i, item := range in.Items {
		res.TotalLines++
		// source из тела верхнего уровня — дефолт для элементов без своего.
		if item.Source == nil {
			item.Source = in.Source
		}
		fe := fieldErrors{}
		validateIocInput(fe, "", item)
		if fe.any() {
			if len(res.Errors) < importMaxErrors {
				res.Errors = append(res.Errors, rules.LineError{Line: i + 1, Reason: firstFieldError(fe)})
			}
			continue
		}
		_, inserted, err := h.d.Store.Iocs.UpsertImport(r.Context(), orgID, item)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		if inserted {
			res.Imported++
		} else {
			res.Updated++
		}
	}
	writeJSON(w, http.StatusOK, res)
}

// firstFieldError — первая ошибка валидации одной строкой «field: msg».
func firstFieldError(fe fieldErrors) string {
	for f, msg := range fe {
		return f + ": " + msg
	}
	return "ошибка валидации"
}
