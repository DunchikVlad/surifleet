// service_action.go — действия над сервисом Suricata инстанса (чанк 105,
// п. 7 ТЗ «действия из UI — перезапуск сервиса»): POST
// /instances/{id}/service_action {action} — синхронная ServiceActionTask
// агенту (reload/restart/start/stop systemd-юнита), ответ — фактическое
// состояние сервиса после действия.
package httpapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
)

// serviceActionInput — POST /instances/{id}/service_action.
type serviceActionInput struct {
	Action string `json:"action"` // reload | restart | start | stop
}

// serviceAction — POST /instances/{id}/service_action (hosts.write):
// действие над systemd-сервисом инстанса через агента. Синхронно
// (SendTaskAndWait): restart Suricata на стенде занимает десятки секунд
// (graceful stop ~55 с) — ждём до 150 с. На агенте действие gated
// capability service_mgmt.
func (h *handlers) serviceAction(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	var in serviceActionInput
	if !decodeJSON(w, r, &in) {
		return
	}
	action, ok := protoServiceAction(in.Action)
	if !ok {
		writeValidation(w, fieldErrors{"action": "reload | restart | start | stop"})
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
		Type: &agentv1.Task_ServiceAction{ServiceAction: &agentv1.ServiceActionTask{
			InstanceId: inst.ID.String(),
			Action:     action,
		}},
	}
	res, ok := h.d.Hub.SendTaskAndWait(r.Context(), agent.ID, task, 150*time.Second)
	if !ok {
		writeError(w, http.StatusConflict, CodeConflict,
			"агент инстанса не подключён (offline) или не ответил вовремя", nil)
		return
	}
	objType := "instance"
	reason := in.Action + " на инстансе " + inst.Name
	if res.GetStatus() != agentv1.TaskStatus_TASK_STATUS_SUCCESS {
		msg := res.GetError()
		if msg == "" {
			msg = "агент не смог выполнить действие"
		}
		h.audit(r, identityFrom(r.Context()), "instances.service_action", &objType, &inst.ID, "error", reason+": "+msg)
		writeError(w, http.StatusBadGateway, CodeInternal, msg, nil)
		return
	}
	h.audit(r, identityFrom(r.Context()), "instances.service_action", &objType, &inst.ID, "success", reason)
	sa := res.GetServiceAction()
	writeJSON(w, http.StatusOK, map[string]any{
		"task_id":       task.TaskId,
		"instance_id":   inst.ID,
		"action":        in.Action,
		"service_state": sa.GetServiceState(),
	})
}

// protoServiceAction — маппинг строки API → proto-enum.
func protoServiceAction(s string) (agentv1.ServiceAction, bool) {
	switch s {
	case "reload":
		return agentv1.ServiceAction_SERVICE_ACTION_RELOAD, true
	case "restart":
		return agentv1.ServiceAction_SERVICE_ACTION_RESTART, true
	case "start":
		return agentv1.ServiceAction_SERVICE_ACTION_START, true
	case "stop":
		return agentv1.ServiceAction_SERVICE_ACTION_STOP, true
	default:
		return agentv1.ServiceAction_SERVICE_ACTION_UNSPECIFIED, false
	}
}
