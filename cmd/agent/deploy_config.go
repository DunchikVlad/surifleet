// deploy_config.go — деплой конфигурации suricata.yaml (чанк 54, план 1B).
// Весь файл под управлением (решение заказчика): бэкап → запись →
// suricata -T → при провале откат из бэкапа; при успехе — restart юнита
// (изменение конфига требует рестарта, reload-rules недостаточно).
// validate_only — только валидация кандидата, без записи.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync/atomic"
	"time"

	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
)

// executeConfig — деплой конфигурации end-to-end. Пути инстанса — из
// привязок HelloAck (bound_instances), systemd-юнит — из discovery-отчёта.
func (e *taskExecutor) executeConfig(task *agentv1.Task, dc *agentv1.DeployConfigTask, disc *atomic.Pointer[agentv1.DiscoveryReport]) {
	taskID := task.GetTaskId()
	log := e.log.With("task_id", taskID, "instance_id", dc.GetInstanceId(), "config_version", dc.GetConfigVersion())

	// Capability-гейт: без config хост не отдаёт управление конфигурацией.
	if !e.hasCap("config") {
		e.failCfgTask(taskID, nil, "capability config не включена для хоста")
		return
	}
	// Идемпотентность: повторная доставка — сохранённый результат.
	if cached, ok := e.lookupProcessed(taskID); ok {
		log.Info("задача уже обработана — повторяю сохранённый результат", "status", cached.Status)
		e.reply(&agentv1.TaskResult{
			TaskId:  taskID,
			Status:  protoStatus(cached.Status),
			Error:   cached.Error,
			Details: configDetails(cached.Config),
		})
		return
	}
	if dl := task.GetDeadline(); dl != nil && time.Now().After(dl.AsTime()) {
		e.failCfgTask(taskID, nil, "дедлайн задачи истёк")
		return
	}

	// Путь к конфигу инстанса — из привязок HelloAck.
	b := e.bindings[dc.GetInstanceId()]
	if b == nil || b.GetConfigPath() == "" {
		e.failCfgTask(taskID, nil, "инстанс не привязан к агенту (нет bound_instances)")
		return
	}
	configPath := b.GetConfigPath()

	// Содержимое: inline или блоб по подписанному URL (та же схема, что
	// у ruleset; sha256 — в ключе S3).
	var data []byte
	var err error
	if inline := dc.GetInlineYaml(); inline != "" {
		data = []byte(inline)
	} else {
		data, err = downloadBlob(dc.GetSignedUrl())
		if err != nil {
			e.failCfgTask(taskID, nil, "скачивание конфига: "+err.Error())
			return
		}
	}
	log.Info("конфиг получен", "bytes", len(data))

	// validate_only: кандидат во временный файл, suricata -T, без записи.
	if dc.GetValidateOnly() {
		tmp := e.dataDir + "/config-candidate.yaml"
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			e.failCfgTask(taskID, nil, "запись кандидата: "+err.Error())
			return
		}
		defer os.Remove(tmp)
		out, verr := validateConfig(tmp)
		res := &agentv1.DeployConfigResult{
			ConfigVersion:    dc.GetConfigVersion(),
			InstanceId:       dc.GetInstanceId(),
			ValidateOnly:     dc.GetValidateOnly(),
			ValidationPassed: verr == nil,
			ValidationOutput: tail(out, 40),
		}
		if verr != nil {
			e.failCfgTask(taskID, res, "suricata -T: "+verr.Error())
			return
		}
		e.replyConfigOK(taskID, res)
		return
	}

	// Бэкап → атомарная запись → suricata -T → откат при провале.
	backup, hadBackup, err := backupFile(configPath)
	if err != nil {
		e.failCfgTask(taskID, nil, "бэкап конфига: "+err.Error())
		return
	}
	if err := writeFileAtomic(configPath, data, 0o644); err != nil {
		e.failCfgTask(taskID, nil, "запись конфига: "+err.Error())
		return
	}
	out, verr := validateConfig(configPath)
	if verr != nil {
		log.Error("suricata -T не пройден — откат конфига", "err", verr)
		rollback(configPath, backup, hadBackup, log)
		e.failCfgTask(taskID, &agentv1.DeployConfigResult{
			ConfigVersion:    dc.GetConfigVersion(),
			InstanceId:       dc.GetInstanceId(),
			ValidateOnly:     dc.GetValidateOnly(),
			ValidationPassed: false,
			ValidationOutput: tail(out, 40),
		}, "suricata -T: "+verr.Error())
		return
	}
	log.Info("suricata -T пройден")

	// Рестарт движка: юнит из discovery (по совпадению config_path).
	// Таймаут 120 с: на стенде 90 с не хватило при конкурентных рестартах
	// suricata (ruleset-деплои) — systemctl ждал чужую транзакцию.
	unit := e.unitForInstance(disc, configPath)
	if unit != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		rout, rerr := exec.CommandContext(ctx, "systemctl", "restart", unit).CombinedOutput()
		if rerr != nil {
			// Конфиг записан и suricata -T пройден — фиксируем попытку
			// применения в истории (deploy_failed), иначе провал не виден
			// в instance_config_history (чанк 101: раньше детали не
			// прикреплялись, сервер пропускал запись).
			e.failCfgTask(taskID, &agentv1.DeployConfigResult{
				ConfigVersion:    dc.GetConfigVersion(),
				InstanceId:       dc.GetInstanceId(),
				ValidateOnly:     dc.GetValidateOnly(),
				ValidationPassed: true,
				ValidationOutput: tail(out, 40),
			}, fmt.Sprintf("systemctl restart %s: %v; вывод: %s", unit, rerr, tail(string(rout), 10)))
			return
		}
		log.Info("юнит перезапущен", "unit", unit)
	} else {
		log.Warn("systemd_unit неизвестен (discovery) — движок не перезапущен, конфиг применится при рестарте")
	}

	res := &agentv1.DeployConfigResult{
		ConfigVersion:    dc.GetConfigVersion(),
		InstanceId:       dc.GetInstanceId(),
		ValidateOnly:     dc.GetValidateOnly(),
		ValidationPassed: true,
		ValidationOutput: tail(out, 40),
	}
	e.saveProcessed(taskID, cachedResult{Status: "succeeded", Config: res})
	e.replyConfigOK(taskID, res)
}

// unitForInstance — systemd-юнит инстанса из discovery-отчёта
// (совпадение по config_path).
func (e *taskExecutor) unitForInstance(disc *atomic.Pointer[agentv1.DiscoveryReport], configPath string) string {
	if disc == nil {
		return ""
	}
	rep := disc.Load()
	if rep == nil {
		return ""
	}
	for _, in := range rep.GetInstances() {
		if in.GetConfigPath() == configPath {
			return in.GetSystemdUnit()
		}
	}
	return ""
}

// failCfgTask — failed для deploy_config (без записи в журнал, как у rules).
func (e *taskExecutor) failCfgTask(taskID string, cfg *agentv1.DeployConfigResult, msg string) {
	e.log.Warn("задача завершилась ошибкой", "task_id", taskID, "error", msg)
	e.reply(&agentv1.TaskResult{
		TaskId:  taskID,
		Status:  agentv1.TaskStatus_TASK_STATUS_FAILED,
		Error:   msg,
		Details: configDetails(cfg),
	})
}

// replyConfigOK — TaskResult succeeded с DeployConfigResult + журнал.
func (e *taskExecutor) replyConfigOK(taskID string, res *agentv1.DeployConfigResult) {
	e.reply(&agentv1.TaskResult{
		TaskId:  taskID,
		Status:  agentv1.TaskStatus_TASK_STATUS_SUCCESS,
		Details: configDetails(res),
	})
}

func configDetails(c *agentv1.DeployConfigResult) *agentv1.TaskResult_DeployConfig {
	if c == nil {
		return nil
	}
	return &agentv1.TaskResult_DeployConfig{DeployConfig: c}
}
