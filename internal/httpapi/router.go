// Package httpapi — REST-слой SuriFleet: chi-роутер /api/v1, единый формат
// ошибок openapi, keyset-пагинация, валидация входа и middleware
// (request-id, access-лог, recoverer, dev-заглушка auth).
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/surifleet/surifleet/internal/blob"
	"github.com/surifleet/surifleet/internal/chlogs"
	"github.com/surifleet/surifleet/internal/feedsync"
	"github.com/surifleet/surifleet/internal/hub"
	"github.com/surifleet/surifleet/internal/notify"
	"github.com/surifleet/surifleet/internal/oidc"
	"github.com/surifleet/surifleet/internal/orchestrator"
	"github.com/surifleet/surifleet/internal/samlauth"
	"github.com/surifleet/surifleet/internal/store"
)

// Deps — зависимости REST-слоя.
type Deps struct {
	Log     *slog.Logger
	Version string
	Commit  string

	Store *store.Store

	// AuthMode — dev (заглушка X-Dev-User) | token (сессии, чанк 28).
	AuthMode string
	// SessionTTL — срок жизни токена сессии (token-режим; 0 → 12h).
	SessionTTL time.Duration

	// Blob — ruleset-блобы в S3 (сборка ruleset, chunk 11).
	Blob *blob.Store
	// Orch — оркестратор волновых деплоев (chunk 11).
	Orch *orchestrator.Orchestrator
	// Hub — прямая отправка задач агентам (deploy_config, чанк 54).
	Hub *hub.Server

	// CHLogs — чтение логов агентов из ClickHouse (chunk 13c; nil — 503).
	CHLogs *chlogs.Client

	// FeedSync — синхронизация IOC-фидов (chunk 18; nil — sync возвращает 503).
	FeedSync *feedsync.Syncer

	// OIDC — OIDC-SSO flow (chunk 35; nil — SSO-эндпоинты возвращают 503).
	OIDC *oidc.Service
	// SAML — SAML 2.0 SSO (chunk 41; nil — SAML-эндпоинты возвращают 503).
	SAML *samlauth.Service

	// Notify — отправка уведомлений в каналы webhook/telegram (chunk 83;
	// nil — POST /notification_channels/{id}/test возвращает 503).
	Notify *notify.Sender

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
	r.Use(h.authMiddleware)

	// Единый формат ошибок и для маршрутов вне API.
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, CodeNotFound, "маршрут не найден", nil)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, CodeNotFound, "метод не поддерживается", nil)
	})

	// React-фронтенд (чанк 14): / → /app/, /app/* из embed web/dist
	// (или заглушка). Ванильный MVP UI выпилен (чанк 30).
	mountReactUI(r)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", h.health)
		r.Get("/version", h.version)

		// Аутентификация (чанк 28): login/refresh — публичные.
		r.Route("/auth", func(r chi.Router) {
			r.Post("/login", h.login)
			r.Post("/refresh", h.refresh)
			r.With(h.requirePerm(PermFleetRead)).Post("/logout", h.logout)
			r.Get("/me", h.getMe)
			// OIDC-SSO (чанк 35): публичные discovery/login/callback.
			r.Get("/sso/providers", h.listSsoProvidersPublic)
			r.Get("/sso/{id}/login", h.ssoLogin)
			r.Get("/sso/callback", h.ssoCallback)
			// LDAP/AD (чанк 40): bind-вход — публичный.
			r.Post("/ldap/login", h.ldapLogin)
			// SAML 2.0 (чанк 41): SP-метаданные/login/ACS — публичные.
			r.Get("/saml/{id}/metadata", h.samlMetadata)
			r.Get("/saml/{id}/login", h.samlLogin)
			r.Post("/saml/acs", h.samlACS)
		})

		// SSO-провайдеры (администрирование; чанк 35).
		r.Route("/sso_providers", func(r chi.Router) {
			r.With(h.requirePerm(PermSsoRead)).Get("/", h.listSsoProviders)
			r.With(h.requirePerm(PermSsoWrite)).Post("/", h.createSsoProvider)
			r.Route("/{id}", func(r chi.Router) {
				r.With(h.requirePerm(PermSsoRead)).Get("/", h.getSsoProvider)
				r.With(h.requirePerm(PermSsoWrite)).Patch("/", h.updateSsoProvider)
				r.With(h.requirePerm(PermSsoWrite)).Delete("/", h.deleteSsoProvider)
			})
		})

		// Пользователи, роли, аудит (chunk 28).
		r.Route("/users", func(r chi.Router) {
			r.With(h.requirePerm(PermUsersRead)).Get("/", h.listUsers)
			r.With(h.requirePerm(PermUsersWrite)).Post("/", h.createUser)
			r.Route("/{id}", func(r chi.Router) {
				r.With(h.requirePerm(PermUsersRead)).Get("/", h.getUser)
				r.With(h.requirePerm(PermUsersWrite)).Patch("/", h.updateUser)
				r.With(h.requirePerm(PermUsersWrite)).Delete("/", h.deleteUser)
				r.With(h.requirePerm(PermUsersWrite)).Post("/revoke_sessions", h.revokeUserSessions)
			})
		})
		r.Route("/roles", func(r chi.Router) {
			r.With(h.requirePerm(PermRolesRead)).Get("/", h.listRoles)
			r.With(h.requirePerm(PermRolesWrite)).Post("/", h.createRole)
			r.Route("/{id}", func(r chi.Router) {
				r.With(h.requirePerm(PermRolesRead)).Get("/", h.getRole)
				r.With(h.requirePerm(PermRolesWrite)).Patch("/", h.updateRole)
				r.With(h.requirePerm(PermRolesWrite)).Delete("/", h.deleteRole)
			})
		})
		r.With(h.requirePerm(PermAuditRead)).Get("/audit_log", h.listAuditLog)
		r.With(h.requirePerm(PermAuditRead)).Get("/audit_log/export", h.exportAuditLog)
		r.With(h.requirePerm(PermAuditRead)).Get("/audit_log/verify", h.verifyAuditChain)

		r.Route("/api_tokens", func(r chi.Router) {
			r.With(h.requirePerm(PermTokensRead)).Get("/", h.listApiTokens)
			r.With(h.requirePerm(PermTokensWrite)).Post("/", h.createApiToken)
			r.With(h.requirePerm(PermTokensWrite)).Delete("/{id}", h.revokeApiToken)
		})

		r.Route("/config_versions", func(r chi.Router) {
			r.With(h.requirePerm(PermConfigRead)).Get("/", h.listConfigVersions)
			r.With(h.requirePerm(PermConfigWrite)).Post("/", h.createConfigVersion)
			r.Route("/{id}", func(r chi.Router) {
				r.With(h.requirePerm(PermConfigRead)).Get("/", h.getConfigVersion)
				r.With(h.requirePerm(PermConfigRead)).Get("/content", h.getConfigContent)
				r.With(h.requirePerm(PermConfigWrite)).Post("/deploy", h.deployConfig)
				r.With(h.requirePerm(PermConfigWrite)).Post("/deploy_wave", h.deployConfigWave)
			})
		})

		r.Route("/organizations", func(r chi.Router) {
			r.With(h.requirePerm(PermHostsRead)).Get("/", h.listOrganizations)
			r.With(h.requirePerm(PermAll)).Post("/", h.createOrganization)
			r.Route("/{id}", func(r chi.Router) {
				r.With(h.requirePerm(PermHostsRead)).Get("/", h.getOrganization)
				r.With(h.requirePerm(PermAll)).Patch("/", h.updateOrganization)
				r.With(h.requirePerm(PermAll)).Delete("/", h.deleteOrganization)
			})
		})

		r.Route("/clusters", func(r chi.Router) {
			r.With(h.requirePerm(PermHostsRead)).Get("/", h.listClusters)
			r.With(h.requirePerm(PermHostsWrite)).Post("/", h.createCluster)
			r.Route("/{id}", func(r chi.Router) {
				r.With(h.requirePerm(PermHostsRead)).Get("/", h.getCluster)
				r.With(h.requirePerm(PermHostsWrite)).Patch("/", h.updateCluster)
				r.With(h.requirePerm(PermHostsWrite)).Delete("/", h.deleteCluster)
				r.With(h.requirePerm(PermHostsWrite)).Get("/join_tokens", h.listJoinTokens)
				r.With(h.requirePerm(PermHostsWrite)).Post("/join_tokens", h.createJoinToken)
				r.With(h.requirePerm(PermHostsRead)).Get("/capabilities", h.getClusterCapabilities)
				r.With(h.requirePerm(PermHostsWrite)).Put("/capabilities", h.setClusterCapabilities)
			})
		})

		r.Route("/hosts", func(r chi.Router) {
			r.With(h.requirePerm(PermHostsRead)).Get("/", h.listHosts)
			r.With(h.requirePerm(PermHostsWrite)).Post("/", h.createHost)
			r.Route("/{id}", func(r chi.Router) {
				r.With(h.requirePerm(PermHostsRead)).Get("/", h.getHost)
				r.With(h.requirePerm(PermHostsWrite)).Patch("/", h.updateHost)
				r.With(h.requirePerm(PermHostsWrite)).Delete("/", h.deleteHost)
				r.With(h.requirePerm(PermHostsRead)).Get("/discovery", h.getDiscovery)
				r.With(h.requirePerm(PermHostsWrite)).Post("/confirm_discovery", h.confirmDiscovery)
				r.With(h.requirePerm(PermHostsRead)).Get("/capabilities", h.getHostCapabilities)
				r.With(h.requirePerm(PermHostsWrite)).Put("/capabilities", h.setHostCapabilities)
			})
		})

		r.Route("/instances", func(r chi.Router) {
			r.With(h.requirePerm(PermHostsRead)).Get("/", h.listInstances)
			r.With(h.requirePerm(PermHostsWrite)).Post("/", h.createInstance)
			r.Route("/{id}", func(r chi.Router) {
				r.With(h.requirePerm(PermHostsRead)).Get("/", h.getInstance)
				r.With(h.requirePerm(PermHostsWrite)).Patch("/", h.updateInstance)
				r.With(h.requirePerm(PermHostsWrite)).Delete("/", h.deleteInstance)
				r.With(h.requirePerm(PermFleetRead)).Get("/state", h.getInstanceState)
				r.With(h.requirePerm(PermFleetRead)).Get("/deploy_history", h.getDeployHistory)
				r.With(h.requirePerm(PermConfigRead)).Get("/config/current", h.fetchInstanceConfig)
				r.With(h.requirePerm(PermConfigRead)).Get("/config/history", h.getInstanceConfigHistory)
			})
		})

		r.Route("/config_profiles", func(r chi.Router) {
			r.With(h.requirePerm(PermConfigRead)).Get("/", h.listConfigProfiles)
			r.With(h.requirePerm(PermConfigWrite)).Post("/", h.createConfigProfile)
			r.Route("/{id}", func(r chi.Router) {
				r.With(h.requirePerm(PermConfigRead)).Get("/", h.getConfigProfile)
				r.With(h.requirePerm(PermConfigWrite)).Patch("/", h.updateConfigProfile)
				r.With(h.requirePerm(PermConfigWrite)).Delete("/", h.deleteConfigProfile)
				r.With(h.requirePerm(PermConfigRead)).Get("/render", h.renderConfigProfile)
				r.With(h.requirePerm(PermConfigWrite)).Post("/deploy", h.deployConfigProfile)
				r.With(h.requirePerm(PermConfigWrite)).Post("/validate", h.validateConfigProfile)
				r.With(h.requirePerm(PermConfigRead)).Get("/versions", h.listConfigProfileVersions)
				r.With(h.requirePerm(PermConfigRead)).Get("/versions/diff", h.diffConfigProfileVersions)
				r.With(h.requirePerm(PermConfigWrite)).Post("/rollback", h.rollbackConfigProfile)
			})
		})

		r.Route("/rules", func(r chi.Router) {
			r.With(h.requirePerm(PermRulesRead)).Get("/", h.listRules)
			r.With(h.requirePerm(PermRulesWrite)).Post("/", h.createRule)
			r.With(h.requirePerm(PermRulesWrite)).Post("/import", h.importRules)
			r.With(h.requirePerm(PermRulesRead)).Post("/export", h.exportRules)
			r.With(h.requirePerm(PermRulesWrite)).Post("/bulk", h.bulkRules)
			r.With(h.requirePerm(PermRulesRead)).Post("/validate", h.validateRules)
			r.With(h.requirePerm(PermRulesRead)).Post("/validate_agent", h.validateRuleOnAgent)
			r.With(h.requirePerm(PermRulesWrite)).Post("/{id}/clone", h.cloneRule)
			r.With(h.requirePerm(PermRulesWrite)).Post("/{id}/revisions", h.createRuleRevision)
			r.Route("/{id}", func(r chi.Router) {
				r.With(h.requirePerm(PermRulesRead)).Get("/", h.getRule)
				r.With(h.requirePerm(PermRulesWrite)).Patch("/", h.updateRule)
				r.With(h.requirePerm(PermRulesWrite)).Delete("/", h.deleteRule)
				r.With(h.requirePerm(PermRulesRead)).Get("/revisions", h.listRuleRevisions)
			})
		})

		r.Route("/rulesets", func(r chi.Router) {
			r.With(h.requirePerm(PermRulesRead)).Get("/", h.listRulesets)
			r.With(h.requirePerm(PermRulesWrite)).Post("/", h.buildRuleset)
			r.With(h.requirePerm(PermRulesRead)).Get("/{id}", h.getRuleset)
			r.With(h.requirePerm(PermRulesRead)).Get("/{id}/rules", h.getRulesetRules)
			r.With(h.requirePerm(PermRulesRead)).Get("/{id}/download", h.downloadRuleset)
		})

		r.Route("/iocs", func(r chi.Router) {
			r.With(h.requirePerm(PermIocRead)).Get("/", h.listIocs)
			r.With(h.requirePerm(PermIocWrite)).Post("/", h.createIoc)
			r.With(h.requirePerm(PermIocWrite)).Post("/import", h.importIocs)
			r.With(h.requirePerm(PermIocWrite)).Post("/generate", h.generateIocRules)
			r.Route("/{id}", func(r chi.Router) {
				r.With(h.requirePerm(PermIocRead)).Get("/", h.getIoc)
				r.With(h.requirePerm(PermIocWrite)).Patch("/", h.updateIoc)
				r.With(h.requirePerm(PermIocWrite)).Delete("/", h.deleteIoc)
			})
		})

		r.Route("/feeds", func(r chi.Router) {
			r.With(h.requirePerm(PermFeedsRead)).Get("/", h.listFeeds)
			r.With(h.requirePerm(PermFeedsWrite)).Post("/", h.createFeed)
			r.Route("/{id}", func(r chi.Router) {
				r.With(h.requirePerm(PermFeedsRead)).Get("/", h.getFeed)
				r.With(h.requirePerm(PermFeedsWrite)).Patch("/", h.updateFeed)
				r.With(h.requirePerm(PermFeedsWrite)).Delete("/", h.deleteFeed)
				r.With(h.requirePerm(PermFeedsWrite)).Post("/sync", h.syncFeed)
				r.With(h.requirePerm(PermFeedsRead)).Get("/runs", h.listFeedRuns)
			})
		})

		r.Route("/notification_channels", func(r chi.Router) {
			r.With(h.requirePerm(PermNotificationsRead)).Get("/", h.listNotificationChannels)
			r.With(h.requirePerm(PermNotificationsWrite)).Post("/", h.createNotificationChannel)
			r.Route("/{id}", func(r chi.Router) {
				r.With(h.requirePerm(PermNotificationsRead)).Get("/", h.getNotificationChannel)
				r.With(h.requirePerm(PermNotificationsWrite)).Patch("/", h.updateNotificationChannel)
				r.With(h.requirePerm(PermNotificationsWrite)).Delete("/", h.deleteNotificationChannel)
				r.With(h.requirePerm(PermNotificationsWrite)).Post("/test", h.testNotificationChannel)
			})
		})

		r.Route("/deployments", func(r chi.Router) {
			r.With(h.requirePerm(PermRulesRead)).Get("/", h.listDeployments)
			r.With(h.requirePerm(PermRulesDeploy)).Post("/", h.createDeployment)
			r.Route("/{id}", func(r chi.Router) {
				r.With(h.requirePerm(PermRulesRead)).Get("/", h.getDeployment)
				r.With(h.requirePerm(PermRulesDeploy)).Post("/pause", h.pauseDeployment)
				r.With(h.requirePerm(PermRulesDeploy)).Post("/resume", h.resumeDeployment)
				r.With(h.requirePerm(PermRulesDeploy)).Post("/cancel", h.cancelDeployment)
				r.With(h.requirePerm(PermRulesRead)).Get("/tasks", h.listDeploymentTasks)
			})
		})

		r.With(h.requirePerm(PermFleetRead)).Get("/fleet/compliance", h.getFleetCompliance)
		r.With(h.requirePerm(PermFleetRead)).Get("/fleet/dashboard", h.getFleetDashboard)

		r.With(h.requirePerm(PermFleetRead)).Get("/matrix/rules", h.getRulesMatrix)

		r.Route("/agents", func(r chi.Router) {
			r.With(h.requirePerm(PermAgentsRead)).Get("/", h.listAgents)
			r.With(h.requirePerm(PermAgentsRead)).Get("/{id}/logs", h.getAgentLogs)
			r.With(h.requirePerm(PermAgentsRead)).Get("/{id}/metrics", h.getAgentMetrics)
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
