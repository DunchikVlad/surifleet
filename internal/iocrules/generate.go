// Package iocrules — генерация Suricata-правил из IOC (чанк 17).
//
// Детерминизм: sid правила — функция от (type, value), поэтому повторная
// генерация по тому же набору IOC идемпотентна (UpsertImport по (org, sid)
// сравнивает sha256 raw с последней ревизией и не пишет БД при совпадении).
//
// Sid-пространство: 8800000..8899999 (100k слотов). Занятые диапазоны:
//   - 2xxxxx/1xxxxxx — публичные фиды (ET Open/Pro);
//   - 9000xxx        — локальные ручные правила (ТЗ, локальное пространство).
//
// 88xxxxx не пересекается ни с одним известным диапазоном.
package iocrules

import (
	"fmt"
	"hash/fnv"
	"net/url"
	"strings"
)

// SidBase / SidRange — диапазон sid правил, сгенерированных из IOC.
const (
	SidBase  = 8_800_000
	SidRange = 100_000
)

// FormatRev — rev генерируемых правил. Детерминированный sid + rev = версия
// формата генератора: при изменении шаблона правила FormatRev
// инкрементируется, и UpsertImport корректно создаёт новую ревизию
// (rev из файла — ключ (rule_id, revision) в rule_revisions).
//
//	1 — nocase на http.host (невалидно для Suricata 8: буфер нормализован);
//	2 — текущий формат (nocase снят с http.host, оставлен на dns.query).
const FormatRev = 2

// SidFor — детерминированный sid из (type, value): FNV-1a(32) по
// "type\x00value", свёрнутый в диапазон [SidBase, SidBase+SidRange).
func SidFor(typ, value string) int64 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToLower(typ)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(normalize(typ, value)))
	return SidBase + int64(h.Sum32()%SidRange)
}

// normalize — канонизация значения перед хэшированием/рендером:
// домены и email нечувствительны к регистру, хэши приводим к lower.
func normalize(typ, value string) string {
	v := strings.TrimSpace(value)
	switch typ {
	case "domain", "email", "md5", "sha1", "sha256":
		return strings.ToLower(v)
	case "url":
		if u, err := url.Parse(v); err == nil {
			u.Host = strings.ToLower(u.Host)
			return u.String()
		}
	}
	return v
}

// escContent — экранирование значения для Suricata content:"...".
// Спецсимволы content: \ " ; : (см. Suricata rule syntax).
func escContent(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `;`, `\;`, `:`, `\:`)
	return r.Replace(s)
}

// escMsg — экранирование для msg:"..." (кавычки и backslash).
func escMsg(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return r.Replace(s)
}

// MsgFor — msg правила IOC (общий формат, нужен и для сверки коллизий sid).
func MsgFor(typ, value string) string {
	return fmt.Sprintf("SuriFleet IOC %s %s", typ, normalize(typ, value))
}

// RuleFor — сырое правило с детерминированным sid (SidFor).
func RuleFor(typ, value string) (raw string, ok bool, reason string) {
	return RuleWithSID(typ, value, SidFor(typ, value))
}

// RuleWithSID — сырое Suricata-правило для IOC с явным sid (пробинг
// хэш-коллизий: SidFor + сдвиг внутри диапазона).
//
// Маппинг типов (Suricata 7+/8, sticky buffers):
//   - ip (адрес или CIDR): alert ip <addr> any -> any any
//   - domain: dns.query content (подстрока — покрывает и поддомены)
//   - url: http.host + http.uri (путь есть) либо только http.host
//   - md5/sha1/sha256, email: ok=false с причиной — инлайн-правилом
//     не выразить (хэши требуют файлов filemd5/filesha256, email не
//     маппится на сетевой индикатор).
//
// rev — FormatRev (версия формата генератора, см. константу выше).
func RuleWithSID(typ, value string, sid int64) (raw string, ok bool, reason string) {
	v := normalize(typ, value)
	msg := MsgFor(typ, value)

	switch typ {
	case "ip":
		return fmt.Sprintf(`alert ip %s any -> any any (msg:"%s"; sid:%d; rev:%d;)`,
			v, escMsg(msg), sid, FormatRev), true, ""
	case "domain":
		return fmt.Sprintf(`alert dns any any -> any any (msg:"%s"; dns.query; content:"%s"; nocase; sid:%d; rev:%d;)`,
			escMsg(msg), escContent(v), sid, FormatRev), true, ""
	case "url":
		u, err := url.Parse(v)
		if err != nil || u.Host == "" {
			return "", false, "url не разбирается: " + v
		}
		host := u.Hostname()
		// http.host нормализуется в lowercase самой Suricata 8 — nocase там
		// запрещён (warning → error парсинга). Значение уже lower из normalize().
		opts := fmt.Sprintf(`http.host; content:"%s";`, escContent(host))
		if p := u.EscapedPath(); p != "" && p != "/" {
			opts += fmt.Sprintf(` http.uri; content:"%s";`, escContent(p))
		}
		return fmt.Sprintf(`alert http any any -> any any (msg:"%s"; %s sid:%d; rev:%d;)`,
			escMsg(msg), opts, sid, FormatRev), true, ""
	case "md5", "sha1", "sha256":
		return "", false, "hash IOC требует файла хэшей (filemd5/filesha256) — генерация не поддержана"
	case "email":
		return "", false, "email не маппится на сетевое правило — пропущен"
	}
	return "", false, "неизвестный тип IOC: " + typ
}
