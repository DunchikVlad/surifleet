// suricata-update на агенте по кнопке (чанк 82): запуск обновления с
// предварительным enable/disable источников, заливка итогового набора в
// S3 и импорт в общий список правил (internal/suriupdate), список
// источников (синхронно).
package httpapi

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
)

// suriUpdatePresignTTL — срок действия PUT URL для заливки набора агентом.
const suriUpdatePresignTTL = 20 * time.Minute

// runSuricataUpdate — POST /instances/{id}/suricata_update (rules.write):
// {enable_sources, disable_sources, reload, import?}. Задача агенту;
// import=true (default) — агент зальёт итоговый suricata.rules в S3, сервер
// по результату импортирует его в мастер-репозиторий (общий список).
func (h *handlers) runSuricataUpdate(w http.ResponseWriter, r *http.Request) {
	_, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	var in struct {
		EnableSources  []string `json:"enable_sources"`
		DisableSources []string `json:"disable_sources"`
		Reload         bool     `json:"reload"`
		Import         *bool    `json:"import"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	inst, err := h.d.Store.Instances.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !h.instanceAllowed(w, r, inst.HostID) { // scoping (чанк 43)
		return
	}
	agent, err := h.d.Store.Agents.GetByHostID(r.Context(), nil, inst.HostID)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	doImport := in.Import == nil || *in.Import
	taskID := uuid.New().String()
	key := "suricata-update/" + taskID + ".rules"
	su := &agentv1.SuricataUpdateTask{
		InstanceId:     id.String(),
		EnableSources:  in.EnableSources,
		DisableSources: in.DisableSources,
		Reload:         in.Reload,
		UploadKey:      key,
	}
	if doImport {
		url, err := h.d.Blob.PresignPut(r.Context(), key, suriUpdatePresignTTL)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		su.UploadUrl = url
		skey := "suricata-update/" + taskID + ".sources.json"
		surl, err := h.d.Blob.PresignPut(r.Context(), skey, suriUpdatePresignTTL)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		su.SourcesUrl = surl
		su.SourcesKey = skey
	}
	task := &agentv1.Task{TaskId: taskID, Type: &agentv1.Task_SuricataUpdate{SuricataUpdate: su}}
	if !h.d.Hub.SendTask(agent.ID, task) {
		writeError(w, http.StatusConflict, CodeConflict, "агент инстанса не подключён (offline)", nil)
		return
	}
	objType := "instance"
	h.audit(r, identityFrom(r.Context()), "instances.suricata_update", &objType, &id, "success",
		"suricata-update, задача "+taskID[:8])
	writeJSON(w, http.StatusAccepted, map[string]any{
		"task_id": taskID, "instance_id": id, "agent_id": agent.ID,
		"import": doImport,
	})
}

// listSuricataUpdateSources — GET /instances/{id}/suricata_update/sources
// (rules.read): синхронный список источников suricata-update агента.
func (h *handlers) listSuricataUpdateSources(w http.ResponseWriter, r *http.Request) {
	_, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	inst, err := h.d.Store.Instances.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !h.instanceAllowed(w, r, inst.HostID) { // scoping (чанк 43)
		return
	}
	agent, err := h.d.Store.Agents.GetByHostID(r.Context(), nil, inst.HostID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	task := &agentv1.Task{
		TaskId: uuid.New().String(),
		Type: &agentv1.Task_SuricataUpdate{SuricataUpdate: &agentv1.SuricataUpdateTask{
			InstanceId:   id.String(),
			ListSources:  true,
			NoUpdate:     true,
			UploadKey:    fmt.Sprintf("noop-%s", uuid.New().String()[:8]),
		}},
	}
	res, ok := h.d.Hub.SendTaskAndWait(r.Context(), agent.ID, task, 60*time.Second)
	if !ok {
		writeError(w, http.StatusConflict, CodeConflict,
			"агент инстанса не подключён (offline) или не ответил вовремя", nil)
		return
	}
	if res.GetStatus() != agentv1.TaskStatus_TASK_STATUS_SUCCESS {
		msg := res.GetError()
		if msg == "" {
			msg = "агент не смог получить список источников"
		}
		writeError(w, http.StatusBadGateway, CodeInternal, msg, nil)
		return
	}
	su := res.GetSuricataUpdate()
	if su == nil {
		writeError(w, http.StatusBadGateway, CodeInternal, "агент вернул результат без списка", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": su.GetSources()})
}
