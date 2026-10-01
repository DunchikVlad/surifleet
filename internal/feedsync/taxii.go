// Коннектор taxii (чанк 24): TAXII 2.x → STIX 2.x indicator → IOC.
//
// URL фида может указывать на:
//   - endpoint объектов коллекции (.../collections/{id}/objects[/]) —
//     используется как есть, с пагинацией по more/next;
//   - коллекцию (.../collections/{id}) — добавляется /objects;
//   - API root (прочее) — discovery: GET {url}/collections/ → объекты
//     всех коллекций с can_read != false.
//
// Ответ objects-endpoint — envelope {"more": bool, "next": "...", "objects":
// [...]} (TAXII 2.1) или STIX bundle (2.0 — те же objects, без пагинации).
// Accept: application/taxii+json;version=2.1. Credentials: "user:pass" →
// Basic, иначе Bearer (как у generic).
//
// Из STIX-объектов берутся только indicator с pattern_type "stix" (или без
// него); revoked и истёкшие (valid_until < now) пропускаются молча.
// Паттерн разбирается упрощённо: извлекаются сравнения 'lhs = value'
// (составные OR/AND-паттерны дают по IOC на сравнение). Маппинг:
// ipv4-addr/ipv6-addr:value → ip, domain-name:value → domain,
// url:value → url, email-addr:value → email, file:hashes.MD5/'SHA-1'/
// 'SHA-256' → md5/sha1/sha256. confidence (0..100) → score, иначе
// defaultScore; valid_until → expires_at IOC.
package feedsync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/surifleet/surifleet/internal/rules"
	"github.com/surifleet/surifleet/internal/store"
)

const (
	// taxiiAccept — media type TAXII 2.1 (серверы 2.0 обычно отвечают тем
	// же envelope; строгая версионность — за рамками MVP).
	taxiiAccept = "application/taxii+json;version=2.1"
	// taxiiMaxPages — защитный предел пагинации more/next.
	taxiiMaxPages = 100
)

// taxiiEnvelope — ответ objects-endpoint (TAXII 2.1 envelope / STIX bundle).
type taxiiEnvelope struct {
	More    bool              `json:"more"`
	Next    string            `json:"next"`
	Objects []json.RawMessage `json:"objects"`
}

// taxiiCollections — ответ GET {api_root}/collections/ (TAXII 2.1).
type taxiiCollections struct {
	Collections []struct {
		ID      string `json:"id"`
		CanRead *bool  `json:"can_read"`
	} `json:"collections"`
}

// syncTaxii — цикл синка TAXII-фида: загрузка объектов (discovery +
// пагинация), разбор STIX-индикаторов, общий импорт IOC.
func (s *Syncer) syncTaxii(ctx context.Context, feed store.Feed, run store.FeedRun,
	fail func(string) (store.FeedRun, error)) (store.FeedRun, error) {

	objects, err := s.fetchTaxiiObjects(ctx, feed)
	if err != nil {
		return fail(err.Error())
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

// fetchTaxiiObjects загружает все STIX-объекты фида (discovery по api root
// при необходимости + пагинация more/next).
func (s *Syncer) fetchTaxiiObjects(ctx context.Context, feed store.Feed) ([]json.RawMessage, error) {
	u := strings.TrimRight(strings.TrimSpace(feed.URL), "/")
	if u == "" {
		return nil, fmt.Errorf("для taxii укажите URL: endpoint объектов коллекции (.../collections/{id}/objects/) или API root TAXII-сервера")
	}

	var objectsURLs []string
	if strings.Contains(u, "/collections/") {
		// Прямой URL коллекции или её объектов.
		if !strings.HasSuffix(u, "/objects") {
			u += "/objects"
		}
		objectsURLs = []string{u + "/"}
	} else {
		// API root → discovery коллекций.
		cols, err := s.fetchTaxiiCollections(ctx, feed, u+"/collections/")
		if err != nil {
			return nil, err
		}
		if len(cols) == 0 {
			return nil, fmt.Errorf("taxii: на API root нет доступных коллекций (can_read)")
		}
		for _, c := range cols {
			objectsURLs = append(objectsURLs, u+"/collections/"+c+"/objects/")
		}
	}

	var out []json.RawMessage
	for _, ou := range objectsURLs {
		objs, err := s.fetchTaxiiPages(ctx, feed, ou)
		if err != nil {
			return nil, err
		}
		out = append(out, objs...)
	}
	return out, nil
}

// fetchTaxiiCollections — discovery: GET {api_root}/collections/, отбор
// коллекций с can_read != false (поле необязательное — отсутствие = можно).
func (s *Syncer) fetchTaxiiCollections(ctx context.Context, feed store.Feed, url string) ([]string, error) {
	body, err := s.fetchTaxii(ctx, feed, url)
	if err != nil {
		return nil, fmt.Errorf("taxii: список коллекций: %w", err)
	}
	var cols taxiiCollections
	if err := json.Unmarshal(body, &cols); err != nil {
		return nil, fmt.Errorf("taxii: разбор списка коллекций: %w", err)
	}
	ids := []string{}
	for _, c := range cols.Collections {
		if c.ID != "" && (c.CanRead == nil || *c.CanRead) {
			ids = append(ids, c.ID)
		}
	}
	return ids, nil
}

// fetchTaxiiPages выкачивает objects-endpoint с пагинацией more/next.
func (s *Syncer) fetchTaxiiPages(ctx context.Context, feed store.Feed, objectsURL string) ([]json.RawMessage, error) {
	out := []json.RawMessage{}
	next := ""
	for page := 0; ; page++ {
		if page >= taxiiMaxPages {
			return nil, fmt.Errorf("taxii: пагинация длиннее %d страниц — прервано", taxiiMaxPages)
		}
		u := objectsURL
		if next != "" {
			sep := "?"
			if strings.Contains(u, "?") {
				sep = "&"
			}
			u += sep + "next=" + url.QueryEscape(next)
		}
		body, err := s.fetchTaxii(ctx, feed, u)
		if err != nil {
			return nil, fmt.Errorf("taxii: объекты коллекции: %w", err)
		}
		var env taxiiEnvelope
		if err := json.Unmarshal(body, &env); err != nil {
			return nil, fmt.Errorf("taxii: разбор envelope: %w", err)
		}
		out = append(out, env.Objects...)
		if !env.More || env.Next == "" {
			return out, nil
		}
		next = env.Next
	}
}

// fetchTaxii — один GET к TAXII-серверу (Accept 2.1, креды как у generic,
// лимит maxBytes на тело).
func (s *Syncer) fetchTaxii(ctx context.Context, feed store.Feed, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("некорректный URL фида: %w", err)
	}
	req.Header.Set("Accept", taxiiAccept)
	if feed.CredentialsRef != nil && *feed.CredentialsRef != "" {
		if user, pass, ok := strings.Cut(*feed.CredentialsRef, ":"); ok {
			req.SetBasicAuth(user, pass)
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

// --- разбор STIX 2.x ---

// stixObject — поля STIX-объекта, нужные коннектору (indicator).
type stixObject struct {
	Type        string     `json:"type"`
	Pattern     string     `json:"pattern"`
	PatternType string     `json:"pattern_type"`
	ValidUntil  *time.Time `json:"valid_until"`
	Revoked     bool       `json:"revoked"`
	Confidence  *int       `json:"confidence"`
}

// stixComparisonRe — сравнения паттерна вида lhs = 'value' (значение в
// одинарных кавычках, \' не разбираем — STIX-значения IOC их не содержат).
var stixComparisonRe = regexp.MustCompile(`([a-zA-Z0-9_\-]+(?::[a-zA-Z0-9_.'\-]+)?)\s*=\s*'([^']*)'`)

// stixToIocType — маппинг левой части сравнения STIX-паттерна (lowercase,
// кавычки вокруг имён хэшей сняты) на тип IOC.
var stixToIocType = map[string]string{
	"ipv4-addr:value":       "ip",
	"ipv6-addr:value":       "ip",
	"domain-name:value":     "domain",
	"url:value":             "url",
	"email-addr:value":      "email",
	"file:hashes.md5":       "md5",
	"file:hashes.sha-1":     "sha1",
	"file:hashes.sha-256":   "sha256",
	"file:hashes.'sha-1'":   "sha1",
	"file:hashes.'sha-256'": "sha256",
}

// ParseStixBody разбирает тело STIX-фида (чанк 25, коннектор stix):
// STIX bundle {"objects": [...]} (2.0/2.1) или голый массив объектов.
func ParseStixBody(body []byte) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(bytes.TrimPrefix(body, []byte("\xef\xbb\xbf")))
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("пустое тело фида")
	}
	switch trimmed[0] {
	case '[':
		var objects []json.RawMessage
		if err := json.Unmarshal(trimmed, &objects); err != nil {
			return nil, fmt.Errorf("разбор STIX-массива: %w", err)
		}
		return objects, nil
	case '{':
		var bundle struct {
			Objects []json.RawMessage `json:"objects"`
		}
		if err := json.Unmarshal(trimmed, &bundle); err != nil {
			return nil, fmt.Errorf("разбор STIX bundle: %w", err)
		}
		if bundle.Objects == nil {
			return nil, fmt.Errorf("STIX bundle без поля objects")
		}
		return bundle.Objects, nil
	}
	return nil, fmt.Errorf("тело не STIX (ожидается bundle {...} или массив объектов)")
}

// ParseStix разбирает STIX-объекты в IocInput. Берутся только indicator
// (остальные типы объектов — молча пропускаются); revoked и истёкшие по
// valid_until — молча. Индикаторы без поддерживаемых сравнений и с
// не-stix pattern_type — в ошибки (не прерывают разбор). Дубликаты
// (type,value) внутри выборки схлопываются.
func ParseStix(objects []json.RawMessage, now time.Time) ([]store.IocInput, []rules.LineError) {
	items := []store.IocInput{}
	errs := []rules.LineError{}
	seen := map[string]bool{}
	for i, raw := range objects {
		var obj stixObject
		if err := json.Unmarshal(raw, &obj); err != nil {
			errs = appendErr(errs, i+1, "некорректный STIX-объект: "+err.Error())
			continue
		}
		if obj.Type != "indicator" {
			continue
		}
		if obj.Revoked || (obj.ValidUntil != nil && obj.ValidUntil.Before(now)) {
			continue
		}
		if obj.PatternType != "" && obj.PatternType != "stix" {
			errs = appendErr(errs, i+1, fmt.Sprintf("pattern_type %q не поддерживается (только stix)", obj.PatternType))
			continue
		}
		found := 0
		for _, m := range stixComparisonRe.FindAllStringSubmatch(obj.Pattern, -1) {
			lhs := strings.ToLower(m[1])
			typ, ok := stixToIocType[lhs]
			if !ok {
				continue
			}
			value := strings.TrimSpace(m[2])
			if !validValue(typ, value) {
				errs = appendErr(errs, i+1, fmt.Sprintf("невалидное значение %s = %q", m[1], value))
				continue
			}
			found++
			key := typ + "\x00" + value
			if seen[key] {
				continue
			}
			seen[key] = true
			score := defaultScore
			if obj.Confidence != nil && *obj.Confidence >= 0 && *obj.Confidence <= 100 {
				score = *obj.Confidence
			}
			items = append(items, store.IocInput{Type: typ, Value: value, Score: score, ExpiresAt: obj.ValidUntil})
			found++
		}
		if found == 0 && len(obj.Pattern) > 0 {
			errs = appendErr(errs, i+1, "паттерн не содержит поддерживаемых IOC (ip/domain/url/email/file-hash)")
		}
	}
	return items, errs
}
