// update_status.go — статус suricata-update на хосте инстанса (чанк 112,
// backlog заказчика): GET /instances/{id}/suricata_update/status (rules.read)
// — свежий блок из heartbeat агента (Redis agent_upd:{agent_id}, TTL 120 с):
// идёт ли обновление сейчас и PID процесса, итог/время последнего прогона
// (агент трекает свои запуски), версия/время индекса источников; плюс PID и
// состояние движка Suricata (heartbeat чанка 80). available=false — агент
// offline или старше чанка 112 (блока нет в heartbeat).
package httpapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// suricataUpdateStatus — GET /instances/{id}/suricata_update/status.
func (h *handlers) suricataUpdateStatus(w http.ResponseWriter, r *http.Request) {
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
	out := map[string]any{
		"instance_id": id,
		"agent_id":    agent.ID,
		"available":   false,
	}
	if st, ok := h.d.Hub.AgentUpdateStatus(r.Context(), agent.ID); ok {
		out["available"] = true
		out["running"] = st.GetRunning()
		out["pid"] = st.GetPid()
		out["last_result"] = st.GetLastResult()
		if t := st.GetLastRunAt(); t != nil {
			out["last_run_at"] = t.AsTime().UTC().Format(time.RFC3339)
		}
		out["last_error"] = st.GetLastError()
		out["index_version"] = st.GetIndexVersion()
		if t := st.GetIndexUpdatedAt(); t != nil {
			out["index_updated_at"] = t.AsTime().UTC().Format(time.RFC3339)
		}
	}
	// PID и состояние движка Suricata (heartbeat чанка 80).
	if state, pid, ok := h.d.Hub.InstanceServiceState(r.Context(), id); ok {
		out["suricata_state"] = state
		out["suricata_pid"] = pid
	}
	writeJSON(w, http.StatusOK, out)
}
