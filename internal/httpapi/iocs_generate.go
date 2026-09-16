package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/surifleet/surifleet/internal/iocrules"
	"github.com/surifleet/surifleet/internal/orchestrator"
	"github.com/surifleet/surifleet/internal/ruleset"
	"github.com/surifleet/surifleet/internal/store"
)

// iocRulesetVersion — префикс имени версии ruleset из IOC-правил
// (полное имя content-addressed: "ioc-current-<sha8>").
const iocRulesetVersion = "ioc-current"

// iocGenerateInput — POST /api/v1/iocs/generate (openapi IocGenerateInput).
type iocGenerateInput struct {
	Deploy      bool           `json:"deploy"`     // деплой собранного ruleset (только по явному запросу)
	Targeting   targetingInput `json:"targeting"`  // обязателен при deploy=true
	BatchSize   int            `json:"batch_size"` // дефолты — как у POST /deployments
	Concurrency int            `json:"concurrency"`
	CanarySize  int            `json:"canary_size"`
}

// iocSkip — IOC, для которого правило не сгенерировано (с причиной).
type iocSkip struct {
	ID     uuid.UUID `json:"id"`
	Type   string    `json:"type"`
	Value  string    `json:"value"`
	Reason string    `json:"reason"`
}

// iocGenerateResult — ответ POST /iocs/generate (openapi IocGenerateResult).
type iocGenerateResult struct {
	SweptExpired   int64      `json:"swept_expired"` // IOC, погашенные свипом перед генерацией
	Active         int        `json:"active"`        // всего активных IOC
	Created        int        `json:"created"`       // новые правила
	Updated        int        `json:"updated"`       // изменившиеся (raw отличался)
	Unchanged      int        `json:"unchanged"`     // идемпотентный повтор
	Skipped        []iocSkip  `json:"skipped"`       // не маппятся на правило (hash/email/битый url)
	RulesetID      *uuid.UUID `json:"ruleset_id"`
	RulesetVersion string     `json:"ruleset_version,omitempty"`
	RulesetCreated bool       `json:"ruleset_created,omitempty"`
	RulesCount     int        `json:"rules_count,omitempty"`
	DeploymentID   *uuid.UUID `json:"deployment_id,omitempty"`
}

// generateIocRules — POST /api/v1/iocs/generate (bulk-вариант описанного
// в ТЗ POST /iocs/{id}/deploy): свип просроченных IOC → детерминированная
// генерация Suricata-правил (source=ioc, sid 8800000..8899999) → сборка
// ruleset "ioc-current" (всегда, если есть хоть одно ioc-правило) →
// опциональный деплой (только при deploy=true с явным targeting).
func (h *handlers) generateIocRules(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	var in iocGenerateInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Deploy && in.Targeting.Mode == "" {
		writeError(w, http.StatusBadRequest, CodeValidation,
			"targeting.mode: обязательное поле при deploy=true", nil)
		return
	}

	res := iocGenerateResult{Skipped: []iocSkip{}}

	// Свип: просроченные active → expired, чтобы не попасть в генерацию.
	swept, err := h.d.Store.Iocs.SweepExpired(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	res.SweptExpired = swept

	iocs, err := h.d.Store.Iocs.ListActiveForGeneration(r.Context(), orgID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	res.Active = len(iocs)

	for _, ioc := range iocs {
		raw, ok, reason := iocrules.RuleFor(ioc.Type, ioc.Value)
		if !ok {
			res.Skipped = append(res.Skipped, iocSkip{ID: ioc.ID, Type: ioc.Type, Value: ioc.Value, Reason: reason})
			continue
		}
		sid, err := h.resolveIocSID(r, orgID, ioc)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		raw, _, _ = iocrules.RuleWithSID(ioc.Type, ioc.Value, sid)

		parsed, _ := json.Marshal(map[string]string{"ioc_id": ioc.ID.String(), "ioc_type": ioc.Type})
		rule, outcome, err := h.d.Store.Rules.UpsertImport(r.Context(), orgID, store.ImportItem{
			SID: sid, Rev: iocrules.FormatRev,
			Msg:    iocrules.MsgFor(ioc.Type, ioc.Value),
			Raw:    raw,
			Parsed: parsed,
		}, "ioc", "ioc")
		if err != nil {
			writeStoreError(w, err)
			return
		}
		// IOC-правила сразу enabled (генерируются только из активных IOC);
		// UpsertImport создаёт в under_review и тюнинг не перетирает.
		if rule.Status != "enabled" {
			enabled := "enabled"
			rule, err = h.d.Store.Rules.Update(r.Context(), rule.ID, store.RulePatch{Status: &enabled})
			if err != nil {
				writeStoreError(w, err)
				return
			}
		}
		switch outcome {
		case store.UpsertImported:
			res.Created++
		case store.UpsertUpdated:
			res.Updated++
		default:
			res.Unchanged++
		}
	}

	// Ruleset собираем всегда, когда в репозитории есть хоть одно enabled
	// ioc-правило (могли остаться от ранее истёкших IOC — состав полный).
	if err := h.buildIocRuleset(w, r, orgID, &res, in); err != nil {
		return // buildIocRuleset уже записал ответ (ошибку или готово)
	}
	writeJSON(w, http.StatusOK, res)
}

// resolveIocSID — sid для IOC с пробингом хэш-коллизий: слот считается
// своим, если он свободен или занят правилом этого же IOC (msg + source=ioc);
// иначе линейный пробинг внутри диапазона iocrules.
func (h *handlers) resolveIocSID(r *http.Request, orgID uuid.UUID, ioc store.IocForGeneration) (int64, error) {
	sid := iocrules.SidFor(ioc.Type, ioc.Value)
	want := iocrules.MsgFor(ioc.Type, ioc.Value)
	for range iocrules.SidRange {
		existing, err := h.d.Store.Rules.GetBySid(r.Context(), orgID, sid)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return sid, nil // свободный слот
			}
			return 0, err
		}
		if existing.SourceType == "ioc" && existing.Msg != nil && *existing.Msg == want {
			return sid, nil // наш слот (идемпотентный повтор)
		}
		sid = iocrules.SidBase + (sid-iocrules.SidBase+1)%iocrules.SidRange
	}
	return 0, fmt.Errorf("sid-пространство IOC исчерпано (коллизии)")
}

// buildIocRuleset — сборка ruleset "ioc-current" из всех enabled ioc-правил
// (content-addressed: тот же состав → та же версия, 200 без дублей) и,
// при in.Deploy, запуск деплоя через общий конвейер. При ошибке пишет ответ
// и возвращает её; при успехе заполняет res и возвращает nil.
func (h *handlers) buildIocRuleset(w http.ResponseWriter, r *http.Request, orgID uuid.UUID, res *iocGenerateResult, in iocGenerateInput) error {
	raw, err := h.d.Store.Rules.SelectRawForBuild(r.Context(), orgID, store.RuleFilter{Source: "ioc", Status: "enabled"})
	if err != nil {
		writeStoreError(w, err)
		return err
	}
	if len(raw) == 0 {
		return nil // правил нет — ruleset не собираем
	}

	rules := make([]ruleset.RawRule, len(raw))
	manifestRules := make([]manifestRule, len(raw))
	for i, rr := range raw {
		rules[i] = ruleset.RawRule{SID: rr.SID, Raw: rr.Raw}
		manifestRules[i] = manifestRule{Sid: rr.SID, Rev: rr.Rev}
	}
	blob := ruleset.Render(rules)
	sha := ruleset.SHA256(blob)
	key := ruleset.BlobKey(sha)

	if _, err := h.d.Blob.PutIfAbsent(r.Context(), key, blob); err != nil {
		errLog.Error("загрузка ioc ruleset-блоба в S3", "err", err)
		writeError(w, http.StatusInternalServerError, CodeInternal, "загрузка блоба в хранилище", nil)
		return err
	}

	manifest, _ := json.Marshal(map[string]any{
		"count": len(manifestRules),
		"note":  "автосборка из активных IOC (POST /iocs/generate)",
		"rules": manifestRules,
	})
	// ruleset_versions UNIQUE (organization_id, version): имя делаем
	// content-addressed ("ioc-current-<sha8>"), иначе повторная сборка с
	// новым составом упёрлась бы в конфликт уникальности версии.
	version := iocRulesetVersion + "-" + sha[:8]
	v, created, err := h.d.Store.Rulesets.Create(r.Context(), orgID, version, sha, key, manifest, len(manifestRules))
	if err != nil {
		writeStoreError(w, err)
		return err
	}
	res.RulesetID = &v.ID
	res.RulesetVersion = v.Version
	res.RulesetCreated = created
	res.RulesCount = len(manifestRules)

	if !in.Deploy {
		return nil
	}

	// Деплой — по общему конвейеру createDeployment (таргетинг из тела).
	if in.BatchSize <= 0 {
		in.BatchSize = 50
	}
	if in.Concurrency <= 0 {
		in.Concurrency = 10
	}
	instanceIDs, ok := h.resolveTargets(w, r, orgID, in.Targeting)
	if !ok {
		return errors.New("таргетинг не разрешился")
	}
	if len(instanceIDs) == 0 {
		writeError(w, http.StatusBadRequest, CodeValidation,
			"таргетинг не выбрал ни одного инстанса", nil)
		return errors.New("пустой таргетинг")
	}
	targetingRaw, _ := json.Marshal(in.Targeting)
	d := store.Deployment{
		OrganizationID:   orgID,
		RulesetVersionID: v.ID,
		Targeting:        targetingRaw,
		BatchSize:        in.BatchSize,
		Concurrency:      in.Concurrency,
		CanarySize:       in.CanarySize,
	}
	waves := orchestrator.ComputeWaves(instanceIDs, in.CanarySize, in.BatchSize)
	dep, err := h.d.Store.Deployments.Create(r.Context(), d, waves)
	if err != nil {
		writeStoreError(w, err)
		return err
	}
	computed := computedRulesFromManifest(v.Manifest)
	for _, id := range instanceIDs {
		if err := h.d.Store.DesiredState.Upsert(r.Context(), id, v.ID, computed); err != nil {
			errLog.Error("desired_state upsert (ioc)", "instance_id", id, "err", err)
		}
	}
	h.d.Orch.Start(dep.ID)
	res.DeploymentID = &dep.ID
	return nil
}
