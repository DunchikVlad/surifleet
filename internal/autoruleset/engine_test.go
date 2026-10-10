package autoruleset

import (
	"testing"
	"time"

	"github.com/surifleet/surifleet/internal/store"
)

func at(s string) *time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return &t
}

func TestDueInterval(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	mins := func(n int) *int { return &n }

	// Интервал 30 мин: сборки не было — пора.
	def := store.AutoRuleset{ScheduleIntervalMinutes: mins(30)}
	if !due(def, now) {
		t.Error("нет last_built_at — должно быть due")
	}
	// Сборка 20 мин назад — рано.
	def.LastBuiltAt = at("2026-10-10T11:40:00Z")
	if due(def, now) {
		t.Error("прошло 20 из 30 мин — не due")
	}
	// Сборка 31 минуту назад — пора.
	def.LastBuiltAt = at("2026-10-10T11:29:00Z")
	if !due(def, now) {
		t.Error("прошло 31 из 30 мин — due")
	}
	// Ровно 30 мин — due (граница).
	def.LastBuiltAt = at("2026-10-10T11:30:00Z")
	if !due(def, now) {
		t.Error("ровно 30 мин — due")
	}
	// Невалидный интервал — никогда.
	def = store.AutoRuleset{ScheduleIntervalMinutes: mins(0)}
	if due(def, now) {
		t.Error("интервал 0 — не due")
	}
}

func TestDueDaily(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, loc)
	tm := func(s string) *string { return &s }

	// Момент сегодня (03:00) прошёл, сборки не было — due.
	def := store.AutoRuleset{ScheduleTime: tm("03:00")}
	if !due(def, now) {
		t.Error("03:00 сегодня прошло, сборки не было — due")
	}
	// Момент сегодня ещё не наступил (18:00) — не due.
	def = store.AutoRuleset{ScheduleTime: tm("18:00")}
	if due(def, now) {
		t.Error("18:00 ещё не наступило — не due")
	}
	// Сборка сегодня в 03:05 — после момента — не due.
	def = store.AutoRuleset{ScheduleTime: tm("03:00"), LastBuiltAt: at("2026-10-10T03:05:00Z")}
	if due(def, now) {
		t.Error("сборка уже после 03:00 — не due")
	}
	// Сборка вчера — due.
	def = store.AutoRuleset{ScheduleTime: tm("03:00"), LastBuiltAt: at("2026-10-09T03:05:00Z")}
	if !due(def, now) {
		t.Error("сборка вчера — due")
	}
	// Невалидный формат — не due.
	def = store.AutoRuleset{ScheduleTime: tm("xyz")}
	if due(def, now) {
		t.Error("мусор в schedule_time — не due")
	}
	// Расписание не задано — не due.
	def = store.AutoRuleset{}
	if due(def, now) {
		t.Error("без расписания — не due")
	}
}
