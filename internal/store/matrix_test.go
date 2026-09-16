package store

import (
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
