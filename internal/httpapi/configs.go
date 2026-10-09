// Версии конфигураций Suricata (чанк 54, план 1B): POST/GET
// /config_versions, GET content, POST deploy (задача deploy_config агенту).
package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
	"github.com/surifleet/surifleet/internal/orchestrator"
	"github.com/surifleet/surifleet/internal/ruleset"
	"github.com/surifleet/surifleet/internal/store"
)

// configVersionInput — POST /config_versions.
type configVersionInput struct {
	Version string `json:"version"` // пусто → авто cfg-v<N> per-org
	YAML    string `json:"yaml"`    // содержимое suricata.yaml
	Note    string `json:"note"`
}

// storeConfigVersion — общее ядро сохранения версии конфигурации
// (POST /config_versions и деплой профиля, чанк 66): sha256,
// content-addressed блоб в S3, запись в БД. version пусто → авто
// cfg-v<N> per-org. Вызыватели сами пишут аудит и ответ.
func (h *handlers) storeConfigVersion(ctx context.Context, orgID uuid.UUID, version, yml, note string) (store.ConfigVersion, bool, error) {
	if version == "" {
		var err error
		version, err = h.d.Store.Configs.NextAutoVersion(ctx, orgID)
		if err != nil {
			return store.ConfigVersion{}, false, err
		}
	}
	sum := sha256.Sum256([]byte(yml))
	sha := hex.EncodeToString(sum[:])
	key := ruleset.BlobKey(sha) // тот же бакет/схема, что у ruleset
	if _, err := h.d.Blob.PutIfAbsent(ctx, key, []byte(yml)); err != nil {
		errLog.Error("загрузка конфиг-блоба в S3", "err", err)
		return store.ConfigVersion{}, false, err
	}
	var createdBy *uuid.UUID
	if id := identityFrom(ctx); id != nil && !id.Dev {
		createdBy = &id.UserID
	}
	v, created, err := h.d.Store.Configs.Create(ctx, orgID, version, sha, key, note, createdBy)
	return v, created, err
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
	if in.Version != "" && !validName(in.Version) {
		writeValidation(w, fieldErrors{"version": "1..200 символов"})
		return
	}
	v, created, err := h.storeConfigVersion(r.Context(), orgID, in.Version, in.YAML, in.Note)
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

// errAgentOffline — агент инстанса не подключён (409, как в deployConfig).
var errAgentOffline = errors.New("агент инстанса не подключён (offline)")

// dispatchDeployConfigTask — общая часть отправки задачи deploy_config
// агенту инстанса (POST /config_versions/{id}/deploy и деплой профиля,
// чанк 66): агент по host_id, подписанный URL блоба, SendTask.
func (h *handlers) dispatchDeployConfigTask(ctx context.Context, cv store.ConfigVersion, inst store.Instance, validateOnly bool) (taskID string, agentID uuid.UUID, err error) {
	agent, err := h.d.Store.Agents.GetByHostID(ctx, nil, inst.HostID)
	if err != nil {
		return "", uuid.Nil, err
	}
	url, err := h.d.Blob.PresignGet(ctx, cv.S3Key, 15*time.Minute)
	if err != nil {
		return "", uuid.Nil, err
	}
	task := &agentv1.Task{
		TaskId: uuid.New().String(),
		Type: &agentv1.Task_DeployConfig{DeployConfig: &agentv1.DeployConfigTask{
			InstanceId:    inst.ID.String(),
			ConfigVersion: cv.Version,
			ValidateOnly:  validateOnly,
			Source:        &agentv1.DeployConfigTask_SignedUrl{SignedUrl: url},
		}},
	}
	if !h.d.Hub.SendTask(agent.ID, task) {
		return "", uuid.Nil, errAgentOffline
	}
	return task.TaskId, agent.ID, nil
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
	taskID, agentID, err := h.dispatchDeployConfigTask(r.Context(), cv, inst, in.ValidateOnly)
	if errors.Is(err, errAgentOffline) {
		writeError(w, http.StatusConflict, CodeConflict, errAgentOffline.Error(), nil)
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	objType := "config_version"
	reason := "deploy на инстанс " + inst.Name + " (задача " + taskID + ")"
	h.audit(r, identityFrom(r.Context()), "configs.deploy", &objType, &cv.ID, "success", reason)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"task_id": taskID, "config_version_id": cv.ID, "version": cv.Version,
		"instance_id": instID, "agent_id": agentID,
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

// getInstanceConfigHistory — GET /instances/{id}/config/history (config.read):
// последние применения конфигураций на инстансе (instance_config_history,
// чанк 57, план 1B; записи появляются по результатам задач deploy_config).
func (h *handlers) getInstanceConfigHistory(w http.ResponseWriter, r *http.Request) {
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
	items, err := h.d.Store.Configs.DeployHistory(r.Context(), inst.ID, 100)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

// deployConfigWaveInput — POST /config_versions/{id}/deploy_wave.
type deployConfigWaveInput struct {
	Targeting   targetingInput `json:"targeting"`
	BatchSize   int            `json:"batch_size"`
	Concurrency int            `json:"concurrency"`
	CanarySize  int            `json:"canary_size"`
}

// deployConfigWave — POST /config_versions/{id}/deploy_wave (config.write):
// волновой деплой версии конфигурации через оркестратор (чанк 68,
// план 1B; kind='config', миграция 000013). Как и у rules: canary —
// первая волна, провал волны → auto-pause. Desired_state не пишется
// (правила не затрагиваются).
func (h *handlers) deployConfigWave(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	var in deployConfigWaveInput
	if !decodeJSON(w, r, &in) {
		return
	}
	fe := fieldErrors{}
	switch in.Targeting.Mode {
	case "all_clusters", "selected_clusters", "all_except_clusters", "specific_hosts", "specific_instances":
	case "":
		fe.add("targeting.mode", "обязательное поле")
	default:
		fe.add("targeting.mode", "допустимы: all_clusters, selected_clusters, all_except_clusters, specific_hosts, specific_instances")
	}
	if in.BatchSize < 0 || in.Concurrency < 0 || in.CanarySize < 0 {
		fe.add("batch_size/concurrency/canary_size", "неотрицательные значения")
	}
	if fe.any() {
		writeValidation(w, fe)
		return
	}
	if in.BatchSize == 0 {
		in.BatchSize = 50
	}
	if in.Concurrency == 0 {
		in.Concurrency = 10
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

	instanceIDs, ok := h.resolveTargets(w, r, orgID, in.Targeting)
	if !ok {
		return
	}
	instanceIDs, ok = h.scopeTargets(w, r, instanceIDs, in.Targeting)
	if !ok {
		return
	}
	if len(instanceIDs) == 0 {
		writeError(w, http.StatusBadRequest, CodeValidation,
			"таргетинг не выбрал ни одного инстанса", nil)
		return
	}

	targetingRaw, _ := json.Marshal(in.Targeting)
	d := store.Deployment{
		OrganizationID:  orgID,
		Kind:            "config",
		ConfigVersionID: &cv.ID,
		Targeting:       targetingRaw,
		BatchSize:       in.BatchSize,
		Concurrency:     in.Concurrency,
		CanarySize:      in.CanarySize,
	}
	waves := orchestrator.ComputeWaves(instanceIDs, in.CanarySize, in.BatchSize)
	created, err := h.d.Store.Deployments.Create(r.Context(), d, waves)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	h.d.Orch.Start(created.ID)
	objType := "config_version"
	h.audit(r, identityFrom(r.Context()), "configs.deploy_wave", &objType, &cv.ID, "success",
		"волновой деплой "+cv.Version+": "+strconv.Itoa(len(instanceIDs))+" инстансов")
	progress, _ := h.d.Store.Deployments.GetProgress(r.Context(), created.ID)
	writeJSON(w, http.StatusCreated, deploymentView{created, progress})
}
