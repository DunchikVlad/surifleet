// Расписания авто-синка фидов (чанк 27): поле schedule фида — либо
// длительность Go ("1h", "30m" — интервал от last_sync_at), либо
// 5-полевое cron-выражение ("*/15 * * * *", "0 3 * * 0", @hourly и др.
// дескрипторы robfig/cron — локальное время сервера).
package feedsync

import (
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// cronParser — стандартный 5-полевой формат + дескрипторы (@daily и т.п.).
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// Schedule — разобранное расписание фида: длительность Go или cron.
type Schedule struct {
	Dur  time.Duration // >0 — интервалное расписание
	Cron cron.Schedule // != nil — cron-расписание
}

// ParseSchedule разбирает schedule фида: сначала как длительность Go,
// затем как cron. Ошибка — с указанием обоих форматов.
func ParseSchedule(s string) (Schedule, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Schedule{}, fmt.Errorf("пустое расписание")
	}
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return Schedule{Dur: d}, nil
	}
	c, err := cronParser.Parse(s)
	if err != nil {
		return Schedule{}, fmt.Errorf("schedule %q не длительность Go (\"1h\") и не cron (\"*/15 * * * *\"): %w", s, err)
	}
	return Schedule{Cron: c}, nil
}

// Due — наступил ли срок синка: для длительности — last_sync_at + dur <= now
// (синка ещё не было → сразу); для cron — ближайшее cron-время после
// last_sync_at уже наступило (синка не было → сразу).
func (sc Schedule) Due(lastSyncAt *time.Time, now time.Time) bool {
	if lastSyncAt == nil {
		return true
	}
	if sc.Dur > 0 {
		return !lastSyncAt.Add(sc.Dur).After(now)
	}
	return !sc.Cron.Next(*lastSyncAt).After(now)
}
