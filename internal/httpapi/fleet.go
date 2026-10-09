package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/surifleet/surifleet/internal/compliance"
	"github.com/surifleet/surifleet/internal/store"
)

// --- GET /api/v1/instances/{id}/state (openapi InstanceState) ---

type desiredView struct {
	RulesetVersionID uuid.UUID `json:"ruleset_version_id"`
	RulesetHash      string    `json:"ruleset_hash"`
	CalcVersion      int64     `json:"calc_version"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type reloadView struct {
	Action     string     `json:"action"`
	Success    bool       `json:"success"`
	Message    string     `json:"message,omitempty"`
	FinishedAt *time.Time `json:"finished_at"`
}

type actualView struct {
	RulesetHash *string                 `json:"ruleset_hash"`
	LoadedCount int                     `json:"loaded_count"`
	FailedCount int                     `json:"failed_count"`
	FailedRules []compliance.FailedRule `json:"failed_rules"`
	LastReload  *reloadView             `json:"last_reload"`
	ReportedAt  *time.Time              `json:"reported_at"`
}

type complianceView struct {
	Status    string          `json:"status"`
	Details   json.RawMessage `json:"details"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type diffView struct {
	MissingRules []int64                 `json:"missing_rules"`
	ExtraRules   []int64                 `json:"extra_rules"`
	FailedRules  []compliance.FailedRule `json:"failed_rules"`
}

type instanceStateView struct {
	InstanceID uuid.UUID       `json:"instance_id"`
	Desired    *desiredView    `json:"desired"`
	Actual     *actualView     `json:"actual"`
	Compliance *complianceView `json:"compliance"`
	Diff       *diffView       `json:"diff"`
}

// getInstanceState — GET /api/v1/instances/{id}/state: desired + actual +
// compliance + diff (missing/extra/failed из compliance.details).
func (h *handlers) getInstanceState(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	if _, err := h.d.Store.Instances.Get(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}

	view := instanceStateView{InstanceID: id}

	if d, err := h.d.Store.DesiredState.Get(r.Context(), id); err == nil {
		dv := desiredView{
			RulesetVersionID: d.RulesetVersionID,
			CalcVersion:      d.CalcVersion,
			UpdatedAt:        d.UpdatedAt,
		}
		if rv, err := h.d.Store.Rulesets.Get(r.Context(), d.RulesetVersionID); err == nil {
			dv.RulesetHash = rv.SHA256
		}
		view.Desired = &dv
	} else if !errors.Is(err, store.ErrNotFound) {
		writeStoreError(w, err)
		return
	}

	if a, err := h.d.Store.ActualState.Get(r.Context(), id); err == nil {
		av := actualView{
			RulesetHash: a.RulesetHash,
			ReportedAt:  a.ReportedAt,
		}
		var loaded []struct {
			Sid int64 `json:"sid"`
		}
		if err := json.Unmarshal(a.LoadedRules, &loaded); err == nil {
			av.LoadedCount = len(loaded)
		}
		if err := json.Unmarshal(a.FailedRules, &av.FailedRules); err == nil {
			av.FailedCount = len(av.FailedRules)
		}
		if a.LastReloadResult != nil {
			var lr struct {
				Action     int32      `json:"action"`
				Success    bool       `json:"success"`
				Message    string     `json:"message"`
				FinishedAt *time.Time `json:"finished_at"`
			}
			if err := json.Unmarshal(a.LastReloadResult, &lr); err == nil {
				action := "reload"
				if lr.Action == 2 {
					action = "restart"
				}
				av.LastReload = &reloadView{Action: action, Success: lr.Success, Message: lr.Message, FinishedAt: lr.FinishedAt}
			}
		}
		view.Actual = &av
	} else if !errors.Is(err, store.ErrNotFound) {
		writeStoreError(w, err)
		return
	}

	if c, err := h.d.Store.Compliance.Get(r.Context(), id); err == nil {
		view.Compliance = &complianceView{Status: c.Status, Details: c.Details, UpdatedAt: c.UpdatedAt}
		var det compliance.Details
		if err := json.Unmarshal(c.Details, &det); err == nil {
			view.Diff = &diffView{
				MissingRules: det.MissingRules,
				ExtraRules:   det.ExtraRules,
				FailedRules:  det.FailedRules,
			}
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		writeStoreError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, view)
}

// --- GET /api/v1/clusters/{id}/dashboard (дашборд кластера, чанк 87) ---

// clusterDashboardView — ответ GET /clusters/{id}/dashboard: те же
// блоки, что у дашборда флота, но в рамках кластера + построчная
// разбивка по хостам (агент/инстансы/compliance).
type clusterDashboardView struct {
	Cluster struct {
		ID   uuid.UUID `json:"id"`
		Name string    `json:"name"`
	} `json:"cluster"`
	Agents struct {
		Total    int            `json:"total"`
		ByStatus map[string]int `json:"by_status"`
	} `json:"agents"`
	Instances struct {
		Total    int            `json:"total"`
		ByStatus map[string]int `json:"by_status"`
	} `json:"instances"`
	Hosts []store.HostAgentStatus `json:"hosts"`
}

// getClusterDashboard — GET /api/v1/clusters/{id}/dashboard (fleet.read,
// чанк 87, план 1C срез 2): сводка по кластеру — агенты по статусам,
// инстансы по compliance, хосты с агентами.
func (h *handlers) getClusterDashboard(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	if !h.clusterAllowed(w, r, id) { // scoping: вне scope → 404 (чанк 43)
		return
	}
	cluster, err := h.d.Store.Clusters.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	ctx := r.Context()
	var view clusterDashboardView
	view.Cluster.ID = cluster.ID
	view.Cluster.Name = cluster.Name

	agentStatuses, err := h.d.Store.Agents.CountByStatusForCluster(ctx, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	view.Agents.ByStatus = agentStatuses
	for _, n := range agentStatuses {
		view.Agents.Total += n
	}

	summary, err := h.d.Store.Compliance.Summary(ctx, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	view.Instances.Total = summary.TotalInstances
	view.Instances.ByStatus = summary.ByStatus

	hosts, err := h.d.Store.Agents.HostsWithAgentStatus(ctx, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	view.Hosts = hosts

	writeJSON(w, http.StatusOK, view)
}

// --- GET /api/v1/fleet/dashboard (дашборд флота, чанк 86) ---

// fleetDashboardView — ответ GET /fleet/dashboard: сводка флота «одним
// экраном» — агенты по статусам, инстансы по compliance, активность
// деплоев за 24 ч, список offline-агентов (топ-10).
type fleetDashboardView struct {
	Agents struct {
		Total    int            `json:"total"`
		ByStatus map[string]int `json:"by_status"`
	} `json:"agents"`
	Instances struct {
		Total    int            `json:"total"`
		ByStatus map[string]int `json:"by_status"`
	} `json:"instances"`
	Deployments24h struct {
		Total    int            `json:"total"`
		ByStatus map[string]int `json:"by_status"`
	} `json:"deployments_24h"`
	OfflineAgents []store.OfflineAgentBrief `json:"offline_agents"`
}

// getFleetDashboard — GET /api/v1/fleet/dashboard (fleet.read, чанк 86,
// план 1C): агрегированная сводка флота. Первый срез дашбордов
// флот/кластер/хост (п. 5.4 ТЗ).
func (h *handlers) getFleetDashboard(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	var view fleetDashboardView

	agentStatuses, err := h.d.Store.Agents.CountByStatus(ctx)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	view.Agents.ByStatus = agentStatuses
	for _, n := range agentStatuses {
		view.Agents.Total += n
	}

	summary, err := h.d.Store.Compliance.Summary(ctx, uuid.Nil)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	view.Instances.Total = summary.TotalInstances
	view.Instances.ByStatus = summary.ByStatus

	depStatuses, err := h.d.Store.Deployments.CountByStatusSince(ctx, orgID, time.Now().Add(-24*time.Hour))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	view.Deployments24h.ByStatus = depStatuses
	for _, n := range depStatuses {
		view.Deployments24h.Total += n
	}

	offline, err := h.d.Store.Agents.ListOffline(ctx, 10)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	view.OfflineAgents = offline

	writeJSON(w, http.StatusOK, view)
}

// --- GET /api/v1/fleet/compliance (openapi FleetCompliance) ---

type fleetComplianceView struct {
	Summary    store.ComplianceSummary `json:"summary"`
	Items      []store.ComplianceItem  `json:"items,omitempty"`
	NextCursor *string                 `json:"next_cursor"`
}

// getFleetCompliance — GET /api/v1/fleet/compliance?status=&cluster_id=:
// сводка по статусам; при ?status= — drill-down страница инстансов.
func (h *handlers) getFleetCompliance(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := queryUUID(w, r, "cluster_id")
	if !ok {
		return
	}
	summary, err := h.d.Store.Compliance.Summary(r.Context(), clusterID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	view := fleetComplianceView{Summary: summary}

	status := r.URL.Query().Get("status")
	if status != "" {
		switch status {
		case compliance.InSync, compliance.Pending, compliance.Partial, compliance.Drift, compliance.Stale:
		default:
			writeError(w, http.StatusBadRequest, CodeValidation,
				"status должен быть одним из: in_sync, pending, partial, drift, stale",
				map[string]any{"status": status})
			return
		}
		cursor, limit, ok := parsePage(w, r)
		if !ok {
			return
		}
		items, next, err := h.d.Store.Compliance.ListByStatus(r.Context(), status, clusterID, cursor, limit)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		view.Items = items
		view.NextCursor = next
	}
	writeJSON(w, http.StatusOK, view)
}

// --- GET /api/v1/instances/{id}/deploy_history ---

// getDeployHistory — GET /api/v1/instances/{id}/deploy_history?limit=.
func (h *handlers) getDeployHistory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	if _, err := h.d.Store.Instances.Get(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	_, limit, ok := parsePage(w, r)
	if !ok {
		return
	}
	items, err := h.d.Store.Deployments.DeployHistory(r.Context(), id, limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
