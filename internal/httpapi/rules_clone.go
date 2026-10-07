// Клонирование правил (чанк 51): копия с новым sid из локального
// диапазона (9000xxx) и новым msg.
package httpapi

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/surifleet/surifleet/internal/rules"
	"github.com/surifleet/surifleet/internal/store"
)

// rewriteRuleRaw — клон текста правила: замена sid, сброс rev в 1 и
// (если задано) замена msg. sid/rev — по первому вхождению опции,
// msg — по значению в кавычках с учётом экранирования.
func rewriteRuleRaw(raw string, newSid int64, newMsg string) string {
	raw = regexp.MustCompile(`sid:\s*\d+`).ReplaceAllString(raw, "sid:"+strconv.FormatInt(newSid, 10))
	raw = regexp.MustCompile(`rev:\s*\d+`).ReplaceAllString(raw, "rev:1")
	if newMsg != "" {
		if i := strings.Index(raw, `msg:"`); i >= 0 {
			// конец значения — первая неэкранированная кавычка после msg:"
			j := i + 5
			for k := j; k < len(raw); k++ {
				if raw[k] == '\\' {
					k++
					continue
				}
				if raw[k] == '"' {
					j = k
					break
				}
			}
			raw = raw[:i+5] + strings.ReplaceAll(newMsg, `"`, `\"`) + raw[j:]
		}
	}
	return raw
}

// cloneRule — POST /api/v1/rules/{id}/clone: копия правила с новым sid
// (локальный диапазон 9000xxx) и msg (по умолчанию "<msg> (копия)").
// Клон создаётся со status=under_review (как ручное правило).
func (h *handlers) cloneRule(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	var in struct {
		Msg string `json:"msg"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}

	src, err := h.d.Store.Rules.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	raws, err := h.d.Store.Rules.SelectRawByIDs(r.Context(), orgID, []uuid.UUID{id})
	if err != nil || len(raws) != 1 {
		writeStoreError(w, err)
		return
	}
	newSid, err := h.d.Store.Rules.NextFreeSid(r.Context(), orgID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	newMsg := strings.TrimSpace(in.Msg)
	if newMsg == "" {
		if src.Msg != nil {
			newMsg = *src.Msg + " (копия)"
		}
	}
	raw := rewriteRuleRaw(raws[0].Raw, newSid, newMsg)

	parsed, err := rules.Parse(raw)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	item, err := importItem(parsed)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	rule, err := h.d.Store.Rules.CreateManual(r.Context(), orgID, item, store.RulePatch{}, "under_review")
	if err != nil {
		writeStoreError(w, err)
		return
	}
	objType := "rule"
	reason := fmt.Sprintf("клон sid %d → %d", src.SID, newSid)
	h.audit(r, identityFrom(r.Context()), "rules.clone", &objType, &rule.ID, "success", reason)
	writeJSON(w, http.StatusCreated, rule)
}
