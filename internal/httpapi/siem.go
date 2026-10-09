// SIEM-конфигурация пересылки EVE-алертов с сервера (чанк 89, пр. 2
// «SIEM-конфиг через ConfigPush»; п. 5.4 ТЗ): GET/PUT/DELETE
// /hosts/{id}/siem и /clusters/{id}/siem. Резолв host → cluster (как
// capabilities); нет записей — сервер SIEM не задаёт (агент на
// agent.yaml). После PUT конфигурация пушится подключённым агентам
// (ConfigPush через hub); офлайн-агенты получают её в HelloAck.
package httpapi

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
	"github.com/surifleet/surifleet/internal/store"
)

// siemConfigInput — PUT /hosts|clusters/{id}/siem.
type siemConfigInput struct {
	Addr     string `json:"addr"`     // host:514; пустой — явное выключение пересылки
	Protocol string `json:"protocol"` // udp|tcp (пусто — udp)
	Format   string `json:"format"`   // cef|json (пусто — cef)
}

func (in *siemConfigInput) validate() fieldErrors {
	var fe fieldErrors
	in.Addr = strings.TrimSpace(in.Addr)
	if in.Addr != "" {
		if _, _, err := net.SplitHostPort(in.Addr); err != nil {
			fe["addr"] = "ожидается host:port (например, siem.local:514)"
		}
	}
	in.Protocol = strings.ToLower(strings.TrimSpace(in.Protocol))
	if in.Protocol == "" {
		in.Protocol = "udp"
	}
	if in.Protocol != "udp" && in.Protocol != "tcp" {
		fe["protocol"] = "udp | tcp"
	}
	in.Format = strings.ToLower(strings.TrimSpace(in.Format))
	if in.Format == "" {
		in.Format = "cef"
	}
	if in.Format != "cef" && in.Format != "json" {
		fe["format"] = "cef | json"
	}
	return fe
}

// siemView — ответ GET: собственная запись scope + эффективная
// (унаследованная) конфигурация.
type siemView struct {
	Scope     string            `json:"scope"` // host | cluster
	Own       *store.SiemConfig `json:"own"`   // запись этого scope (null — нет)
	Effective *store.SiemConfig `json:"effective"`
	// Source — откуда effective: host | cluster | none.
	Source string `json:"source"`
}

// getHostSiem — GET /hosts/{id}/siem (hosts.read).
func (h *handlers) getHostSiem(w http.ResponseWriter, r *http.Request) {
	host, ok := h.hostScoped(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	view := siemView{Scope: "host", Source: "none"}
	if own, err := h.d.Store.SiemConfigs.GetHost(ctx, host.ID); err == nil {
		view.Own = &own
	} else if !errors.Is(err, store.ErrNotFound) {
		writeStoreError(w, err)
		return
	}
	if eff, err := h.d.Store.SiemConfigs.ForHost(ctx, host.ID, host.ClusterID); err == nil {
		view.Effective = &eff
		if eff.HostID != nil {
			view.Source = "host"
		} else {
			view.Source = "cluster"
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// putHostSiem — PUT /hosts/{id}/siem (hosts.write): upsert host-level
// записи + мгновенный push подключённому агенту (ConfigPush). Аудит.
func (h *handlers) putHostSiem(w http.ResponseWriter, r *http.Request) {
	host, ok := h.hostScoped(w, r)
	if !ok {
		return
	}
	var in siemConfigInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if fe := in.validate(); len(fe) > 0 {
		writeValidation(w, fe)
		return
	}
	var userID *uuid.UUID
	if id := identityFrom(r.Context()); id != nil {
		userID = &id.UserID
	}
	c, err := h.d.Store.SiemConfigs.SetHost(r.Context(), host.ID, in.Addr, in.Protocol, in.Format, userID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	pushed := h.pushSiemToHost(r, host.ID)
	objType := "host"
	h.audit(r, identityFrom(r.Context()), "hosts.siem_set", &objType, &host.ID, "success",
		"SIEM "+in.Addr+" ("+in.Protocol+"/"+in.Format+")")
	writeJSON(w, http.StatusOK, map[string]any{"config": c, "pushed": pushed})
}

// deleteHostSiem — DELETE /hosts/{id}/siem (hosts.write): удалить
// host-level запись (хост возвращается к кластерной/локальной) + push
// эффективной конфигурации агенту. 404, если записи не было.
func (h *handlers) deleteHostSiem(w http.ResponseWriter, r *http.Request) {
	host, ok := h.hostScoped(w, r)
	if !ok {
		return
	}
	if err := h.d.Store.SiemConfigs.DeleteHost(r.Context(), host.ID); err != nil {
		writeStoreError(w, err)
		return
	}
	pushed := h.pushSiemToHost(r, host.ID)
	objType := "host"
	h.audit(r, identityFrom(r.Context()), "hosts.siem_delete", &objType, &host.ID, "success",
		"host-level SIEM-конфиг удалён")
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "pushed": pushed})
}

// getClusterSiem — GET /clusters/{id}/siem (hosts.read).
func (h *handlers) getClusterSiem(w http.ResponseWriter, r *http.Request) {
	cluster, ok := h.clusterScoped(w, r)
	if !ok {
		return
	}
	view := siemView{Scope: "cluster", Source: "none"}
	if own, err := h.d.Store.SiemConfigs.GetCluster(r.Context(), cluster.ID); err == nil {
		view.Own = &own
		view.Effective = &own
		view.Source = "cluster"
	} else if !errors.Is(err, store.ErrNotFound) {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// putClusterSiem — PUT /clusters/{id}/siem (hosts.write): upsert
// cluster-level записи + push всем подключённым агентам кластера,
// у которых нет host-level записи (host-level приоритетнее). Аудит.
func (h *handlers) putClusterSiem(w http.ResponseWriter, r *http.Request) {
	cluster, ok := h.clusterScoped(w, r)
	if !ok {
		return
	}
	var in siemConfigInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if fe := in.validate(); len(fe) > 0 {
		writeValidation(w, fe)
		return
	}
	var userID *uuid.UUID
	if id := identityFrom(r.Context()); id != nil {
		userID = &id.UserID
	}
	c, err := h.d.Store.SiemConfigs.SetCluster(r.Context(), cluster.ID, in.Addr, in.Protocol, in.Format, userID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	pushed := h.pushSiemToCluster(r, cluster.ID)
	objType := "cluster"
	h.audit(r, identityFrom(r.Context()), "clusters.siem_set", &objType, &cluster.ID, "success",
		"SIEM "+in.Addr+" ("+in.Protocol+"/"+in.Format+") для кластера, агентов с push: "+strconv.Itoa(pushed))
	writeJSON(w, http.StatusOK, map[string]any{"config": c, "pushed": pushed})
}

// deleteClusterSiem — DELETE /clusters/{id}/siem (hosts.write): удалить
// cluster-level запись + push эффективной конфигурации агентам кластера.
func (h *handlers) deleteClusterSiem(w http.ResponseWriter, r *http.Request) {
	cluster, ok := h.clusterScoped(w, r)
	if !ok {
		return
	}
	if err := h.d.Store.SiemConfigs.DeleteCluster(r.Context(), cluster.ID); err != nil {
		writeStoreError(w, err)
		return
	}
	pushed := h.pushSiemToCluster(r, cluster.ID)
	objType := "cluster"
	h.audit(r, identityFrom(r.Context()), "clusters.siem_delete", &objType, &cluster.ID, "success",
		"cluster-level SIEM-конфиг удалён, агентов с push: "+strconv.Itoa(pushed))
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "pushed": pushed})
}

// pushSiemToHost — ConfigPush эффективной SIEM-конфигурации агенту хоста
// (если он online и если конфигурация задана сервером; нет записей — агент
// остаётся на agent.yaml, push не нужен). Возвращает true, если сообщение
// поставлено в очередь стрима.
func (h *handlers) pushSiemToHost(r *http.Request, hostID uuid.UUID) bool {
	if h.d.Hub == nil {
		return false
	}
	ctx := r.Context()
	host, err := h.d.Store.Hosts.Get(ctx, hostID)
	if err != nil {
		return false
	}
	eff, err := h.d.Store.SiemConfigs.ForHost(ctx, host.ID, host.ClusterID)
	if errors.Is(err, store.ErrNotFound) {
		return false // сервер SIEM не задаёт — агент на agent.yaml
	}
	if err != nil {
		errLog.Error("siem push: резолв конфигурации", "host_id", hostID, "err", err)
		return false
	}
	agent, err := h.d.Store.Agents.GetByHostID(ctx, nil, hostID)
	if err != nil {
		return false // агента нет — получит конфиг при enrollment/reconnect
	}
	return h.d.Hub.PushSiemConfig(agent.ID, &agentv1.SiemConfig{
		Addr: eff.Addr, Protocol: eff.Protocol, Format: eff.Format,
	})
}

// pushSiemToCluster — ConfigPush эффективной конфигурации всем агентам
// кластера (host-level записи не перетираются: ForHost их резолвит).
// Возвращает число агентов, которым сообщение поставлено в очередь.
func (h *handlers) pushSiemToCluster(r *http.Request, clusterID uuid.UUID) int {
	if h.d.Hub == nil {
		return 0
	}
	ctx := r.Context()
	hostIDs, err := h.d.Store.SiemConfigs.HostIDsForCluster(ctx, clusterID)
	if err != nil {
		errLog.Error("siem push: хосты кластера", "cluster_id", clusterID, "err", err)
		return 0
	}
	pushed := 0
	for _, hostID := range hostIDs {
		if h.pushSiemToHost(r, hostID) {
			pushed++
		}
	}
	return pushed
}

// hostScoped — хост по {id} с проверкой scoping (кластер хоста вне
// scope → 404, чанк 43).
func (h *handlers) hostScoped(w http.ResponseWriter, r *http.Request) (store.Host, bool) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return store.Host{}, false
	}
	host, err := h.d.Store.Hosts.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return store.Host{}, false
	}
	if !h.clusterAllowed(w, r, host.ClusterID) {
		return store.Host{}, false
	}
	return host, true
}

// clusterScoped — кластер по {id} с проверкой scoping (чанк 43).
func (h *handlers) clusterScoped(w http.ResponseWriter, r *http.Request) (store.Cluster, bool) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return store.Cluster{}, false
	}
	cluster, err := h.d.Store.Clusters.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return store.Cluster{}, false
	}
	if !h.clusterAllowed(w, r, cluster.ID) {
		return store.Cluster{}, false
	}
	return cluster, true
}
