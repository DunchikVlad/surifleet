// SAML 2.0 SSO — публичные SP-эндпоинты (чанк 41, п. 9 ТЗ). Реализация —
// internal/samlauth (crewjam/saml); JIT-провижининг — общий oidc.Provision.
//
//	GET /auth/saml/{id}/metadata — SP-метаданные (XML, импортируют в IdP);
//	GET /auth/saml/{id}/login    — AuthnRequest → 302 на IdP;
//	POST /auth/saml/acs          — SAMLResponse → проверка → JIT → сессия →
//	                               HTML с токеном (как OIDC-callback).
//	Провайдер для ACS определяется query-параметром provider_id (его
//	добавляют в ACS-URL при регистрации в IdP: …/acs?provider_id=<uuid>).
package httpapi

import (
	"encoding/xml"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/crewjam/saml"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/surifleet/surifleet/internal/authn"
	"github.com/surifleet/surifleet/internal/oidc"
	"github.com/surifleet/surifleet/internal/samlauth"
	"github.com/surifleet/surifleet/internal/store"
)

// samlResolve — провайдер type=saml из path {id} + его ServiceProvider.
// При ошибке ответ уже записан — ok=false.
func (h *handlers) samlResolve(w http.ResponseWriter, r *http.Request) (*saml.ServiceProvider, samlauth.Config, bool) {
	var nilCfg samlauth.Config
	if h.d.SAML == nil {
		writeError(w, http.StatusServiceUnavailable, CodeInternal, "SAML не настроен на сервере", nil)
		return nil, nilCfg, false
	}
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return nil, nilCfg, false
	}
	p, sp, cfg, ok := h.samlBuild(w, r, id)
	if !ok {
		return nil, nilCfg, false
	}
	_ = p
	return sp, cfg, true
}

// samlBuild — общая сборка SP по id провайдера (для {id}- и ACS-путей).
func (h *handlers) samlBuild(w http.ResponseWriter, r *http.Request, id uuid.UUID) (store.SsoProvider, *saml.ServiceProvider, samlauth.Config, bool) {
	var nilP store.SsoProvider
	var nilCfg samlauth.Config
	p, err := h.d.Store.SsoProviders.GetByID(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return nilP, nil, nilCfg, false
	}
	if p.Type != "saml" || !p.Enabled {
		writeError(w, http.StatusNotFound, CodeNotFound, "SAML-провайдер не найден или отключён", nil)
		return nilP, nil, nilCfg, false
	}
	sp, cfg, err := h.d.SAML.SP(r.Context(), p)
	if err != nil {
		errLog.Error("saml: build SP", "provider", id, "err", err)
		writeError(w, http.StatusBadGateway, CodeInternal, "SAML-провайдер: "+err.Error(), nil)
		return nilP, nil, nilCfg, false
	}
	return p, sp, cfg, true
}

// samlResolveACS — провайдер для ACS: query provider_id.
func (h *handlers) samlResolveACS(w http.ResponseWriter, r *http.Request) (store.SsoProvider, samlauth.Config, *saml.ServiceProvider, bool) {
	var nilP store.SsoProvider
	var nilCfg samlauth.Config
	idStr := r.URL.Query().Get("provider_id")
	if idStr == "" {
		h.writeSsoResult(w, false, "", "ACS без provider_id — добавьте его в ACS-URL при регистрации в IdP")
		return nilP, nilCfg, nil, false
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.writeSsoResult(w, false, "", "некорректный provider_id")
		return nilP, nilCfg, nil, false
	}
	p, sp, cfg, ok := h.samlBuildErr(w, r, id)
	return p, cfg, sp, ok
}

// samlBuildErr — как samlBuild, но ошибки — HTML (браузерный POST ACS).
func (h *handlers) samlBuildErr(w http.ResponseWriter, r *http.Request, id uuid.UUID) (store.SsoProvider, *saml.ServiceProvider, samlauth.Config, bool) {
	var nilP store.SsoProvider
	var nilCfg samlauth.Config
	p, err := h.d.Store.SsoProviders.GetByID(r.Context(), id)
	if err != nil || p.Type != "saml" || !p.Enabled {
		h.writeSsoResult(w, false, "", "SAML-провайдер не найден или отключён")
		return nilP, nil, nilCfg, false
	}
	sp, cfg, err := h.d.SAML.SP(r.Context(), p)
	if err != nil {
		h.writeSsoResult(w, false, "", "SAML-провайдер: "+err.Error())
		return nilP, nil, nilCfg, false
	}
	return p, sp, cfg, true
}

// samlMetadata — GET /auth/saml/{id}/metadata: SP-метаданные для IdP.
func (h *handlers) samlMetadata(w http.ResponseWriter, r *http.Request) {
	sp, _, ok := h.samlResolve(w, r)
	if !ok {
		return
	}
	xmlBytes, err := xml.Marshal(sp.Metadata())
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "метаданные SP: "+err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/samlmetadata+xml; charset=utf-8")
	_, _ = w.Write(xmlBytes)
}

// samlLogin — GET /auth/saml/{id}/login: AuthnRequest → 302 на IdP.
func (h *handlers) samlLogin(w http.ResponseWriter, r *http.Request) {
	sp, _, ok := h.samlResolve(w, r)
	if !ok {
		return
	}
	redirectURL, err := sp.MakeRedirectAuthenticationRequest("")
	if err != nil {
		errLog.Error("saml login: authn request", "err", err)
		writeError(w, http.StatusInternalServerError, CodeInternal, "не удалось построить AuthnRequest", nil)
		return
	}
	http.Redirect(w, r, redirectURL.String(), http.StatusFound)
}

// samlACS — POST /auth/saml/acs: SAMLResponse → проверка → JIT → сессия.
func (h *handlers) samlACS(w http.ResponseWriter, r *http.Request) {
	if h.d.SAML == nil {
		h.writeSsoResult(w, false, "", "SAML не настроен на сервере")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.writeSsoResult(w, false, "", "некорректная форма ACS")
		return
	}
	if r.Form.Get("SAMLResponse") == "" {
		h.writeSsoResult(w, false, "", "отсутствует SAMLResponse")
		return
	}
	p, cfg, sp, ok := h.samlResolveACS(w, r)
	if !ok {
		return
	}
	assertion, err := sp.ParseResponse(r, []string{""})
	if err != nil {
		h.auditAnon(r, "", "auth.login_saml", "denied", "assertion: "+err.Error())
		h.writeSsoResult(w, false, "", "проверка SAML-ответа не удалась: "+err.Error())
		return
	}
	profile, err := cfg.ProfileFromAssertion(assertion)
	if err != nil {
		h.auditAnon(r, "", "auth.login_saml", "denied", err.Error())
		h.writeSsoResult(w, false, "", "assertion без нужных атрибутов: "+err.Error())
		return
	}
	// JIT — общий с OIDC/LDAP механизм (маппинг групп → роли).
	user, err := h.d.OIDC.Provision(r.Context(), p, oidc.Claims{
		Sub: profile.ExternalID, Email: profile.Email, Name: profile.DisplayName, Groups: profile.Groups,
	})
	if err != nil {
		if errors.Is(err, oidc.ErrUserInactive) {
			h.writeSsoResult(w, false, "", "пользователь деактивирован")
		} else {
			h.writeSsoResult(w, false, "", "JIT-провижининг: "+err.Error())
		}
		return
	}

	// Сессия SuriFleet (как в OIDC/LDAP login).
	token, err := authn.NewToken()
	if err != nil {
		h.writeSsoResult(w, false, "", "внутренняя ошибка (токен)")
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
		h.writeSsoResult(w, false, "", "внутренняя ошибка (сессия)")
		return
	}
	if err := h.d.Store.Users.TouchLogin(r.Context(), user.ID); err != nil {
		errLog.Error("saml login: last_login_at", "err", err)
	}
	id := &Identity{UserID: user.ID, SessionID: sess.ID, OrgID: user.OrganizationID, Email: user.Email}
	h.audit(r, id, "auth.login_saml", nil, nil, "success", "")
	h.writeSsoResult(w, true, token, "")
}
