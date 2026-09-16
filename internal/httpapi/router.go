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

	"github.com/surifleet/surifleet/internal/blob"
	"github.com/surifleet/surifleet/internal/chlogs"
	"github.com/surifleet/surifleet/internal/feedsync"
	"github.com/surifleet/surifleet/internal/orchestrator"
	"github.com/surifleet/surifleet/internal/store"
)

// Deps — зависимости REST-слоя.
type Deps struct {
	Log     *slog.Logger
	Version string
	Commit  string

	Store *store.Store

	// Blob — ruleset-блобы в S3 (сборка ruleset, chunk 11).
	Blob *blob.Store
	// Orch — оркестратор волновых деплоев (chunk 11).
	Orch *orchestrator.Orchestrator

	// CHLogs — чтение логов агентов из ClickHouse (chunk 13c; nil — 503).
	CHLogs *chlogs.Client

	// FeedSync — синхронизация IOC-фидов (chunk 18; nil — sync возвращает 503).
	FeedSync *feedsync.Syncer

	// PingDB проверяет живость PostgreSQL для /health (nil — проверка выкл.).
	PingDB func(ctx context.Context) error
}

// NewRouter собирает chi-роутер со всеми маршрутами /api/v1.
func NewRouter(d Deps) http.Handler {
	h := &handlers{d: d}
	if d.Log != nil {
		errLog = d.Log
	}

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

	// Встроенный Web UI (MVP): / и /ui/* — статика из embed.
	mountWebUI(r)
	// React-фронтенд (чанк 14): /app/* из embed web/dist (или заглушка).
	mountReactUI(r)

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
				r.Get("/join_tokens", h.listJoinTokens)
				r.Post("/join_tokens", h.createJoinToken)
			})
		})

		r.Route("/hosts", func(r chi.Router) {
			r.Get("/", h.listHosts)
			r.Post("/", h.createHost)
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", h.getHost)
				r.Patch("/", h.updateHost)
				r.Delete("/", h.deleteHost)
				r.Get("/discovery", h.getDiscovery)
				r.Post("/confirm_discovery", h.confirmDiscovery)
			})
		})

		r.Route("/instances", func(r chi.Router) {
			r.Get("/", h.listInstances)
			r.Post("/", h.createInstance)
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", h.getInstance)
				r.Patch("/", h.updateInstance)
				r.Delete("/", h.deleteInstance)
				r.Get("/state", h.getInstanceState)
				r.Get("/deploy_history", h.getDeployHistory)
			})
		})

		r.Route("/rules", func(r chi.Router) {
			r.Get("/", h.listRules)
			r.Post("/", h.createRule)
			r.Post("/import", h.importRules)
			r.Post("/bulk", h.bulkRules)
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", h.getRule)
				r.Patch("/", h.updateRule)
				r.Delete("/", h.deleteRule)
				r.Get("/revisions", h.listRuleRevisions)
			})
		})

		r.Route("/rulesets", func(r chi.Router) {
			r.Get("/", h.listRulesets)
			r.Post("/", h.buildRuleset)
			r.Get("/{id}", h.getRuleset)
		})

		r.Route("/iocs", func(r chi.Router) {
			r.Get("/", h.listIocs)
			r.Post("/", h.createIoc)
			r.Post("/import", h.importIocs)
			r.Post("/generate", h.generateIocRules)
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", h.getIoc)
				r.Patch("/", h.updateIoc)
				r.Delete("/", h.deleteIoc)
			})
		})

		r.Route("/feeds", func(r chi.Router) {
			r.Get("/", h.listFeeds)
			r.Post("/", h.createFeed)
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", h.getFeed)
				r.Patch("/", h.updateFeed)
				r.Delete("/", h.deleteFeed)
				r.Post("/sync", h.syncFeed)
				r.Get("/runs", h.listFeedRuns)
			})
		})

		r.Route("/deployments", func(r chi.Router) {
			r.Get("/", h.listDeployments)
			r.Post("/", h.createDeployment)
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", h.getDeployment)
				r.Post("/pause", h.pauseDeployment)
				r.Post("/resume", h.resumeDeployment)
				r.Post("/cancel", h.cancelDeployment)
				r.Get("/tasks", h.listDeploymentTasks)
			})
		})

		r.Get("/fleet/compliance", h.getFleetCompliance)

		r.Get("/matrix/rules", h.getRulesMatrix)

		r.Route("/agents", func(r chi.Router) {
			r.Get("/", h.listAgents)
			r.Get("/{id}/logs", h.getAgentLogs)
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
