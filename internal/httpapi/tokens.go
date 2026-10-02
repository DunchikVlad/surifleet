// API-токены автоматизации (чанк 29): выпуск/листинг/отзыв.
// Аутентификация самих токенов — X-API-Key в authMiddleware (auth.go).
package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/surifleet/surifleet/internal/authn"
	"github.com/surifleet/surifleet/internal/store"
)

// apiTokenInput — POST /api_tokens (openapi ApiTokenInput).
type apiTokenInput struct {
	Name      string     `json:"name"`
	Scopes    []string   `json:"scopes"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// apiTokenCreated — ответ POST /api_tokens: токен показывается один раз.
type apiTokenCreated struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID uuid.UUID  `json:"organization_id"`
	Name           string     `json:"name"`
	Scopes         []string   `json:"scopes"`
	ExpiresAt      *time.Time `json:"expires_at"`
	CreatedAt      time.Time  `json:"created_at"`
	Token          string     `json:"token"`
}

// listApiTokens — GET /api_tokens (tokens.read): активные токены организации.
func (h *handlers) listApiTokens(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	cursor, limit, ok := parsePage(w, r)
	if !ok {
		return
	}
	items, next, err := h.d.Store.ApiTokens.List(r.Context(), orgID, cursor, limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page[store.ApiToken]{Items: items, NextCursor: next})
}

// createApiToken — POST /api_tokens (tokens.write): выпуск токена,
// значение возвращается один раз в поле token.
func (h *handlers) createApiToken(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	var in apiTokenInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	fe := fieldErrors{}
	if in.Name == "" {
		fe.add("name", "обязательное непустое поле")
	}
	if len(in.Scopes) == 0 {
		fe.add("scopes", "обязательное непустое поле")
	}
	for _, s := range in.Scopes {
		if !validPermission(s) {
			fe.add("scopes", "неизвестное разрешение: "+s)
		}
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(time.Now()) {
		fe.add("expires_at", "должно быть в будущем")
	}
	if fe.any() {
		writeValidation(w, fe)
		return
	}
	token, err := authn.NewToken()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	actor := identityFrom(r.Context())
	var userID *uuid.UUID
	if actor != nil && !actor.Dev && actor.APITokenID == nil {
		userID = &actor.UserID
	}
	t, err := h.d.Store.ApiTokens.Create(r.Context(), orgID, userID, in.Name, authn.TokenHash(token), in.Scopes, in.ExpiresAt)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	objType := "api_token"
	h.audit(r, actor, "tokens.create", &objType, &t.ID, "success", "")
	writeJSON(w, http.StatusCreated, apiTokenCreated{
		ID: t.ID, OrganizationID: t.OrganizationID, Name: t.Name,
		Scopes: t.Scopes, ExpiresAt: t.ExpiresAt, CreatedAt: t.CreatedAt,
		Token: token,
	})
}

// revokeApiToken — DELETE /api_tokens/{id} (tokens.write): отзыв (запись
// остаётся для аудита; из листинга скрывается).
func (h *handlers) revokeApiToken(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	if err := h.d.Store.ApiTokens.Revoke(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	objType := "api_token"
	h.audit(r, identityFrom(r.Context()), "tokens.revoke", &objType, &id, "success", "")
	w.WriteHeader(http.StatusNoContent)
}
