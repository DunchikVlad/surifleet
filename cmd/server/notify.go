package main

// Эмиттеры событий движка уведомлений (чанк 84) — доработки по замечаниям
// живого стенда (чанк 103, docs/known-issues.md):
//   - KI-2: recovery «агент снова онлайн» — только после зафиксированного
//     offline-эпизода (трекер; защита от флапов/reconnect-шторма);
//   - KI-3: hostname + IP агента в текстах событий по агентам;
//   - KI-4: уведомление о провале задачи деплоя (ruleset/инстанс/ошибка/
//     попытки/откат).
// Все отправки асинхронны (EmitAsync) — горячие пути (стримы) не блокируются.

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/surifleet/surifleet/internal/notify"
	"github.com/surifleet/surifleet/internal/store"
)

// agentIPResolver — последний известный IP агента (реестр хаба, чанк 78).
type agentIPResolver func(agentID uuid.UUID) (string, bool)

// resolveAgentEndpoint — hostname + IP агента для текстов уведомлений
// (KI-3): живой IP из реестра хаба, иначе первый адрес карточки хоста,
// иначе только hostname. Возвращает «test1 (192.168.31.67)» и сам ip
// (пусто — адрес неизвестен).
func resolveAgentEndpoint(host store.Host, agentID uuid.UUID, ipOf agentIPResolver) (endpoint, ip string) {
	if ipOf != nil {
		ip, _ = ipOf(agentID)
	}
	if ip == "" && len(host.IPAddresses) > 0 {
		ip = host.IPAddresses[0]
	}
	if ip != "" {
		return host.Hostname + " (" + ip + ")", ip
	}
	return host.Hostname, ""
}

// emitAgentStatusEvent — событие смены статуса агента → движок
// уведомлений (KI-2/KI-3). Резолвит agent → host → cluster → org и шлёт
// асинхронно (дедуп и отправка в фоне). Ошибки резолва — только лог.
func emitAgentStatusEvent(engine *notify.Engine, db *store.Store, tracker *notify.OfflineTracker, log *slog.Logger, agentID uuid.UUID, status, ip string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	agent, err := db.Agents.GetByID(ctx, agentID)
	if err != nil {
		log.Error("notify: резолв агента", "agent_id", agentID, "err", err)
		return
	}
	host, err := db.Hosts.Get(ctx, agent.HostID)
	if err != nil {
		log.Error("notify: резолв хоста", "host_id", agent.HostID, "err", err)
		return
	}
	cluster, err := db.Clusters.Get(ctx, host.ClusterID)
	if err != nil {
		log.Error("notify: резолв кластера", "cluster_id", host.ClusterID, "err", err)
		return
	}
	hostname := host.Hostname
	if hostname == "" {
		hostname = agentID.String()[:8]
	}
	where := hostname
	if ip != "" {
		where = hostname + " (" + ip + ")"
	} else if len(host.IPAddresses) > 0 {
		where = hostname + " (" + host.IPAddresses[0] + ")"
	}

	var ev notify.Event
	switch status {
	case "offline":
		// Эпизод зафиксирован — recovery по нему имеет право уйти (KI-2).
		tracker.TrackOffline(agentID, time.Now())
		ev = notify.Event{
			Type:     "agent.offline",
			ObjectID: agentID,
			Title:    "SuriFleet: агент offline",
			Text:     fmt.Sprintf("Агент %s, кластер %s, перешёл в статус offline.", where, cluster.Name),
			Severity: "critical",
		}
	case "online":
		since, ok := tracker.ConsumeOffline(agentID)
		if !ok {
			// Не видели offline в этом процессе (рестарт сервера, первое
			// подключение) — recovery не отправляем, иначе reconnect-шторм
			// даст волну ложных «снова онлайн» по флоту (KI-2).
			log.Debug("notify: online без зафиксированного offline — recovery подавлено",
				"agent_id", agentID)
			return
		}
		ev = notify.Event{
			Type:     "agent.online",
			ObjectID: agentID,
			Title:    "SuriFleet: агент снова онлайн",
			Text: fmt.Sprintf("Агент %s, кластер %s, снова онлайн (был недоступен %s).",
				where, cluster.Name, formatOfflineDuration(time.Since(since))),
			Severity: "info",
		}
	default:
		return
	}
	ev.Fields = map[string]any{
		"agent_id": agentID.String(), "host_id": host.ID.String(), "hostname": hostname,
		"cluster_id": cluster.ID.String(), "cluster": cluster.Name, "status": status,
	}
	if ip != "" {
		ev.Fields["ip"] = ip
	} else if len(host.IPAddresses) > 0 {
		ev.Fields["ip"] = host.IPAddresses[0]
	}
	engine.EmitAsync(cluster.OrganizationID, ev)
}

// emitDeployFailedEvent — событие провала задачи деплоя → движок
// уведомлений (KI-4): ruleset (версия, хэш), инстанс (hostname + IP),
// текст ошибки (первые строки), число попыток, отметка об автооткате.
// Дедупликация по fingerprint deploy.task_failed:<task_id> — повторные
// провалы той же задачи в окне 10 мин не спамят.
func emitDeployFailedEvent(engine *notify.Engine, db *store.Store, ipOf agentIPResolver, log *slog.Logger, t store.DeploymentTask, errMsg string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d, err := db.Deployments.Get(ctx, t.DeploymentID)
	if err != nil {
		log.Error("notify: резолв деплоя", "deployment_id", t.DeploymentID, "err", err)
		return
	}
	inst, err := db.Instances.Get(ctx, t.InstanceID)
	if err != nil {
		log.Error("notify: резолв инстанса", "instance_id", t.InstanceID, "err", err)
		return
	}
	host, err := db.Hosts.Get(ctx, inst.HostID)
	if err != nil {
		log.Error("notify: резолв хоста", "host_id", inst.HostID, "err", err)
		return
	}
	cluster, err := db.Clusters.Get(ctx, host.ClusterID)
	if err != nil {
		log.Error("notify: резолв кластера", "cluster_id", host.ClusterID, "err", err)
		return
	}
	agentID := uuid.Nil
	agent, err := db.Agents.GetByHostID(ctx, nil, host.ID)
	if err == nil {
		agentID = agent.ID
	} else {
		log.Debug("notify: агент хоста не найден (онбординг?)", "host_id", host.ID, "err", err)
	}
	endpoint, agentIP := resolveAgentEndpoint(host, agentID, ipOf)

	var target string
	fields := map[string]any{
		"deployment_id": t.DeploymentID.String(), "task_id": t.ID.String(),
		"kind": d.Kind, "instance_id": t.InstanceID.String(), "instance": inst.Name,
		"hostname": host.Hostname, "cluster_id": cluster.ID.String(), "cluster": cluster.Name,
		"attempts": t.Attempts, "max_attempts": t.MaxAttempts,
	}
	if agentIP != "" {
		fields["agent_ip"] = agentIP
	}
	title := "SuriFleet: деплой не удался"
	switch d.Kind {
	case "config":
		title = "SuriFleet: деплой конфигурации не удался"
		if d.ConfigVersionID != nil {
			if cv, err := db.Configs.Get(ctx, *d.ConfigVersionID); err == nil {
				target = "Конфигурация: " + cv.Version
				fields["config_version"] = cv.Version
			}
		}
	default:
		title = "SuriFleet: деплой правил не удался"
		if d.RulesetVersionID != nil {
			if rv, err := db.Rulesets.Get(ctx, *d.RulesetVersionID); err == nil {
				target = fmt.Sprintf("Ruleset: %s, sha %s", rv.Version, shortHash(rv.SHA256))
				fields["ruleset_version"] = rv.Version
				fields["ruleset_hash"] = rv.SHA256
			}
		}
	}

	errText := notify.ClipText(errMsg, 4, 300)
	fields["error"] = errText
	// Поток деплоя агента: бэкап → запись → suricata -T → при провале
	// откат (или изменения не применялись вовсе) — сенсор на прежней версии.
	fields["rolled_back"] = true

	lines := fmt.Sprintf("Инстанс: %s (хост %s)\n", inst.Name, endpoint)
	if target != "" {
		lines += target + "\n"
	}
	lines += fmt.Sprintf("Попытки: %d/%d\nОшибка: %s\nПрежняя версия на сенсоре сохранена (агент откатывает изменения при провале).",
		t.Attempts, t.MaxAttempts, errText)

	engine.EmitAsync(cluster.OrganizationID, notify.Event{
		Type:     "deploy.task_failed",
		ObjectID: t.ID,
		Title:    title,
		Text:     lines,
		Severity: "critical",
		Fields:   fields,
	})
}

// formatOfflineDuration — человекочитаемая длительность: «45с», «12м34с»,
// «2ч05м» (для текста recovery-уведомлений).
func formatOfflineDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%dс", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dм%02dс", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dч%02dм", int(d.Hours()), int(d.Minutes())%60)
}

// shortHash — первые 12 символов hex-хэша для компактных текстов.
func shortHash(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
