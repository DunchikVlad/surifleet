package httpapi

import (
	"testing"

	"github.com/surifleet/surifleet/internal/store"
)

// Логика статуса ячейки матрицы: failed > desired(loaded|missing) > extra > нет ячейки.
func TestCellStatusOf(t *testing.T) {
	st := &store.MatrixInstanceState{
		Desired: map[int64]bool{1: true, 2: true, 3: true},
		Loaded:  map[int64]bool{1: true, 4: true},
		Failed:  map[int64]bool{3: true},
	}
	cases := []struct {
		sid  int64
		want string
	}{
		{1, cellLoaded},  // в desired и загружен
		{2, cellMissing}, // в desired, но не загружен
		{3, cellFailed},  // в desired, но движок отклонил (failed приоритетнее)
		{4, cellExtra},   // загружен вне desired
		{5, ""},          // вне контекста инстанса — ячейки нет
	}
	for _, c := range cases {
		if got := cellStatusOf(c.sid, st); got != c.want {
			t.Errorf("sid %d: хочу %q, получил %q", c.sid, c.want, got)
		}
	}
	if got := cellStatusOf(1, nil); got != "" {
		t.Errorf("nil state: хочу пусто, получил %q", got)
	}
}
