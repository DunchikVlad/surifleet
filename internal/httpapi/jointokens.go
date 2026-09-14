package httpapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/surifleet/surifleet/internal/store"
)

// Пределы TTL join token — по openapi JoinTokenInput (default 3600, max 86400).
const (
	defaultTokenTTL = 3600
	maxTokenTTL     = 86400
)

// joinTokenInput — тело POST /clusters/{id}/join_tokens (openapi JoinTokenInput
// + опциональное расширение max_uses; спека допускает ttl_seconds и comment).
type joinTokenInput struct {
	TTLSeconds *int   `json:"ttl_seconds"`
	Comment    string `json:"comment"`
	MaxUses    *int   `json:"max_uses"` // расширение сверх спеки (default 1)
}

// joinTokenCreated — ответ создания: спека JoinToken + служебные id/name.
// token показывается ОДИН раз — в БД хранится только SHA-256 хэш.
type joinTokenCreated struct {
	ID        string    `json:"id"`
	Token     string    `json:"token"`
	ClusterID string    `json:"cluster_id"`
	Name      string    `json:"name"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// createJoinToken — POST /api/v1/clusters/{id}/join_tokens.
func (h *handlers) createJoinToken(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	var in joinTokenInput
	if !decodeJSON(w, r, &in) {
		return
	}

	ttl := defaultTokenTTL
	if in.TTLSeconds != nil {
		ttl = *in.TTLSeconds
	}
	maxUses := 1
	if in.MaxUses != nil {
		maxUses = *in.MaxUses
	}
	fe := fieldErrors{}
	if ttl < 60 || ttl > maxTokenTTL {
		fe.add("ttl_seconds", "от 60 до 86400 секунд")
	}
	if maxUses < 1 || maxUses > 100 {
		fe.add("max_uses", "от 1 до 100")
	}
	if fe.any() {
		writeValidation(w, fe)
		return
	}

	// Кластер должен существовать — 404 иначе (org берём из кластера).
	cl, err := h.d.Store.Clusters.Get(r.Context(), clusterID)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	token, hash, err := store.GenerateToken()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// TODO(security): после реального OIDC класть сюда id пользователя из
	// контекста; сейчас dev-заглушка — created_by = NULL.
	t, err := h.d.Store.JoinTokens.Create(r.Context(), hash, cl.OrganizationID, clusterID,
		in.Comment, time.Now().Add(time.Duration(ttl)*time.Second), maxUses, nil)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, joinTokenCreated{
		ID:        t.ID.String(),
		Token:     token,
		ClusterID: t.ClusterID.String(),
		Name:      t.Name,
		ExpiresAt: t.ExpiresAt,
		CreatedAt: t.CreatedAt,
	})
}

// listJoinTokens — GET /api/v1/clusters/{id}/join_tokens: метаданные токенов
// (без самих токенов — их нет даже в БД, только хэши).
func (h *handlers) listJoinTokens(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	if _, err := h.d.Store.Clusters.Get(r.Context(), clusterID); err != nil {
		writeStoreError(w, err)
		return
	}
	items, err := h.d.Store.JoinTokens.ListByCluster(r.Context(), clusterID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}
