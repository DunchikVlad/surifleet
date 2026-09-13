package httpapi

import (
	"net"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// Простая валидация входа без тяжёлых библиотек (по ТЗ).
var (
	// slug: строчные буквы/цифры/дефис, начинается с буквы или цифры, 1..63.
	slugRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	// hostname: RFC 1123 (буквы/цифры/дефис/точка), 1..253.
	hostnameRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9.-]{0,251}[a-zA-Z0-9])?$`)
)

// fieldErrors накапливает ошибки полей формы для details ответа.
type fieldErrors map[string]string

func (fe fieldErrors) add(field, msg string) { fe[field] = msg }
func (fe fieldErrors) any() bool             { return len(fe) > 0 }

// writeValidation пишет validation_failed с деталями по полям.
func writeValidation(w http.ResponseWriter, fe fieldErrors) {
	details := map[string]any{"fields": map[string]string(fe)}
	writeError(w, http.StatusBadRequest, CodeValidation, "ошибка валидации входных данных", details)
}

// validSlug проверяет формат slug.
func validSlug(s string) bool { return slugRe.MatchString(s) }

// validName проверяет непустое имя разумной длины.
func validName(s string) bool {
	n := len(strings.TrimSpace(s))
	return n >= 1 && n <= 200
}

// validHostname проверяет формат hostname.
func validHostname(s string) bool { return hostnameRe.MatchString(s) }

// validIP проверяет, что строка — IP-адрес (v4/v6, без маски).
func validIP(s string) bool { return net.ParseIP(s) != nil }

// pathUUID извлекает UUID path-параметра; при ошибке пишет validation_failed.
func pathUUID(w http.ResponseWriter, s, param string) (uuid.UUID, bool) {
	id, err := uuid.Parse(s)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation,
			"некорректный UUID в пути", map[string]any{param: s})
		return uuid.Nil, false
	}
	return id, true
}

// queryUUID разбирает необязательный UUID query-параметра (пусто → uuid.Nil).
func queryUUID(w http.ResponseWriter, r *http.Request, param string) (uuid.UUID, bool) {
	s := r.URL.Query().Get(param)
	if s == "" {
		return uuid.Nil, true
	}
	id, err := uuid.Parse(s)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation,
			"некорректный UUID в query", map[string]any{param: s})
		return uuid.Nil, false
	}
	return id, true
}
