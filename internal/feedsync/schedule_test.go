package feedsync

import (
	"testing"
	"time"
)

// TestParseSchedule — длительность Go, cron, ошибки.
func TestParseSchedule(t *testing.T) {
	// длительность Go
	sc, err := ParseSchedule("1h30m")
	if err != nil || sc.Dur != 90*time.Minute || sc.Cron != nil {
		t.Errorf("длительность: %+v err=%v", sc, err)
	}
	// cron 5-полевой
	sc, err = ParseSchedule("*/15 * * * *")
	if err != nil || sc.Dur != 0 || sc.Cron == nil {
		t.Errorf("cron: %+v err=%v", sc, err)
	}
	// дескриптор
	if _, err = ParseSchedule("@daily"); err != nil {
		t.Errorf("@daily: %v", err)
	}
	// ошибки: пусто, мусор, отрицательная/нулевая длительность, 6-полевой cron
	for _, bad := range []string{"", "  ", "каждый час", "-5m", "0s", "* * * * * *", "61 * * * *"} {
		if _, err := ParseSchedule(bad); err == nil {
			t.Errorf("schedule %q: ожидалась ошибка", bad)
		}
	}
}

// TestScheduleDue — семантика наступления срока для длительности и cron.
func TestScheduleDue(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 7, 30, 0, time.UTC)

	// Длительность: nil → сразу; не наступил; наступил ровно.
	dur, _ := ParseSchedule("1h")
	if !dur.Due(nil, now) {
		t.Error("dur: last=nil → due")
	}
	last := now.Add(-30 * time.Minute)
	if dur.Due(&last, now) {
		t.Error("dur: прошло 30m из 1h → не due")
	}
	last = now.Add(-time.Hour)
	if !dur.Due(&last, now) {
		t.Error("dur: прошло ровно 1h → due")
	}

	// Cron "*/5 * * * *": nil → сразу; следующее кратное 5 мин время после
	// last уже прошло → due; ещё не наступило → не due.
	cr, _ := ParseSchedule("*/5 * * * *")
	if !cr.Due(nil, now) {
		t.Error("cron: last=nil → due")
	}
	last = time.Date(2026, 10, 1, 12, 5, 0, 0, time.UTC) // next = 12:10 > now
	if cr.Due(&last, now) {
		t.Error("cron: next 12:10 > 12:07:30 → не due")
	}
	last = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC) // next = 12:05 <= now
	if !cr.Due(&last, now) {
		t.Error("cron: next 12:05 <= 12:07:30 → due")
	}

	// Cron "@daily" (00:00): синк был сегодня в 00:00 → не due; вчера — due.
	daily, _ := ParseSchedule("@daily")
	last = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if daily.Due(&last, now) {
		t.Error("@daily: синк сегодня 00:00 → не due")
	}
	last = time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	if !daily.Due(&last, now) {
		t.Error("@daily: синк вчера → due")
	}
}
