// update_config.go — конфиги suricata-update с хоста (чанк 113, backlog
// заказчика): GET /instances/{id}/update_config?file=&source_name= —
// синхронный fetch (FetchUpdateConfigTask) для редактора «как на хосте»
// (по образцу fetch_config для suricata.yaml); POST
// /instances/{id}/update_config {file, content, expected_sha256?} — deploy
// с бэкапом на сенсоре и записью в историю применений; GET
// /instances/{id}/update_config/history — история. Права: config.read/
// config.write (параллельно suricata.yaml); на агенте gated capability
// config. Аудит — deploy (instances.update_config).
package httpapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
)

// updateConfigWait — синхронное ожидание агента (чтение/запись малых файлов
// — секунды; запас как у fetch_config).
const updateConfigWait = 60 * time.Second

// parseUpdateConfigFile — маппинг строки API → enum; source_name —
// отдельный параметр (только для source_yaml).
func parseUpdateConfigFile(s string) (agentv1.UpdateConfigFile, bool) {
	switch s {
	case "update_yaml":
		return agentv1.UpdateConfigFile_UPDATE_CONFIG_FILE_UPDATE_YAML, true
	case "enable_conf":
		return agentv1.UpdateConfigFile_UPDATE_CONFIG_FILE_ENABLE_CONF, true
	case "disable_conf":
		return agentv1.UpdateConfigFile_UPDATE_CONFIG_FILE_DISABLE_CONF, true
	case "modify_conf":
		return agentv1.UpdateConfigFile_UPDATE_CONFIG_FILE_MODIFY_CONF, true
	case "source_yaml":
		return agentv1.UpdateConfigFile_UPDATE_CONFIG_FILE_SOURCE_YAML, true
	default:
		return agentv1.UpdateConfigFile_UPDATE_CONFIG_FILE_UNSPECIFIED, false
	}
}

// resolveUpdateConfigTarget — инстанс + агент хоста со scoping (общий пролог
// трёх эндпоинтов).
func (h *handlers) resolveUpdateConfigTarget(w http.ResponseWriter, r *http.Request) (instID, agentID string, ok bool) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return "", "", false
	}
	inst, err := h.d.Store.Instances.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return "", "", false
	}
	if !h.instanceAllowed(w, r, inst.HostID) { // scoping (чанк 43)
		return "", "", false
	}
	agent, err := h.d.Store.Agents.GetByHostID(r.Context(), nil, inst.HostID)
	if err != nil {
		writeStoreError(w, err)
		return "", "", false
	}
	return inst.ID.String(), agent.ID.String(), true
}

// fetchUpdateConfig — GET /instances/{id}/update_config?file=&source_name=
// (config.read): синхронный fetch конфига suricata-update с сенсора.
func (h *handlers) fetchUpdateConfig(w http.ResponseWriter, r *http.Request) {
	instID, agentID, ok := h.resolveUpdateConfigTarget(w, r)
	if !ok {
		return
	}
	fileStr := r.URL.Query().Get("file")
	file, ok := parseUpdateConfigFile(fileStr)
	if !ok {
		writeValidation(w, fieldErrors{"file": "update_yaml | enable_conf | disable_conf | modify_conf | source_yaml"})
		return
	}
	sourceName := r.URL.Query().Get("source_name")
	if file == agentv1.UpdateConfigFile_UPDATE_CONFIG_FILE_SOURCE_YAML && sourceName == "" {
		writeValidation(w, fieldErrors{"source_name": "обязателен для source_yaml"})
		return
	}
	agentUUID, _ := uuid.Parse(agentID)
	task := &agentv1.Task{
		TaskId: uuid.New().String(),
		Type: &agentv1.Task_FetchUpdateConfig{FetchUpdateConfig: &agentv1.FetchUpdateConfigTask{
			InstanceId: instID,
			File:       file,
			SourceName: sourceName,
		}},
	}
	res, ok := h.d.Hub.SendTaskAndWait(r.Context(), agentUUID, task, updateConfigWait)
	if !ok {
		writeError(w, http.StatusConflict, CodeConflict,
			"агент инстанса не подключён (offline) или не ответил вовремя", nil)
		return
	}
	if res.GetStatus() != agentv1.TaskStatus_TASK_STATUS_SUCCESS {
		msg := res.GetError()
		if msg == "" {
			msg = "агент не смог прочитать конфиг suricata-update"
		}
		writeError(w, http.StatusBadGateway, CodeInternal, msg, nil)
		return
	}
	fu := res.GetFetchUpdateConfig()
	writeJSON(w, http.StatusOK, map[string]any{
		"file":        fileStr,
		"source_name": sourceName,
		"path":        fu.GetPath(),
		"exists":      fu.GetExists(),
		"content":     fu.GetContent(),
		"sha256":      fu.GetSha256(),
		"size_bytes":  fu.GetSizeBytes(),
	})
}

// deployUpdateConfigInput — POST /instances/{id}/update_config.
type deployUpdateConfigInput struct {
	File           string `json:"file"`
	SourceName     string `json:"source_name"`
	Content        string `json:"content"`
	ExpectedSha256 string `json:"expected_sha256"`
}

// deployUpdateConfig — POST /instances/{id}/update_config (config.write):
// запись конфига suricata-update на сенсор (бэкап, история применений,
// аудит instances.update_config).
func (h *handlers) deployUpdateConfig(w http.ResponseWriter, r *http.Request) {
	instID, agentID, ok := h.resolveUpdateConfigTarget(w, r)
	if !ok {
		return
	}
	var in deployUpdateConfigInput
	if !decodeJSON(w, r, &in) {
		return
	}
	file, ok := parseUpdateConfigFile(in.File)
	if !ok {
		writeValidation(w, fieldErrors{"file": "update_yaml | enable_conf | disable_conf | modify_conf | source_yaml"})
		return
	}
	if file == agentv1.UpdateConfigFile_UPDATE_CONFIG_FILE_SOURCE_YAML && in.SourceName == "" {
		writeValidation(w, fieldErrors{"source_name": "обязателен для source_yaml"})
		return
	}
	instUUID, _ := uuid.Parse(instID)
	agentUUID, _ := uuid.Parse(agentID)
	task := &agentv1.Task{
		TaskId: uuid.New().String(),
		Type: &agentv1.Task_DeployUpdateConfig{DeployUpdateConfig: &agentv1.DeployUpdateConfigTask{
			InstanceId:     instID,
			File:           file,
			SourceName:     in.SourceName,
			Content:        in.Content,
			ExpectedSha256: in.ExpectedSha256,
		}},
	}
	res, ok := h.d.Hub.SendTaskAndWait(r.Context(), agentUUID, task, updateConfigWait)
	objType := "instance"
	actor := identityFrom(r.Context())
	reason := in.File + " suricata-update на инстансе " + instID
	if !ok {
		writeError(w, http.StatusConflict, CodeConflict,
			"агент инстанса не подключён (offline) или не ответил вовремя", nil)
		return
	}
	if res.GetStatus() != agentv1.TaskStatus_TASK_STATUS_SUCCESS {
		msg := res.GetError()
		if msg == "" {
			msg = "агент не смог записать конфиг suricata-update"
		}
		h.audit(r, actor, "instances.update_config", &objType, &instUUID, "error", reason+": "+msg)
		writeError(w, http.StatusBadGateway, CodeInternal, msg, nil)
		return
	}
	h.audit(r, actor, "instances.update_config", &objType, &instUUID, "success", reason)
	du := res.GetDeployUpdateConfig()
	writeJSON(w, http.StatusOK, map[string]any{
		"file":        in.File,
		"source_name": in.SourceName,
		"path":        du.GetPath(),
		"backup":      du.GetBackup(),
		"sha256":      du.GetSha256(),
		"size_bytes":  du.GetSizeBytes(),
		"unchanged":   du.GetUnchanged(),
	})
}

// updateConfigHistory — GET /instances/{id}/update_config/history
// (config.read): последние применения конфигов suricata-update на инстансе.
func (h *handlers) updateConfigHistory(w http.ResponseWriter, r *http.Request) {
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
	items, err := h.d.Store.Configs.UpdateConfigHistory(r.Context(), id, 100)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}
