package iocrules

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/surifleet/surifleet/internal/store"
)

// RevokedTag — метка в rules.tags у отозванного IOC-правила (чанк 19).
// Пометка именно в тегах, а не в msg: msg — ключ владения слотом sid
// (см. пробинг коллизий в генераторе), его изменение сломало бы
// идемпотентность повторной генерации.
const RevokedTag = "ioc-revoked"

// RuleStore — минимальный интерфейс репозитория правил для RevokeForIoc
// (*store.RulesRepo удовлетворяет; в тестах — подделка).
type RuleStore interface {
	GetBySid(ctx context.Context, orgID uuid.UUID, sid int64) (store.Rule, error)
	Update(ctx context.Context, id uuid.UUID, p store.RulePatch) (store.Rule, error)
}

// RevokeForIoc — отзыв правила, сгенерированного из IOC: правило
// (source_type='ioc', слот sid — тот же пробинг, что у генератора)
// переводится в status='disabled' + тег RevokedTag. Выбираем disabled,
// а не deleted: отзыв обратим — при возврате IOC в active повторная
// генерация снова включит правило.
//
// Правила нет (IOC не маппился на правило / не генерировался), слот занят
// чужим правилом до конца пробинга, либо правило уже disabled/deleted —
// (false, nil). Вызывается после revoke/delete/expire IOC.
func RevokeForIoc(ctx context.Context, orgID uuid.UUID, typ, value string, rs RuleStore) (bool, error) {
	sid := SidFor(typ, value)
	want := MsgFor(typ, value)
	for range SidRange {
		rule, err := rs.GetBySid(ctx, orgID, sid)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return false, nil // свободный слот — правила точно нет дальше
			}
			return false, err
		}
		if rule.SourceType != "ioc" || rule.Msg == nil || *rule.Msg != want {
			sid = SidBase + (sid-SidBase+1)%SidRange // чужой слот — пробинг дальше
			continue
		}
		if rule.Status == "disabled" || rule.Status == "deleted" {
			return false, nil // уже отозвано
		}
		tags := rule.Tags
		if !hasTag(tags, RevokedTag) {
			tags = append(append([]string{}, tags...), RevokedTag)
		}
		disabled := "disabled"
		if _, err := rs.Update(ctx, rule.ID, store.RulePatch{Status: &disabled, Tags: tags}); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// hasTag — есть ли тег в списке.
func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}
