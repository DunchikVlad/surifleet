// Package cfgrender — рендер профилей конфигурации Suricata (чанк 65,
// план 1B): слияние цепочки наследования (кластер → хост → инстанс) и
// подстановка переменных {{var}}.
//
// Синтаксис переменных — {{имя}} (допускаются пробелы внутри скобок).
// В YAML плейсхолдер обязателен в кавычках: interface: "{{iface}}" —
// без кавычек {{...}} парсится как flow-map, а не строка. Значения
// берутся из двух источников (приоритет — первый):
//
//  1. top-level ключ vars: профиля — map, объявляет переменные профиля.
//     Ключ vars объявляется в любом профиле цепочки, мержится root→tip
//     (более специфичный профиль перекрывает) и в итоговый YAML не
//     попадает. Значения типизированы (vars: {threads: 4} → int).
//  2. Встроенный контекст цели (только чтение):
//     instance.id, instance.name, instance.config_path,
//     instance.rules_dir, instance.log_dir, instance.interface
//     (первый capture-интерфейс), host.id, host.hostname, host.ip
//     (первый адрес), cluster.id, cluster.name.
//
// Строгий режим: неизвестная переменная — ошибка рендера со списком
// имён (тихая подстановка пустой строки в suricata.yaml недопустима).
//
// Слияние цепочки (root→tip): map сливаются рекурсивно, массивы и
// скаляры перекрываются целиком, явный null в более специфичном
// профиле удаляет ключ родителя.
//
// Подстановка применяется к строковым значениям дерева. Если строка
// целиком состоит из одного плейсхолдера — подставляется исходное
// типизированное значение (vars: {port: 8080} + "port: {{port}}" →
// port: 8080 числом); встроенный в текст плейсхолдер всегда даёт строку.
package cfgrender

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Profile — профиль цепочки наследования (вход рендера).
type Profile struct {
	ID        string
	Name      string
	ScopeType string
	Content   string // content_yaml
}

// Source — элемент цепочки наследования для RenderResult.sources
// (openapi: от кластера к инстансу).
type Source struct {
	ProfileID string `json:"profile_id"`
	Name      string `json:"name"`
	ScopeType string `json:"scope_type"`
}

// Target — факты об инстансе-цели для встроенных переменных.
type Target struct {
	InstanceID   string
	InstanceName string
	ConfigPath   string
	RulesDir     string
	LogDir       string
	Interface    string // первый capture-интерфейс
	HostID       string
	Hostname     string
	HostIP       string
	ClusterID    string
	ClusterName  string
}

// varsKey — зарезервированный top-level ключ с переменными профиля.
const varsKey = "vars"

var varNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

var openBrace = "{{"

// UnknownVarsError — строгий режим: встретились переменные без значений.
type UnknownVarsError struct {
	Names []string
}

func (e *UnknownVarsError) Error() string {
	return "неизвестные переменные: " + strings.Join(e.Names, ", ")
}

// Render сливает цепочку профилей (root→tip: кластер, затем хост, затем
// инстанс) и подставляет переменные. Возвращает итоговый YAML и цепочку
// источников. Ошибка — некорректный YAML, vars не map или неизвестные
// переменные.
func Render(chain []Profile, target Target) (string, []Source, error) {
	if len(chain) == 0 {
		return "", nil, fmt.Errorf("пустая цепочка профилей")
	}
	vars := map[string]any{}
	merged := map[string]any{}
	sources := make([]Source, 0, len(chain))
	for _, p := range chain {
		doc, err := parseDoc(p, p.Content)
		if err != nil {
			return "", nil, err
		}
		if v, ok := doc[varsKey]; ok {
			vm, ok := v.(map[string]any)
			if !ok {
				return "", nil, fmt.Errorf("профиль %q: ключ %q должен быть map", p.Name, varsKey)
			}
			mergeVars(vars, vm)
			delete(doc, varsKey)
		}
		merged = mergeMaps(merged, doc).(map[string]any)
		sources = append(sources, Source{ProfileID: p.ID, Name: p.Name, ScopeType: p.ScopeType})
	}

	ctx := newContext(vars, target)
	if err := substitute(merged, ctx, pather{}); err != nil {
		return "", nil, err
	}

	var sb strings.Builder
	enc := yaml.NewEncoder(&sb)
	enc.SetIndent(2)
	if err := enc.Encode(merged); err != nil {
		return "", nil, fmt.Errorf("сериализация результата: %w", err)
	}
	if err := enc.Close(); err != nil {
		return "", nil, fmt.Errorf("сериализация результата: %w", err)
	}
	return sb.String(), sources, nil
}

// parseDoc разбирает content_yaml профиля в map (пустое содержимое —
// пустая map; строго один документ).
func parseDoc(p Profile, content string) (map[string]any, error) {
	doc := map[string]any{}
	if strings.TrimSpace(content) == "" {
		return doc, nil
	}
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return nil, fmt.Errorf("профиль %q: некорректный YAML: %w", p.Name, err)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	return doc, nil
}

// mergeVars: более специфичный профиль перекрывает vars рекурсивно.
func mergeVars(dst, src map[string]any) {
	for k, v := range src {
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				mergeVars(dm, sm)
				continue
			}
		}
		dst[k] = v
	}
}

// mergeMaps deep-мерж: dst — база (родитель), src — оверлей (потомок).
// Map сливаются рекурсивно; массивы/скаляры перекрываются; явный null
// удаляет ключ.
func mergeMaps(dst, src any) any {
	smap, sok := src.(map[string]any)
	dmap, dok := dst.(map[string]any)
	if !sok || !dok {
		if src == nil {
			return nil // нет ключа — mergeKeys удалит
		}
		return src
	}
	out := make(map[string]any, len(dmap)+len(smap))
	for k, v := range dmap {
		out[k] = v
	}
	for k, v := range smap {
		if v == nil {
			delete(out, k)
			continue
		}
		if base, ok := out[k]; ok {
			out[k] = mergeMaps(base, v)
			continue
		}
		out[k] = v
	}
	return out
}

// context — резолвер переменных: vars профилей + встроенные факты цели.
type context struct {
	vars     map[string]any
	builtins map[string]string
}

func newContext(vars map[string]any, t Target) *context {
	return &context{
		vars: vars,
		builtins: map[string]string{
			"instance.id":          t.InstanceID,
			"instance.name":        t.InstanceName,
			"instance.config_path": t.ConfigPath,
			"instance.rules_dir":   t.RulesDir,
			"instance.log_dir":     t.LogDir,
			"instance.interface":   t.Interface,
			"host.id":              t.HostID,
			"host.hostname":        t.Hostname,
			"host.ip":              t.HostIP,
			"cluster.id":           t.ClusterID,
			"cluster.name":         t.ClusterName,
		},
	}
}

func (c *context) lookup(name string) (any, bool) {
	if v, ok := c.vars[name]; ok {
		return v, true
	}
	if v, ok := c.builtins[name]; ok {
		return v, true
	}
	return nil, false
}

// pather — путь в дереве для сообщений об ошибках.
type pather struct {
	parts []string
}

func (p pather) child(k string) pather {
	return pather{parts: append(append([]string{}, p.parts...), k)}
}

func (p pather) index(i int) pather {
	return pather{parts: append(append([]string{}, p.parts...), fmt.Sprintf("[%d]", i))}
}

func (p pather) String() string {
	if len(p.parts) == 0 {
		return "(корень)"
	}
	return strings.Join(p.parts, ".")
}

// substitute обходит дерево и подставляет {{var}} в строковых значениях.
// Неизвестные переменные собираются и дают одну ошибку (строгий режим).
func substitute(node any, ctx *context, path pather) error {
	unknown := map[string]bool{}
	substNode(node, ctx, path, unknown)
	if len(unknown) > 0 {
		names := make([]string, 0, len(unknown))
		for n := range unknown {
			names = append(names, n)
		}
		sort.Strings(names)
		return &UnknownVarsError{Names: names}
	}
	return nil
}

func substNode(node any, ctx *context, path pather, unknown map[string]bool) {
	switch v := node.(type) {
	case map[string]any:
		for k, val := range v {
			v[k] = substValue(val, ctx, path.child(k), unknown)
		}
	case []any:
		for i, val := range v {
			v[i] = substValue(val, ctx, path.index(i), unknown)
		}
	}
}

func substValue(val any, ctx *context, path pather, unknown map[string]bool) any {
	s, ok := val.(string)
	if !ok {
		substNode(val, ctx, path, unknown) // вложенные map/list
		return val
	}
	return substString(s, ctx, path, unknown)
}

// substString подставляет плейсхолдеры в строку. Если строка целиком —
// один плейсхолдер, возвращает типизированное значение (int/bool/map
// сохраняют тип); иначе — строку со встроенными подстановками.
func substString(s string, ctx *context, path pather, unknown map[string]bool) any {
	if isSinglePlaceholder(s) {
		name := strings.TrimSpace(s[2 : len(s)-2])
		val, ok := ctx.lookup(name)
		if !ok {
			unknown[name] = true
			return s // placeholder останется в сообщении об ошибке
		}
		return val
	}
	var sb strings.Builder
	rest := s
	for {
		i := strings.Index(rest, openBrace)
		if i < 0 {
			sb.WriteString(rest)
			break
		}
		rel := strings.Index(rest[i+2:], "}}")
		if rel < 0 {
			// нет закрывающих скобок — остаток буквальный
			sb.WriteString(rest)
			break
		}
		name := strings.TrimSpace(rest[i+2 : i+2+rel])
		if !varNameRe.MatchString(name) {
			// не плейсхолдер (буквальные {{ в конфиге) — копируем как есть
			sb.WriteString(rest[:i+2])
			rest = rest[i+2:]
			continue
		}
		val, ok := ctx.lookup(name)
		if !ok {
			unknown[name] = true
			val = ""
		}
		sb.WriteString(rest[:i])
		sb.WriteString(scalarToString(val))
		rest = rest[i+2+rel+2:]
	}
	return sb.String()
}

// isSinglePlaceholder — строка вида "{{name}}" целиком.
func isSinglePlaceholder(s string) bool {
	if !strings.HasPrefix(s, openBrace) || !strings.HasSuffix(s, "}}") {
		return false
	}
	name := strings.TrimSpace(s[2 : len(s)-2])
	return varNameRe.MatchString(name)
}

// scalarToString — строковое представление значения для встроенной
// подстановки (в числа/bool приводится к YAML-виду).
func scalarToString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		var sb strings.Builder
		enc := yaml.NewEncoder(&sb)
		if err := enc.Encode(v); err == nil {
			_ = enc.Close()
			return strings.TrimRight(sb.String(), "\n")
		}
		return fmt.Sprint(v)
	}
}
