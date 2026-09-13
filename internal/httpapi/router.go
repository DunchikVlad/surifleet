// Package httpapi — REST-слой SuriFleet: chi-роутер /api/v1, единый формат
// ошибок openapi, keyset-пагинация, валидация входа и middleware
// (request-id, access-лог, recoverer, dev-заглушка auth).
package httpapi

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/surifleet/surifleet/internal/store"
)

// Deps — зависимости REST-слоя.
type Deps struct {
	Log     *slog.Logger
	Version string
	Commit  string

	Store *store.Store

	// PingDB проверяет живость PostgreSQL для /health (nil — проверка выкл.).
	PingDB func(ctx context.Context) error
}

// NewRouter собирает chi-роутер со всеми маршрутами /api/v1.
func NewRouter(d Deps) http.Handler {
	h := &handlers{d: d}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(AccessLog(d.Log))
	r.Use(DevAuth)

	// Единый формат ошибок и для маршрутов вне API.
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, CodeNotFound, "маршрут не найден", nil)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, CodeNotFound, "метод не поддерживается", nil)
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", h.health)
		r.Get("/version", h.version)

		r.Route("/organizations", func(r chi.Router) {
			r.Get("/", h.listOrganizations)
			r.Post("/", h.createOrganization)
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", h.getOrganization)
				r.Patch("/", h.updateOrganization)
				r.Delete("/", h.deleteOrganization)
			})
		})

		r.Route("/clusters", func(r chi.Router) {
			r.Get("/", h.listClusters)
			r.Post("/", h.createCluster)
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", h.getCluster)
				r.Patch("/", h.updateCluster)
				r.Delete("/", h.deleteCluster)
			})
		})

		r.Route("/hosts", func(r chi.Router) {
			r.Get("/", h.listHosts)
			r.Post("/", h.createHost)
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", h.getHost)
				r.Patch("/", h.updateHost)
				r.Delete("/", h.deleteHost)
			})
		})
	})

	return r
}

// handlers — обработчики маршрутов с доступом к зависимостям.
type handlers struct {
	d Deps
}

// health — GET /api/v1/health: живость процесса и PostgreSQL.
func (h *handlers) health(w http.ResponseWriter, r *http.Request) {
	status := "ok"
	checks := map[string]string{}
	if h.d.PingDB != nil {
		if err := h.d.PingDB(r.Context()); err != nil {
			status = "degraded"
			checks["postgres"] = "down"
		} else {
			checks["postgres"] = "up"
		}
	}
	code := http.StatusOK
	if status != "ok" {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{"status": status, "checks": checks})
}

// version — GET /api/v1/version: версия и коммит сборки.
func (h *handlers) version(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"version": h.d.Version, "commit": h.d.Commit})
}
