package httpapi

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/surifleet/surifleet/internal/feedsync"
	"github.com/surifleet/surifleet/internal/store"
)

// feedTypes — допустимые типы фидов (CHECK в DDL + enum openapi).
// Синхронизация (POST /feeds/{id}/sync) поддерживает generic (IOC-листы
// plain/CSV/JSON → таблица iocs) и et_open (.rules-файл ET Open →
// репозиторий rules); остальные типы — под будущие коннекторы.
var feedTypes = map[string]bool{
	"et_open": true, "et_pro": true, "taxii": true,
	"stix": true, "misp": true, "generic": true,
}

// validFeedURL — URL фида: абсолютный http(s).
func validFeedURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// validateFeedInput — общая валидация полей фида (create/update).
func validateFeedInput(fe fieldErrors, name, url_ *string, feedType *string) {
	if name != nil && strings.TrimSpace(*name) == "" {
		fe.add("name", "обязательное непустое поле")
	}
	if feedType != nil && !feedTypes[*feedType] {
		fe.add("type", "et_open|et_pro|taxii|stix|misp|generic")
	}
	if url_ != nil && !validFeedURL(*url_) {
		fe.add("url", "абсолютный URL http(s)")
	}
}

// listFeeds — GET /api/v1/feeds[?type].
func (h *handlers) listFeeds(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	cursor, limit, ok := parsePage(w, r)
	if !ok {
		return
	}
	feedType := r.URL.Query().Get("type")
	if feedType != "" && !feedTypes[feedType] {
		writeValidation(w, fieldErrors{"type": "et_open|et_pro|taxii|stix|misp|generic"})
		return
	}
	items, next, err := h.d.Store.Feeds.List(r.Context(), orgID, feedType, cursor, limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page[store.Feed]{Items: items, NextCursor: next})
}

// createFeed — POST /api/v1/feeds.
func (h *handlers) createFeed(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	var in store.FeedInput
	if !decodeJSON(w, r, &in) {
		return
	}
	// et_open без URL → дефолтный полный набор ET Open (чанк 21).
	if in.Type == "et_open" && strings.TrimSpace(in.URL) == "" {
		in.URL = feedsync.DefaultETOpenURL
	}
	fe := fieldErrors{}
	validateFeedInput(fe, &in.Name, &in.URL, &in.Type)
	if fe.any() {
		writeValidation(w, fe)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	feed, err := h.d.Store.Feeds.Create(r.Context(), orgID, in)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, feed)
}

// getFeed — GET /api/v1/feeds/{id}.
func (h *handlers) getFeed(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	feed, err := h.d.Store.Feeds.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, feed)
}

// updateFeed — PATCH /api/v1/feeds/{id}.
func (h *handlers) updateFeed(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	var p store.FeedPatch
	if !decodeJSON(w, r, &p) {
		return
	}
	fe := fieldErrors{}
	validateFeedInput(fe, p.Name, p.URL, nil)
	if fe.any() {
		writeValidation(w, fe)
		return
	}
	feed, err := h.d.Store.Feeds.Update(r.Context(), id, p)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, feed)
}

// deleteFeed — DELETE /api/v1/feeds/{id}: фид удаляется, импортированные
// IOC остаются (feed_id → NULL по FK).
func (h *handlers) deleteFeed(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	if err := h.d.Store.Feeds.Delete(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// feedSyncResult — ответ POST /feeds/{id}/sync: запись запуска +
// (при успехе) итог автопрогона генерации правил из IOC.
type feedSyncResult struct {
	store.FeedRun
	RulesCreated   int    `json:"rules_created,omitempty"`
	RulesUpdated   int    `json:"rules_updated,omitempty"`
	RulesUnchanged int    `json:"rules_unchanged,omitempty"`
	RulesetVersion string `json:"ruleset_version,omitempty"`
}

// syncFeed — POST /api/v1/feeds/{id}/sync: синхронная синхронизация фида
// (HTTP GET → разбор → импорт: generic → iocs, et_open → rules). Ошибки
// загрузки/разбора — не 5xx, а status=failed + error в теле (и last_error у
// фида). После успешного импорта generic-фида — автопрогон генерации
// правил из IOC (без деплоя).
func (h *handlers) syncFeed(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	if h.d.FeedSync == nil {
		writeError(w, http.StatusServiceUnavailable, CodeInternal,
			"синхронизация фидов не настроена", nil)
		return
	}
	feed, err := h.d.Store.Feeds.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	run, err := h.d.FeedSync.Sync(r.Context(), feed)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	res := feedSyncResult{FeedRun: run}
	// Автопрогон генерации правил из IOC (без деплоя) — только для generic
	// (IOC-фиды) и только если импорт что-то принёс; et_open сам импортирует
	// правила. Ошибка генерации не валит ответ синка: логируем.
	if feed.Type == "generic" && run.Status == "success" && run.Imported+run.Updated > 0 {
		gen := iocGenerateResult{Skipped: []iocSkip{}}
		v, err := h.generateIocRulesCore(r.Context(), feed.OrganizationID, &gen)
		if err != nil {
			errLog.Error("автогенерация правил после sync фида", "feed_id", feed.ID, "err", err)
		} else {
			res.RulesCreated = gen.Created
			res.RulesUpdated = gen.Updated
			res.RulesUnchanged = gen.Unchanged
			if v != nil {
				res.RulesetVersion = v.Version
			}
		}
	}
	writeJSON(w, http.StatusOK, res)
}

// listFeedRuns — GET /api/v1/feeds/{id}/runs: история запусков, свежие
// первыми (keyset-курсор по (started_at, id)).
func (h *handlers) listFeedRuns(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	if _, err := h.d.Store.Feeds.Get(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	limit := defaultLimit
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, CodeValidation,
				"limit должен быть целым числом от 1 до 1000",
				map[string]any{"limit": s})
			return
		}
		if n > maxLimit {
			n = maxLimit
		}
		limit = n
	}
	items, next, err := h.d.Store.Feeds.ListRuns(r.Context(), id, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page[store.FeedRun]{Items: items, NextCursor: next})
}
