// packages.go — управление пакетами suricata/suricata-update на сенсоре
// (чанк 111, backlog заказчика 2026-10-11): POST /instances/{id}/packages
// {package, action} — синхронная PackageTask агенту (apt/dpkg; на агенте
// gated capability packages, белый список пакетов). Ответ — фактическое
// состояние пакета после действия (dpkg-query). Аудит
// instances.package_action.
package httpapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
)

// packagesInput — POST /instances/{id}/packages.
type packagesInput struct {
	Package string `json:"package"` // suricata | suricata-update
	Action  string `json:"action"`  // check | install | remove | update
}

// packagesTimeout — синхронное ожидание агента: apt-get на сенсоре может
// качать пакеты минуты (как updateRunTimeout у suricata-update — 10 мин).
const packagesTimeout = 10 * time.Minute

// packagesAction — POST /instances/{id}/packages (hosts.write).
func (h *handlers) packagesAction(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	var in packagesInput
	if !decodeJSON(w, r, &in) {
		return
	}
	action, ok := protoPackageAction(in.Action)
	if !ok {
		writeValidation(w, fieldErrors{"action": "check | install | remove | update"})
		return
	}
	if in.Package != "suricata" && in.Package != "suricata-update" {
		writeValidation(w, fieldErrors{"package": "suricata | suricata-update"})
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
		Type: &agentv1.Task_Packages{Packages: &agentv1.PackageTask{
			Package: in.Package,
			Action:  action,
		}},
	}
	res, ok := h.d.Hub.SendTaskAndWait(r.Context(), agent.ID, task, packagesTimeout)
	if !ok {
		writeError(w, http.StatusConflict, CodeConflict,
			"агент инстанса не подключён (offline) или не ответил вовремя", nil)
		return
	}
	objType := "instance"
	reason := in.Action + " пакета " + in.Package + " на инстансе " + inst.Name
	if res.GetStatus() != agentv1.TaskStatus_TASK_STATUS_SUCCESS {
		msg := res.GetError()
		if msg == "" {
			msg = "агент не смог выполнить действие над пакетом"
		}
		h.audit(r, identityFrom(r.Context()), "instances.package_action", &objType, &inst.ID, "error", reason+": "+msg)
		writeError(w, http.StatusBadGateway, CodeInternal, msg, nil)
		return
	}
	h.audit(r, identityFrom(r.Context()), "instances.package_action", &objType, &inst.ID, "success", reason)
	pkg := res.GetPackages()
	writeJSON(w, http.StatusOK, map[string]any{
		"task_id":     task.TaskId,
		"instance_id": inst.ID,
		"package":     pkg.GetPackage(),
		"action":      pkg.GetAction(),
		"installed":   pkg.GetInstalled(),
		"version":     pkg.GetVersion(),
		"output":      pkg.GetOutput(),
	})
}

// protoPackageAction — маппинг строки API → proto-enum.
func protoPackageAction(s string) (agentv1.PackageAction, bool) {
	switch s {
	case "check":
		return agentv1.PackageAction_PACKAGE_ACTION_CHECK, true
	case "install":
		return agentv1.PackageAction_PACKAGE_ACTION_INSTALL, true
	case "remove":
		return agentv1.PackageAction_PACKAGE_ACTION_REMOVE, true
	case "update":
		return agentv1.PackageAction_PACKAGE_ACTION_UPDATE, true
	default:
		return agentv1.PackageAction_PACKAGE_ACTION_UNSPECIFIED, false
	}
}
