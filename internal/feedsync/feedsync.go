// Package feedsync — синхронизация фидов: загрузка по HTTP(S) и разбор.
// type=generic — IOC-листы (plain text / CSV / JSON) с угадыванием типа
// IOC и идемпотентным импортом в iocs (UpsertImport); type=et_open/et_pro —
// фиды ПРАВИЛ Emerging Threats Open/Pro (.rules-файл) с импортом в
// репозиторий rules (см. rulesfeed.go); type=taxii — TAXII 2.x/STIX
// и type=stix — статический STIX bundle/JSON по URL (индикаторы → iocs,
// см. taxii.go).
//
// Поддерживаемые форматы тела:
//   - plain text: один IOC на строку, '#' и '//' — комментарии;
//   - CSV: "value,type" или "type,value" (тип — из известного набора);
//   - JSON: массив строк (значения) или объектов {type, value, score}.
//
// Тип угадывается по значению: IP/CIDR, домен, URL, md5/sha1/sha256.
package feedsync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/surifleet/surifleet/internal/rules"
	"github.com/surifleet/surifleet/internal/store"
)

// Дефолты синхронизации.
const (
	defaultTimeout  = 30 * time.Second
	defaultMaxBytes = 32 << 20 // 32 МБ на тело фида
	// defaultScore — скоринг IOC, пришедших из фида без явного score.
	defaultScore = 50
	// maxLineErrors — сколько первых ошибок разбора копим в run.Error
	// (когда синк в целом успешен, но часть строк мусорная).
	maxLineErrors = 20
)

// Syncer выполняет синхронизацию фидов (используется HTTP-хендлером
// ручного синка и фоновым планировщиком).
type Syncer struct {
	Store *store.Store
	// Client — HTTP-клиент загрузки; nil → дефолт с таймаутом defaultTimeout.
	Client *http.Client
	// MaxBytes — предел размера тела фида; 0 → defaultMaxBytes.
	MaxBytes int64
	// Log — необязательный логгер (nil — тихо).
	Log *slog.Logger
}

func (s *Syncer) httpClient() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return &http.Client{Timeout: defaultTimeout}
}

func (s *Syncer) maxBytes() int64 {
	if s.MaxBytes > 0 {
		return s.MaxBytes
	}
	return defaultMaxBytes
}

func (s *Syncer) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Sync — полный цикл синхронизации фида: создаёт feed_run (running),
// загружает и разбирает тело, импортирует IOC (UpsertImport, source = имя
// фида, feed_id = id фида), закрывает run и обновляет last_sync_* фида.
//
// Ошибки загрузки/разбора — НЕ возвращаются как error: они фиксируются
// в run (status=failed, error=...) и last_error фида, HTTP-слой отвечает
// 200 с описанием run. error возвращается только при сбое слоя БД.
func (s *Syncer) Sync(ctx context.Context, feed store.Feed) (store.FeedRun, error) {
	log := s.log()
	run, err := s.Store.Feeds.CreateRun(ctx, feed.ID)
	if err != nil {
		return store.FeedRun{}, fmt.Errorf("создание feed_run: %w", err)
	}

	// fail/succeed закрывают run и карточку фида; ошибки БД логируем —
	// они не должны маскировать исходный результат синка.
	fail := func(msg string) (store.FeedRun, error) {
		run, err = s.Store.Feeds.FinishRun(ctx, run.ID, "failed", 0, 0, 0, &msg)
		if err != nil {
			log.Error("feedsync: закрытие run (failed)", "feed_id", feed.ID, "err", err)
			run.Status, run.Error = "failed", &msg
		}
		if err := s.Store.Feeds.MarkSync(ctx, feed.ID, "failed", &msg); err != nil {
			log.Error("feedsync: MarkSync (failed)", "feed_id", feed.ID, "err", err)
		}
		return run, nil
	}

	switch feed.Type {
	case "generic", "et_open", "et_pro", "taxii", "stix":
		// поддерживаемые коннекторы: generic — IOC-листы,
		// et_open/et_pro — фиды правил ET, taxii — TAXII 2.x (см. taxii.go),
		// stix — статический STIX bundle/JSON по URL (там же).
	default:
		return fail(fmt.Sprintf("синхронизация типа %q не поддерживается (generic — IOC-фиды, et_open/et_pro — фиды правил ET, taxii — TAXII 2.x, stix — STIX bundle)", feed.Type))
	}

	// et_pro: код подписки — из credentials; пустой URL строится из кода.
	if feed.Type == "et_pro" {
		u, err := etProURL(feed)
		if err != nil {
			return fail(err.Error())
		}
		feed.URL = u
	}

	// taxii: своя загрузка (discovery + пагинация envelope), не fetch().
	if feed.Type == "taxii" {
		return s.syncTaxii(ctx, feed, run, fail)
	}

	body, err := s.fetch(ctx, feed)
	if err != nil {
		return fail(err.Error())
	}

	if feed.Type == "et_open" || feed.Type == "et_pro" {
		return s.syncRules(ctx, feed, run, body, feed.Type, fail)
	}

	// stix: тело — STIX bundle/массив, разбор тем же ParseStix, что taxii.
	if feed.Type == "stix" {
		objects, err := ParseStixBody(body)
		if err != nil {
			return fail("stix: " + err.Error())
		}
		items, lineErrs := ParseStix(objects, time.Now())
		if len(items) == 0 {
			msg := "фид не содержит валидных IOC"
			if len(lineErrs) > 0 {
				msg = fmt.Sprintf("%s (%d ошибочных индикаторов, первый: %s)", msg, len(lineErrs), lineErrs[0].Reason)
			}
			return fail(msg)
		}
		return s.importIocs(ctx, feed, run, items, lineErrs)
	}

	items, lineErrs := Parse(body)
	if len(items) == 0 {
		msg := "фид не содержит валидных IOC"
		if len(lineErrs) > 0 {
			msg = fmt.Sprintf("%s (%d ошибочных строк, первая: %s)", msg, len(lineErrs), lineErrs[0].Reason)
		}
		return fail(msg)
	}
	return s.importIocs(ctx, feed, run, items, lineErrs)
}

// importIocs — общий импорт IOC-коннекторов (generic, taxii): идемпотентный
// upsert по (org, type, value); source — имя фида, feed_id — id фида.
// Закрывает run (success + частичные ошибки — в error) и last_sync_* фида.
func (s *Syncer) importIocs(ctx context.Context, feed store.Feed, run store.FeedRun,
	items []store.IocInput, lineErrs []rules.LineError) (store.FeedRun, error) {
	log := s.log()

	imported, updated, skipped := 0, 0, 0
	var firstErr string
	for i, item := range items {
		item.Source = &feed.Name
		item.FeedID = &feed.ID
		_, inserted, err := s.Store.Iocs.UpsertImport(ctx, feed.OrganizationID, item)
		if err != nil {
			skipped++
			if firstErr == "" {
				firstErr = fmt.Sprintf("строка %d (%s): %v", i+1, item.Value, err)
			}
			continue
		}
		if inserted {
			imported++
		} else {
			updated++
		}
	}
	skipped += len(lineErrs)

	status := "success"
	var syncErr *string
	if skipped > 0 {
		msg := fmt.Sprintf("пропущено %d некорректных записей", skipped)
		if firstErr != "" {
			msg += ", первая: " + firstErr
		} else if len(lineErrs) > 0 {
			msg += ", первая: " + lineErrs[0].Reason
		}
		syncErr = &msg // частичный успех: статус success, детали — в last_error
	}

	run, err := s.Store.Feeds.FinishRun(ctx, run.ID, status, imported, updated, skipped, syncErr)
	if err != nil {
		return store.FeedRun{}, fmt.Errorf("закрытие feed_run: %w", err)
	}
	if err := s.Store.Feeds.MarkSync(ctx, feed.ID, status, syncErr); err != nil {
		return run, fmt.Errorf("обновление last_sync фида: %w", err)
	}
	log.Info("feedsync: фид синхронизирован",
		"feed_id", feed.ID, "name", feed.Name,
		"imported", imported, "updated", updated, "skipped", skipped)
	return run, nil
}

// fetch загружает тело фида по URL (GET, таймаут клиента, лимит размера).
func (s *Syncer) fetch(ctx context.Context, feed store.Feed) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feed.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("некорректный URL фида: %w", err)
	}
	// Креды фида (MVP: plaintext в credentials_ref): "user:pass" → Basic Auth,
	// иначе — Bearer-токен. Для et_pro credentials — код подписки, он уже
	// в URL (см. etProURL), auth-заголовок не нужен.
	if feed.Type != "et_pro" && feed.CredentialsRef != nil && *feed.CredentialsRef != "" {
		if u, p, ok := strings.Cut(*feed.CredentialsRef, ":"); ok {
			req.SetBasicAuth(u, p)
		} else {
			req.Header.Set("Authorization", "Bearer "+*feed.CredentialsRef)
		}
	}
	resp, err := s.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("загрузка фида: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("загрузка фида: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, s.maxBytes()+1))
	if err != nil {
		return nil, fmt.Errorf("чтение тела фида: %w", err)
	}
	if int64(len(body)) > s.maxBytes() {
		return nil, fmt.Errorf("фид больше лимита %d МБ", s.maxBytes()>>20)
	}
	return body, nil
}

// --- разбор тела фида ---

// knownTypes — допустимые типы IOC (CHECK таблицы iocs).
var knownTypes = map[string]bool{
	"ip": true, "domain": true, "url": true,
	"md5": true, "sha1": true, "sha256": true, "email": true,
}

var hexRe = regexp.MustCompile(`^[0-9a-fA-F]+$`)

var hostnameRe = regexp.MustCompile(`^(?i)[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

// GuessType угадывает тип IOC по значению: IP/CIDR, URL, хэши, email, домен.
// Пустая строка — тип не распознан.
func GuessType(v string) string {
	if net.ParseIP(v) != nil {
		return "ip"
	}
	if _, _, err := net.ParseCIDR(v); err == nil {
		return "ip"
	}
	if u, err := url.Parse(v); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
		return "url"
	}
	switch len(v) {
	case 32:
		if hexRe.MatchString(v) {
			return "md5"
		}
	case 40:
		if hexRe.MatchString(v) {
			return "sha1"
		}
	case 64:
		if hexRe.MatchString(v) {
			return "sha256"
		}
	}
	if strings.Contains(v, "@") && hostnameRe.MatchString(v[strings.LastIndex(v, "@")+1:]) {
		return "email"
	}
	if hostnameRe.MatchString(v) {
		return "domain"
	}
	return ""
}

// validValue — облегчённая проверка значения по типу (как в httpapi):
// отсеивает явный мусор до записи в репозиторий.
func validValue(typ, v string) bool {
	switch typ {
	case "ip":
		return net.ParseIP(v) != nil || func() bool { _, _, err := net.ParseCIDR(v); return err == nil }()
	case "domain":
		return hostnameRe.MatchString(v)
	case "url":
		u, err := url.Parse(v)
		return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
	case "md5":
		return len(v) == 32 && hexRe.MatchString(v)
	case "sha1":
		return len(v) == 40 && hexRe.MatchString(v)
	case "sha256":
		return len(v) == 64 && hexRe.MatchString(v)
	case "email":
		return strings.Contains(v, "@") && hostnameRe.MatchString(v[strings.LastIndex(v, "@")+1:])
	}
	return false
}

// Parse разбирает тело фида в список IocInput (score по умолчанию —
// defaultScore, source/feed_id проставит Sync). Ошибочные строки не
// прерывают разбор: они возвращаются как []rules.LineError (первые
// maxLineErrors, номера строк 1-based по исходному телу).
func Parse(body []byte) ([]store.IocInput, []rules.LineError) {
	body = bytes.TrimPrefix(body, []byte("\xef\xbb\xbf")) // UTF-8 BOM
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, nil
	}
	if trimmed[0] == '[' {
		return parseJSON(trimmed)
	}
	return parseLines(string(body))
}

// jsonItem — элемент JSON-фида: объект {type?, value, score?} или строка.
type jsonItem struct {
	Type  string `json:"type"`
	Value string `json:"value"`
	Score *int   `json:"score"`
}

func parseJSON(body []byte) ([]store.IocInput, []rules.LineError) {
	var raw []json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, []rules.LineError{{Line: 0, Reason: "некорректный JSON: " + err.Error()}}
	}
	items := []store.IocInput{}
	errs := []rules.LineError{}
	for i, r := range raw {
		var it jsonItem
		var s string
		switch {
		case json.Unmarshal(r, &s) == nil: // строка — значение, тип угадываем
			it.Value = strings.TrimSpace(s)
		case json.Unmarshal(r, &it) == nil:
			it.Value = strings.TrimSpace(it.Value)
		default:
			errs = appendErr(errs, i+1, "элемент не строка и не объект {type,value}")
			continue
		}
		if it.Type == "" {
			it.Type = GuessType(it.Value)
		}
		score := defaultScore
		if it.Score != nil {
			score = *it.Score
		}
		if !knownTypes[it.Type] || !validValue(it.Type, it.Value) || score < 0 || score > 100 {
			errs = appendErr(errs, i+1, fmt.Sprintf("невалидный IOC (type=%q value=%q)", it.Type, it.Value))
			continue
		}
		items = append(items, store.IocInput{Type: it.Type, Value: it.Value, Score: score})
	}
	return items, errs
}

func parseLines(text string) ([]store.IocInput, []rules.LineError) {
	items := []store.IocInput{}
	errs := []rules.LineError{}
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		typ, value := splitCSV(line)
		if typ == "" {
			typ = GuessType(value)
		}
		if !knownTypes[typ] || !validValue(typ, value) {
			errs = appendErr(errs, i+1, fmt.Sprintf("не распознан IOC: %q", line))
			continue
		}
		items = append(items, store.IocInput{Type: typ, Value: value, Score: defaultScore})
	}
	return items, errs
}

// splitCSV разбирает строку вида "value,type" / "type,value"; если запятой
// нет или тип не из известного набора — вся строка считается значением.
func splitCSV(line string) (typ, value string) {
	if !strings.Contains(line, ",") {
		return "", line
	}
	parts := strings.Split(line, ",")
	first := strings.ToLower(strings.TrimSpace(parts[0]))
	last := strings.ToLower(strings.TrimSpace(parts[len(parts)-1]))
	switch {
	case knownTypes[first] && len(parts) >= 2:
		return first, strings.TrimSpace(parts[1])
	case knownTypes[last]:
		return last, strings.TrimSpace(parts[0])
	}
	return "", line
}

func appendErr(errs []rules.LineError, line int, reason string) []rules.LineError {
	if len(errs) >= maxLineErrors {
		return errs
	}
	return append(errs, rules.LineError{Line: line, Reason: reason})
}
