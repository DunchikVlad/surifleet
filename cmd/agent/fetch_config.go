// fetch_config.go — чтение фактического suricata.yaml с сенсора (чанк 55,
// план 1B): сервер шлёт FetchConfigTask, агент читает config_path инстанса
// из привязок HelloAck и возвращает содержимое в FetchConfigResult —
// исходник для редактора «как на хосте». Только чтение, движок не трогаем.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
)

// fetchMaxConfigSize — предел размера конфига (4 МБ; suricata.yaml —
// десятки КБ, большее — аномалия, не тащим по стриму).
const fetchMaxConfigSize = 4 << 20

// executeFetch — прочитать suricata.yaml инстанса и вернуть содержимое.
func (e *taskExecutor) executeFetch(task *agentv1.Task, fc *agentv1.FetchConfigTask) {
	taskID := task.GetTaskId()
	log := e.log.With("task_id", taskID, "instance_id", fc.GetInstanceId())

	if !e.hasCap("config") {
		e.reply(&agentv1.TaskResult{
			TaskId: taskID,
			Status: agentv1.TaskStatus_TASK_STATUS_FAILED,
			Error:  "capability config не включена для хоста",
		})
		return
	}
	if dl := task.GetDeadline(); dl != nil && time.Now().After(dl.AsTime()) {
		e.reply(&agentv1.TaskResult{TaskId: taskID, Status: agentv1.TaskStatus_TASK_STATUS_FAILED, Error: "дедлайн задачи истёк"})
		return
	}
	b := e.bindings[fc.GetInstanceId()]
	if b == nil || b.GetConfigPath() == "" {
		e.reply(&agentv1.TaskResult{TaskId: taskID, Status: agentv1.TaskStatus_TASK_STATUS_FAILED, Error: "инстанс не привязан к агенту (нет bound_instances)"})
		return
	}
	data, err := os.ReadFile(b.GetConfigPath())
	if err != nil {
		e.reply(&agentv1.TaskResult{TaskId: taskID, Status: agentv1.TaskStatus_TASK_STATUS_FAILED, Error: fmt.Sprintf("чтение %s: %v", b.GetConfigPath(), err)})
		return
	}
	if len(data) > fetchMaxConfigSize {
		e.reply(&agentv1.TaskResult{TaskId: taskID, Status: agentv1.TaskStatus_TASK_STATUS_FAILED, Error: fmt.Sprintf("конфиг %s больше предела %d байт", b.GetConfigPath(), fetchMaxConfigSize)})
		return
	}
	sum := sha256.Sum256(data)
	log.Info("конфиг прочитан", "path", b.GetConfigPath(), "bytes", len(data))
	e.reply(&agentv1.TaskResult{
		TaskId: taskID,
		Status: agentv1.TaskStatus_TASK_STATUS_SUCCESS,
		Details: &agentv1.TaskResult_FetchConfig{FetchConfig: &agentv1.FetchConfigResult{
			Content:   string(data),
			Sha256:    hex.EncodeToString(sum[:]),
			SizeBytes: int64(len(data)),
		}},
	})
}
