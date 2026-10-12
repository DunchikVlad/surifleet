package store

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Round-trip курсора оси правил (sid) и разбор краевых случаев.
func TestSidCursorRoundTrip(t *testing.T) {
	for _, sid := range []int64{1, 42, 21009123, 9223372036854775806} {
		c := EncodeSidCursor(sid)
		got, err := DecodeSidCursor(c)
		if err != nil {
			t.Fatalf("sid %d: %v", sid, err)
		}
		if got != sid {
			t.Errorf("sid %d: round-trip дал %d", sid, got)
		}
	}

	// Пустой курсор — первая страница.
	if got, err := DecodeSidCursor(""); err != nil || got != 0 {
		t.Errorf("пустой курсор: хочу (0, nil), получил (%d, %v)", got, err)
	}
	// Битый курсор — ошибка.
	if _, err := DecodeSidCursor("!!!не-base64!!!"); err == nil {
		t.Error("битый курсор должен давать ошибку")
	}
	if _, err := DecodeSidCursor(EncodeCursor(uuid.MustParse("468c9c71-6ed3-4a3e-be56-5f1f3af93874"))); err == nil {
		t.Error("uuid-курсор другой оси не должен парситься как sid")
	}
}

// TestMatrixRulesPageSQLCellFilter — чанк 119: состав запроса оси правил
// с cell_status-фильтром (подтягивание подходящих правил в ось).
func TestMatrixRulesPageSQLCellFilter(t *testing.T) {
	// Фильтр подключён через $7/$8 и отключаем при NULL.
	for _, want := range []string{
		"($7::text IS NULL OR rules.sid IN (",
		"jsonb_array_elements(ds.computed_rules)",
		"jsonb_array_elements(a.loaded_rules)",
		"jsonb_array_elements(a.failed_rules)",
		"a.reported_at IS NOT NULL",
		"ANY($8::uuid[])",
		"'failed' AS st",
		"'loaded'",
		"'missing'",
		"'extra'",
		"st = $7::text",
		"AND sid > $5",
		"ORDER BY sid LIMIT $6",
	} {
		if !strings.Contains(matrixRulesPageSQL, want) {
			t.Fatalf("matrixRulesPageSQL не содержит %q", want)
		}
	}
	// Семантика приоритета failed: loaded/missing/extra исключают f.
	if strings.Count(matrixCellFilterSQL, "NOT EXISTS (SELECT 1 FROM f") != 3 {
		t.Fatalf("ожидается 3 исключения failed-набора (loaded/missing/extra)")
	}
}
