package httpapi

import (
	"encoding/csv"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/surifleet/surifleet/internal/store"
)

func TestWriteAuditCSV(t *testing.T) {
	oid := uuid.New()
	name := "a,b@corp.example" // запятая — проверка квотинга
	reason := "сказал \"да\""
	diff := json.RawMessage(`{"before":{"x":1},"after":{"x":2}}`)
	items := []store.AuditListItem{
		{ID: uuid.New(), CreatedAt: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC),
			ActorType: "user", ActorName: &name, Action: "users.update",
			ObjectType: strPtr("user"), ObjectID: &oid, Result: "success", Reason: &reason, Diff: diff},
		{ID: uuid.New(), CreatedAt: time.Date(2026, 10, 3, 12, 1, 0, 0, time.UTC),
			ActorType: "api_token", Action: "auth.login", Result: "denied"}, // nil-поля
	}

	rec := httptest.NewRecorder()
	writeAuditCSV(rec, items)
	out := rec.Body.String()

	rows, err := csv.NewReader(strings.NewReader(out)).ReadAll()
	if err != nil {
		t.Fatalf("CSV не парсится: %v\n%s", err, out)
	}
	if len(rows) != 3 { // заголовок + 2 строки
		t.Fatalf("строк = %d, ожидается 3", len(rows))
	}
	if rows[0][0] != "created_at" || rows[0][9] != "diff" {
		t.Errorf("заголовок = %v", rows[0])
	}
	// Запятая в actor_name и кавычки в reason — корректно экранированы (csv.ReadAll разобрал).
	if rows[1][2] != name {
		t.Errorf("actor_name с запятой = %q", rows[1][2])
	}
	if rows[1][7] != reason {
		t.Errorf("reason с кавычками = %q", rows[1][7])
	}
	if rows[1][9] != string(diff) {
		t.Errorf("diff = %q", rows[1][9])
	}
	// nil-поля → пустые строки.
	if rows[2][2] != "" || rows[2][5] != "" || rows[2][9] != "" {
		t.Errorf("nil-поля должны быть пустыми: %v", rows[2])
	}
}

func strPtr(s string) *string { return &s }
