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

// DevAuth — ЗАГЛУЖЕНО (чанк 28): аутентификация теперь в authMiddleware
// (auth.go): режим server.auth_mode=dev — условный админ из X-Dev-User,
// token — Bearer-токен сессии. Оставлено для обратной совместимости вызовов.

// IdentityFrom возвращает email/имя текущего пользователя для access-лога
// (identity в контексте ставит authMiddleware, см. auth.go).
func IdentityFrom(ctx context.Context) string {
	if id := identityFrom(ctx); id != nil {
		return id.Email
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
