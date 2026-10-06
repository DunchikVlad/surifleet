package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/surifleet/surifleet/internal/orchestrator"
	"github.com/surifleet/surifleet/internal/store"
)

// deploymentInput — POST /api/v1/deployments (openapi DeploymentInput).
type deploymentInput struct {
	RulesetID        string         `json:"ruleset_id"`
	DeployTemplateID *string        `json:"deploy_template_id"`
	Targeting        targetingInput `json:"targeting"`
	BatchSize        int            `json:"batch_size"`
	Concurrency      int            `json:"concurrency"`
	CanarySize       int            `json:"canary_size"`
}

// targetingInput — openapi Targeting: mode + списки id.
type targetingInput struct {
	Mode        string   `json:"mode"`
	ClusterIDs  []string `json:"cluster_ids"`
	HostIDs     []string `json:"host_ids"`
	InstanceIDs []string `json:"instance_ids"`
}

// deploymentView — Deployment + progress (openapi Deployment).
type deploymentView struct {
	store.Deployment
	Progress store.DeploymentProgress `json:"progress"`
}

// deploymentDetail — GET /deployments/{id}: деплой + progress + события.
type deploymentDetail struct {
	deploymentView
	Events []store.DeployEvent `json:"events"`
}

// createDeployment — POST /api/v1/deployments: резолв таргетинга в инстансы,
// раскладка по волнам, создание деплоя + desired_state, запуск оркестратора.
func (h *handlers) createDeployment(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	var in deploymentInput
	if !decodeJSON(w, r, &in) {
		return
	}

	fe := fieldErrors{}
	rulesetID, err := uuid.Parse(in.RulesetID)
	if err != nil {
		fe.add("ruleset_id", "обязательный UUID")
	}
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
	var templateID *uuid.UUID
	if in.DeployTemplateID != nil {
		tid, terr := uuid.Parse(*in.DeployTemplateID)
		if terr != nil {
			fe.add("deploy_template_id", "некорректный UUID")
		} else {
			templateID = &tid
		}
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
	// Дефолт openapi canary_size=1; явный 0 отличить от «не задан» нельзя —
	// считаем 0 валидным выбором «без canary» (см. отчёт, решения).

	rv, err := h.d.Store.Rulesets.Get(r.Context(), rulesetID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if rv.OrganizationID != orgID {
		writeError(w, http.StatusNotFound, CodeNotFound, "ruleset не найден в организации", nil)
		return
	}

	instanceIDs, ok := h.resolveTargets(w, r, orgID, in.Targeting)
	if !ok {
		return
	}
	// Scoping таргетинга (чанк 46, п. 8): cluster-restricted пользователь
	// не может деплоить на чужие кластеры — цели пересекаются с его scope.
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
		OrganizationID:   orgID,
		RulesetVersionID: rv.ID,
		DeployTemplateID: templateID,
		Targeting:        targetingRaw,
		BatchSize:        in.BatchSize,
		Concurrency:      in.Concurrency,
		CanarySize:       in.CanarySize,
	}
	waves := orchestrator.ComputeWaves(instanceIDs, in.CanarySize, in.BatchSize)
	created, err := h.d.Store.Deployments.Create(r.Context(), d, waves)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	// Целевое состояние инстансов — рассчитанный набор правил ruleset'а.
	computed := computedRulesFromManifest(rv.Manifest)
	for _, id := range instanceIDs {
		if err := h.d.Store.DesiredState.Upsert(r.Context(), id, rv.ID, computed); err != nil {
			errLog.Error("desired_state upsert", "instance_id", id, "err", err)
		}
	}

	h.d.Orch.Start(created.ID)

	progress, _ := h.d.Store.Deployments.GetProgress(r.Context(), created.ID)
	writeJSON(w, http.StatusCreated, deploymentView{created, progress})
}

// resolveTargets — targeting.mode → список id инстансов.
func (h *handlers) resolveTargets(w http.ResponseWriter, r *http.Request, orgID uuid.UUID, t targetingInput) ([]uuid.UUID, bool) {
	badUUID := func(val string) ([]uuid.UUID, bool) {
		writeError(w, http.StatusBadRequest, CodeValidation,
			"некорректный UUID в targeting", map[string]any{"value": val})
		return nil, false
	}
	switch t.Mode {
	case "all_clusters":
		ids, err := h.d.Store.Instances.IDsForOrg(r.Context(), orgID, nil)
		if err != nil {
			writeStoreError(w, err)
			return nil, false
		}
		return ids, true
	case "all_except_clusters":
		excl, bad := uuidStrings(t.ClusterIDs)
		if bad != "" {
			return badUUID(bad)
		}
		ids, err := h.d.Store.Instances.IDsForOrg(r.Context(), orgID, excl)
		if err != nil {
			writeStoreError(w, err)
			return nil, false
		}
		return ids, true
	case "selected_clusters":
		cids, bad := uuidStrings(t.ClusterIDs)
		if bad != "" {
			return badUUID(bad)
		}
		ids, err := h.d.Store.Instances.IDsForClusters(r.Context(), cids)
		if err != nil {
			writeStoreError(w, err)
			return nil, false
		}
		return ids, true
	case "specific_hosts":
		hids, bad := uuidStrings(t.HostIDs)
		if bad != "" {
			return badUUID(bad)
		}
		ids, err := h.d.Store.Instances.IDsForHosts(r.Context(), hids)
		if err != nil {
			writeStoreError(w, err)
			return nil, false
		}
		return ids, true
	case "specific_instances":
		iids, bad := uuidStrings(t.InstanceIDs)
		if bad != "" {
			return badUUID(bad)
		}
		ids, err := h.d.Store.Instances.ExistingIDs(r.Context(), orgID, iids)
		if err != nil {
			writeStoreError(w, err)
			return nil, false
		}
		if len(ids) != len(iids) {
			writeError(w, http.StatusBadRequest, CodeValidation,
				"часть instance_ids не существует в организации",
				map[string]any{"requested": len(iids), "found": len(ids)})
			return nil, false
		}
		return ids, true
	}
	writeError(w, http.StatusBadRequest, CodeValidation, "неизвестный targeting.mode", nil)
	return nil, false
}

// scopeTargets — scoping таргетинга деплоя (чанк 46, п. 8): для
// cluster-restricted пользователя цели деплоя пересекаются с его кластерами.
// Явные списки (selected_clusters/specific_hosts/specific_instances) —
// проверяются по одному: чужой id → 404 (объект вне scope неотличим от
// несуществующего). Режимы all_clusters/all_except_clusters — молча
// сужаются до разрешённых кластеров.
func (h *handlers) scopeTargets(w http.ResponseWriter, r *http.Request, ids []uuid.UUID, t targetingInput) ([]uuid.UUID, bool) {
	id := identityFrom(r.Context())
	if id == nil || !id.ScopeRestricted {
		return ids, true
	}
	// Явные id: проверка членства в scope (чужой → 404).
	check := func(val string, clusterOf func(uuid.UUID) bool) ([]uuid.UUID, bool) {
		u, err := uuid.Parse(val)
		if err != nil {
			return nil, true // битый UUID уже отловлен в resolveTargets
		}
		if !clusterOf(u) {
			writeError(w, http.StatusNotFound, CodeNotFound, "ресурс не найден", nil)
			return nil, false
		}
		return nil, true
	}
	for _, cs := range t.ClusterIDs {
		if out, ok := check(cs, id.ClusterScopeAllowed); !ok {
			return out, false
		}
	}
	for _, hs := range t.HostIDs {
		if out, ok := check(hs, func(hid uuid.UUID) bool {
			host, err := h.d.Store.Hosts.Get(r.Context(), hid)
			return err == nil && id.ClusterScopeAllowed(host.ClusterID)
		}); !ok {
			return out, false
		}
	}
	for _, is := range t.InstanceIDs {
		if out, ok := check(is, func(iid uuid.UUID) bool {
			inst, err := h.d.Store.Instances.Get(r.Context(), iid)
			if err != nil {
				return false
			}
			host, err := h.d.Store.Hosts.Get(r.Context(), inst.HostID)
			return err == nil && id.ClusterScopeAllowed(host.ClusterID)
		}); !ok {
			return out, false
		}
	}
	// Молчаливое сужение результата до разрешённых кластеров (режимы
	// all_clusters / all_except_clusters — ids уже разрешёны из БД без
	// учёта scope): оставляем только инстансы хостов разрешённых кластеров.
	if t.Mode == "all_clusters" || t.Mode == "all_except_clusters" {
		scoped, err := h.d.Store.Instances.IDsForClusters(r.Context(), id.ScopeClusters)
		if err != nil {
			writeStoreError(w, err)
			return nil, false
		}
		return intersectIDs(ids, scoped), true
	}
	return ids, true
}

// intersectIDs — пересечение списка целей с разрешённым множеством (порядок
// сохраняется). Чистая функция для теста (чанк 46).
func intersectIDs(ids, allowed []uuid.UUID) []uuid.UUID {
	set := make(map[uuid.UUID]bool, len(allowed))
	for _, a := range allowed {
		set[a] = true
	}
	out := make([]uuid.UUID, 0, len(ids))
	for _, i := range ids {
		if set[i] {
			out = append(out, i)
		}
	}
	return out
}

// listDeployments — GET /api/v1/deployments?status=...: keyset-листинг.
func (h *handlers) listDeployments(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	cursor, limit, ok := parsePage(w, r)
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	items, next, err := h.d.Store.Deployments.List(r.Context(), orgID, status, cursor, limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	views := make([]deploymentView, 0, len(items))
	for _, d := range items {
		progress, err := h.d.Store.Deployments.GetProgress(r.Context(), d.ID)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		views = append(views, deploymentView{d, progress})
	}
	writeJSON(w, http.StatusOK, page[deploymentView]{Items: views, NextCursor: next})
}

// getDeployment — GET /api/v1/deployments/{id}: деплой + progress + события.
func (h *handlers) getDeployment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	d, err := h.d.Store.Deployments.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	progress, err := h.d.Store.Deployments.GetProgress(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	events, err := h.d.Store.Deployments.EventsByDeployment(r.Context(), id, 50)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, deploymentDetail{deploymentView{d, progress}, events})
}

// listDeploymentTasks — GET /api/v1/deployments/{id}/tasks?status=&wave=.
func (h *handlers) listDeploymentTasks(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	cursor, limit, ok := parsePage(w, r)
	if !ok {
		return
	}
	var wave *int
	if s := r.URL.Query().Get("wave"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, CodeValidation,
				"wave должен быть неотрицательным целым", map[string]any{"wave": s})
			return
		}
		wave = &n
	}
	items, next, err := h.d.Store.Deployments.ListTasks(r.Context(), id, r.URL.Query().Get("status"), wave, cursor, limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page[store.DeploymentTask]{Items: items, NextCursor: next})
}

// pauseDeployment — POST /api/v1/deployments/{id}/pause.
func (h *handlers) pauseDeployment(w http.ResponseWriter, r *http.Request) {
	h.setDeploymentStatus(w, r, "paused", "running")
}

// resumeDeployment — POST /api/v1/deployments/{id}/resume: failed-задачи
// возвращаются в pending (волна прогоняется заново), деплой продолжается.
func (h *handlers) resumeDeployment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	d, err := h.d.Store.Deployments.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Resume допустим из paused (оператор снял паузу) и из финального
	// failed (повторный прогон: failed-задачи → pending ниже). Из
	// completed/cancelled повтор запрещён — создавайте новый деплой.
	if d.Status != "paused" && d.Status != "failed" {
		writeError(w, http.StatusConflict, CodeConflict,
			"resume возможен только из paused или failed (текущий: "+d.Status+")", nil)
		return
	}
	if _, err := h.d.Store.Deployments.RetryFailedTasks(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	if err := h.d.Store.Deployments.SetStatus(r.Context(), id, "running"); err != nil {
		writeStoreError(w, err)
		return
	}
	h.deploymentEvent(r, id, "resumed", "деплой возобновлён (failed-задачи → pending)")
	h.d.Orch.Start(id)
	h.getDeployment(w, r)
}

// cancelDeployment — POST /api/v1/deployments/{id}/cancel.
func (h *handlers) cancelDeployment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	d, err := h.d.Store.Deployments.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	switch d.Status {
	case "completed", "failed", "cancelled":
		writeError(w, http.StatusConflict, CodeConflict,
			"деплой уже завершён (статус: "+d.Status+")", nil)
		return
	}
	if err := h.d.Store.Deployments.SetStatus(r.Context(), id, "cancelled"); err != nil {
		writeStoreError(w, err)
		return
	}
	if err := h.d.Store.Deployments.CancelPendingTasks(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	h.deploymentEvent(r, id, "cancelled", "деплой отменён через API")
	h.getDeployment(w, r)
}

// setDeploymentStatus — общий переход статуса (pause) с проверкой исходного.
func (h *handlers) setDeploymentStatus(w http.ResponseWriter, r *http.Request, target, from string) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	d, err := h.d.Store.Deployments.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if d.Status != from {
		writeError(w, http.StatusConflict, CodeConflict,
			target+" возможен только из "+from+" (текущий: "+d.Status+")", nil)
		return
	}
	if err := h.d.Store.Deployments.SetStatus(r.Context(), id, target); err != nil {
		writeStoreError(w, err)
		return
	}
	h.deploymentEvent(r, id, target, "деплой переведён в "+target+" через API")
	h.getDeployment(w, r)
}

// deploymentEvent — событие деплоя из API-слоя (best-effort).
func (h *handlers) deploymentEvent(r *http.Request, id uuid.UUID, typ, msg string) {
	if err := h.d.Store.Deployments.EventAdd(r.Context(), store.DeployEvent{
		DeploymentID: id,
		EventType:    typ,
		Message:      &msg,
	}); err != nil {
		errLog.Error("событие деплоя", "deployment_id", id, "type", typ, "err", err)
	}
}
