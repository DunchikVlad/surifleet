// Package rules — парсер Suricata-правил для импорта в мастер-репозиторий
// (ТЗ п.5.1). Без внешних зависимостей: заголовок (action/protocol/адреса/
// порты/направление) + опции в скобках. Извлекаются sid (обязателен), rev
// (default 1), msg, classtype, priority, reference, metadata; content-опции
// не интерпретируются, исходная строка сохраняется в Raw целиком.
package rules

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Actions — допустимые действия правила (ТЗ: alert/drop/reject/pass/log;
// добавлены штатные варианты Suricata 7+/8 rejectsrc/rejectdst/rejectboth,
// чтобы реальные фиды не давали ложных ошибок импорта).
var Actions = map[string]bool{
	"alert": true, "drop": true, "reject": true,
	"rejectsrc": true, "rejectdst": true, "rejectboth": true,
	"pass": true, "log": true,
}

// Parsed — разобранная строка правила.
type Parsed struct {
	Action    string   `json:"action"`
	Protocol  string   `json:"protocol"`
	SrcAddr   string   `json:"src_addr"`
	SrcPort   string   `json:"src_port"`
	Direction string   `json:"direction"`
	DstAddr   string   `json:"dst_addr"`
	DstPort   string   `json:"dst_port"`
	SID       int64    `json:"sid"`
	Rev       int      `json:"rev"`
	Msg       string   `json:"msg"`
	Classtype string   `json:"classtype,omitempty"`
	Priority  int      `json:"priority,omitempty"`
	Reference []string `json:"reference,omitempty"`
	Metadata  []string `json:"metadata,omitempty"`
	Raw       string   `json:"-"`
}

// LineError — ошибка разбора конкретной строки файла.
type LineError struct {
	Line   int    `json:"line"`
	Reason string `json:"reason"`
}

// Parse разбирает одну строку правила (без перевода строки в конце).
// Комментарии (# ...) и пустые строки возвращают (nil, nil) — «пропустить».
func Parse(line string) (*Parsed, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return nil, nil
	}

	// Заголовок до первой '(' и опции до последней ')'.
	open := strings.Index(trimmed, "(")
	close_ := strings.LastIndex(trimmed, ")")
	if open < 0 || close_ < 0 || close_ < open {
		return nil, fmt.Errorf("нет секции опций в скобках")
	}
	header, opts := trimmed[:open], trimmed[open+1:close_]

	toks := splitHeader(header)
	if len(toks) != 7 {
		return nil, fmt.Errorf("заголовок: ожидалось 7 полей (action proto src sport dir dst dport), получено %d", len(toks))
	}
	p := &Parsed{
		Action: toks[0], Protocol: toks[1],
		SrcAddr: toks[2], SrcPort: toks[3],
		Direction: toks[4], DstAddr: toks[5], DstPort: toks[6],
		Rev: 1, Raw: trimmed,
	}
	if !Actions[p.Action] {
		return nil, fmt.Errorf("неизвестный action %q", p.Action)
	}
	if p.Direction != "->" && p.Direction != "<>" {
		return nil, fmt.Errorf("направление %q: ожидалось -> или <>", p.Direction)
	}

	if err := p.parseOptions(opts); err != nil {
		return nil, err
	}
	if p.SID <= 0 {
		return nil, fmt.Errorf("sid: обязательная опция, положительное число")
	}
	if p.Msg == "" {
		return nil, fmt.Errorf("msg: обязательная опция, непустая")
	}
	return p, nil
}

// splitHeader делит заголовок на поля по пробелам, не разрывая списки
// в квадратных скобках ([$HOME_NET,10.0.0.0/8] — один токен).
func splitHeader(s string) []string {
	var toks []string
	depth := 0
	start := -1
	for i, r := range s {
		switch {
		case r == '[':
			depth++
		case r == ']':
			if depth > 0 {
				depth--
			}
		case (r == ' ' || r == '\t') && depth == 0:
			if start >= 0 {
				toks = append(toks, s[start:i])
				start = -1
			}
			continue
		}
		if start < 0 && r != ' ' && r != '\t' {
			start = i
		}
	}
	if start >= 0 {
		toks = append(toks, s[start:])
	}
	return toks
}

// parseOptions разбирает тело опций: пары key[:value]; разделитель ';',
// значения в кавычках могут содержать экранированные \; \" \\.
func (p *Parsed) parseOptions(body string) error {
	for _, opt := range splitOptions(body) {
		key, val, _ := strings.Cut(opt, ":")
		key = strings.TrimSpace(key)
		val = unquote(strings.TrimSpace(val))
		switch key {
		case "sid":
			n, err := strconv.ParseInt(val, 10, 64)
			if err != nil || n <= 0 {
				return fmt.Errorf("sid %q: ожидалось положительное число", val)
			}
			p.SID = n
		case "rev":
			n, err := strconv.Atoi(val)
			if err != nil || n <= 0 {
				return fmt.Errorf("rev %q: ожидалось положительное число", val)
			}
			p.Rev = n
		case "msg":
			p.Msg = val
		case "classtype":
			p.Classtype = val
		case "priority":
			n, err := strconv.Atoi(val)
			if err != nil {
				return fmt.Errorf("priority %q: ожидалось число", val)
			}
			p.Priority = n
		case "reference":
			if val != "" {
				p.Reference = append(p.Reference, val)
			}
		case "metadata":
			if val != "" {
				p.Metadata = append(p.Metadata, val)
			}
		}
		// Прочие опции (content, pcre, flow, threshold, ...) не
		// интерпретируем — исходник целиком в Raw.
	}
	return nil
}

// splitOptions делит тело опций по ';' вне кавычек (учитывает экранирование).
func splitOptions(s string) []string {
	var out []string
	var b strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			b.WriteByte(c)
			b.WriteByte(s[i+1])
			i++
			continue
		}
		if c == '"' {
			inQuote = !inQuote
			b.WriteByte(c)
			continue
		}
		if c == ';' && !inQuote {
			if t := strings.TrimSpace(b.String()); t != "" {
				out = append(out, t)
			}
			b.Reset()
			continue
		}
		b.WriteByte(c)
	}
	if t := strings.TrimSpace(b.String()); t != "" {
		out = append(out, t)
	}
	return out
}

// unquote снимает обрамляющие кавычки и экранирование (\; \" \\).
func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// ParseResult — итог потокового разбора файла правил.
type ParseResult struct {
	Rules      []*Parsed
	Errors     []LineError
	TotalLines int // физических строк прочитано (включая комментарии/пустые)
}

// ParseReader потоково разбирает .rules-файл. Комментарии и пустые строки
// пропускаются; строки с ошибками собираются в Errors (не роняют разбор);
// maxErrors ограничивает список ошибок (0 — без лимита). Строки,
// заканчивающиеся на '\', склеиваются (line continuation).
func ParseReader(r io.Reader, maxErrors int) *ParseResult {
	res := &ParseResult{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 256*1024), 4*1024*1024) // длинные правила (base64-блобы)
	var logical strings.Builder
	logicalStart := 0
	flush := func() {
		if logical.Len() == 0 {
			return
		}
		text := logical.String()
		lineNo := logicalStart
		logical.Reset()
		parsed, err := Parse(text)
		if err != nil {
			if maxErrors <= 0 || len(res.Errors) < maxErrors {
				res.Errors = append(res.Errors, LineError{Line: lineNo, Reason: err.Error()})
			}
			return
		}
		if parsed != nil {
			res.Rules = append(res.Rules, parsed)
		}
	}
	for sc.Scan() {
		res.TotalLines++
		line := sc.Text()
		if logical.Len() == 0 {
			logicalStart = res.TotalLines
		}
		if strings.HasSuffix(strings.TrimRight(line, " \t"), "\\") {
			logical.WriteString(strings.TrimSuffix(strings.TrimRight(line, " \t"), "\\"))
			continue
		}
		logical.WriteString(line)
		flush()
	}
	flush() // файл без перевода строки в конце / висячий continuation
	return res
}
