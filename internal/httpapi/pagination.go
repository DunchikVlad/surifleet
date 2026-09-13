package httpapi

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/surifleet/surifleet/internal/store"
)

// Пределы пагинации по openapi Limit: default 100, min 1, max 1000.
const (
	defaultLimit = 100
	maxLimit     = 1000
)

// parsePage разбирает ?limit и ?cursor. При ошибке пишет validation_failed
// и возвращает ok == false.
func parsePage(w http.ResponseWriter, r *http.Request) (cursor uuid.UUID, limit int, ok bool) {
	limit = defaultLimit
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, CodeValidation,
				"limit должен быть целым числом от 1 до 1000",
				map[string]any{"limit": s})
			return uuid.Nil, 0, false
		}
		if n > maxLimit {
			n = maxLimit
		}
		limit = n
	}

	cursor, err := store.DecodeCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation,
			"некорректный cursor", map[string]any{"cursor": r.URL.Query().Get("cursor")})
		return uuid.Nil, 0, false
	}
	return cursor, limit, true
}

// page — обёртка списочного ответа openapi: {items, next_cursor}.
type page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}
