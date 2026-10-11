package chlogs

import (
	"fmt"
	"strings"
	"testing"
	"time"
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

// TestAgentLogsQuery — сборка SELECT с фильтрами (чанк 117): WHERE
// компонуется из условий, строки экранируются, пустой фильтр — прежний
// запрос.
func TestAgentLogsQuery(t *testing.T) {
	from := time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)
	to := from.Add(2 * time.Hour)

	base := agentLogsQuery("surifleet", "agent-1", 200, LogFilter{})
	if !strings.Contains(base, "WHERE agent_id = 'agent-1' ORDER BY ts DESC LIMIT 200") {
		t.Fatalf("базовый запрос: %s", base)
	}
	for _, unwanted := range []string{"level =", "positionCaseInsensitive", "2026-10-11"} {
		if strings.Contains(base, unwanted) {
			t.Fatalf("базовый запрос содержит фильтр %q: %s", unwanted, base)
		}
	}

	f := LogFilter{Level: "error", Q: "it's a \\ test", From: from, To: to}
	q := agentLogsQuery("surifleet", "a'b", 50, f)
	for _, want := range []string{
		"level = 'error'",
		"positionCaseInsensitive(message, 'it\\'s a \\\\ test') > 0",
		"ts >= '2026-10-11 10:00:00.000'",
		"ts <= '2026-10-11 12:00:00.000'",
		"agent_id = 'a\\'b'",
		"LIMIT 50",
	} {
		if !strings.Contains(q, want) {
			t.Fatalf("запрос не содержит %q:\n%s", want, q)
		}
	}
	// from/to в локальном сдвиге приводятся к UTC.
	local := time.FixedZone("X", 3*3600)
	q2 := agentLogsQuery("surifleet", "a", 10, LogFilter{From: time.Date(2026, 10, 11, 13, 0, 0, 0, local)})
	if !strings.Contains(q2, "ts >= '2026-10-11 10:00:00.000'") {
		t.Fatalf("TZ-нормализация: %s", q2)
	}
}
