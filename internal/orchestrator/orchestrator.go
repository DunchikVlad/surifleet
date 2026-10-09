// Package orchestrator — волновой деплой ruleset и конфигураций
// (ТЗ п.6; kind=config — миграция 000013, чанк 67): раскладка
// инстансов по волнам (canary + батчи), отправка DeployRulesTask /
// DeployConfigTask агентам через hub, ожидание подтверждения фактической
// загрузки (TaskResult), auto-pause при ошибке волны, подхват
// pending-задач при подключении агента.
//
// Деплой успешен ТОЛЬКО по подтверждению агента (TaskResult + actual state),
// не по факту отправки задачи.
package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/surifleet/surifleet/internal/blob"
	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
	"github.com/surifleet/surifleet/internal/store"
)

const (
	// pollInterval — период опроса статусов задач/деплоя в цикле волны.
	pollInterval = 3 * time.Second
	// taskTimeout — задача без результата дольше этого срока → failed
	// (агент офлайн или завис); волна уходит в auto-pause.
	taskTimeout = 10 * time.Minute
	// presignTTL — срок действия подписанного URL блоба для агента.
	presignTTL = 15 * time.Minute
)

// Router — доставка задачи агенту (реализует hub.Server.SendTask).
// false — агент не подключён (задача остаётся pending до его Hello).
type Router interface {
	SendTask(agentID uuid.UUID, task *agentv1.Task) bool
}

// Orchestrator — фоновый исполнитель волновых деплоев.
type Orchestrator struct {
	db     *store.Store
	blob   *blob.Store
	router Router
	log    *slog.Logger

	mu      sync.Mutex
	running map[uuid.UUID]bool // деплои с активной горутиной run
}

// New собирает оркестратор.
func New(db *store.Store, blobStore *blob.Store, router Router, log *slog.Logger) *Orchestrator {
	return &Orchestrator{db: db, blob: blobStore, router: router, log: log, running: map[uuid.UUID]bool{}}
}

// ComputeWaves раскладывает инстансы по волнам: canary_size первых — волна 0
// (если canary > 0), остальные батчами batch_size — следующие волны.
// Порядок инстансов детерминирован (сортировка по id).
func ComputeWaves(instanceIDs []uuid.UUID, canarySize, batchSize int) []store.TaskWave {
	if batchSize <= 0 {
		batchSize = 50
	}
	ids := make([]uuid.UUID, len(instanceIDs))
	copy(ids, instanceIDs)
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })

	waves := []store.TaskWave{}
	wave := 0
	rest := ids
	if canarySize > 0 {
		n := canarySize
		if n > len(ids) {
			n = len(ids)
		}
		for _, id := range ids[:n] {
			waves = append(waves, store.TaskWave{InstanceID: id, Wave: 0})
		}
		rest = ids[n:]
		wave = 1
	}
	for i, id := range rest {
		waves = append(waves, store.TaskWave{InstanceID: id, Wave: wave + i/batchSize})
	}
	return waves
}

// Start запускает (или продолжает после pause/restart) деплой в фоне.
// Повторный вызов для уже выполняющегося деплоя — no-op.
func (o *Orchestrator) Start(deploymentID uuid.UUID) {
	o.mu.Lock()
	if o.running[deploymentID] {
		o.mu.Unlock()
		return
	}
	o.running[deploymentID] = true
	o.mu.Unlock()

	go func() {
		defer func() {
			o.mu.Lock()
			delete(o.running, deploymentID)
			o.mu.Unlock()
		}()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if err := o.run(ctx, deploymentID); err != nil {
			o.log.Error("оркестратор: деплой завершился с ошибкой", "deployment_id", deploymentID, "err", err)
		}
	}()
}

// Recover — восстановление после рестарта сервера: задачи в sent (ответ мог
// потеряться) возвращаются в pending, активные деплои продолжаются.
func (o *Orchestrator) Recover(ctx context.Context) error {
	n, err := o.db.Deployments.ResetSentTasks(ctx)
	if err != nil {
		return fmt.Errorf("сброс sent-задач: %w", err)
	}
	if n > 0 {
		o.log.Info("оркестратор: sent-задачи возвращены в pending", "count", n)
	}
	active, err := o.db.Deployments.ListActive(ctx)
	if err != nil {
		return fmt.Errorf("список активных деплоев: %w", err)
	}
	for _, d := range active {
		if d.Status == "paused" {
			continue // paused ждёт явного resume
		}
		o.log.Info("оркестратор: продолжаю деплой после рестарта", "deployment_id", d.ID, "status", d.Status)
		o.Start(d.ID)
	}
	return nil
}

// run — главный цикл деплоя: волны по порядку, ожидание терминальности
// задач волны, auto-pause при первой упавшей волне.
func (o *Orchestrator) run(ctx context.Context, deploymentID uuid.UUID) error {
	log := o.log.With("deployment_id", deploymentID)

	d, err := o.db.Deployments.Get(ctx, deploymentID)
	if err != nil {
		return fmt.Errorf("чтение деплоя: %w", err)
	}
	if d.Status == "pending" {
		if err := o.db.Deployments.SetStatus(ctx, deploymentID, "running"); err != nil {
			return fmt.Errorf("старт деплоя: %w", err)
		}
		o.event(ctx, deploymentID, nil, nil, "started", "деплой запущен")
	}

	maxWave, err := o.db.Deployments.MaxWave(ctx, deploymentID)
	if err != nil {
		return fmt.Errorf("max wave: %w", err)
	}

	for wave := 0; wave <= maxWave; wave++ {
		// Ожидание снятия с паузы / выход при отмене.
		for {
			d, err = o.db.Deployments.Get(ctx, deploymentID)
			if err != nil {
				return fmt.Errorf("опрос статуса деплоя: %w", err)
			}
			switch d.Status {
			case "cancelled":
				_ = o.db.Deployments.CancelPendingTasks(ctx, deploymentID)
				o.event(ctx, deploymentID, nil, nil, "cancelled", "деплой отменён")
				return nil
			case "completed", "failed":
				return nil
			case "paused":
				if !sleepCtx(ctx, pollInterval) {
					return nil
				}
				continue
			}
			break
		}

		wlog := log.With("wave", wave)
		wlog.Info("волна запущена")
		o.event(ctx, deploymentID, nil, nil, "wave_started", fmt.Sprintf("волна %d запущена", wave))

		// Отправка pending-задач волны подключённым агентам.
		if err := o.dispatchWave(ctx, d, wave); err != nil {
			wlog.Error("отправка задач волны", "err", err)
		}

		// Ожидание терминальности всех задач волны.
		waveFailed, err := o.waitWave(ctx, d, wave)
		if err != nil {
			return err
		}
		if waveFailed {
			// Auto-pause: деплой останавливается до решения оператора;
			// resume вернёт failed-задачи в pending и прогонит волну заново.
			if err := o.db.Deployments.SetStatus(ctx, deploymentID, "paused"); err != nil {
				return fmt.Errorf("auto-pause: %w", err)
			}
			o.event(ctx, deploymentID, nil, nil, "auto_paused",
				fmt.Sprintf("волна %d: есть упавшие задачи — деплой на паузе", wave))
			wlog.Warn("волна завершилась с ошибками — auto-pause")
			return nil
		}
		o.event(ctx, deploymentID, nil, nil, "wave_completed", fmt.Sprintf("волна %d завершена", wave))
		wlog.Info("волна завершена")
	}

	// Все волны пройдены.
	progress, err := o.db.Deployments.GetProgress(ctx, deploymentID)
	if err != nil {
		return fmt.Errorf("финальный прогресс: %w", err)
	}
	final := "completed"
	msg := fmt.Sprintf("деплой завершён: %d/%d успешно", progress.Succeeded, progress.Total)
	if progress.Failed > 0 || progress.Succeeded < progress.Total {
		final = "failed"
		msg = fmt.Sprintf("деплой завершён с ошибками: %d успешно, %d failed из %d",
			progress.Succeeded, progress.Failed, progress.Total)
	}
	if err := o.db.Deployments.SetStatus(ctx, deploymentID, final); err != nil {
		return fmt.Errorf("финальный статус: %w", err)
	}
	o.event(ctx, deploymentID, nil, nil, final, msg)
	log.Info("деплой завершён", "status", final)
	return nil
}

// dispatchWave отправляет pending-задачи волны онлайн-агентам; задачи
// офлайн-агентов остаются pending (подхват при Hello или таймаут волны).
func (o *Orchestrator) dispatchWave(ctx context.Context, d store.Deployment, wave int) error {
	tasks, err := o.db.Deployments.TasksOfWave(ctx, d.ID, wave)
	if err != nil {
		return fmt.Errorf("задачи волны: %w", err)
	}
	for _, t := range tasks {
		if t.Status != "pending" {
			continue
		}
		o.sendDeployTask(ctx, d, t)
	}
	return nil
}

// sendDeployTask собирает задачу деплоя по виду деплоя (d.Kind,
// миграция 000013): rules — DeployRulesTask из ruleset_versions (с
// подписанным URL блоба и путями инстанса), config — DeployConfigTask из
// config_versions. Отправляет агенту через hub. Успешная отправка → sent.
func (o *Orchestrator) sendDeployTask(ctx context.Context, d store.Deployment, t store.DeploymentTask) {
	log := o.log.With("deployment_id", d.ID, "task_id", t.ID, "instance_id", t.InstanceID)

	agentID, err := o.db.Deployments.AgentIDForInstance(ctx, t.InstanceID)
	if err != nil {
		o.failTask(ctx, d.ID, t, "агент инстанса не найден: "+err.Error())
		return
	}
	inst, err := o.db.Instances.Get(ctx, t.InstanceID)
	if err != nil {
		o.failTask(ctx, d.ID, t, "инстанс не найден: "+err.Error())
		return
	}

	task := &agentv1.Task{
		TaskId:   t.ID.String(),
		Deadline: timestamppb.New(time.Now().Add(taskTimeout)),
	}
	var what string
	switch d.Kind {
	case "config":
		if d.ConfigVersionID == nil {
			o.failTask(ctx, d.ID, t, "config-деплой без config_version_id")
			return
		}
		cv, err := o.db.Configs.Get(ctx, *d.ConfigVersionID)
		if err != nil {
			o.failTask(ctx, d.ID, t, "версия конфигурации не найдена: "+err.Error())
			return
		}
		url, err := o.blob.PresignGet(ctx, cv.S3Key, presignTTL)
		if err != nil {
			// Временная проблема S3 — задача останется pending, повтор на следующей итерации.
			log.Error("presign URL конфиг-блоба", "err", err)
			return
		}
		task.Type = &agentv1.Task_DeployConfig{DeployConfig: &agentv1.DeployConfigTask{
			InstanceId:    t.InstanceID.String(),
			ConfigVersion: cv.Version,
			Source:        &agentv1.DeployConfigTask_SignedUrl{SignedUrl: url},
		}}
		what = "config " + cv.Version
	default: // rules
		if d.RulesetVersionID == nil {
			o.failTask(ctx, d.ID, t, "rules-деплой без ruleset_version_id")
			return
		}
		rv, err := o.db.Rulesets.Get(ctx, *d.RulesetVersionID)
		if err != nil {
			o.failTask(ctx, d.ID, t, "ruleset не найден: "+err.Error())
			return
		}
		url, err := o.blob.PresignGet(ctx, rv.S3Key, presignTTL)
		if err != nil {
			// Временная проблема S3 — задача останется pending, повтор на следующей итерации.
			log.Error("presign URL блоба", "err", err)
			return
		}
		task.Type = &agentv1.Task_DeployRules{DeployRules: &agentv1.DeployRulesTask{
			InstanceId:     t.InstanceID.String(),
			RulesetVersion: rv.Version,
			RulesetHash:    rv.SHA256,
			SignedUrl:      url,
			RulesDir:       inst.RulesDir,
			ConfigPath:     inst.ConfigPath,
			SystemdUnit:    derefStr(inst.SystemdUnit),
		}}
		what = "ruleset " + rv.Version
	}
	if !o.router.SendTask(agentID, task) {
		log.Debug("агент офлайн — задача остаётся pending", "agent_id", agentID)
		return
	}
	if err := o.db.Deployments.MarkTaskSent(ctx, t.ID); err != nil {
		log.Error("mark sent", "err", err)
	}
	log.Info("задача отправлена агенту", "agent_id", agentID, "deploy", what)
}

// waitWave опрашивает задачи волны до терминальности. true — волна с ошибками.
func (o *Orchestrator) waitWave(ctx context.Context, d store.Deployment, wave int) (bool, error) {
	for {
		// Статус деплоя мог смениться (pause/cancel из API).
		cur, err := o.db.Deployments.Get(ctx, d.ID)
		if err != nil {
			return false, fmt.Errorf("опрос статуса деплоя: %w", err)
		}
		if cur.Status == "paused" || cur.Status == "cancelled" {
			return false, nil // выход в главный цикл — там обработается
		}

		tasks, err := o.db.Deployments.TasksOfWave(ctx, d.ID, wave)
		if err != nil {
			return false, fmt.Errorf("задачи волны: %w", err)
		}
		allTerminal := true
		waveFailed := false
		for _, t := range tasks {
			switch t.Status {
			case "succeeded":
			case "failed", "cancelled", "skipped":
				waveFailed = true
			case "pending":
				// Задачу никто не забрал: либо агент офлайн (повторная
				// отправка), либо таймаут ожидания агента.
				if time.Since(t.UpdatedAt) > taskTimeout {
					o.failTask(ctx, d.ID, t, "agent offline timeout: задача не подхвачена за "+taskTimeout.String())
					waveFailed = true
					continue
				}
				o.sendDeployTask(ctx, cur, t) // повторная попытка (агент мог подключиться)
				allTerminal = false
			case "sent", "running":
				if time.Since(t.UpdatedAt) > taskTimeout {
					o.failTask(ctx, d.ID, t, "task timeout: результат не получен за "+taskTimeout.String())
					waveFailed = true
					continue
				}
				allTerminal = false
			}
		}
		if allTerminal {
			return waveFailed, nil
		}
		if !sleepCtx(ctx, pollInterval) {
			return false, nil
		}
	}
}

// HandleTaskResult — колбэк hub.OnTaskResult: фиксация результата задачи
// (деплой успешен только по подтверждению агента о фактической загрузке).
func (o *Orchestrator) HandleTaskResult(ctx context.Context, agentID uuid.UUID, res *agentv1.TaskResult) {
	log := o.log.With("task_id", res.GetTaskId(), "agent_id", agentID)
	taskID, err := uuid.Parse(res.GetTaskId())
	if err != nil {
		log.Warn("TaskResult: некорректный task_id")
		return
	}
	t, err := o.db.Deployments.GetTask(ctx, taskID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			log.Error("TaskResult: чтение задачи", "err", err)
		}
		return // чужая/неизвестная задача
	}

	var status string
	switch res.GetStatus() {
	case agentv1.TaskStatus_TASK_STATUS_SUCCESS:
		status = "succeeded"
	case agentv1.TaskStatus_TASK_STATUS_FAILED:
		status = "failed"
	case agentv1.TaskStatus_TASK_STATUS_CANCELLED:
		status = "cancelled"
	default:
		log.Warn("TaskResult: неизвестный статус", "status", res.GetStatus().String())
		return
	}

	var resultJSON json.RawMessage
	if dr := res.GetDeployRules(); dr != nil {
		resultJSON, _ = json.Marshal(dr)
	} else if dc := res.GetDeployConfig(); dc != nil {
		resultJSON, _ = json.Marshal(dc)
	}
	var errMsg *string
	if res.GetError() != "" {
		e := res.GetError()
		errMsg = &e
	}
	if err := o.db.Deployments.UpdateTaskResult(ctx, taskID, status, resultJSON, errMsg); err != nil {
		log.Error("TaskResult: обновление задачи", "err", err)
		return
	}
	o.event(ctx, t.DeploymentID, &t.ID, &t.InstanceID, "task_"+status,
		fmt.Sprintf("задача %s: %s", taskID, status))
	log.Info("результат задачи зафиксирован", "status", status, "deployment_id", t.DeploymentID)
}

// DispatchPending — колбэк hub.OnAgentOnline: подхват накопленных
// pending-задач агента (деплой ждал подключения).
func (o *Orchestrator) DispatchPending(ctx context.Context, agentID uuid.UUID) {
	log := o.log.With("agent_id", agentID)
	tasks, err := o.db.Deployments.PendingTasksForAgent(ctx, agentID)
	if err != nil {
		log.Error("подхват pending-задач", "err", err)
		return
	}
	for _, t := range tasks {
		d, err := o.db.Deployments.Get(ctx, t.DeploymentID)
		if err != nil {
			log.Error("подхват: чтение деплоя", "deployment_id", t.DeploymentID, "err", err)
			continue
		}
		o.sendDeployTask(ctx, d, t)
	}
	if len(tasks) > 0 {
		log.Info("подхвачены pending-задачи при подключении агента", "count", len(tasks))
	}
}

// failTask переводит задачу в failed с событием.
func (o *Orchestrator) failTask(ctx context.Context, deploymentID uuid.UUID, t store.DeploymentTask, msg string) {
	if err := o.db.Deployments.UpdateTaskResult(ctx, t.ID, "failed", nil, &msg); err != nil {
		o.log.Error("fail task", "task_id", t.ID, "err", err)
		return
	}
	o.event(ctx, deploymentID, &t.ID, &t.InstanceID, "task_failed", msg)
}

// event — запись в историю деплоя (best-effort: ошибка только в лог).
func (o *Orchestrator) event(ctx context.Context, deploymentID uuid.UUID, taskID, instanceID *uuid.UUID, typ, msg string) {
	if err := o.db.Deployments.EventAdd(ctx, store.DeployEvent{
		DeploymentID:     deploymentID,
		DeploymentTaskID: taskID,
		InstanceID:       instanceID,
		EventType:        typ,
		Message:          &msg,
	}); err != nil {
		o.log.Error("событие деплоя", "type", typ, "err", err)
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
