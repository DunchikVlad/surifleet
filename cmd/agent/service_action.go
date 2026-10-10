// service_action.go — управление systemd-сервисом инстанса (чанк 105,
// п. 7 ТЗ «действия из UI — перезапуск сервиса»): сервер шлёт
// ServiceActionTask (reload/restart/start/stop), агент выполняет systemctl
// для юнита инстанса и возвращает ServiceActionResult с фактическим
// состоянием после действия (по systemctl is-active).
//
// Capability-гейт: service_mgmt (отдельная от rules/config — передача
// управления сервисом поэтапная, п. 4 ТЗ). Юнит — из discovery-отчёта
// по config_path инстанса (как в deploy_config). Задача меняет состояние
// сервиса, но не файлы — журнал идемпотентности не пишется: повторное
// выполнение systemctl безопасно по семантике systemd.
package main

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
)

// serviceActionTimeout — предел на systemctl-операцию. На стенде graceful
// stop Suricata занимал до ~55 с (чанк 12c-2) — берём с запасом, как у
// restart в deploy_config (120 с, чанк 101).
const serviceActionTimeout = 120 * time.Second

// executeServiceAction — reload/restart/stop/start сервиса инстанса.
func (e *taskExecutor) executeServiceAction(task *agentv1.Task, sa *agentv1.ServiceActionTask) {
	taskID := task.GetTaskId()
	action := sa.GetAction()
	log := e.log.With("task_id", taskID, "instance_id", sa.GetInstanceId(), "action", action.String())

	if !e.hasCap("service_mgmt") {
		e.replySvc(taskID, nil, "capability service_mgmt не включена для хоста")
		return
	}
	if dl := task.GetDeadline(); dl != nil && time.Now().After(dl.AsTime()) {
		e.replySvc(taskID, nil, "дедлайн задачи истёк")
		return
	}
	verb, ok := serviceActionVerb(action)
	if !ok {
		e.replySvc(taskID, nil, "неизвестное действие: "+action.String())
		return
	}
	// Юнит инстанса — из discovery по config_path привязки (как в
	// deploy_config): bindings — единственный источник config_path.
	b := e.bindings[sa.GetInstanceId()]
	if b == nil || b.GetConfigPath() == "" {
		e.replySvc(taskID, nil, "инстанс не привязан к агенту (нет bound_instances)")
		return
	}
	unit := e.unitForInstance(e.disc, b.GetConfigPath())
	if unit == "" {
		e.replySvc(taskID, nil, "systemd_unit инстанса неизвестен (discovery не дал юнит)")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), serviceActionTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", verb, unit).CombinedOutput()
	if err != nil {
		e.replySvc(taskID, nil, fmt.Sprintf("systemctl %s %s: %v; вывод: %s", verb, unit, err, tail(string(out), 10)))
		return
	}
	log.Info("действие над сервисом выполнено", "unit", unit)

	// Фактическое состояние после действия — по systemctl is-active
	// (exit code 3 = inactive — не ошибка выполнения, а валидное состояние).
	state := serviceStateOf(unit)
	e.reply(&agentv1.TaskResult{
		TaskId: taskID,
		Status: agentv1.TaskStatus_TASK_STATUS_SUCCESS,
		Details: &agentv1.TaskResult_ServiceAction{ServiceAction: &agentv1.ServiceActionResult{
			InstanceId:   sa.GetInstanceId(),
			ServiceState: state,
		}},
	})
}

// serviceActionVerb — маппинг proto-enum → глагол systemctl.
func serviceActionVerb(a agentv1.ServiceAction) (string, bool) {
	switch a {
	case agentv1.ServiceAction_SERVICE_ACTION_RELOAD:
		return "reload", true
	case agentv1.ServiceAction_SERVICE_ACTION_RESTART:
		return "restart", true
	case agentv1.ServiceAction_SERVICE_ACTION_STOP:
		return "stop", true
	case agentv1.ServiceAction_SERVICE_ACTION_START:
		return "start", true
	default:
		return "", false
	}
}

// serviceStateOf — фактическое состояние юнита (active/inactive/failed/…).
// is-active завершается ненулевым кодом на неактивном юните — это не ошибка
// диагностики, а само состояние.
func serviceStateOf(unit string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "is-active", unit).Output()
	if err != nil {
		// При inactive/failed systemctl всё равно печатает состояние в stdout.
		if len(out) > 0 {
			return firstLine(string(out))
		}
		return "unknown"
	}
	return firstLine(string(out))
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}

// replySvc — failed для service_action (единая точка, как failCfgTask).
func (e *taskExecutor) replySvc(taskID string, res *agentv1.ServiceActionResult, msg string) {
	e.log.Warn("задача завершилась ошибкой", "task_id", taskID, "error", msg)
	var details *agentv1.TaskResult_ServiceAction
	if res != nil {
		details = &agentv1.TaskResult_ServiceAction{ServiceAction: res}
	}
	e.reply(&agentv1.TaskResult{
		TaskId:  taskID,
		Status:  agentv1.TaskStatus_TASK_STATUS_FAILED,
		Error:   msg,
		Details: details,
	})
}
