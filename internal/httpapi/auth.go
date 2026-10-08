// Аутентификация и авторизация REST API (чанк 28).
//
// Режимы (server.auth_mode):
//   - dev (default) — прежняя заглушка: identity из X-Dev-User, все права;
//   - token — локальные пользователи: POST /auth/login (email+пароль) →
//     непрозрачный токен сессии (Authorization: Bearer), сессия в БД
//     (хэш токена, TTL server.session_ttl, отзыв), права из ролей
//     (user_roles → roles.permissions, '*' — все).
//
// Публичные пути в token-режиме: /api/v1/health, /version, /auth/login,
// /auth/refresh. Всё остальное требует валидный токен; права проверяются
// middleware RequirePerm (см. router.go). Действия аудируются в audit_log
// (auth.login/logout/refresh, users.*, roles.*, отказы 403).
package httpapi

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/surifleet/surifleet/internal/auditdiff"
	"github.com/surifleet/surifleet/internal/authn"
	"github.com/surifleet/surifleet/internal/store"
)

// Каталог разрешений (строки в roles.permissions; '*' — все).
const (
	PermFleetRead   = "fleet.read"   // обзор, compliance, матрица
	PermHostsRead   = "hosts.read"   // организации/кластеры/хосты/инстансы — чтение
	PermHostsWrite  = "hosts.write"  // кластеры/хосты/инстансы — изменение, join-токены, confirm_discovery
	PermAgentsRead  = "agents.read"  // список агентов, логи агентов
	PermRulesRead   = "rules.read"   // правила, ruleset'ы, ревизии — чтение
	PermRulesWrite  = "rules.write"  // правила CRUD/import/bulk, сборка ruleset
	PermRulesDeploy = "rules.deploy" // деплои: create/pause/resume/cancel, iocs/generate
	PermIocRead     = "ioc.read"
	PermIocWrite    = "ioc.write"
	PermFeedsRead   = "feeds.read"
	PermFeedsWrite  = "feeds.write" // CRUD фидов + sync
	PermConfigRead  = "config.read"  // версии конфигураций (чанк 54)
	PermConfigWrite = "config.write" // создание версий + deploy_config
	PermUsersRead   = "users.read"
	PermUsersWrite  = "users.write" // пользователи + отзыв сессий
	PermTokensRead  = "tokens.read" // API-токены автоматизации (чанк 29)
	PermTokensWrite = "tokens.write"
	PermRolesRead   = "roles.read"
	PermRolesWrite  = "roles.write" // кастомные роли
	PermAuditRead   = "audit.read"
	PermSsoRead     = "sso.read" // SSO-провайдеры (чанк 35)
	PermSsoWrite    = "sso.write"
	PermAll         = "*" // admin; также admin-only операции (организации CUD)
)

// knownPermissions — весь каталог (валидация permissions кастомных ролей).
var knownPermissions = map[string]bool{
	PermFleetRead: true, PermHostsRead: true, PermHostsWrite: true,
	PermAgentsRead: true, PermRulesRead: true, PermRulesWrite: true,
	PermRulesDeploy: true, PermIocRead: true, PermIocWrite: true,
	PermFeedsRead: true, PermFeedsWrite: true, PermConfigRead: true,
	PermConfigWrite: true, PermUsersRead: true,
	PermUsersWrite: true, PermTokensRead: true, PermTokensWrite: true,
	PermRolesRead: true, PermRolesWrite: true,
	PermAuditRead: true, PermSsoRead: true, PermSsoWrite: true,
}

// validPermission — разрешение из каталога или '*'.
func validPermission(p string) bool {
	return p == PermAll || knownPermissions[p]
}

// Identity — аутентифицированный контекст запроса.
type Identity struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
	OrgID     uuid.UUID
	Email     string
	Perms     map[string]bool
	// Dev — dev-режим (auth_mode=dev): все права, пользователь условный.
	Dev bool
	// BreakGlass — break-glass администратор (входы аудируются отдельно).
	BreakGlass bool
	// APITokenID — запрос аутентифицирован API-токеном (X-API-Key, чанк 29).
	APITokenID *uuid.UUID
	// ScopeRestricted — пользователь ограничен подмножеством кластеров
	// (user_roles только scope_type='clusters'; чанк 43). false — вся org.
	ScopeRestricted bool
	// ScopeClusters — разрешённые cluster_id (при ScopeRestricted).
	ScopeClusters []uuid.UUID
}

// HasPerm проверяет разрешение ('*' — все).
func (id Identity) HasPerm(perm string) bool {
	if id.Dev {
		return true
	}
	return id.Perms[PermAll] || id.Perms[perm]
}

// ClusterScopeAllowed — доступен ли кластер текущему identity (scoping по
// кластерам, чанк 43). Dev/API-токен/org-scope — всегда true.
func (id Identity) ClusterScopeAllowed(clusterID uuid.UUID) bool {
	if id.Dev || !id.ScopeRestricted {
		return true
	}
	for _, c := range id.ScopeClusters {
		if c == clusterID {
			return true
		}
	}
	return false
}

// identityFrom возвращает identity из контекста (nil — не аутентифицирован).
func identityFrom(ctx context.Context) *Identity {
	if v, ok := ctx.Value(identityKey{}).(*Identity); ok {
		return v
	}
	return nil
}

// authPublicPaths — пути без аутентификации в token-режиме.
var authPublicPaths = map[string]bool{
	"/api/v1/health":          true,
	"/api/v1/version":         true,
	"/api/v1/auth/login":      true,
	"/api/v1/auth/refresh":    true,
	"/api/v1/auth/ldap/login": true,
}

// authMiddleware — аутентификация запроса по режиму (dev|token).
// В dev — прежняя заглушка X-Dev-User. В token — Bearer-токен сессии.
func (h *handlers) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.d.AuthMode != "token" {
			// dev-режим: условный админ (совместимость dev-контура).
			email := r.Header.Get("X-Dev-User")
			if email == "" {
				email = "dev-admin"
			}
			ctx := context.WithValue(r.Context(), identityKey{}, &Identity{
				Email: email, Dev: true, Perms: map[string]bool{PermAll: true},
			})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Auth — только для API; статика UI (/app/, /ui/, /) публична,
		// иначе форма входа не загрузится (чанк 30).
		if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		if authPublicPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		// OIDC-SSO flow (чанк 35): discovery/начало/callback — публичные
		// (пользователь ещё не аутентифицирован; защита — state+nonce+PKCE).
		if strings.HasPrefix(r.URL.Path, "/api/v1/auth/sso/") {
			next.ServeHTTP(w, r)
			return
		}
		// SAML 2.0 (чанк 41): SP-метаданные/login/ACS — публичные (защита —
		// подпись assertion и audience, проверяемые crewjam/saml).
		if strings.HasPrefix(r.URL.Path, "/api/v1/auth/saml/") {
			next.ServeHTTP(w, r)
			return
		}
		// API-токен автоматизации (X-API-Key; чанк 29) — приоритетнее Bearer.
		if key := r.Header.Get("X-API-Key"); key != "" {
			tok, err := h.d.Store.ApiTokens.GetValidByHash(r.Context(), authn.TokenHash(key))
			if err != nil {
				writeError(w, http.StatusUnauthorized, CodeUnauthenticated,
					"недействительный, отозванный или истёкший API-токен", nil)
				return
			}
			if err := h.d.Store.ApiTokens.TouchUsed(r.Context(), tok.ID); err != nil {
				errLog.Error("api-token: last_used_at", "err", err)
			}
			permSet := make(map[string]bool, len(tok.Scopes))
			for _, p := range tok.Scopes {
				permSet[p] = true
			}
			email := "api-token:" + tok.Name
			id := &Identity{OrgID: tok.OrganizationID, Email: email,
				Perms: permSet, APITokenID: &tok.ID}
			if tok.UserID != nil {
				id.UserID = *tok.UserID
			}
			ctx := context.WithValue(r.Context(), identityKey{}, id)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" {
			writeError(w, http.StatusUnauthorized, CodeUnauthenticated,
				"требуется аутентификация (Authorization: Bearer <token>, вход — POST /auth/login)", nil)
			return
		}
		sess, err := h.d.Store.Sessions.GetValidByHash(r.Context(), authn.TokenHash(token))
		if err != nil {
			writeError(w, http.StatusUnauthorized, CodeUnauthenticated,
				"недействительный или истёкший токен", nil)
			return
		}
		user, err := h.d.Store.Users.GetByID(r.Context(), sess.UserID)
		if err != nil || !user.IsActive {
			writeError(w, http.StatusUnauthorized, CodeUnauthenticated,
				"пользователь деактивирован или удалён", nil)
			return
		}
		perms, err := h.d.Store.Roles.PermissionsForUser(r.Context(), user.ID)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		permSet := make(map[string]bool, len(perms))
		for _, p := range perms {
			permSet[p] = true
		}
		// Scoping по кластерам (чанк 43): только cluster-scoped назначения
		// → ограничение подмножеством кластеров. Ошибка чтения — без ограничений
		// (fail-open нехорошо, но и не блокируем системных; scoping уточнят).
		scopeClusters, scopeRestricted, err := h.d.Store.Users.ClusterScope(r.Context(), user.ID)
		if err != nil {
			errLog.Error("auth: cluster scope", "err", err)
		}
		ctx := context.WithValue(r.Context(), identityKey{}, &Identity{
			UserID: user.ID, SessionID: sess.ID, OrgID: user.OrganizationID,
			Email: user.Email, Perms: permSet, BreakGlass: user.IsBreakGlass,
			ScopeRestricted: scopeRestricted, ScopeClusters: scopeClusters,
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requirePerm — проверка разрешения (после authMiddleware). Отказ аудируется.
func (h *handlers) requirePerm(perm string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := identityFrom(r.Context())
			if id == nil {
				writeError(w, http.StatusUnauthorized, CodeUnauthenticated,
					"требуется аутентификация", nil)
				return
			}
			if !id.HasPerm(perm) {
				h.audit(r, id, "authz.denied", nil, nil, "denied", "нет права "+perm)
				writeError(w, http.StatusForbidden, CodeForbidden,
					"недостаточно прав: требуется "+perm, nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// hasPerm — проверка права внутри хендлера (напр. deploy:true в iocs/generate).
func hasPerm(ctx context.Context, perm string) bool {
	id := identityFrom(ctx)
	return id != nil && id.HasPerm(perm)
}

// ---------------------------------------------------------------------------
// Аудит
// ---------------------------------------------------------------------------

// audit пишет запись в audit_log (best-effort: ошибки только в лог).
func (h *handlers) audit(r *http.Request, id *Identity, action string,
	objType *string, objID *uuid.UUID, result, reason string) {

	e := store.AuditEntry{
		ActorType:  "user",
		Action:     action,
		ObjectType: objType,
		ObjectID:   objID,
		Result:     result,
	}
	if reason != "" {
		e.Reason = &reason
	}
	if id != nil {
		e.OrganizationID = &id.OrgID
		e.ActorName = id.Email
		switch {
		case id.Dev:
			e.ActorType = "system"
		case id.APITokenID != nil:
			e.ActorType = "api_token"
			e.ActorAPIKeyID = id.APITokenID
		default:
			e.ActorUserID = &id.UserID
			e.ActorSessionID = &id.SessionID
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		e.IP = &host
	}
	if ua := r.UserAgent(); ua != "" {
		e.UserAgent = &ua
	}
	if err := h.d.Store.Audit.Log(r.Context(), e); err != nil {
		errLog.Error("аудит: запись", "action", action, "err", err)
	}
}

// auditAnon — аудит без identity (неудачный login).
func (h *handlers) auditAnon(r *http.Request, email, action, result, reason string) {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	ua := r.UserAgent()
	e := store.AuditEntry{
		ActorType: "user", ActorName: email, Action: action,
		Result: result, UserAgent: &ua,
	}
	if host != "" {
		e.IP = &host
	}
	if reason != "" {
		e.Reason = &reason
	}
	if err := h.d.Store.Audit.Log(r.Context(), e); err != nil {
		errLog.Error("аудит: запись", "action", action, "err", err)
	}
}

// auditDiff — аудит изменения с diff «было → стало» (чанк 37). Секретные
// поля (пароль, client_secret) исключаются в auditdiff.Compute.
func (h *handlers) auditDiff(r *http.Request, id *Identity, action string,
	objType *string, objID *uuid.UUID, before, after any) {

	e := store.AuditEntry{
		ActorType:  "user",
		Action:     action,
		ObjectType: objType,
		ObjectID:   objID,
		Result:     "success",
		Diff:       auditdiff.Compute(before, after),
	}
	if id != nil {
		e.OrganizationID = &id.OrgID
		e.ActorName = id.Email
		switch {
		case id.Dev:
			e.ActorType = "system"
		case id.APITokenID != nil:
			e.ActorType = "api_token"
			e.ActorAPIKeyID = id.APITokenID
		default:
			e.ActorUserID = &id.UserID
			e.ActorSessionID = &id.SessionID
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		e.IP = &host
	}
	if ua := r.UserAgent(); ua != "" {
		e.UserAgent = &ua
	}
	if err := h.d.Store.Audit.Log(r.Context(), e); err != nil {
		errLog.Error("аудит: запись", "action", action, "err", err)
	}
}

// ---------------------------------------------------------------------------
// Хендлеры /auth/*
// ---------------------------------------------------------------------------

// loginRequest — POST /auth/login (openapi LoginRequest).
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// authTokens — ответ login/refresh (openapi AuthTokens; MVP: access и
// refresh — один и тот же токен сессии; refresh ротирует его).
type authTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

// loginOrg — организация для входа: ?organization_id= или старейшая
// (MVP: email уникален в рамках org; мульти-org выбор — через параметр).
func (h *handlers) loginOrg(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	return h.resolveOrgID(w, r)
}

// login — POST /auth/login: локальный вход (email + пароль).
func (h *handlers) login(w http.ResponseWriter, r *http.Request) {
	if h.d.AuthMode != "token" {
		writeError(w, http.StatusBadRequest, CodeValidation,
			"auth_mode=dev: вход не требуется (identity из X-Dev-User)", nil)
		return
	}
	var in loginRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Email = strings.TrimSpace(in.Email)
	if in.Email == "" || in.Password == "" {
		writeValidation(w, fieldErrors{"email": "обязательное поле", "password": "обязательное поле"})
		return
	}
	orgID, ok := h.loginOrg(w, r)
	if !ok {
		return
	}

	fail := func(reason string) {
		h.auditAnon(r, in.Email, "auth.login", "denied", reason)
		writeError(w, http.StatusUnauthorized, CodeUnauthenticated, "неверный email или пароль", nil)
	}
	user, err := h.d.Store.Users.GetByEmail(r.Context(), orgID, in.Email)
	if err != nil || user.PasswordHash == nil || !authn.CheckPassword(*user.PasswordHash, in.Password) {
		fail("неверные креды")
		return
	}
	if !user.IsActive {
		h.auditAnon(r, in.Email, "auth.login", "denied", "пользователь деактивирован")
		writeError(w, http.StatusForbidden, CodeForbidden, "пользователь деактивирован", nil)
		return
	}

	token, err := authn.NewToken()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	ttl := h.d.SessionTTL
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	ua := r.UserAgent()
	var ip *string
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		ip = &host
	}
	sess, err := h.d.Store.Sessions.Create(r.Context(), user.ID, authn.TokenHash(token), &ua, ip, time.Now().Add(ttl))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if err := h.d.Store.Users.TouchLogin(r.Context(), user.ID); err != nil {
		errLog.Error("login: last_login_at", "err", err)
	}
	id := &Identity{UserID: user.ID, SessionID: sess.ID, OrgID: user.OrganizationID, Email: user.Email}
	action := "auth.login"
	if user.IsBreakGlass {
		action = "auth.login_break_glass"
	}
	h.audit(r, id, action, nil, nil, "success", "")
	writeJSON(w, http.StatusOK, authTokens{
		AccessToken: token, RefreshToken: token, TokenType: "Bearer",
		ExpiresIn: int(ttl.Seconds()),
	})
}

// refreshRequest — POST /auth/refresh и /auth/logout (openapi RefreshRequest).
type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// refresh — POST /auth/refresh: ротация токена сессии (старый отзывается).
func (h *handlers) refresh(w http.ResponseWriter, r *http.Request) {
	if h.d.AuthMode != "token" {
		writeError(w, http.StatusBadRequest, CodeValidation, "auth_mode=dev: refresh не требуется", nil)
		return
	}
	var in refreshRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.RefreshToken == "" {
		writeValidation(w, fieldErrors{"refresh_token": "обязательное поле"})
		return
	}
	sess, err := h.d.Store.Sessions.GetValidByHash(r.Context(), authn.TokenHash(in.RefreshToken))
	if err != nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthenticated, "недействительный или истёкший refresh-токен", nil)
		return
	}
	user, err := h.d.Store.Users.GetByID(r.Context(), sess.UserID)
	if err != nil || !user.IsActive {
		writeError(w, http.StatusUnauthorized, CodeUnauthenticated, "пользователь деактивирован или удалён", nil)
		return
	}
	token, err := authn.NewToken()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	ttl := h.d.SessionTTL
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	newSess, err := h.d.Store.Sessions.Create(r.Context(), user.ID, authn.TokenHash(token), sess.UserAgent, sess.IP, time.Now().Add(ttl))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if err := h.d.Store.Sessions.Revoke(r.Context(), sess.ID); err != nil {
		errLog.Error("refresh: отзыв старой сессии", "err", err)
	}
	id := &Identity{UserID: user.ID, SessionID: newSess.ID, OrgID: user.OrganizationID, Email: user.Email}
	h.audit(r, id, "auth.refresh", nil, nil, "success", "")
	writeJSON(w, http.StatusOK, authTokens{
		AccessToken: token, RefreshToken: token, TokenType: "Bearer",
		ExpiresIn: int(ttl.Seconds()),
	})
}

// logout — POST /auth/logout: отзыв токена сессии (идемпотентно — 204 даже
// для недействительного токена).
func (h *handlers) logout(w http.ResponseWriter, r *http.Request) {
	var in refreshRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.RefreshToken != "" && h.d.AuthMode == "token" {
		if sess, err := h.d.Store.Sessions.GetValidByHash(r.Context(), authn.TokenHash(in.RefreshToken)); err == nil {
			if err := h.d.Store.Sessions.Revoke(r.Context(), sess.ID); err != nil {
				errLog.Error("logout: отзыв сессии", "err", err)
			}
			if id := identityFrom(r.Context()); id != nil {
				h.audit(r, id, "auth.logout", nil, nil, "success", "")
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// getMe — GET /auth/me: текущий пользователь, роли, итоговые разрешения.
func (h *handlers) getMe(w http.ResponseWriter, r *http.Request) {
	id := identityFrom(r.Context())
	if id == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthenticated, "требуется аутентификация", nil)
		return
	}
	if id.Dev {
		writeJSON(w, http.StatusOK, map[string]any{
			"user":        map[string]any{"email": id.Email, "display_name": id.Email},
			"roles":       []any{},
			"permissions": []string{PermAll},
		})
		return
	}
	user, err := h.d.Store.Users.GetByID(r.Context(), id.UserID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	roles := []store.Role{}
	perms := make([]string, 0, len(id.Perms))
	for p := range id.Perms {
		perms = append(perms, p)
	}
	for _, ra := range user.Roles {
		if ro, err := h.d.Store.Roles.GetByID(r.Context(), ra.RoleID); err == nil {
			roles = append(roles, ro)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user": user, "roles": roles, "permissions": perms,
	})
}
