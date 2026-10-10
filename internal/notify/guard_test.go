package notify

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestOfflineTracker_Pair — классика KI-2: offline зафиксирован →
// recovery с моментом эпизода; повторный online без нового offline
// подавляется (шторм/флап).
func TestOfflineTracker_Pair(t *testing.T) {
	tr := NewOfflineTracker()
	id := uuid.New()
	now := time.Now()

	if _, ok := tr.ConsumeOffline(id); ok {
		t.Fatal("online без предшествующего offline не должен проходить")
	}
	tr.TrackOffline(id, now)
	if _, ok := tr.ConsumeOffline(id); !ok {
		t.Fatal("recovery после зафиксированного offline обязан пройти")
	}
	if _, ok := tr.ConsumeOffline(id); ok {
		t.Fatal("второй recovery по тому же эпизоду подавляется")
	}
}

// TestOfflineTracker_Duration — момент эпизода возвращается корректно
// (для «был недоступен N мин»).
func TestOfflineTracker_Duration(t *testing.T) {
	tr := NewOfflineTracker()
	id := uuid.New()
	at := time.Now().Add(-12 * time.Minute)
	tr.TrackOffline(id, at)
	since, ok := tr.ConsumeOffline(id)
	if !ok || !since.Equal(at) {
		t.Fatalf("ожидали %v, получили %v (ok=%v)", at, since, ok)
	}
}

// TestOfflineTracker_IndependentAgents — эпизоды агентов независимы.
func TestOfflineTracker_IndependentAgents(t *testing.T) {
	tr := NewOfflineTracker()
	a, b := uuid.New(), uuid.New()
	tr.TrackOffline(a, time.Now())
	if _, ok := tr.ConsumeOffline(b); ok {
		t.Fatal("чужой эпизод не должен засчитываться")
	}
	if _, ok := tr.ConsumeOffline(a); !ok {
		t.Fatal("свой эпизод должен засчитываться")
	}
}

func TestClipText(t *testing.T) {
	cases := []struct {
		name         string
		in           string
		maxLines     int
		maxChars     int
		wantLines    int
		wantSuffix   bool
		wantMaxRunes int
	}{
		{name: "короткий текст без обрезки", in: "одна строка", maxLines: 3, maxChars: 100, wantLines: 1},
		{name: "обрезка по строкам", in: "a\nb\nc\nd\ne", maxLines: 3, maxChars: 0, wantLines: 4, wantSuffix: true}, // 3 строки + «…»
		{name: "обрезка по символам", in: strings.Repeat("ж", 50), maxLines: 0, maxChars: 10, wantLines: 1, wantSuffix: true, wantMaxRunes: 11},
		{name: "многострочный с обрезкой символов", in: "abcdef\nghijkl", maxLines: 5, maxChars: 8, wantLines: 2, wantSuffix: true, wantMaxRunes: 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClipText(tc.in, tc.maxLines, tc.maxChars)
			if n := strings.Count(got, "\n") + 1; n != tc.wantLines {
				t.Fatalf("строк: %d, ожидали %d (%q)", n, tc.wantLines, got)
			}
			if tc.wantSuffix && !strings.HasSuffix(got, "…") {
				t.Fatalf("ожидали хвост «…»: %q", got)
			}
			if tc.wantMaxRunes > 0 {
				if n := len([]rune(got)); n > tc.wantMaxRunes {
					t.Fatalf("рун: %d > %d", n, tc.wantMaxRunes)
				}
			}
		})
	}
}
