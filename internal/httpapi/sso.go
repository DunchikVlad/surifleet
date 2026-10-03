// SSO-провайдеры и OIDC-flow (чанк 35 — OIDC-SSO, п. 9 ТЗ).
//
// Администрирование провайдеров: GET/POST /sso_providers, GET/PATCH/DELETE
// /sso_providers/{id} (права sso.read/sso.write; client_secret — writeOnly,
// в ответах обнуляется).
//
// Публичный OIDC-flow (Authorization Code + PKCE, реализация — internal/oidc):
//
//	GET /auth/sso/providers     — включённые OIDC-провайдеры (для формы входа);
//	GET /auth/sso/{id}/login    — редирект на IdP (state+nonce+PKCE);
//	GET /auth/sso/callback      — колбэк IdP → проверка → JIT → сессия →
//	                              HTML-страница, кладущая токен в localStorage.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/surifleet/surifleet/internal/authn"
	"github.com/surifleet/surifleet/internal/oidc"
	"github.com/surifleet/surifleet/internal/store"
)

// ---------------------------------------------------------------------------
// Публичный OIDC-flow
// ---------------------------------------------------------------------------

// ssoProviderPublic — публичное представление провайдера для формы входа.
type ssoProviderPublic struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Type string    `json:"type"`
}

// listSsoProvidersPublic — GET /auth/sso/providers: включённые OIDC-провайдеры
// (без конфигов/секретов) — для кнопки «Войти через SSO» на форме входа.
func (h *handlers) listSsoProvidersPublic(w http.ResponseWriter, r *http.Request) {
	items, err := h.d.Store.SsoProviders.ListEnabledOIDC(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]ssoProviderPublic, 0, len(items))
	for _, p := range items {
		out = append(out, ssoProviderPublic{ID: p.ID, Name: p.Name, Type: p.Type})
	}
	writeJSON(w, http.StatusOK, page[ssoProviderPublic]{Items: out, NextCursor: nil})
}

// ssoLogin — GET /auth/sso/{id}/login: начало OIDC-flow — редирект на IdP.
func (h *handlers) ssoLogin(w http.ResponseWriter, r *http.Request) {
	if h.d.OIDC == nil {
		writeError(w, http.StatusServiceUnavailable, CodeInternal, "OIDC не настроен на сервере", nil)
		return
	}
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	p, err := h.d.Store.SsoProviders.GetByID(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if p.Type != "oidc" || !p.Enabled {
		writeError(w, http.StatusNotFound, CodeNotFound, "OIDC-провайдер не найден или отключён", nil)
		return
	}
	authURL, err := h.d.OIDC.BeginAuth(r.Context(), p)
	if err != nil {
		errLog.Error("sso login: begin", "err", err)
		writeError(w, http.StatusBadGateway, CodeInternal,
			"не удалось инициировать вход через IdP (discovery недоступен?)", nil)
		return
	}
	http.Redirect(w, r, authURL, http.StatusFound)
}

// ssoCallback — GET /auth/sso/callback?state&code: завершение OIDC-flow.
// На успех — HTML-страница, сохраняющая токен сессии в localStorage и
// редиректящая в SPA; на ошибку — HTML с текстом (это браузерный редирект,
// не XHR — JSON тут неуместен).
func (h *handlers) ssoCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("error") != "" {
		desc := q.Get("error_description")
		h.auditAnon(r, "", "auth.login_sso", "denied", "IdP: "+q.Get("error")+" "+desc)
		h.writeSsoResult(w, false, "", "IdP отказал во входе: "+q.Get("error")+" "+desc)
		return
	}
	state, code := q.Get("state"), q.Get("code")
	if state == "" || code == "" {
		h.writeSsoResult(w, false, "", "некорректный callback: отсутствует state или code")
		return
	}
	if h.d.OIDC == nil {
		h.writeSsoResult(w, false, "", "OIDC не настроен на сервере")
		return
	}
	user, err := h.d.OIDC.HandleCallback(r.Context(), state, code)
	if err != nil {
		reason := err.Error()
		switch {
		case errors.Is(err, oidc.ErrStateInvalid):
			reason = "недействительный/истёкший state (повторите вход)"
		case errors.Is(err, oidc.ErrUserInactive):
			reason = "пользователь деактивирован"
		case errors.Is(err, oidc.ErrNoEmail):
			reason = "IdP не вернул email"
		}
		h.auditAnon(r, "", "auth.login_sso", "denied", err.Error())
		h.writeSsoResult(w, false, "", "вход через SSO не удался: "+reason)
		return
	}

	// Сессия SuriFleet (как в локальном login).
	token, err := authn.NewToken()
	if err != nil {
		h.writeSsoResult(w, false, "", "внутренняя ошибка (генерация токена)")
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
		h.writeSsoResult(w, false, "", "внутренняя ошибка (создание сессии)")
		return
	}
	if err := h.d.Store.Users.TouchLogin(r.Context(), user.ID); err != nil {
		errLog.Error("sso login: last_login_at", "err", err)
	}
	id := &Identity{UserID: user.ID, SessionID: sess.ID, OrgID: user.OrganizationID, Email: user.Email}
	h.audit(r, id, "auth.login_sso", nil, nil, "success", "")
	h.writeSsoResult(w, true, token, "")
}

// writeSsoResult — HTML-ответ callback'а: на успех кладёт access token в
// localStorage и редиректит в SPA; на ошибку показывает текст со ссылкой
// на форму входа.
func (h *handlers) writeSsoResult(w http.ResponseWriter, ok bool, token, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if ok {
		// Токен передаётся через JS localStorage (как login-форма), не через URL.
		fmt.Fprintf(w, `<!doctype html><html lang="ru"><head><meta charset="utf-8"><title>Вход через SSO</title></head>
<body style="background:#0f141b;color:#dbe4ee;font:14px/1.5 system-ui,sans-serif;padding:40px">
<p>Вход выполнен. Перенаправление…</p>
<script>
try { localStorage.setItem("surifleet_token", %q); } catch (e) {}
window.location.replace("/app/");
</script>
</body></html>`, token)
		return
	}
	w.WriteHeader(http.StatusUnauthorized)
	fmt.Fprintf(w, `<!doctype html><html lang="ru"><head><meta charset="utf-8"><title>Ошибка входа SSO</title></head>
<body style="background:#0f141b;color:#dbe4ee;font:14px/1.5 system-ui,sans-serif;padding:40px">
<h1>Вход через SSO не удался</h1><p>%s</p><p><a href="/app/" style="color:#6cf">← к форме входа</a></p>
</body></html>`, escapeHTML(errMsg))
}

// escapeHTML — минимальное экранирование для подстановки в HTML.
func escapeHTML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
	return r.Replace(s)
}

// ---------------------------------------------------------------------------
// Администрирование SSO-провайдеров (/sso_providers)
// ---------------------------------------------------------------------------

// ssoProviderInput — POST/PATCH /sso_providers.
type ssoProviderInput struct {
	Name             *string             `json:"name"`
	Type             *string             `json:"type"`
	Config           *store.OIDCConfig   `json:"config"`
	GroupRoleMapping map[string][]string `json:"group_role_mapping"`
	Enabled          *bool               `json:"enabled"`
}

// validateSsoConfig — проверка OIDC-конфига (при создании — обязательные поля).
func validateSsoConfig(fe fieldErrors, cfg *store.OIDCConfig, require bool) {
	if cfg == nil {
		if require {
			fe.add("config", "обязательное поле (issuer_url, client_id, redirect_url)")
		}
		return
	}
	if strings.TrimSpace(cfg.IssuerURL) == "" {
		fe.add("config.issuer_url", "обязательное поле")
	} else if _, err := url.ParseRequestURI(cfg.IssuerURL); err != nil {
		fe.add("config.issuer_url", "некорректный URL")
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		fe.add("config.client_id", "обязательное поле")
	}
	if strings.TrimSpace(cfg.RedirectURL) == "" {
		fe.add("config.redirect_url", "обязательное поле")
	} else if _, err := url.ParseRequestURI(cfg.RedirectURL); err != nil {
		fe.add("config.redirect_url", "некорректный URL")
	}
}

// validateGroupRoleMapping — role_id в маппинге существуют и доступны org.
func (h *handlers) validateGroupRoleMapping(r *http.Request, orgID uuid.UUID, fe fieldErrors, m map[string][]string) {
	for group, ids := range m {
		if strings.TrimSpace(group) == "" {
			fe.add("group_role_mapping", "пустое имя группы")
		}
		for _, rs := range ids {
			rid, err := uuid.Parse(rs)
			if err != nil {
				fe.add("group_role_mapping", "role_id не UUID: "+rs)
				continue
			}
			ro, err := h.d.Store.Roles.GetByID(r.Context(), rid)
			if err != nil {
				fe.add("group_role_mapping", "роль не найдена: "+rs)
				continue
			}
			if ro.OrganizationID != nil && *ro.OrganizationID != orgID {
				fe.add("group_role_mapping", "роль "+ro.Name+" принадлежит другой организации")
			}
		}
	}
}

// listSsoProviders — GET /sso_providers (sso.read; client_secret скрыт).
func (h *handlers) listSsoProviders(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	items, err := h.d.Store.SsoProviders.List(r.Context(), orgID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]store.SsoProvider, 0, len(items))
	for _, p := range items {
		out = append(out, p.Public())
	}
	writeJSON(w, http.StatusOK, page[store.SsoProvider]{Items: out, NextCursor: nil})
}

// createSsoProvider — POST /sso_providers (sso.write).
func (h *handlers) createSsoProvider(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	var in ssoProviderInput
	if !decodeJSON(w, r, &in) {
		return
	}
	fe := fieldErrors{}
	if in.Name == nil || strings.TrimSpace(*in.Name) == "" {
		fe.add("name", "обязательное непустое поле")
	}
	typ := "oidc"
	if in.Type != nil && *in.Type != "" {
		typ = *in.Type
	}
	if typ != "oidc" {
		fe.add("type", "поддерживается только oidc (saml/ldap — следующие чанки)")
	}
	validateSsoConfig(fe, in.Config, true)
	if in.GroupRoleMapping != nil {
		h.validateGroupRoleMapping(r, orgID, fe, in.GroupRoleMapping)
	}
	if fe.any() {
		writeValidation(w, fe)
		return
	}
	grm, _ := json.Marshal(in.GroupRoleMapping)
	p, err := h.d.Store.SsoProviders.Create(r.Context(), orgID, store.SsoProviderInput{
		Name: strings.TrimSpace(*in.Name), Type: typ, Config: *in.Config,
		GroupRoleMapping: grm, Enabled: in.Enabled,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	objType, objID := "sso_provider", p.ID
	h.audit(r, identityFrom(r.Context()), "sso.create", &objType, &objID, "success", "")
	writeJSON(w, http.StatusCreated, p.Public())
}

// getSsoProvider — GET /sso_providers/{id} (sso.read; client_secret скрыт).
func (h *handlers) getSsoProvider(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	p, err := h.d.Store.SsoProviders.GetByID(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p.Public())
}

// updateSsoProvider — PATCH /sso_providers/{id} (sso.write). Пустой
// client_secret в config — «не менять» (сохраняется старый).
func (h *handlers) updateSsoProvider(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	var in ssoProviderInput
	if !decodeJSON(w, r, &in) {
		return
	}
	fe := fieldErrors{}
	if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
		fe.add("name", "непустое поле")
	}
	if in.Type != nil && *in.Type != "" && *in.Type != "oidc" {
		fe.add("type", "поддерживается только oidc")
	}
	if in.Config != nil {
		validateSsoConfig(fe, in.Config, false)
	}
	if in.GroupRoleMapping != nil {
		h.validateGroupRoleMapping(r, orgID, fe, in.GroupRoleMapping)
	}
	if fe.any() {
		writeValidation(w, fe)
		return
	}
	upd := store.SsoProviderInput{Enabled: in.Enabled}
	if in.Name != nil {
		upd.Name = strings.TrimSpace(*in.Name)
	}
	if in.Type != nil {
		upd.Type = *in.Type
	}
	if in.Config != nil {
		upd.Config = *in.Config
	}
	if in.GroupRoleMapping != nil {
		grm, _ := json.Marshal(in.GroupRoleMapping)
		upd.GroupRoleMapping = grm
	}
	oldProv, _ := h.d.Store.SsoProviders.GetByID(r.Context(), id) // «было» для diff (чанк 37)
	p, err := h.d.Store.SsoProviders.Update(r.Context(), id, upd)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	objType := "sso_provider"
	h.auditDiff(r, identityFrom(r.Context()), "sso.update", &objType, &id, oldProv, p)
	writeJSON(w, http.StatusOK, p.Public())
}

// deleteSsoProvider — DELETE /sso_providers/{id} (sso.write). JIT-пользователи
// сохраняются (provider_id → NULL), но войти через SSO больше не смогут.
func (h *handlers) deleteSsoProvider(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	if err := h.d.Store.SsoProviders.Delete(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	objType := "sso_provider"
	h.audit(r, identityFrom(r.Context()), "sso.delete", &objType, &id, "success", "")
	w.WriteHeader(http.StatusNoContent)
}
