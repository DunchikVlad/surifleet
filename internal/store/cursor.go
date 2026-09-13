package store

import (
	"encoding/base64"
	"fmt"

	"github.com/google/uuid"
)

// Keyset-пагинация (docs/data-model.md §5): курсор — base64url последнего
// id страницы; выборка идёт по `id > cursor ORDER BY id LIMIT n+1`.
// Кодирование без padding — курсор безопасен для query-параметра.

// EncodeCursor кодирует id последней записи страницы в курсор.
func EncodeCursor(id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id.String()))
}

// DecodeCursor раскодирует курсор обратно в UUID.
// Пустая строка — отсутствие курсора (первая страница), ошибкой не является.
func DecodeCursor(s string) (uuid.UUID, error) {
	if s == "" {
		return uuid.Nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return uuid.Nil, fmt.Errorf("некорректный курсор: %w", err)
	}
	id, err := uuid.ParseBytes(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("некорректный курсор: %w", err)
	}
	return id, nil
}
