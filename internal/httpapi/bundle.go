// bundle.go — диагностический бандл агента одной кнопкой (чанк 106,
// п. 7 ТЗ «Реагирование: сбор диагностического бандла»): POST
// /agents/{id}/bundle {include_*} — presigned PUT-ключ в S3 → синхронная
// CollectBundleTask агенту (SendTaskAndWait) → ответ с ключом и
// подписанным URL скачивания. Бандл (tar.gz) собирается агентом из логов,
// конфигов и системной информации хоста.
package httpapi

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
)

// collectBundleInput — POST /agents/{id}/bundle. Пустое тело → всё включено.
type collectBundleInput struct {
	IncludeAgentLogs    *bool `json:"include_agent_logs"`
	IncludeSuricataLogs *bool `json:"include_suricata_logs"`
	IncludeConfigs      *bool `json:"include_configs"`
	IncludeSystemInfo   *bool `json:"include_system_info"`
}

// collectBundle — POST /agents/{id}/bundle (agents.read): собрать
// диагностический бандл на хосте агента и загрузить в S3. Синхронно
// (SendTaskAndWait 120 с — сбор и загрузка десятки секунд). Ответ:
// ключ блоба + presigned GET для скачивания (TTL 15 мин).
func (h *handlers) collectBundle(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	var in collectBundleInput
	if !decodeJSON(w, r, &in) {
		return
	}
	agent, err := h.d.Store.Agents.GetByID(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !h.instanceAllowed(w, r, agent.HostID) { // scoping по кластеру хоста агента
		return
	}
	key := fmt.Sprintf("bundles/%s/%s/%s.tar.gz",
		orgID, agent.ID, time.Now().UTC().Format("20060102-150405")+"-"+uuid.NewString()[:8])
	putURL, err := h.d.Blob.PresignPut(r.Context(), key, 15*time.Minute)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	task := &agentv1.Task{
		TaskId: uuid.New().String(),
		Type: &agentv1.Task_CollectBundle{CollectBundle: &agentv1.CollectBundleTask{
			IncludeAgentLogs:    boolOr(in.IncludeAgentLogs, true),
			IncludeSuricataLogs: boolOr(in.IncludeSuricataLogs, true),
			IncludeConfigs:      boolOr(in.IncludeConfigs, true),
			IncludeSystemInfo:   boolOr(in.IncludeSystemInfo, true),
			UploadUrl:           putURL,
		}},
	}
	res, ok := h.d.Hub.SendTaskAndWait(r.Context(), agent.ID, task, 120*time.Second)
	if !ok {
		writeError(w, http.StatusConflict, CodeConflict,
			"агент не подключён (offline) или не ответил вовремя", nil)
		return
	}
	objType := "agent"
	if res.GetStatus() != agentv1.TaskStatus_TASK_STATUS_SUCCESS {
		msg := res.GetError()
		if msg == "" {
			msg = "агент не смог собрать бандл"
		}
		h.audit(r, identityFrom(r.Context()), "agents.bundle", &objType, &agent.ID, "error", msg)
		writeError(w, http.StatusBadGateway, CodeInternal, msg, nil)
		return
	}
	br := res.GetBundle()
	dlURL, err := h.d.Blob.PresignGet(r.Context(), key, 15*time.Minute)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	h.audit(r, identityFrom(r.Context()), "agents.bundle", &objType, &agent.ID, "success",
		fmt.Sprintf("бандл %s (%d байт)", key, br.GetSizeBytes()))
	writeJSON(w, http.StatusOK, map[string]any{
		"task_id":      task.TaskId,
		"agent_id":     agent.ID,
		"bundle_key":   key,
		"size_bytes":   br.GetSizeBytes(),
		"download_url": dlURL,
	})
}

// boolOr — *bool с дефолтом (nil → def).
func boolOr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}
