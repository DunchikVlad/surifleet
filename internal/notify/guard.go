// Вспомогательные типы для источников событий движка уведомлений
// (чанк 103 — доработки по замечаниям живого стенда, docs/known-issues.md
// KI-2/KI-4): трекер offline-эпизодов агентов (защита recovery от флапов)
// и обрезка текстов ошибок для тел сообщений.
package notify

import (
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// OfflineTracker — трекер offline-эпизодов агентов (KI-2): recovery-событие
// («агент снова онлайн») отправляется ТОЛЬКО если offline-эпизод был виден
// этим процессом. Иначе reconnect-шторм (рестарт сервера, сетевой флап на
// минуты) превратился бы в волну recovery по всему флоту. Повторы в пределах
// одного эпизода всё равно подавляет дедупликация движка (окно 10 мин).
// In-process: события online/offline рождаются в хабе одного процесса.
type OfflineTracker struct {
	mu    sync.Mutex
	since map[uuid.UUID]time.Time
}

// NewOfflineTracker — пустой трекер.
func NewOfflineTracker() *OfflineTracker {
	return &OfflineTracker{since: map[uuid.UUID]time.Time{}}
}

// TrackOffline фиксирует начало offline-эпизода агента.
func (t *OfflineTracker) TrackOffline(agentID uuid.UUID, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.since[agentID] = now
}

// ConsumeOffline закрывает offline-эпизод и возвращает его начало
// (для текста «был недоступен N мин»). ok=false — эпизода трекер не видел:
// recovery не отправляем. Один вызов — один recovery.
func (t *OfflineTracker) ConsumeOffline(agentID uuid.UUID) (time.Time, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	ts, ok := t.since[agentID]
	if ok {
		delete(t.since, agentID)
	}
	return ts, ok
}

// ClipText обрезает текст для тела уведомления (KI-4 — «текст ошибки,
// первые строки»): не более maxLines строк и maxChars символов, хвост
// заменяется на «…». Обрезка посимвольная по рунам (UTF-8 не ломается).
func ClipText(s string, maxLines, maxChars int) string {
	if maxLines > 0 {
		lines := strings.Split(s, "\n")
		if len(lines) > maxLines {
			s = strings.Join(lines[:maxLines], "\n") + "\n…"
		}
	}
	if maxChars > 0 && utf8.RuneCountInString(s) > maxChars {
		runes := []rune(s)
		s = string(runes[:maxChars]) + "…"
	}
	return s
}
