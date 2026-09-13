package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/surifleet/surifleet/internal/store"
)

// Машиночитаемые коды ошибок — строго по openapi Error.code.
const (
	CodeValidation = "validation_failed"
	CodeNotFound   = "not_found"
	CodeConflict   = "conflict"
	CodeInternal   = "internal"
)

// errorBody — единый формат ошибки openapi: {"error": {code, message, details?}}.
type errorBody struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details,omitempty"`
	} `json:"error"`
}

// writeJSON пишет JSON-ответ с заданным статусом.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError пишет ошибку в формате openapi Error.
func writeError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	var b errorBody
	b.Error.Code = code
	b.Error.Message = message
	b.Error.Details = details
	writeJSON(w, status, b)
}

// writeStoreError отображает доменные ошибки store на HTTP-коды:
// ErrNotFound → 404, ErrConflict → 409, ErrForeignKey → 400, прочее → 500.
func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, CodeNotFound, "ресурс не найден", nil)
	case errors.Is(err, store.ErrConflict):
		var ce *store.ConflictError
		details := map[string]any{}
		if errors.As(err, &ce) && ce.Constraint != "" {
			details["constraint"] = ce.Constraint
		}
		writeError(w, http.StatusConflict, CodeConflict, "запись конфликтует с существующей (уникальность)", details)
	case errors.Is(err, store.ErrForeignKey):
		writeError(w, http.StatusBadRequest, CodeValidation, "родительская запись не существует", nil)
	default:
		writeError(w, http.StatusInternalServerError, CodeInternal, "внутренняя ошибка сервера", nil)
	}
}

// decodeJSON разбирает тело запроса; при ошибке пишет validation_failed
// и возвращает false.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation,
			"некорректный JSON в теле запроса", map[string]any{"reason": err.Error()})
		return false
	}
	return true
}
