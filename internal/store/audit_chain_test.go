package store

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestComputeAuditHashDeterministic(t *testing.T) {
	org := uuid.New()
	uid := uuid.New()
	ts := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	e := AuditEntry{
		OrganizationID: &org, ActorType: "user", ActorUserID: &uid,
		ActorName: "a@b.c", Action: "users.update", Result: "success",
	}
	h1 := computeAuditHash(e, ts, "")
	h2 := computeAuditHash(e, ts, "")
	if h1 != h2 {
		t.Fatalf("хэш не детерминирован: %q != %q", h1, h2)
	}
	if h1 == "" {
		t.Fatal("пустой хэш")
	}
}

func TestComputeAuditHashSensitivity(t *testing.T) {
	ts := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	base := AuditEntry{ActorType: "user", ActorName: "a@b.c", Action: "users.update", Result: "success"}
	h := computeAuditHash(base, ts, "prev")

	cases := map[string]func(e *AuditEntry, ts time.Time, prev string) (AuditEntry, time.Time, string){
		"поле": func(e *AuditEntry, ts time.Time, p string) (AuditEntry, time.Time, string) {
			e.ActorName = "other@b.c"
			return *e, ts, p
		},
		"время": func(e *AuditEntry, ts time.Time, p string) (AuditEntry, time.Time, string) {
			return *e, ts.Add(time.Second), p
		},
		"prev_hash": func(e *AuditEntry, ts time.Time, p string) (AuditEntry, time.Time, string) {
			return *e, ts, "other-prev"
		},
		"diff": func(e *AuditEntry, ts time.Time, p string) (AuditEntry, time.Time, string) {
			e.Diff = json.RawMessage(`{"a":1}`)
			return *e, ts, p
		},
		"reason": func(e *AuditEntry, ts time.Time, p string) (AuditEntry, time.Time, string) {
			r := "x"
			e.Reason = &r
			return *e, ts, p
		},
	}
	for name, mutate := range cases {
		e2, ts2, p2 := mutate(&base, ts, "prev")
		if got := computeAuditHash(e2, ts2, p2); got == h {
			t.Errorf("%s: хэш не изменился при мутации", name)
		}
	}
}

// TestChainLinkage — моделирование цепочки в памяти: вставка под тем же
// алгоритмом, что logChained; проверка связности и детекта подделки.
func TestChainLinkage(t *testing.T) {
	type rec struct {
		e         AuditEntry
		createdAt time.Time
		prevHash  string
		hash      string
	}
	ts := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	chain := []rec{}
	insert := func(action string) {
		prev := ""
		if len(chain) > 0 {
			prev = chain[len(chain)-1].hash
		}
		e := AuditEntry{ActorType: "user", ActorName: "u", Action: action, Result: "success"}
		ct := ts.Add(time.Duration(len(chain)) * time.Second)
		chain = append(chain, rec{e, ct, prev, computeAuditHash(e, ct, prev)})
	}
	insert("users.create")
	insert("roles.update")
	insert("sso.update")

	// Связность + пересчёт (как VerifyChain).
	for i, cr := range chain {
		if got := computeAuditHash(cr.e, cr.createdAt, cr.prevHash); got != cr.hash {
			t.Fatalf("запись %d: hash не совпадает", i)
		}
		if i+1 < len(chain) && chain[i+1].prevHash != cr.hash {
			t.Fatalf("разрыв между %d и %d", i, i+1)
		}
	}
	// Подделка поля ломает хэш записи.
	tampered := chain[1]
	tampered.e.ActorName = "attacker"
	if got := computeAuditHash(tampered.e, tampered.createdAt, tampered.prevHash); got == tampered.hash {
		t.Fatal("подделка actor_name не изменила хэш")
	}
	// Удаление средней записи ломает связь.
	if chain[2].prevHash == chain[0].hash {
		t.Fatal("после удаления записи связь должна ломаться")
	}
}

func TestNullableJSONEmpty(t *testing.T) {
	if nullableJSON(nil) != nil {
		t.Fatal("nil → NULL")
	}
	if nullableJSON(json.RawMessage{}) != nil {
		t.Fatal("пустой → NULL")
	}
	if string(nullableJSON(json.RawMessage(`{"a":1}`))) != `{"a":1}` {
		t.Fatal("значение сохраняется")
	}
}
