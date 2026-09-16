// Коннектор фидов ПРАВИЛ Emerging Threats (чанк 21): type=et_open.
// В отличие от generic (IOC), фид et_open импортирует Suricata-правила
// в мастер-репозиторий rules (НЕ в iocs): HTTP GET .rules-файла → разбор
// (парсер internal/rules) → идемпотентный upsert по (org, sid) с
// source_type='et_open' и feed_id фида.
//
// Выключенные в фиде правила ET (строки "#alert ..." — комментарий без
// пробела перед action) импортируются со статусом disabled (только при
// создании; дальше статус — дело аналитика, импорт его не перетирает).
// Прочие строки-комментарии («# ...») пропускаются.
//
// URL по умолчанию (подставляется HTTP-слоем при создании фида без URL) —
// DefaultETOpenURL; поддерживается любой URL на .rules-файл (отдельные
// категории ET, свой файл и т.п.). et_pro отличается только URL с кодом
// подписки и пока не включён (останется failed до отдельного решения).
package feedsync

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/surifleet/surifleet/internal/rules"
	"github.com/surifleet/surifleet/internal/store"
)

// DefaultETOpenURL — полный набор правил ET Open (~30k правил).
const DefaultETOpenURL = "https://rules.emergingthreats.net/open/suricata/rules/emerging-all.rules"

// ParsedRule — правило из .rules-фида с признаком «выключено в фиде».
type ParsedRule struct {
	*rules.Parsed
	Disabled bool
}

// ParseRules разбирает тело .rules-фида: активные правила и выключенные
// («#alert ...»/«#drop ...» и т.п.). Комментарии и пустые строки
// пропускаются; битые строки — в ошибки (первые maxLineErrors, номера
// 1-based), разбор не прерывается. Строки-continuation ('\' в конце)
// склеиваются. У выключенных правил Raw — текст без ведущего '#', чтобы
// хэш ревизии был стабилен между синками.
func ParseRules(body []byte) ([]ParsedRule, []rules.LineError) {
	body = bytes.TrimPrefix(body, []byte("\xef\xbb\xbf")) // UTF-8 BOM
	out := []ParsedRule{}
	errs := []rules.LineError{}
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 256*1024), 4*1024*1024) // длинные правила (base64-блобы)
	var logical strings.Builder
	logicalStart, lineNo := 0, 0
	flush := func() {
		if logical.Len() == 0 {
			return
		}
		text := strings.TrimSpace(logical.String())
		ln := logicalStart
		logical.Reset()
		if text == "" {
			return
		}
		if strings.HasPrefix(text, "#") {
			// Выключенное правило ET: '#' сразу перед action. Если остаток
			// не парсится как правило — это обычный комментарий, пропускаем.
			if p, err := rules.Parse(strings.TrimSpace(text[1:])); err == nil && p != nil {
				out = append(out, ParsedRule{Parsed: p, Disabled: true})
			}
			return
		}
		p, err := rules.Parse(text)
		if err != nil {
			if len(errs) < maxLineErrors {
				errs = append(errs, rules.LineError{Line: ln, Reason: err.Error()})
			}
			return
		}
		if p != nil {
			out = append(out, ParsedRule{Parsed: p})
		}
	}
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		if logical.Len() == 0 {
			logicalStart = lineNo
		}
		if strings.HasSuffix(strings.TrimRight(line, " \t"), "\\") {
			logical.WriteString(strings.TrimSuffix(strings.TrimRight(line, " \t"), "\\"))
			continue
		}
		logical.WriteString(line)
		flush()
	}
	flush() // файл без перевода строки в конце / висячий continuation
	return out, errs
}

// syncRules — ветка синхронизации фида правил (type=et_open): разбор
// .rules-тела и upsert в репозиторий rules. Счётчики run: imported —
// новые sid, updated — изменившийся raw (новая ревизия), skipped —
// битые строки и ошибки БД; совпавшие без изменений не входят в счётчики,
// их число фиксируется в error-сообщении частичного/полного успеха.
func (s *Syncer) syncRules(ctx context.Context, feed store.Feed, run store.FeedRun, body []byte,
	fail func(string) (store.FeedRun, error)) (store.FeedRun, error) {
	log := s.log()

	parsed, lineErrs := ParseRules(body)
	if len(parsed) == 0 {
		msg := "фид не содержит валидных правил"
		if len(lineErrs) > 0 {
			msg = fmt.Sprintf("%s (%d ошибочных строк, первая: %s)", msg, len(lineErrs), lineErrs[0].Reason)
		}
		return fail(msg)
	}

	imported, updated, unchanged, skipped := 0, 0, 0, 0
	var firstErr string
	for _, pr := range parsed {
		parsedJSON, err := json.Marshal(pr.Parsed)
		if err != nil {
			skipped++
			if firstErr == "" {
				firstErr = fmt.Sprintf("sid %d: %v", pr.SID, err)
			}
			continue
		}
		initialStatus := ""
		if pr.Disabled {
			initialStatus = "disabled"
		}
		_, outcome, err := s.Store.Rules.UpsertImport(ctx, feed.OrganizationID, store.ImportItem{
			SID: pr.SID, Rev: pr.Rev, Msg: pr.Msg, Classtype: pr.Classtype,
			Raw: pr.Raw, Parsed: parsedJSON,
			FeedID: &feed.ID, InitialStatus: initialStatus,
		}, "", "et_open")
		if err != nil {
			skipped++
			if firstErr == "" {
				firstErr = fmt.Sprintf("sid %d: %v", pr.SID, err)
			}
			continue
		}
		switch outcome {
		case store.UpsertImported:
			imported++
		case store.UpsertUpdated:
			updated++
		default:
			unchanged++
		}
	}
	skipped += len(lineErrs)

	status := "success"
	var syncErr *string
	var details []string
	if unchanged > 0 {
		details = append(details, fmt.Sprintf("без изменений %d", unchanged))
	}
	if skipped > 0 {
		d := fmt.Sprintf("пропущено %d некорректных записей", skipped)
		if firstErr != "" {
			d += ", первая: " + firstErr
		} else if len(lineErrs) > 0 {
			d += ", первая: " + lineErrs[0].Reason
		}
		details = append(details, d)
	}
	if len(details) > 0 {
		msg := strings.Join(details, "; ")
		syncErr = &msg // частичный успех: статус success, детали — в last_error
	}

	run, err := s.Store.Feeds.FinishRun(ctx, run.ID, status, imported, updated, skipped, syncErr)
	if err != nil {
		return store.FeedRun{}, fmt.Errorf("закрытие feed_run: %w", err)
	}
	if err := s.Store.Feeds.MarkSync(ctx, feed.ID, status, syncErr); err != nil {
		return run, fmt.Errorf("обновление last_sync фида: %w", err)
	}
	log.Info("feedsync: фид правил синхронизирован",
		"feed_id", feed.ID, "name", feed.Name,
		"imported", imported, "updated", updated, "unchanged", unchanged, "skipped", skipped)
	return run, nil
}
