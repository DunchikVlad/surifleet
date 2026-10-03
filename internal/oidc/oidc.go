// Package oidc — OIDC-SSO (Authorization Code + PKCE) для SuriFleet
// (чанк 35, п. 9 ТЗ). Проверенные библиотеки: coreos/go-oidc/v3 (проверка
// id_token, nonce, discovery) и golang.org/x/oauth2 (flow с PKCE S256) —
// самодельной криптографии нет.
//
// Поток:
//  1. BeginAuth (GET /auth/sso/{id}/login) — генерирует state+nonce,
//     сохраняет их одноразово (store.OidcStates), возвращает URL авторизации
//     IdP (с PKCE challenge; верификатор не покидает сервер — выводится из
//     state при callback, поэтому хранить его отдельно не нужно).
//  2. HandleCallback (GET /auth/sso/callback) — Consume(state) (одноразово,
//     защита от replay/CSRF), обмен кода на токены, проверка id_token (nonce),
//     claims (sub/email/name/groups), маппинг групп → роли, JIT-провижининг
//     пользователя, создание локальной сессии SuriFleet.
package oidc

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"golang.org/x/oauth2"

	"github.com/surifleet/surifleet/internal/authn"
	"github.com/surifleet/surifleet/internal/store"
)

// StateTTL — срок жизни state/nonce OIDC-flow (защита от CSRF).
const StateTTL = 10 * time.Minute

// Ошибки домена OIDC (HTTP-слой отображает на понятные ответы).
var (
	ErrStateInvalid = errors.New("недействительный, истёкший или уже использованный state")
	ErrNonce        = errors.New("nonce id_token не совпадает")
	ErrNoEmail      = errors.New("IdP не вернул email (scope openid email)")
	ErrUserInactive = errors.New("пользователь деактивирован")
)

// Claims — профиль пользователя из id_token/userinfo.
type Claims struct {
	Sub     string
	Email   string
	Name    string
	Groups  []string
	RawJSON string
}

// Service — OIDC-сервис: ленивая инициализация провайдеров (discovery),
// flow и JIT-провижининг.
type Service struct {
	Store *store.Store

	mu       sync.Mutex
	prov     map[uuid.UUID]*gooidc.Provider
	verifier map[uuid.UUID]*gooidc.IDTokenVerifier
}

// NewService — OIDC-сервис поверх store.
func NewService(st *store.Store) *Service {
	return &Service{
		Store:    st,
		prov:     map[uuid.UUID]*gooidc.Provider{},
		verifier: map[uuid.UUID]*gooidc.IDTokenVerifier{},
	}
}

// pkceVerifierFromState детерминированно выводит PKCE-верификатор из state:
// хранить его отдельно не нужно — state одноразовый и известен только серверу.
func pkceVerifierFromState(state string) string {
	sum := sha256.Sum256([]byte("surifleet-pkce:" + state))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// provider — gooidc.Provider для SSO-провайдера (discovery кэшируется).
func (s *Service) provider(ctx context.Context, p store.SsoProvider) (*gooidc.Provider, *gooidc.IDTokenVerifier, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if pv, ok := s.prov[p.ID]; ok {
		return pv, s.verifier[p.ID], nil
	}
	pv, err := gooidc.NewProvider(ctx, p.Config.IssuerURL)
	if err != nil {
		return nil, nil, fmt.Errorf("discovery IdP %s: %w", p.Config.IssuerURL, err)
	}
	vf := pv.Verifier(&gooidc.Config{ClientID: p.Config.ClientID})
	s.prov[p.ID] = pv
	s.verifier[p.ID] = vf
	return pv, vf, nil
}

// oauth2Config — конфигурация flow для провайдера (endpoint из discovery).
func oauth2Config(p store.SsoProvider, ep oauth2.Endpoint) *oauth2.Config {
	scopes := p.Config.Scopes
	if len(scopes) == 0 {
		scopes = []string{gooidc.ScopeOpenID, "email", "profile"}
	}
	return &oauth2.Config{
		ClientID:     p.Config.ClientID,
		ClientSecret: p.Config.ClientSecret,
		Endpoint:     ep,
		RedirectURL:  p.Config.RedirectURL,
		Scopes:       scopes,
	}
}

// BeginAuth — начало flow: создаёт одноразовый state+nonce и возвращает
// URL авторизации IdP (PKCE S256). providerID — SSO-провайдер из БД.
func (s *Service) BeginAuth(ctx context.Context, p store.SsoProvider) (authURL string, err error) {
	pv, _, err := s.provider(ctx, p)
	if err != nil {
		return "", err
	}
	state, err := authn.NewToken()
	if err != nil {
		return "", err
	}
	nonce, err := authn.NewToken()
	if err != nil {
		return "", err
	}
	if err := s.Store.OidcStates.Create(ctx, p.ID, state, nonce, StateTTL); err != nil {
		return "", err
	}
	cfg := oauth2Config(p, pv.Endpoint())
	url := cfg.AuthCodeURL(state,
		oauth2.S256ChallengeOption(pkceVerifierFromState(state)),
		oauth2.SetAuthURLParam("nonce", nonce),
	)
	return url, nil
}

// HandleCallback — завершение flow: проверка state (одноразово) и id_token,
// извлечение claims, JIT-провижининг. Возвращает пользователя SuriFleet.
func (s *Service) HandleCallback(ctx context.Context, state, code string) (store.User, error) {
	providerID, nonce, err := s.Store.OidcStates.Consume(ctx, state)
	if err != nil {
		return store.User{}, ErrStateInvalid
	}
	p, err := s.Store.SsoProviders.GetByID(ctx, providerID)
	if err != nil {
		return store.User{}, fmt.Errorf("SSO-провайдер state: %w", err)
	}
	if !p.Enabled {
		return store.User{}, errors.New("SSO-провайдер отключён")
	}
	pv, verifier, err := s.provider(ctx, p)
	if err != nil {
		return store.User{}, err
	}
	cfg := oauth2Config(p, pv.Endpoint())
	tok, err := cfg.Exchange(ctx, code, oauth2.VerifierOption(pkceVerifierFromState(state)))
	if err != nil {
		return store.User{}, fmt.Errorf("обмен кода на токен: %w", err)
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok || rawID == "" {
		return store.User{}, errors.New("IdP не вернул id_token")
	}
	idToken, err := verifier.Verify(ctx, rawID)
	if err != nil {
		return store.User{}, fmt.Errorf("проверка id_token: %w", err)
	}
	if idToken.Nonce != nonce {
		return store.User{}, ErrNonce
	}
	claims, err := extractClaims(ctx, pv, cfg, tok, idToken)
	if err != nil {
		return store.User{}, err
	}
	return s.Provision(ctx, p, claims)
}

// extractClaims — claims из id_token (+ userinfo как fallback для groups).
func extractClaims(ctx context.Context, pv *gooidc.Provider, cfg *oauth2.Config, tok *oauth2.Token, idToken *gooidc.IDToken) (Claims, error) {
	var raw map[string]any
	if err := idToken.Claims(&raw); err != nil {
		return Claims{}, fmt.Errorf("разбор claims id_token: %w", err)
	}
	c := Claims{Sub: idToken.Subject}
	c.Email, _ = raw["email"].(string)
	c.Name, _ = raw["name"].(string)
	c.Groups = groupsFrom(raw)
	// Нет groups/email в id_token — пробуем userinfo (некоторые IdP отдают там).
	if (len(c.Groups) == 0 || c.Email == "") && pv.UserInfoEndpoint() != "" {
		if ui, err := pv.UserInfo(ctx, oauth2.StaticTokenSource(tok)); err == nil {
			var uraw map[string]any
			if ui.Claims(&uraw) == nil {
				if c.Email == "" {
					c.Email, _ = uraw["email"].(string)
				}
				if c.Name == "" {
					c.Name, _ = uraw["name"].(string)
				}
				if len(c.Groups) == 0 {
					c.Groups = groupsFrom(uraw)
				}
			}
		}
	}
	if c.Email == "" {
		return Claims{}, ErrNoEmail
	}
	b, _ := json.Marshal(raw)
	c.RawJSON = string(b)
	return c, nil
}

// groupsFrom извлекает groups из claims (массив строк или одна строка).
func groupsFrom(raw map[string]any) []string {
	out := []string{}
	switch g := raw["groups"].(type) {
	case []any:
		for _, v := range g {
			if s, ok := v.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	case []string:
		out = append(out, g...)
	case string:
		if g != "" {
			out = append(out, g)
		}
	}
	return out
}

// roleIDsForGroups — маппинг групп IdP → role_id системы по
// sso_providers.group_role_mapping {"<группа>": ["<role_uuid>", ...]}.
// Неизвестные/не-UUID значения пропускаются (дедуп результата).
func roleIDsForGroups(mapping json.RawMessage, groups []string) []uuid.UUID {
	if len(mapping) == 0 || len(groups) == 0 {
		return nil
	}
	var m map[string][]string
	if err := json.Unmarshal(mapping, &m); err != nil {
		return nil
	}
	seen := map[uuid.UUID]bool{}
	out := []uuid.UUID{}
	for _, g := range groups {
		for _, rs := range m[g] {
			if id, err := uuid.Parse(rs); err == nil && !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

// Provision — JIT-провижининг: найти/создать пользователя по (provider, sub)
// или email, назначить роли по группам. Возвращает активного пользователя.
// Экспортирован для других SSO-механизмов (LDAP, чанк 40).
func (s *Service) Provision(ctx context.Context, p store.SsoProvider, c Claims) (store.User, error) {
	email := strings.ToLower(strings.TrimSpace(c.Email))
	displayName := strings.TrimSpace(c.Name)
	if displayName == "" {
		displayName = email
	}
	roleIDs := roleIDsForGroups(p.GroupRoleMapping, c.Groups)
	assignments := make([]store.RoleAssignmentInput, 0, len(roleIDs))
	for _, rid := range roleIDs {
		assignments = append(assignments, store.RoleAssignmentInput{RoleID: rid, ScopeType: "organization"})
	}

	// 1) Уже JIT-создан через этот IdP — обновляем роли по текущим группам.
	if u, err := s.Store.Users.GetByExternalID(ctx, p.ID, c.Sub); err == nil {
		if err := s.Store.Users.SetRoles(ctx, u.ID, assignments); err != nil {
			return store.User{}, err
		}
		if !u.IsActive {
			return store.User{}, ErrUserInactive
		}
		u.Roles = rolesOf(assignments)
		return u, nil
	}

	// 2) Локальный пользователь с таким email — привязываем к IdP (дедуп).
	if u, err := s.Store.Users.GetByEmail(ctx, p.OrganizationID, email); err == nil {
		linked, err := s.Store.Users.LinkExternal(ctx, u.ID, p.ID, c.Sub)
		if err != nil {
			return store.User{}, err
		}
		if err := s.Store.Users.SetRoles(ctx, u.ID, assignments); err != nil {
			return store.User{}, err
		}
		if !linked.IsActive {
			return store.User{}, ErrUserInactive
		}
		linked.Roles = rolesOf(assignments)
		return linked, nil
	}

	// 3) Новый JIT-пользователь.
	u, err := s.Store.Users.CreateExternal(ctx, p.OrganizationID, p.ID, c.Sub, email, displayName, assignments)
	if err != nil {
		return store.User{}, err
	}
	return u, nil
}

// rolesOf — проекция назначений для ответа (имена подтянутся при /auth/me).
func rolesOf(in []store.RoleAssignmentInput) []store.UserRoleAssignment {
	out := make([]store.UserRoleAssignment, 0, len(in))
	for _, a := range in {
		out = append(out, store.UserRoleAssignment{RoleID: a.RoleID, ScopeType: a.ScopeType})
	}
	return out
}
