// Package auditdiff — вычисление diff «было → стало» для аудит-лога
// (чанк 37, п. 8 ТЗ: «diff „было → стало“ для изменений»). Хранится в
// audit_log.diff в формате {"before": {…}, "after": {…}} — только
// изменившиеся поля. Поля-секреты (пароль, client_secret, credentials,
// token) никогда не попадают в diff.
package auditdiff

import (
	"encoding/json"
	"reflect"
	"strings"
)

// secretKeys — подстроки имён полей, исключаемые из diff (секреты).
var secretKeys = []string{"password", "secret", "credentials", "token", "hash"}

// Compute возвращает JSON-diff изменённых полей между before и after
// (одинаковые структуры/мапы/указатели). Возвращает {"before":{…},"after":{…}}
// либо nil, если полей не изменилось или вход не удалось разобрать.
// Секретные поля исключаются.
func Compute(before, after any) json.RawMessage {
	b := flatten(before)
	a := flatten(after)
	if b == nil || a == nil {
		return nil
	}
	db := map[string]any{}
	da := map[string]any{}
	// Изменённые и добавленные поля.
	for k, av := range a {
		if isSecret(k) {
			continue
		}
		bv, ok := b[k]
		if !ok || !reflect.DeepEqual(bv, av) {
			da[k] = av
			if ok {
				db[k] = bv
			} else {
				db[k] = nil
			}
		}
	}
	// Удалённые поля.
	for k, bv := range b {
		if isSecret(k) {
			continue
		}
		if _, ok := a[k]; !ok {
			db[k] = bv
			da[k] = nil
		}
	}
	if len(da) == 0 && len(db) == 0 {
		return nil
	}
	out, err := json.Marshal(map[string]any{"before": db, "after": da})
	if err != nil {
		return nil
	}
	return out
}

// flatten приводит значение к map[string]any (через JSON-маршалинг —
// учитываются json-теги структур; поля с json:"-" отбрасываются).
// Не-объект (массив/скаляр/ошибка) → nil.
func flatten(v any) map[string]any {
	if v == nil {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	return m
}

// isSecret — имя поля содержит маркер секрета (регистронезависимо).
func isSecret(key string) bool {
	k := strings.ToLower(key)
	for _, s := range secretKeys {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}
