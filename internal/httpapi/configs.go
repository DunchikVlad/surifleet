// Версии конфигураций Suricata (чанк 54, план 1B): POST/GET
// /config_versions, GET content, POST deploy (задача deploy_config агенту).
package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
	"github.com/surifleet/surifleet/internal/ruleset"
	"github.com/surifleet/surifleet/internal/store"
)

// configVersionInput — POST /config_versions.
type configVersionInput struct {
	Version string `json:"version"` // пусто → авто cfg-v<N> per-org
	YAML    string `json:"yaml"`    // содержимое suricata.yaml
	Note    string `json:"note"`
}

// createConfigVersion — POST /config_versions (config.write): сохранить
// конфигурацию как версию (контент — блоб в S3, content-addressed).
func (h *handlers) createConfigVersion(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	var in configVersionInput
	if !decodeJSON(w, r, &in) {
		return
	}
	fe := fieldErrors{}
	if strings.TrimSpace(in.YAML) == "" {
		fe.add("yaml", "обязательное поле (содержимое suricata.yaml)")
	}
	if fe.any() {
		writeValidation(w, fe)
		return
	}
	version := in.Version
	if version == "" {
		var err error
		version, err = h.d.Store.Configs.NextAutoVersion(r.Context(), orgID)
		if err != nil {
			writeStoreError(w, err)
			return
		}
	} else if !validName(version) {
		writeValidation(w, fieldErrors{"version": "1..200 символов"})
		return
	}

	sum := sha256.Sum256([]byte(in.YAML))
	sha := hex.EncodeToString(sum[:])
	key := ruleset.BlobKey(sha) // тот же бакет/схема, что у ruleset
	if _, err := h.d.Blob.PutIfAbsent(r.Context(), key, []byte(in.YAML)); err != nil {
		errLog.Error("загрузка конфиг-блоба в S3", "err", err)
		writeError(w, http.StatusInternalServerError, CodeInternal, "загрузка блоба в хранилище", nil)
		return
	}

	var createdBy *uuid.UUID
	if id := identityFrom(r.Context()); id != nil && !id.Dev {
		createdBy = &id.UserID
	}
	v, created, err := h.d.Store.Configs.Create(r.Context(), orgID, version, sha, key, in.Note, createdBy)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	objType := "config_version"
	h.audit(r, identityFrom(r.Context()), "configs.create", &objType, &v.ID, "success", "")
	code := http.StatusOK
	if created {
		code = http.StatusCreated
	}
	writeJSON(w, code, v)
}

// listConfigVersions — GET /config_versions (config.read).
func (h *handlers) listConfigVersions(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	cursor, limit, ok := parsePage(w, r)
	if !ok {
		return
	}
	items, next, err := h.d.Store.Configs.List(r.Context(), orgID, cursor, limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page[store.ConfigVersion]{Items: items, NextCursor: next})
}

// getConfigVersion — GET /config_versions/{id} (config.read).
func (h *handlers) getConfigVersion(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	v, err := h.d.Store.Configs.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// getConfigContent — GET /config_versions/{id}/content (config.read):
// сам YAML (для редактора UI).
func (h *handlers) getConfigContent(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	v, err := h.d.Store.Configs.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	body, err := h.d.Blob.Get(r.Context(), v.S3Key)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// deployConfigInput — POST /config_versions/{id}/deploy.
type deployConfigInput struct {
	InstanceID   string `json:"instance_id"`
	ValidateOnly bool   `json:"validate_only"` // только suricata -T, не применять
}

// deployConfig — POST /config_versions/{id}/deploy (config.write):
// задача deploy_config агенту инстанса (прямая отправка через hub;
// волновой оркестратор для конфигов — следующие чанки).
func (h *handlers) deployConfig(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	var in deployConfigInput
	if !decodeJSON(w, r, &in) {
		return
	}
	instID, err := uuid.Parse(in.InstanceID)
	if err != nil {
		writeValidation(w, fieldErrors{"instance_id": "обязательный UUID"})
		return
	}
	cv, err := h.d.Store.Configs.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if cv.OrganizationID != orgID {
		writeError(w, http.StatusNotFound, CodeNotFound, "ресурс не найден", nil)
		return
	}
	inst, err := h.d.Store.Instances.Get(r.Context(), instID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	agent, err := h.d.Store.Agents.GetByHostID(r.Context(), nil, inst.HostID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	url, err := h.d.Blob.PresignGet(r.Context(), cv.S3Key, 15*time.Minute)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	task := &agentv1.Task{
		TaskId: uuid.New().String(),
		Type: &agentv1.Task_DeployConfig{DeployConfig: &agentv1.DeployConfigTask{
			InstanceId:    instID.String(),
			ConfigVersion: cv.Version,
			ValidateOnly:  in.ValidateOnly,
			Source:        &agentv1.DeployConfigTask_SignedUrl{SignedUrl: url},
		}},
	}
	if !h.d.Hub.SendTask(agent.ID, task) {
		writeError(w, http.StatusConflict, CodeConflict,
			"агент инстанса не подключён (offline)", nil)
		return
	}
	objType := "config_version"
	reason := "deploy на инстанс " + inst.Name + " (задача " + task.TaskId + ")"
	h.audit(r, identityFrom(r.Context()), "configs.deploy", &objType, &cv.ID, "success", reason)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"task_id": task.TaskId, "config_version_id": cv.ID, "version": cv.Version,
		"instance_id": instID, "agent_id": agent.ID,
	})
}

// fetchInstanceConfig — GET /instances/{id}/config/current (config.read):
// фактический suricata.yaml инстанса с сенсора — синхронная FetchConfigTask
// агенту через hub (ожидание результата до 30 с; чанк 55, план 1B).
func (h *handlers) fetchInstanceConfig(w http.ResponseWriter, r *http.Request) {
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
		Type:   &agentv1.Task_FetchConfig{FetchConfig: &agentv1.FetchConfigTask{InstanceId: inst.ID.String()}},
	}
	res, ok := h.d.Hub.SendTaskAndWait(r.Context(), agent.ID, task, 30*time.Second)
	if !ok {
		writeError(w, http.StatusConflict, CodeConflict,
			"агент инстанса не подключён (offline) или не ответил вовремя", nil)
		return
	}
	if res.GetStatus() != agentv1.TaskStatus_TASK_STATUS_SUCCESS {
		msg := res.GetError()
		if msg == "" {
			msg = "агент не смог прочитать конфигурацию"
		}
		writeError(w, http.StatusBadGateway, CodeInternal, msg, nil)
		return
	}
	fc := res.GetFetchConfig()
	if fc == nil {
		writeError(w, http.StatusBadGateway, CodeInternal, "агент вернул результат без содержимого", nil)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("X-Config-Sha256", fc.GetSha256())
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(fc.GetContent()))
}
