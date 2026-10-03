package auditdiff

import (
	"encoding/json"
	"testing"
)

type obj struct {
	Name     string  `json:"name"`
	Active   bool    `json:"active"`
	Score    int     `json:"score"`
	Password string  `json:"password,omitempty"`
	Note     *string `json:"note,omitempty"`
}

func parse(t *testing.T, raw json.RawMessage) map[string]map[string]any {
	t.Helper()
	var m map[string]map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("diff не JSON: %v", err)
	}
	return m
}

func TestComputeChangedFields(t *testing.T) {
	before := obj{Name: "old", Active: true, Score: 10}
	after := obj{Name: "new", Active: true, Score: 20}
	d := Compute(before, after)
	if d == nil {
		t.Fatal("ожидается diff — name и score изменились")
	}
	m := parse(t, d)
	if m["after"]["name"] != "new" || m["before"]["name"] != "old" {
		t.Errorf("name: %+v", m)
	}
	if m["after"]["score"] != 20.0 || m["before"]["score"] != 10.0 {
		t.Errorf("score: %+v", m)
	}
	// Неизменное поле active в diff не попадает.
	if _, ok := m["after"]["active"]; ok {
		t.Errorf("active не должно быть в diff: %+v", m)
	}
}

func TestComputeNoChanges(t *testing.T) {
	a := obj{Name: "x", Active: true, Score: 1}
	if d := Compute(a, a); d != nil {
		t.Fatalf("без изменений diff должен быть nil, получено %s", d)
	}
}

func TestComputeSecretExcluded(t *testing.T) {
	before := obj{Name: "u", Password: "old-pass"}
	after := obj{Name: "u", Password: "new-pass"}
	if d := Compute(before, after); d != nil {
		t.Fatalf("изменение только пароля не должно попадать в diff: %s", d)
	}
	// Пароль + обычное поле: в diff только обычное.
	before.Score = 1
	after.Score = 2
	d := Compute(before, after)
	if d == nil {
		t.Fatal("ожидается diff по score")
	}
	m := parse(t, d)
	if _, ok := m["after"]["password"]; ok {
		t.Errorf("password не должно быть в diff: %+v", m)
	}
	if m["after"]["score"] != 2.0 {
		t.Errorf("score: %+v", m)
	}
}

func TestComputeAddedRemovedField(t *testing.T) {
	note := "n"
	before := obj{Name: "u"}             // note отсутствует
	after := obj{Name: "u", Note: &note} // note появилось
	d := Compute(before, after)
	if d == nil {
		t.Fatal("ожидается diff — добавлено note")
	}
	m := parse(t, d)
	if m["after"]["note"] != "n" {
		t.Errorf("добавленное note: %+v", m)
	}
	if _, present := m["before"]["note"]; !present {
		t.Errorf("before.note должно присутствовать (null): %+v", m)
	}
}

func TestComputeMaps(t *testing.T) {
	before := map[string]any{"a": 1, "b": "x"}
	after := map[string]any{"a": 2, "b": "x"}
	d := Compute(before, after)
	if d == nil {
		t.Fatal("ожидается diff по a")
	}
	m := parse(t, d)
	if m["after"]["a"] != 2.0 || m["before"]["a"] != 1.0 {
		t.Errorf("map diff: %+v", m)
	}
}

func TestComputeNonObject(t *testing.T) {
	if d := Compute(42, 43); d != nil {
		t.Fatalf("скаляр не диффится: %s", d)
	}
	if d := Compute(nil, nil); d != nil {
		t.Fatalf("nil: %s", d)
	}
}
