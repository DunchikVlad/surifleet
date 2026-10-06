package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/surifleet/surifleet/internal/rules"
	"github.com/surifleet/surifleet/internal/store"
)

// Лимиты импорта: размер тела и число возвращаемых ошибок парсинга.
const (
	importMaxBytes  = 64 << 20 // 64 МБ — хватает на ET Pro целиком
	importMaxErrors = 100
)

// ruleStatuses — допустимые статусы (CHECK в DDL + enum openapi).
var ruleStatuses = map[string]bool{
	"enabled": true, "disabled": true, "expired": true, "under_review": true, "deleted": true,
}

// resolveOrgID — организация контекста запроса. Token-режим (чанк 28):
// организация пользователя из identity (параметр ?organization_id=,
// указывающий на чужую org, → 404 по конвенции scoping). Dev-режим:
// ?organization_id= либо старейшая организация.
func (h *handlers) resolveOrgID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	if id := identityFrom(r.Context()); id != nil && !id.Dev {
		if s := r.URL.Query().Get("organization_id"); s != "" {
			reqID, err := uuid.Parse(s)
			if err != nil {
				writeValidation(w, fieldErrors{"organization_id": "ожидается UUID"})
				return uuid.Nil, false
			}
			if reqID != id.OrgID {
				writeError(w, http.StatusNotFound, CodeNotFound, "ресурс не найден", nil)
				return uuid.Nil, false
			}
		}
		return id.OrgID, true
	}
	if s := r.URL.Query().Get("organization_id"); s != "" {
		return pathUUID(w, s, "organization_id")
	}
	org, err := h.d.Store.Organizations.First(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return uuid.Nil, false
	}
	return org.ID, true
}

// clusterAllowed — scoping по кластерам (чанк 43): если identity ограничен
// подмножеством кластеров и clusterID вне scope — пишет 404 (объект вне
// scope неотличим от несуществующего, п. 8) и возвращает false.
func (h *handlers) clusterAllowed(w http.ResponseWriter, r *http.Request, clusterID uuid.UUID) bool {
	id := identityFrom(r.Context())
	if id == nil || id.ClusterScopeAllowed(clusterID) {
		return true
	}
	writeError(w, http.StatusNotFound, CodeNotFound, "ресурс не найден", nil)
	return false
}

// instanceAllowed — scoping инстанса по кластеру его хоста (чанк 43).
// При внутренней ошибке чтения хоста — пишет её и false.
func (h *handlers) instanceAllowed(w http.ResponseWriter, r *http.Request, hostID uuid.UUID) bool {
	id := identityFrom(r.Context())
	if id == nil || !id.ScopeRestricted {
		return true
	}
	host, err := h.d.Store.Hosts.Get(r.Context(), hostID)
	if err != nil {
		writeStoreError(w, err)
		return false
	}
	return h.clusterAllowed(w, r, host.ClusterID)
}

// clusterScopeFilter — разрешённый набор кластеров для фильтра списков
// (nil = без ограничений). Возвращает restricted + множество.
func (h *handlers) clusterScopeFilter(r *http.Request) (map[uuid.UUID]bool, bool) {
	id := identityFrom(r.Context())
	if id == nil || !id.ScopeRestricted {
		return nil, false
	}
	set := make(map[uuid.UUID]bool, len(id.ScopeClusters))
	for _, c := range id.ScopeClusters {
		set[c] = true
	}
	return set, true
}

// importItem — ImportItem из разобранного правила (parsed → jsonb ревизии).
func importItem(p *rules.Parsed) (store.ImportItem, error) {
	parsedJSON, err := json.Marshal(p)
	if err != nil {
		return store.ImportItem{}, err
	}
	return store.ImportItem{
		SID: p.SID, Rev: p.Rev, Msg: p.Msg, Classtype: p.Classtype,
		Raw: p.Raw, Parsed: parsedJSON,
	}, nil
}

// listRules — GET /api/v1/rules[?status&category&tag&source&sid&q].
func (h *handlers) listRules(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	cursor, limit, ok := parsePage(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := store.RuleFilter{
		Status:   q.Get("status"),
		Category: q.Get("category"),
		Tag:      q.Get("tag"),
		Source:   q.Get("source"),
		Q:        q.Get("q"),
	}
	fe := fieldErrors{}
	if f.Status != "" && !ruleStatuses[f.Status] {
		fe.add("status", "enabled|disabled|expired|under_review|deleted")
	}
	if f.Source != "" && f.Source != "file" && f.Source != "feed" && f.Source != "ioc" && f.Source != "et_open" && f.Source != "et_pro" {
		fe.add("source", "file|feed|ioc|et_open|et_pro")
	}
	if s := q.Get("sid"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n <= 0 {
			fe.add("sid", "положительное целое")
		} else {
			f.SID = n
		}
	}
	if s := q.Get("feed_id"); s != "" {
		id, err := uuid.Parse(s)
		if err != nil {
			fe.add("feed_id", "некорректный UUID")
		} else {
			f.FeedID = id
		}
	}
	if fe.any() {
		writeValidation(w, fe)
		return
	}
	items, next, err := h.d.Store.Rules.List(r.Context(), orgID, f, cursor, limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page[store.Rule]{Items: items, NextCursor: next})
}

// createRule — POST /api/v1/rules (ручное создание из raw-текста).
func (h *handlers) createRule(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	var in struct {
		Raw       string          `json:"raw"`
		Category  *string         `json:"category"`
		Tags      []string        `json:"tags"`
		Status    string          `json:"status"`
		Priority  *int            `json:"priority"`
		Threshold json.RawMessage `json:"threshold"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	fe := fieldErrors{}
	if strings.TrimSpace(in.Raw) == "" {
		fe.add("raw", "обязательное поле (текст правила)")
	}
	if in.Status != "" && in.Status != "enabled" && in.Status != "disabled" && in.Status != "under_review" {
		fe.add("status", "enabled|disabled|under_review")
	}
	if fe.any() {
		writeValidation(w, fe)
		return
	}
	parsed, err := rules.Parse(in.Raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation,
			"правило не разобрано", map[string]any{"reason": err.Error()})
		return
	}
	if parsed == nil {
		writeError(w, http.StatusBadRequest, CodeValidation,
			"raw — комментарий или пустая строка, а не правило", nil)
		return
	}
	item, err := importItem(parsed)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	rule, err := h.d.Store.Rules.CreateManual(r.Context(), orgID, item,
		store.RulePatch{Category: in.Category, Tags: in.Tags, Priority: in.Priority, Threshold: in.Threshold}, in.Status)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

// getRule — GET /api/v1/rules/{id}.
func (h *handlers) getRule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	rule, err := h.d.Store.Rules.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

// updateRule — PATCH /api/v1/rules/{id} (тюнинг аналитика).
func (h *handlers) updateRule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	var p store.RulePatch
	if !decodeJSON(w, r, &p) {
		return
	}
	fe := fieldErrors{}
	if p.Status != nil && !ruleStatuses[*p.Status] {
		fe.add("status", "enabled|disabled|expired|under_review|deleted")
	}
	if len(p.Threshold) > 0 && !json.Valid(p.Threshold) {
		fe.add("threshold", "некорректный JSON")
	}
	if fe.any() {
		writeValidation(w, fe)
		return
	}
	oldRule, _ := h.d.Store.Rules.Get(r.Context(), id) // «было» для аудит-diff (чанк 39)
	rule, err := h.d.Store.Rules.Update(r.Context(), id, p)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	objType := "rule"
	h.auditDiff(r, identityFrom(r.Context()), "rules.update", &objType, &id, oldRule, rule)
	writeJSON(w, http.StatusOK, rule)
}

// deleteRule — DELETE /api/v1/rules/{id} (soft: status='deleted').
func (h *handlers) deleteRule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	if err := h.d.Store.Rules.SoftDelete(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// importResult — ответ POST /rules/import (openapi ImportResult).
type importResult struct {
	Imported   int               `json:"imported"`
	Updated    int               `json:"updated"`
	Unchanged  int               `json:"unchanged"`
	Errors     []rules.LineError `json:"errors"`
	TotalLines int               `json:"total_lines"`
}

// importRules — POST /api/v1/rules/import: multipart/form-data (file) или
// application/json ({text}); опционально category (override classtype) и
// source (file|feed, default file). Идемпотентно по (org, sid): новые sid —
// imported, изменившийся raw — updated (новая ревизия), совпавший — unchanged.
func (h *handlers) importRules(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	var (
		content  io.Reader
		category string
		source   = "file"
	)
	ct := r.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "multipart/form-data"):
		r.Body = http.MaxBytesReader(w, r.Body, importMaxBytes)
		if err := r.ParseMultipartForm(importMaxBytes); err != nil {
			writeError(w, http.StatusBadRequest, CodeValidation,
				"multipart: "+err.Error(), nil)
			return
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, CodeValidation,
				"multipart: поле file обязательно", nil)
			return
		}
		defer f.Close()
		content = f
		category = r.FormValue("category")
		if s := r.FormValue("source"); s != "" {
			source = s
		}
	case strings.HasPrefix(ct, "application/json"):
		var in struct {
			Text     string `json:"text"`
			Category string `json:"category"`
			Source   string `json:"source"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}
		if strings.TrimSpace(in.Text) == "" {
			writeError(w, http.StatusBadRequest, CodeValidation,
				"text: обязательное поле (содержимое .rules)", nil)
			return
		}
		content = strings.NewReader(in.Text)
		category = in.Category
		if in.Source != "" {
			source = in.Source
		}
	default:
		writeError(w, http.StatusBadRequest, CodeValidation,
			"Content-Type должен быть multipart/form-data или application/json", map[string]any{"content_type": ct})
		return
	}
	if source != "file" && source != "feed" {
		writeError(w, http.StatusBadRequest, CodeValidation, "source: file|feed", nil)
		return
	}

	parsed := rules.ParseReader(content, importMaxErrors)
	res := importResult{Errors: parsed.Errors, TotalLines: parsed.TotalLines}
	if res.Errors == nil {
		res.Errors = []rules.LineError{}
	}
	for _, p := range parsed.Rules {
		item, err := importItem(p)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		_, outcome, err := h.d.Store.Rules.UpsertImport(r.Context(), orgID, item, category, source)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		switch outcome {
		case store.UpsertImported:
			res.Imported++
		case store.UpsertUpdated:
			res.Updated++
		case store.UpsertUnchanged:
			res.Unchanged++
		}
	}
	writeJSON(w, http.StatusOK, res)
}

// bulkRules — POST /api/v1/rules/bulk: цель — ids (приоритетно) или filter;
// действия enable|disable|delete|set_priority|add_tag|remove_tag.
func (h *handlers) bulkRules(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	var in struct {
		IDs    []uuid.UUID `json:"ids"`
		Filter struct {
			Status   string    `json:"status"`
			Category string    `json:"category"`
			Tag      string    `json:"tag"`
			Source   string    `json:"source"`
			FeedID   uuid.UUID `json:"feed_id"`
		} `json:"filter"`
		Action string `json:"action"`
		Params struct {
			Priority *int    `json:"priority"`
			Tag      *string `json:"tag"`
		} `json:"params"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	fe := fieldErrors{}
	switch in.Action {
	case "enable", "disable", "delete":
	case "set_priority":
		if in.Params.Priority == nil {
			fe.add("params.priority", "обязателен для set_priority")
		}
	case "add_tag", "remove_tag":
		if in.Params.Tag == nil || strings.TrimSpace(*in.Params.Tag) == "" {
			fe.add("params.tag", "обязателен для add_tag/remove_tag")
		}
	default:
		fe.add("action", "enable|disable|delete|set_priority|add_tag|remove_tag")
	}
	if in.Filter.Status != "" && !ruleStatuses[in.Filter.Status] {
		fe.add("filter.status", "enabled|disabled|expired|under_review|deleted")
	}
	if in.Filter.Source != "" && in.Filter.Source != "file" && in.Filter.Source != "feed" && in.Filter.Source != "ioc" {
		fe.add("filter.source", "file|feed|ioc")
	}
	if len(in.IDs) == 0 && in.Filter.Status == "" && in.Filter.Category == "" &&
		in.Filter.Tag == "" && in.Filter.Source == "" && in.Filter.FeedID == uuid.Nil {
		fe.add("filter", "нужен ids или непустой filter (иначе операция накрыла бы весь репозиторий)")
	}
	if fe.any() {
		writeValidation(w, fe)
		return
	}
	affected, err := h.d.Store.Rules.Bulk(r.Context(), orgID, in.IDs,
		store.RuleFilter{
			Status: in.Filter.Status, Category: in.Filter.Category, Tag: in.Filter.Tag,
			Source: in.Filter.Source, FeedID: in.Filter.FeedID,
		}, in.Action, in.Params.Priority, in.Params.Tag)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"affected": affected})
}

// listRuleRevisions — GET /api/v1/rules/{id}/revisions (keyset по номеру
// ревизии: ?cursor — последний revision предыдущей страницы).
func (h *handlers) listRuleRevisions(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	limit := defaultLimit
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, CodeValidation,
				"limit должен быть целым числом от 1 до 1000", map[string]any{"limit": s})
			return
		}
		if n > maxLimit {
			n = maxLimit
		}
		limit = n
	}
	afterRev := 0
	if s := r.URL.Query().Get("cursor"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, CodeValidation,
				"некорректный cursor (ожидается номер ревизии)", map[string]any{"cursor": s})
			return
		}
		afterRev = n
	}
	items, next, err := h.d.Store.Rules.ListRevisions(r.Context(), id, afterRev, limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page[store.RuleRevision]{Items: items, NextCursor: next})
}
