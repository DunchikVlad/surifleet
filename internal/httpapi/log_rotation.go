// log_rotation.go — ротация логов Suricata инстанса (чанк 111, backlog
// заказчика 2026-10-11): POST /instances/{id}/log_rotation {action:
// report|rotate, min_size_kb?, keep?} — синхронная LogRotationTask агенту
// (copytruncate в log_dir инстанса; на агенте gated capability
// log_rotation). Ответ — состав файлов, что ротировано, архивы, освобождённый
// объём. Аудит instances.log_rotation.
package httpapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
)

// logRotationInput — POST /instances/{id}/log_rotation.
type logRotationInput struct {
	Action    string `json:"action"` // report | rotate
	MinSizeKB *int64 `json:"min_size_kb"`
	Keep      *int32 `json:"keep"`
}

// logRotationTimeout — синхронное ожидание агента: копирование больших
// eve.json занимает секунды, но на нагруженном сенсоре берём запас.
const logRotationTimeout = 180 * time.Second

// logRotation — POST /instances/{id}/log_rotation (hosts.write):
// отчёт/ротация логов Suricata инстанса через агента (capability
// log_rotation на агенте).
func (h *handlers) logRotation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	var in logRotationInput
	if !decodeJSON(w, r, &in) {
		return
	}
	reportOnly, ok := protoLogRotationAction(in.Action)
	if !ok {
		writeValidation(w, fieldErrors{"action": "report | rotate"})
		return
	}
	if in.MinSizeKB != nil && *in.MinSizeKB < 0 {
		writeValidation(w, fieldErrors{"min_size_kb": "неотрицательное число (0 — порог по умолчанию)"})
		return
	}
	if in.Keep != nil && *in.Keep < 0 {
		writeValidation(w, fieldErrors{"keep": "неотрицательное число (0 — по умолчанию 5)"})
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
	lr := &agentv1.LogRotationTask{InstanceId: inst.ID.String(), ReportOnly: reportOnly}
	if in.MinSizeKB != nil {
		lr.MinSizeKb = *in.MinSizeKB
	}
	if in.Keep != nil {
		lr.Keep = *in.Keep
	}
	task := &agentv1.Task{
		TaskId: uuid.New().String(),
		Type:   &agentv1.Task_LogRotation{LogRotation: lr},
	}
	res, ok := h.d.Hub.SendTaskAndWait(r.Context(), agent.ID, task, logRotationTimeout)
	if !ok {
		writeError(w, http.StatusConflict, CodeConflict,
			"агент инстанса не подключён (offline) или не ответил вовремя", nil)
		return
	}
	objType := "instance"
	reason := "ротация логов (" + in.Action + ") на инстансе " + inst.Name
	if res.GetStatus() != agentv1.TaskStatus_TASK_STATUS_SUCCESS {
		msg := res.GetError()
		if msg == "" {
			msg = "агент не смог выполнить ротацию логов"
		}
		h.audit(r, identityFrom(r.Context()), "instances.log_rotation", &objType, &inst.ID, "error", reason+": "+msg)
		writeError(w, http.StatusBadGateway, CodeInternal, msg, nil)
		return
	}
	h.audit(r, identityFrom(r.Context()), "instances.log_rotation", &objType, &inst.ID, "success", reason)
	rot := res.GetLogRotation()
	writeJSON(w, http.StatusOK, map[string]any{
		"task_id":       task.TaskId,
		"instance_id":   inst.ID,
		"action":        in.Action,
		"files":         rot.GetFiles(),
		"rotated_count": rot.GetRotatedCount(),
		"freed_bytes":   rot.GetFreedBytes(),
		"archived":      rot.GetArchived(),
	})
}

// protoLogRotationAction — маппинг строки API → report_only задачи.
func protoLogRotationAction(s string) (bool, bool) {
	switch s {
	case "report":
		return true, true
	case "rotate":
		return false, true
	default:
		return false, false
	}
}
