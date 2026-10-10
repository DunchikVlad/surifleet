// Package suriupdate — импорт правил, подтянутых suricata-update на
// агенте, в мастер-репозиторий (общий список правил, чанк 82). Агент
// заливает итоговый suricata.rules на presigned PUT в S3; по результату
// задачи (TaskResult.suricata_update с uploaded_bytes) сервер читает
// блоб, разбирает парсером internal/rules и идемпотентно мержит через
// Rules.UpsertImport (source_type='file', ревизии по изменениям raw).
package suriupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/surifleet/surifleet/internal/blob"
	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
	"github.com/surifleet/surifleet/internal/rules"
	"github.com/surifleet/surifleet/internal/store"
)

// MaxImportErrors — лимит ошибок разбора при импорте (0 — без лимита;
// suricata-наборы большие, ошибки не должны ронять импорт).
const maxParseErrors = 100

// HandleResult — колбэк результата задачи (цепочка за OnTaskResult
// оркестратора): успешный suricata_update с залитым набором → импорт
// блоба в мастер-репозиторий организации агента. Ошибки импорта — в лог,
// на задачу не влияют (набор уже на сенсоре применён).
func HandleResult(ctx context.Context, log *slog.Logger, st *store.Store, b *blob.Store, agentID uuid.UUID, res *agentv1.TaskResult) {
	su := res.GetSuricataUpdate()
	if su == nil || su.GetUploadedBytes() == 0 || su.GetUploadKey() == "" {
		return
	}
	if res.GetStatus() != agentv1.TaskStatus_TASK_STATUS_SUCCESS {
		return
	}
	log = log.With("task_id", res.GetTaskId(), "agent_id", agentID, "upload_key", su.GetUploadKey())

	orgID, err := orgByAgent(ctx, st, agentID)
	if err != nil {
		log.Error("suriupdate: организация агента", "err", err)
		return
	}
	data, err := b.Get(ctx, su.GetUploadKey())
	if err != nil {
		log.Error("suriupdate: чтение блоба набора", "err", err)
		return
	}
	// Карта sid → источник (чанк 95): блоб есть — поднимаем source_name.
	srcBySid := map[int64]string{}
	if su.GetSourcesKey() != "" && su.GetSourcesBytes() > 0 {
		if mraw, merr := b.Get(ctx, su.GetSourcesKey()); merr == nil {
			_ = json.Unmarshal(mraw, &srcBySid)
		} else {
			log.Warn("suriupdate: карта источников не прочитана", "err", merr)
		}
	}
	parsed := rules.ParseReader(bytes.NewReader(data), maxParseErrors)
	var imported, updated, unchanged int
	for _, p := range parsed.Rules {
		parsedJSON, err := json.Marshal(p)
		if err != nil {
			continue
		}
		// ET Open и др. — доверенные фиды: suricata-update применяет их
		// включёнными, значит и в репозитории новые — enabled (тюнинг
		// аналитика при перевыпусках не перетирается).
		_, outcome, err := st.Rules.UpsertImport(ctx, orgID, store.ImportItem{
			SID: p.SID, Rev: p.Rev, Msg: p.Msg, Classtype: p.Classtype,
			Raw: p.Raw, Parsed: parsedJSON, Origin: "suriupdate",
			InitialStatus: "enabled", SourceName: srcBySid[p.SID],
		}, "", "file")
		if err != nil {
			log.Error("suriupdate: upsert правила", "sid", p.SID, "err", err)
			continue
		}
		switch outcome {
		case store.UpsertImported:
			imported++
		case store.UpsertUpdated:
			updated++
		case store.UpsertUnchanged:
			unchanged++
		}
	}
	if len(srcBySid) > 0 {
		if n, serr := st.Rules.SetSourceNames(ctx, orgID, srcBySid); serr != nil {
			log.Warn("suriupdate: простановка source_name", "err", serr)
		} else {
			log.Info("suriupdate: source_name обновлены", "rules", n)
		}
	}
	log.Info("suriupdate: набор импортирован в мастер-репозиторий",
		"rules", len(parsed.Rules), "imported", imported, "updated", updated,
		"unchanged", unchanged, "parse_errors", len(parsed.Errors))
}

// orgByAgent — организация через агента → хост → кластер.
func orgByAgent(ctx context.Context, st *store.Store, agentID uuid.UUID) (uuid.UUID, error) {
	agent, err := st.Agents.GetByID(ctx, agentID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("агент: %w", err)
	}
	host, err := st.Hosts.Get(ctx, agent.HostID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("хост: %w", err)
	}
	cluster, err := st.Clusters.Get(ctx, host.ClusterID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("кластер: %w", err)
	}
	return cluster.OrganizationID, nil
}
