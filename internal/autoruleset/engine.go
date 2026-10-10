// Package autoruleset — движок авто-обновляемых ruleset'ов (чанк 92):
// пересборка определения (internal/store.AutoRuleset) в новую версию
// ruleset и волновой деплой на таргетинг. Триггеры: кнопка (API
// POST /auto_rulesets/{id}/rebuild) и импорт suricata-update
// (HandleSuriupdateResult в цепочке OnTaskResult).
package autoruleset

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/surifleet/surifleet/internal/blob"
	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
	"github.com/surifleet/surifleet/internal/orchestrator"
	"github.com/surifleet/surifleet/internal/ruleset"
	"github.com/surifleet/surifleet/internal/store"
)

// RebuildResult — итог пересборки.
type RebuildResult struct {
	Version    store.RulesetVersion
	Deployment uuid.UUID
	Instances  int
	Skipped    bool // нечего деплоить (нет правил/инстансов) — версия не создана
}

// Rebuild — собрать авто-ruleset заново: выборка raw-правил по origin/
// тегам/категориям минус exclude_sids → блоб → версия ruleset →
// резолв таргетинга → деплой (kind=rules) → старт оркестратора.
func Rebuild(ctx context.Context, log *slog.Logger, st *store.Store, b *blob.Store, orch *orchestrator.Orchestrator, def store.AutoRuleset) (*RebuildResult, error) {
	log = log.With("auto_ruleset_id", def.ID, "name", def.Name)

	var origins []string
	if def.IncludeSuriupdate {
		origins = append(origins, "suriupdate")
	}
	if def.IncludeIoc {
		origins = append(origins, "ioc")
	}
	if def.IncludeManual {
		origins = append(origins, "manual")
	}
	if def.IncludeFeeds {
		origins = append(origins, "feed")
	}
	if len(origins) == 0 {
		return nil, fmt.Errorf("состав пуст: все include_* выключены")
	}

	raw, err := st.Rules.SelectRawByOrigins(ctx, def.OrganizationID, origins,
		def.IncludeTags, def.IncludeCategories, def.IncludeSources, def.ExcludeSids)
	if err != nil {
		return nil, fmt.Errorf("выборка правил: %w", err)
	}
	if len(raw) == 0 {
		return &RebuildResult{Skipped: true}, nil
	}

	rr := make([]ruleset.RawRule, len(raw))
	manifestRules := make([]map[string]any, len(raw))
	for i, x := range raw {
		rr[i] = ruleset.RawRule{SID: x.SID, Raw: x.Raw}
		manifestRules[i] = map[string]any{"sid": x.SID, "rev": x.Rev}
	}
	data := ruleset.Render(rr)
	sha := ruleset.SHA256(data)
	key := ruleset.BlobKey(sha)
	if _, err := b.PutIfAbsent(ctx, key, data); err != nil {
		return nil, fmt.Errorf("загрузка блоба: %w", err)
	}

	manifest, _ := json.Marshal(map[string]any{
		"count":          len(manifestRules),
		"auto_ruleset":   def.ID.String(),
		"exclude_sids":   def.ExcludeSids,
		"rules":          manifestRules,
		"built_at":       time.Now().UTC().Format(time.RFC3339),
	})
	version, err := st.Rulesets.NextAutoVersion(ctx, def.OrganizationID)
	if err != nil {
		return nil, err
	}
	v, _, err := st.Rulesets.Create(ctx, def.OrganizationID, version, sha, key, manifest, len(manifestRules))
	if err != nil {
		return nil, fmt.Errorf("создание версии ruleset: %w", err)
	}

	// Таргетинг → инстансы (системный контекст, без scoping-пользователя).
	var t struct {
		Mode        string   `json:"mode"`
		ClusterIDs  []string `json:"cluster_ids"`
		HostIDs     []string `json:"host_ids"`
		InstanceIDs []string `json:"instance_ids"`
	}
	if len(def.Targeting) > 0 {
		_ = json.Unmarshal(def.Targeting, &t)
	}
	if t.Mode == "" {
		t.Mode = "all_clusters"
	}
	instanceIDs, err := resolveTargets(ctx, st, def.OrganizationID, t)
	if err != nil {
		return nil, err
	}
	if len(instanceIDs) == 0 {
		return &RebuildResult{Version: v, Skipped: true}, nil
	}

	targetingRaw, _ := json.Marshal(t)
	dep, err := st.Deployments.Create(ctx, store.Deployment{
		OrganizationID:   def.OrganizationID,
		Kind:             "rules",
		RulesetVersionID: &v.ID,
		Targeting:        targetingRaw,
		BatchSize:        def.BatchSize,
		Concurrency:      10,
		CanarySize:       def.CanarySize,
	}, orchestrator.ComputeWaves(instanceIDs, def.CanarySize, def.BatchSize))
	if err != nil {
		return nil, fmt.Errorf("создание деплоя: %w", err)
	}
	computed, _ := json.Marshal(manifestRules)
	for _, id := range instanceIDs {
		if err := st.DesiredState.Upsert(ctx, id, v.ID, computed); err != nil {
			log.Error("desired_state upsert (auto)", "instance_id", id, "err", err)
		}
	}
	orch.Start(dep.ID)
	if err := st.AutoRulesets.SetLastBuild(ctx, def.ID, v.ID, dep.ID); err != nil {
		log.Error("фиксация пересборки", "err", err)
	}
	log.Info("auto-ruleset пересобран", "version", v.Version, "rules", len(raw),
		"instances", len(instanceIDs), "deployment", dep.ID)
	return &RebuildResult{Version: v, Deployment: dep.ID, Instances: len(instanceIDs)}, nil
}

// RunScheduled — пересборка по расписанию (чанк 96): включённые определения
// с schedule_time 'HH:MM'; due, если сегодняшний момент расписания прошёл,
// а последняя сборка была раньше его (или ни разу не была).
func RunScheduled(ctx context.Context, log *slog.Logger, st *store.Store, b *blob.Store, orch *orchestrator.Orchestrator, now time.Time) int {
	defs, err := st.AutoRulesets.ListScheduled(ctx)
	if err != nil {
		log.Error("auto: список расписаний", "err", err)
		return 0
	}
	n := 0
	for _, def := range defs {
		if def.ScheduleTime == nil || !due(def, now) {
			continue
		}
		if _, err := Rebuild(ctx, log, st, b, orch, def); err != nil {
			log.Error("auto: пересборка по расписанию", "auto_ruleset_id", def.ID, "err", err)
			continue
		}
		n++
	}
	return n
}

// due — момент расписания сегодня прошёл и сборка была раньше него.
func due(def store.AutoRuleset, now time.Time) bool {
	var hh, mm int
	if _, err := fmt.Sscanf(*def.ScheduleTime, "%d:%d", &hh, &mm); err != nil {
		return false
	}
	at := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, now.Location())
	if now.Before(at) {
		return false
	}
	return def.LastBuiltAt == nil || def.LastBuiltAt.Before(at)
}

// HandleSuriupdateResult — триггер после успешного импорта suricata-update:
// пересобирает включённые авто-ruleset'ы организации с include_suriupdate.
func HandleSuriupdateResult(ctx context.Context, log *slog.Logger, st *store.Store, b *blob.Store, orch *orchestrator.Orchestrator, agentID uuid.UUID, res *agentv1.TaskResult) {
	// Отсоединённый контекст — как в suriupdate (хаб отменяет свой).
	bg, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	ctx = bg
	su := res.GetSuricataUpdate()
	if su == nil || res.GetStatus() != agentv1.TaskStatus_TASK_STATUS_SUCCESS {
		return
	}
	orgID, err := orgByAgent(ctx, st, agentID)
	if err != nil {
		log.Error("auto: организация агента", "err", err)
		return
	}
	defs, err := st.AutoRulesets.ListEnabledForRebuild(ctx, orgID)
	if err != nil {
		log.Error("auto: список авто-ruleset'ов", "err", err)
		return
	}
	for _, def := range defs {
		if _, err := Rebuild(ctx, log, st, b, orch, def); err != nil {
			log.Error("auto: пересборка", "auto_ruleset_id", def.ID, "err", err)
		}
	}
}

// resolveTargets — targeting авто-ruleset'а → id инстансов (как в API,
// но без scoping-пользователя: системный триггер).
func resolveTargets(ctx context.Context, st *store.Store, orgID uuid.UUID, t struct {
	Mode        string   `json:"mode"`
	ClusterIDs  []string `json:"cluster_ids"`
	HostIDs     []string `json:"host_ids"`
	InstanceIDs []string `json:"instance_ids"`
}) ([]uuid.UUID, error) {
	toUUIDs := func(ss []string) ([]uuid.UUID, error) {
		out := make([]uuid.UUID, 0, len(ss))
		for _, s := range ss {
			id, err := uuid.Parse(s)
			if err != nil {
				return nil, fmt.Errorf("невалидный UUID в targeting: %s", s)
			}
			out = append(out, id)
		}
		return out, nil
	}
	switch t.Mode {
	case "all_clusters":
		return st.Instances.IDsForOrg(ctx, orgID, []uuid.UUID{})
	case "all_except_clusters":
		excl, err := toUUIDs(t.ClusterIDs)
		if err != nil {
			return nil, err
		}
		return st.Instances.IDsForOrg(ctx, orgID, excl)
	case "selected_clusters":
		ids, err := toUUIDs(t.ClusterIDs)
		if err != nil {
			return nil, err
		}
		return st.Instances.IDsForClusters(ctx, ids)
	case "specific_hosts":
		ids, err := toUUIDs(t.HostIDs)
		if err != nil {
			return nil, err
		}
		return st.Instances.IDsForHosts(ctx, ids)
	case "specific_instances":
		ids, err := toUUIDs(t.InstanceIDs)
		if err != nil {
			return nil, err
		}
		return st.Instances.ExistingIDs(ctx, orgID, ids)
	}
	return nil, fmt.Errorf("неизвестный targeting.mode: %q", t.Mode)
}

// orgByAgent — организация через агента → хост → кластер (как в suriupdate).
func orgByAgent(ctx context.Context, st *store.Store, agentID uuid.UUID) (uuid.UUID, error) {
	agent, err := st.Agents.GetByID(ctx, agentID)
	if err != nil {
		return uuid.Nil, err
	}
	host, err := st.Hosts.Get(ctx, agent.HostID)
	if err != nil {
		return uuid.Nil, err
	}
	cluster, err := st.Clusters.Get(ctx, host.ClusterID)
	if err != nil {
		return uuid.Nil, err
	}
	return cluster.OrganizationID, nil
}
