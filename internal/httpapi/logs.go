package httpapi

// Логи агентов (chunk 13c): список агентов для UI-селектора и чтение
// операционных логов агента из ClickHouse (surifleet.agent_logs).

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/surifleet/surifleet/internal/chlogs"
)

// listAgents — GET /api/v1/agents: все агенты (с hostname), свежие первыми.
// Нужен UI для выбора агента во вкладке «Логи».
func (h *handlers) listAgents(w http.ResponseWriter, r *http.Request) {
	items, err := h.d.Store.Agents.List(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// agentLogEntry — запись лога агента в ответе API (ts — RFC3339 UTC).
type agentLogEntry struct {
	AgentID    string `json:"agent_id"`
	InstanceID string `json:"instance_id"`
	Ts         string `json:"ts"`
	Level      string `json:"level"`
	Message    string `json:"message"`
}

// getAgentLogs — GET /api/v1/agents/{id}/logs[?limit=200]:
// последние записи лога агента из ClickHouse (ts DESC, limit ≤ 1000).
func (h *handlers) getAgentLogs(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	limit := 200
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 || n > 1000 {
			fe := fieldErrors{}
			fe.add("limit", "целое число 1..1000")
			writeValidation(w, fe)
			return
		}
		limit = n
	}
	if h.d.CHLogs == nil {
		writeError(w, http.StatusServiceUnavailable, CodeInternal,
			"ClickHouse не настроен (server.clickhouse_dsn)", nil)
		return
	}
	rows, err := h.d.CHLogs.AgentLogs(r.Context(), id.String(), limit)
	if err != nil {
		errLog.Error("agent logs: запрос ClickHouse", "agent_id", id, "err", err)
		writeError(w, http.StatusBadGateway, CodeInternal, "ошибка запроса к ClickHouse", nil)
		return
	}
	items := make([]agentLogEntry, 0, len(rows))
	for _, row := range rows {
		ts := row.Ts
		if t, err := chlogs.ParseTS(row.Ts); err == nil {
			ts = t.UTC().Format(time.RFC3339Nano)
		}
		items = append(items, agentLogEntry{
			AgentID:    row.AgentID,
			InstanceID: row.InstanceID,
			Ts:         ts,
			Level:      row.Level,
			Message:    row.Message,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// getAgentMetrics — GET /api/v1/agents/{id}/metrics[?minutes=60&names=a,b]
// (чанк 33): ряды метрик агента из ClickHouse, сгруппированные по имени
// (ts ASC). Ответ: {"series": {name: [{ts, value}, ...]}}.
func (h *handlers) getAgentMetrics(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	minutes := 60
	if s := r.URL.Query().Get("minutes"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 || n > 24*60 {
			writeValidation(w, fieldErrors{"minutes": "целое число 1..1440"})
			return
		}
		minutes = n
	}
	var names []string
	if s := r.URL.Query().Get("names"); s != "" {
		for _, n := range strings.Split(s, ",") {
			if n = strings.TrimSpace(n); n != "" {
				names = append(names, n)
			}
		}
	}
	if h.d.CHLogs == nil {
		writeError(w, http.StatusServiceUnavailable, CodeInternal,
			"ClickHouse не настроен (server.clickhouse_dsn)", nil)
		return
	}
	rows, err := h.d.CHLogs.AgentMetrics(r.Context(), id.String(), minutes, names, 20000)
	if err != nil {
		errLog.Error("agent metrics: запрос ClickHouse", "agent_id", id, "err", err)
		writeError(w, http.StatusBadGateway, CodeInternal, "ошибка запроса к ClickHouse", nil)
		return
	}
	series := map[string][]map[string]any{}
	for _, row := range rows {
		ts := row.Ts
		if t, err := chlogs.ParseTS(row.Ts); err == nil {
			ts = t.UTC().Format(time.RFC3339Nano)
		}
		series[row.Name] = append(series[row.Name], map[string]any{"ts": ts, "value": row.Value})
	}
	writeJSON(w, http.StatusOK, map[string]any{"series": series, "minutes": minutes})
}
