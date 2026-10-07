package chlogs

import (
	"fmt"
	"testing"
)

// TestRetentionSQL проверяет формирование TTL-выражения (чанк 36).
func TestRetentionSQL(t *testing.T) {
	cases := []struct {
		days int
		want string
	}{
		{0, "REMOVE TTL"},
		{-5, "REMOVE TTL"}, // отрицательное — тоже без TTL
		{30, "MODIFY TTL toDateTime(ts) + INTERVAL 30 DAY"},
		{90, "MODIFY TTL toDateTime(ts) + INTERVAL 90 DAY"},
	}
	for _, tc := range cases {
		got := retentionExpr(tc.days)
		if got != tc.want {
			t.Errorf("retentionExpr(%d) = %q, ожидается %q", tc.days, got, tc.want)
		}
	}
}

// TestRetentionAlterQuery — полный ALTER-запрос по таблицам.
func TestRetentionAlterQuery(t *testing.T) {
	for _, table := range []string{"agent_logs", "agent_metrics"} {
		q := fmt.Sprintf("ALTER TABLE %s.%s %s", "surifleet", table, retentionExpr(14))
		want := "ALTER TABLE surifleet." + table + " MODIFY TTL toDateTime(ts) + INTERVAL 14 DAY"
		if q != want {
			t.Errorf("alter %s = %q, ожидается %q", table, q, want)
		}
	}
}
