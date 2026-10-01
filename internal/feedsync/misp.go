// Коннектор misp (чанк 26): MISP core format feed → IOC.
//
// URL фида — базовый URL MISP-фида (core format, публичные OSINT-фиды):
// GET {url}/manifest.json → {<event-uuid>: {info, timestamp, Orgc, ...}},
// затем GET {url}/{uuid}.json → {"Event": {"Attribute": [...],
// "threat_level_id": ...}}. Загрузка событий последовательная, предел
// mispMaxEvents на один синк (защита от гигантских фидов; события
// сортируются по timestamp манифеста — свежие первыми). Инкрементальности
// нет (MVP): полный обход, upsert идемпотентен.
//
// Атрибуты: to_ids=false или deleted=true пропускаются (не предназначены
// для IDS). Маппинг типов: ip-src/ip-dst → ip, domain/hostname → domain,
// url → url, md5/sha1/sha256 → хэши, email-src/email-dst/email → email;
// составные: domain|ip → доменная часть, ip-*|port → IP-часть,
// filename|md5|... → хэш-часть. Score — по threat_level_id события
// (1 High→80, 2 Medium→60, 3 Low→40, прочее → defaultScore).
// MISP Event Object (объекты-группы атрибутов) не разбираются (MVP).
package feedsync

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/surifleet/surifleet/internal/rules"
	"github.com/surifleet/surifleet/internal/store"
)

// mispMaxEvents — предел событий за один синк (свежие первыми).
const mispMaxEvents = 2000

// mispManifest — manifest.json MISP-фида: uuid события → метаданные.
type mispManifest map[string]struct {
	Info      string          `json:"info"`
	Timestamp json.RawMessage `json:"timestamp"` // в фидах бывает и строкой, и числом
}

// mispEvent — файл события {uuid}.json.
type mispEvent struct {
	Event struct {
		Info          string `json:"info"`
		ThreatLevelID string `json:"threat_level_id"`
		Attribute     []struct {
			Type    string `json:"type"`
			Value   string `json:"value"`
			ToIDS   *bool  `json:"to_ids"`
			Deleted bool   `json:"deleted"`
		} `json:"Attribute"`
	} `json:"Event"`
}

// syncMisp — цикл синка MISP-фида: manifest → события → атрибуты → IOC.
func (s *Syncer) syncMisp(ctx context.Context, feed store.Feed, run store.FeedRun,
	fail func(string) (store.FeedRun, error)) (store.FeedRun, error) {

	base := strings.TrimRight(strings.TrimSpace(feed.URL), "/")
	if base == "" {
		return fail("для misp укажите URL — базовый адрес MISP-фида (core format: manifest.json + события)")
	}

	body, err := s.fetch(ctx, store.Feed{URL: base + "/manifest.json", CredentialsRef: feed.CredentialsRef})
	if err != nil {
		return fail("misp: manifest.json: " + err.Error())
	}
	var manifest mispManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return fail("misp: разбор manifest.json: " + err.Error())
	}
	if len(manifest) == 0 {
		return fail("misp: manifest.json не содержит событий")
	}

	// Свежие события первыми (по timestamp манифеста), предел mispMaxEvents.
	type evRef struct {
		id string
		ts int64
	}
	events := make([]evRef, 0, len(manifest))
	for id, meta := range manifest {
		events = append(events, evRef{id: id, ts: mispTimestamp(meta.Timestamp)})
	}
	sort.Slice(events, func(i, j int) bool { return events[i].ts > events[j].ts })
	truncated := false
	if len(events) > mispMaxEvents {
		events = events[:mispMaxEvents]
		truncated = true
	}

	items := []store.IocInput{}
	errs := []rules.LineError{}
	seen := map[string]bool{}
	for i, ev := range events {
		body, err := s.fetch(ctx, store.Feed{URL: base + "/" + ev.id + ".json", CredentialsRef: feed.CredentialsRef})
		if err != nil {
			errs = appendErr(errs, i+1, fmt.Sprintf("событие %s: %v", ev.id, err))
			continue
		}
		evItems, evErrs := ParseMispEvent(body)
		for _, e := range evErrs {
			e.Line = i + 1
			errs = appendErr(errs, e.Line, fmt.Sprintf("событие %s: %s", ev.id, e.Reason))
		}
		for _, it := range evItems {
			key := it.Type + "\x00" + it.Value
			if seen[key] {
				continue
			}
			seen[key] = true
			items = append(items, it)
		}
	}
	if len(items) == 0 {
		msg := "фид не содержит валидных IOC"
		if len(errs) > 0 {
			msg = fmt.Sprintf("%s (%d ошибок, первая: %s)", msg, len(errs), errs[0].Reason)
		}
		return fail(msg)
	}
	if truncated {
		s.log().Warn("misp: фид обрезан по пределу событий", "feed_id", feed.ID, "max_events", mispMaxEvents)
	}
	return s.importIocs(ctx, feed, run, items, errs)
}

// mispTimestamp приводит timestamp манифеста (строка или число) к int64.
func mispTimestamp(raw json.RawMessage) int64 {
	var n int64
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		n, _ = strconv.ParseInt(s, 10, 64)
	}
	return n
}

// mispAttrTypes — прямой маппинг типа атрибута MISP на тип IOC.
var mispAttrTypes = map[string]string{
	"ip-src": "ip", "ip-dst": "ip",
	"domain": "domain", "hostname": "domain",
	"url": "url",
	"md5": "md5", "sha1": "sha1", "sha256": "sha256",
	"email-src": "email", "email-dst": "email", "email": "email",
}

// mispScore — score IOC по threat_level_id события (1 High, 2 Medium,
// 3 Low, 4 Undefined).
func mispScore(threatLevel string) int {
	switch threatLevel {
	case "1":
		return 80
	case "2":
		return 60
	case "3":
		return 40
	}
	return defaultScore
}

// ParseMispEvent разбирает JSON события MISP в IocInput (без дедупликации —
// дедуп по (type,value) делает syncMisp). Атрибуты с to_ids=false или
// deleted пропускаются молча; неподдерживаемые типы — в ошибки.
func ParseMispEvent(body []byte) ([]store.IocInput, []rules.LineError) {
	var ev mispEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return nil, []rules.LineError{{Reason: "некорректный JSON события: " + err.Error()}}
	}
	score := mispScore(ev.Event.ThreatLevelID)
	items := []store.IocInput{}
	errs := []rules.LineError{}
	for j, attr := range ev.Event.Attribute {
		if attr.Deleted || (attr.ToIDS != nil && !*attr.ToIDS) {
			continue
		}
		typ, value := mispMapAttribute(attr.Type, strings.TrimSpace(attr.Value))
		if typ == "" {
			errs = appendErr(errs, j+1, fmt.Sprintf("неподдерживаемый тип атрибута %q", attr.Type))
			continue
		}
		if !validValue(typ, value) {
			errs = appendErr(errs, j+1, fmt.Sprintf("невалидное значение %s = %q", attr.Type, attr.Value))
			continue
		}
		items = append(items, store.IocInput{Type: typ, Value: value, Score: score})
	}
	return items, errs
}

// mispMapAttribute — тип/значение IOC из атрибута MISP, включая составные
// типы (domain|ip → домен, ip-*|port → IP, filename|md5 → хэш).
func mispMapAttribute(attrType, value string) (string, string) {
	if typ, ok := mispAttrTypes[attrType]; ok {
		return typ, value
	}
	head, tail, composite := strings.Cut(attrType, "|")
	if !composite {
		return "", ""
	}
	parts := strings.SplitN(value, "|", 2)
	switch {
	case head == "domain" && tail == "ip":
		return "domain", parts[0]
	case strings.HasPrefix(head, "ip-") && tail == "port":
		return "ip", parts[0]
	case head == "filename" && mispAttrTypes[tail] != "":
		if len(parts) == 2 {
			return mispAttrTypes[tail], strings.TrimSpace(parts[1])
		}
	}
	return "", ""
}
