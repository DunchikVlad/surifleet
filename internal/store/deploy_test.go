package store

import (
	"strings"
	"testing"
)

// taskColumnsT — квалифицированный вариант taskColumns для запросов с JOIN:
// каждая колонка должна иметь префикс t. (регрессия: «column reference
// "status" is ambiguous» в PendingTasksForAgent — префикс ставился только
// на первую колонку конкатенацией строк).
func TestTaskColumnsTQualified(t *testing.T) {
	plain := strings.Split(strings.ReplaceAll(taskColumns, "\n\t", " "), ", ")
	qual := strings.Split(strings.ReplaceAll(taskColumnsT, "\n\t", " "), ", ")
	if len(plain) != len(qual) {
		t.Fatalf("число колонок различается: taskColumns=%d, taskColumnsT=%d", len(plain), len(qual))
	}
	for i, c := range qual {
		if want := "t." + strings.TrimSpace(plain[i]); c != want {
			t.Errorf("колонка %d: хочу %q, получил %q", i, want, c)
		}
	}
}
