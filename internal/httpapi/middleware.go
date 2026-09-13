package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// identityKey — ключ контекста для identity текущего пользователя.
type identityKey struct{}

// DevAuth — ЗАГЛУШКА аутентификации для dev-контура.
//
// TODO(security): заменить на реальный OIDC (проверка JWT access-token'а,
// JIT-провижининг пользователя, RBAC по правам вида organizations.read).
// Сейчас identity берётся из заголовка X-Dev-User (default "dev-admin")
// без какой-либо проверки — допустимо только в тестовой LAN.
func DevAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := r.Header.Get("X-Dev-User")
		if user == "" {
			user = "dev-admin"
		}
		ctx := context.WithValue(r.Context(), identityKey{}, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// IdentityFrom возвращает identity пользователя из контекста (см. DevAuth).
func IdentityFrom(ctx context.Context) string {
	if v, ok := ctx.Value(identityKey{}).(string); ok {
		return v
	}
	return ""
}

// statusWriter запоминает статус ответа для access-лога.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// AccessLog — structured access-лог запросов через slog.
func AccessLog(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)
			log.Info("http",
				"request_id", middleware.GetReqID(r.Context()),
				"user", IdentityFrom(r.Context()),
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"remote", r.RemoteAddr,
			)
		})
	}
}
