// Реестр стримов агентов и обработка actual state (chunk 11):
// in-process доставка задач агентам (SendTask), приём StateReport /
// RuleLoadReport / TaskResult, инкрементальный пересчёт compliance.
package hub

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/surifleet/surifleet/internal/compliance"
	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
	"github.com/surifleet/surifleet/internal/store"
)

// actualCacheTTL — TTL кэша actual state в Redis (actual:{instance_id}).
const actualCacheTTL = 300 * time.Second

// streamHandle — очередь исходящих сообщений одного подключённого агента.
// sessionID — для защиты статуса: старая сессия при разрыве не гасит
// агента, если зарегистрирована более новая (дубль-стрим, chunk 28).
type streamHandle struct {
	out       chan *agentv1.ServerMessage
	sessionID string
}

// SendTask ставит задачу в очередь стрима агента (in-process доставка
// «один хаб — один контур»). false — агент не подключён к этому хабу
// (задача останется pending и будет подхвачена при следующем Hello)
// или очередь переполнена.
func (s *Server) SendTask(agentID uuid.UUID, task *agentv1.Task) bool {
	v, ok := s.streams.Load(agentID)
	if !ok {
		return false
	}
	msg := &agentv1.ServerMessage{
		MsgId:   uuid.New().String(),
		SentAt:  timestamppb.Now(),
		Payload: &agentv1.ServerMessage_Task{Task: task},
	}
	select {
	case v.(*streamHandle).out <- msg:
		return true
	default:
		return false
	}
}

// SendTaskAndWait — синхронная вариация SendTask (chunk 55, fetch_config):
// ставит задачу и ждёт результат агента до timeout. Второе false —
// результат не получен (таймаут/отказ).
func (s *Server) SendTaskAndWait(ctx context.Context, agentID uuid.UUID, task *agentv1.Task, timeout time.Duration) (*agentv1.TaskResult, bool) {
	ch := make(chan *agentv1.TaskResult, 1)
	s.taskWaiters.Store(task.GetTaskId(), ch)
	defer s.taskWaiters.Delete(task.GetTaskId())
	if !s.SendTask(agentID, task) {
		return nil, false
	}
	select {
	case res := <-ch:
		return res, true
	case <-time.After(timeout):
		return nil, false
	case <-ctx.Done():
		return nil, false
	}
}

// handleStateReport — полный/инкрементальный снапшот actual state инстанса:
// upsert в actual_state, кэш в Redis, пересчёт compliance.
// При full=false loaded_rules не менялись с прошлого отчёта — сохраняем
// прежний список из БД.
func (s *Server) handleStateReport(ctx context.Context, log *slog.Logger, rep *agentv1.StateReport) {
	instanceID, err := uuid.Parse(rep.GetInstanceId())
	if err != nil {
		log.Warn("StateReport: некорректный instance_id", "instance_id", rep.GetInstanceId())
		return
	}

	loaded, err := json.Marshal(rep.GetLoadedRules())
	if err != nil {
		log.Error("StateReport: marshal loaded_rules", "err", err)
		return
	}
	if !rep.GetFull() {
		// Снапшот инкрементальный: loaded_rules прежние — берём из БД.
		if prev, err := s.db.ActualState.Get(ctx, instanceID); err == nil {
			loaded = prev.LoadedRules
		}
	}
	failed, err := json.Marshal(rep.GetFailedRules())
	if err != nil {
		log.Error("StateReport: marshal failed_rules", "err", err)
		return
	}
	var lastReload json.RawMessage
	if rep.GetLastReload() != nil {
		lastReload, _ = json.Marshal(rep.GetLastReload())
	}
	reportedAt := rep.GetReportedAt().AsTime()

	st := store.ActualState{
		InstanceID:       instanceID,
		RulesetHash:      strp(rep.GetRulesetHash()),
		LoadedRules:      loaded,
		FailedRules:      failed,
		LastReloadResult: lastReload,
		ReportedAt:       &reportedAt,
	}
	if err := s.db.ActualState.Upsert(ctx, st); err != nil {
		log.Error("StateReport: upsert actual_state", "instance_id", instanceID, "err", err)
		return
	}
	s.cacheActual(ctx, log, instanceID, rep.GetRulesetHash())
	s.recomputeCompliance(ctx, log, instanceID, true)
	log.Info("StateReport сохранён",
		"instance_id", instanceID, "ruleset_hash", rep.GetRulesetHash(),
		"loaded", len(rep.GetLoadedRules()), "failed", len(rep.GetFailedRules()), "full", rep.GetFull())
}

// handleRuleLoadReport — оперативная верификация после reload: хэш и
// failed-правила известны, список loaded — только счётчиком (список
// loaded_rules в БД не трогаем, дождёмся полного StateReport).
func (s *Server) handleRuleLoadReport(ctx context.Context, log *slog.Logger, rep *agentv1.RuleLoadReport) {
	instanceID, err := uuid.Parse(rep.GetInstanceId())
	if err != nil {
		log.Warn("RuleLoadReport: некорректный instance_id", "instance_id", rep.GetInstanceId())
		return
	}

	var loaded json.RawMessage
	if prev, err := s.db.ActualState.Get(ctx, instanceID); err == nil {
		loaded = prev.LoadedRules
	}
	failed, err := json.Marshal(rep.GetFailedRules())
	if err != nil {
		log.Error("RuleLoadReport: marshal failed_rules", "err", err)
		return
	}
	reportedAt := rep.GetVerifiedAt().AsTime()

	st := store.ActualState{
		InstanceID:  instanceID,
		RulesetHash: strp(rep.GetRulesetHash()),
		LoadedRules: loaded,
		FailedRules: failed,
		ReportedAt:  &reportedAt,
	}
	if err := s.db.ActualState.Upsert(ctx, st); err != nil {
		log.Error("RuleLoadReport: upsert actual_state", "instance_id", instanceID, "err", err)
		return
	}
	s.cacheActual(ctx, log, instanceID, rep.GetRulesetHash())
	s.recomputeCompliance(ctx, log, instanceID, true)
	log.Info("RuleLoadReport сохранён",
		"instance_id", instanceID, "ruleset_hash", rep.GetRulesetHash(),
		"loaded_count", rep.GetLoadedCount(), "failed", len(rep.GetFailedRules()))
}

// handleTaskResult — результат задачи от агента: вложенный state_after
// сохраняем как обычный StateReport, дальше — подписчик (оркестратор).
func (s *Server) handleTaskResult(ctx context.Context, log *slog.Logger, agentID uuid.UUID, res *agentv1.TaskResult) {
	log.Info("TaskResult",
		"task_id", res.GetTaskId(), "status", res.GetStatus().String(), "error", res.GetError())
	if sa := res.GetStateAfter(); sa != nil {
		s.handleStateReport(ctx, log, sa)
	}
	if dc := res.GetDeployConfig(); dc != nil {
		// История применения конфигураций (чанк 57, план 1B).
		st := configDeployStatus(res.GetStatus() == agentv1.TaskStatus_TASK_STATUS_SUCCESS, dc.GetValidateOnly(), dc.GetValidationPassed())
		if err := s.db.Configs.RecordDeploy(ctx, dc.GetInstanceId(), dc.GetConfigVersion(), st, dc.GetValidationOutput()); err != nil {
			log.Error("config history: запись", "task_id", res.GetTaskId(), "err", err)
		}
	}
	if w, ok := s.taskWaiters.LoadAndDelete(res.GetTaskId()); ok {
		select {
		case w.(chan *agentv1.TaskResult) <- res:
		default:
		}
	}
	if s.OnTaskResult != nil {
		s.OnTaskResult(ctx, agentID, res)
	}
}

// cacheActual — быстрый кэш actual state в Redis (actual:{instance_id}).
func (s *Server) cacheActual(ctx context.Context, log *slog.Logger, instanceID uuid.UUID, hash string) {
	val, _ := json.Marshal(map[string]any{
		"ruleset_hash": hash,
		"at":           time.Now().UTC().Format(time.RFC3339),
	})
	if err := s.rdb.Set(ctx, "actual:"+instanceID.String(), val, actualCacheTTL).Err(); err != nil {
		log.Error("actual state в Redis", "instance_id", instanceID, "err", err)
	}
}

// recomputeCompliance — инкрементальный пересчёт соответствия инстанса
// по сохранённым desired/actual и факту онлайна агента.
func (s *Server) recomputeCompliance(ctx context.Context, log *slog.Logger, instanceID uuid.UUID, agentOnline bool) {
	var desired *compliance.Desired
	if d, err := s.db.DesiredState.Get(ctx, instanceID); err == nil {
		if rv, err := s.db.Rulesets.Get(ctx, d.RulesetVersionID); err == nil {
			desired = &compliance.Desired{
				RulesetHash: rv.SHA256,
				RuleSids:    sidsFromComputed(d.ComputedRules),
			}
		} else {
			log.Error("compliance: ruleset версии не найден", "instance_id", instanceID, "err", err)
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		log.Error("compliance: чтение desired_state", "instance_id", instanceID, "err", err)
	}

	var actual *compliance.Actual
	if a, err := s.db.ActualState.Get(ctx, instanceID); err == nil && a.ReportedAt != nil {
		actual = &compliance.Actual{
			RulesetHash: derefStr(a.RulesetHash),
			LoadedSids:  sidsFromLoaded(a.LoadedRules),
			FailedRules: failedFromJSON(a.FailedRules),
		}
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		log.Error("compliance: чтение actual_state", "instance_id", instanceID, "err", err)
	}

	status, details := compliance.Compute(desired, actual, agentOnline)
	raw, err := json.Marshal(details)
	if err != nil {
		log.Error("compliance: marshal details", "instance_id", instanceID, "err", err)
		return
	}
	if err := s.db.Compliance.Upsert(ctx, instanceID, status, raw); err != nil {
		log.Error("compliance: upsert", "instance_id", instanceID, "err", err)
	}
}

// recomputeHostCompliance — пересчёт compliance всех инстансов хоста агента
// (при connect — online=true, при disconnect — online=false → stale).
func (s *Server) recomputeHostCompliance(ctx context.Context, log *slog.Logger, agentID uuid.UUID, online bool) {
	host, err := s.db.Hosts.GetByAgentID(ctx, agentID)
	if err != nil {
		log.Error("compliance: хост агента не найден", "err", err)
		return
	}
	instances, _, err := s.db.Instances.List(ctx, host.ID, uuid.Nil, uuid.Nil, 1000)
	if err != nil {
		log.Error("compliance: список инстансов хоста", "host_id", host.ID, "err", err)
		return
	}
	for _, inst := range instances {
		s.recomputeCompliance(ctx, log, inst.ID, online)
	}
}

// hostCapabilities — включённые capability хоста агента (host → cluster →
// дефолт monitoring) для HelloAck.Config.
func (s *Server) hostCapabilities(ctx context.Context, log *slog.Logger, agentID uuid.UUID) []string {
	host, err := s.db.Hosts.GetByAgentID(ctx, agentID)
	if err != nil {
		log.Error("capabilities: хост агента не найден", "err", err)
		return []string{"monitoring"}
	}
	caps, err := s.db.Capabilities.ForHost(ctx, host.ID, host.ClusterID)
	if err != nil {
		log.Error("capabilities: чтение", "host_id", host.ID, "err", err)
		return []string{"monitoring"}
	}
	return caps
}

// instanceBindings — привязка агента к зарегистрированным инстансам Suricata
// его хоста для HelloAck.bound_instances: агент узнаёт свои instance_id
// сразу при подключении, не дожидаясь первой задачи. Ошибка чтения не
// блокирует сессию — агент просто получит пустой список.
func (s *Server) instanceBindings(ctx context.Context, log *slog.Logger, agentID uuid.UUID) []*agentv1.InstanceBinding {
	host, err := s.db.Hosts.GetByAgentID(ctx, agentID)
	if err != nil {
		log.Error("bindings: хост агента не найден", "err", err)
		return nil
	}
	instances, _, err := s.db.Instances.List(ctx, host.ID, uuid.Nil, uuid.Nil, 100)
	if err != nil {
		log.Error("bindings: список инстансов хоста", "host_id", host.ID, "err", err)
		return nil
	}
	out := make([]*agentv1.InstanceBinding, 0, len(instances))
	for _, inst := range instances {
		out = append(out, &agentv1.InstanceBinding{
			InstanceId: inst.ID.String(),
			Name:       inst.Name,
			ConfigPath: inst.ConfigPath,
			RulesDir:   inst.RulesDir,
			LogDir:     inst.LogDir,
		})
	}
	return out
}

// --- разбор jsonb-колонок desired/actual в структуры compliance ---

// computedRule — элемент desired_state.computed_rules ([{sid,rev,status}]).
type computedRule struct {
	Sid int64 `json:"sid"`
}

func sidsFromComputed(raw json.RawMessage) []int64 {
	var rules []computedRule
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil
	}
	out := make([]int64, 0, len(rules))
	for _, r := range rules {
		out = append(out, r.Sid)
	}
	return out
}

// loadedRule — элемент actual_state.loaded_rules ([{sid,rev}]).
type loadedRule struct {
	Sid int64 `json:"sid"`
}

func sidsFromLoaded(raw json.RawMessage) []int64 {
	var rules []loadedRule
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil
	}
	out := make([]int64, 0, len(rules))
	for _, r := range rules {
		out = append(out, r.Sid)
	}
	return out
}

func failedFromJSON(raw json.RawMessage) []compliance.FailedRule {
	var rules []compliance.FailedRule
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil
	}
	return rules
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func strp(s string) *string { return &s }

// configDeployStatus — статус записи истории применения конфигурации
// (instance_config_history): validate_only ok → validated; ok → applied;
// не пройден suricata -T (ValidationPassed=false) → validation_failed;
// иной провал (запись/restart) → deploy_failed.
func configDeployStatus(succeeded, validateOnly, validationPassed bool) string {
	switch {
	case succeeded && validateOnly:
		return "validated"
	case succeeded:
		return "applied"
	case !validationPassed:
		return "validation_failed"
	default:
		return "deploy_failed"
	}
}
