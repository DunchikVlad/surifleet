package notify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/surifleet/surifleet/internal/store"
)

// fakeChannelStore — in-memory ChannelStore для юнит-тестов движка.
type fakeChannelStore struct {
	mu        sync.Mutex
	channels  []store.NotificationChannel
	delivered map[string]bool // channel_id|fingerprint → уже отправлено (в окне)
	failed    map[string]string
	tryErr    error
}

func (f *fakeChannelStore) ListEnabled(_ context.Context, _ uuid.UUID) ([]store.NotificationChannel, error) {
	return f.channels, nil
}

func (f *fakeChannelStore) TryDelivery(_ context.Context, channelID uuid.UUID, fingerprint string, _ time.Duration) (bool, error) {
	if f.tryErr != nil {
		return false, f.tryErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.delivered == nil {
		f.delivered = map[string]bool{}
	}
	key := channelID.String() + "|" + fingerprint
	if f.delivered[key] {
		return false, nil
	}
	f.delivered[key] = true
	return true, nil
}

func (f *fakeChannelStore) FailDelivery(_ context.Context, channelID uuid.UUID, fingerprint, errText string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failed == nil {
		f.failed = map[string]string{}
	}
	f.failed[channelID.String()+"|"+fingerprint] = errText
	return nil
}

func webhookChannel(t *testing.T, url string) store.NotificationChannel {
	t.Helper()
	cfg, _ := json.Marshal(WebhookConfig{URL: url})
	return store.NotificationChannel{
		ID: uuid.New(), Name: "wh", Type: "webhook", Config: cfg, Enabled: true,
	}
}

func testEvent() Event {
	return Event{
		Type: "agent.offline", ObjectID: uuid.New(),
		Title: "Агент offline", Text: "текст", Severity: "critical",
	}
}

func TestEngineEmitSendsToAllChannels(t *testing.T) {
	hits := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits <- r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	fs := &fakeChannelStore{channels: []store.NotificationChannel{
		webhookChannel(t, srv.URL+"/a"), webhookChannel(t, srv.URL+"/b"),
	}}
	e := &Engine{Store: fs, Sender: NewSender(), Log: nil}
	got := e.Emit(context.Background(), uuid.New(), testEvent())
	if got != 2 {
		t.Fatalf("отправок = %d, ожидалось 2", got)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-hits:
		case <-time.After(2 * time.Second):
			t.Fatal("вебхук не вызван")
		}
	}
}

func TestEngineEmitDedupSuppressesRepeat(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	fs := &fakeChannelStore{channels: []store.NotificationChannel{webhookChannel(t, srv.URL)}}
	e := &Engine{Store: fs, Sender: NewSender()}
	ev := testEvent()
	org := uuid.New()
	if n := e.Emit(context.Background(), org, ev); n != 1 {
		t.Fatalf("первый Emit: отправок = %d, ожидалось 1", n)
	}
	// Повтор того же события (тот же fingerprint) — подавлен.
	if n := e.Emit(context.Background(), org, ev); n != 0 {
		t.Fatalf("повторный Emit: отправок = %d, ожидалось 0 (дедуп)", n)
	}
	if calls != 1 {
		t.Fatalf("вебхук вызван %d раз, ожидался 1", calls)
	}
}

func TestEngineEmitDeliveryErrorDoesNotBlockOthers(t *testing.T) {
	okCalls := 0
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		okCalls++
		w.WriteHeader(http.StatusOK)
	}))
	defer okSrv.Close()
	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer badSrv.Close()

	badCh := webhookChannel(t, badSrv.URL)
	fs := &fakeChannelStore{channels: []store.NotificationChannel{badCh, webhookChannel(t, okSrv.URL)}}
	e := &Engine{Store: fs, Sender: NewSender()}
	ev := testEvent()
	got := e.Emit(context.Background(), uuid.New(), ev)
	if got != 1 {
		t.Fatalf("отправок = %d, ожидалась 1 (второй канал успешен несмотря на провал первого)", got)
	}
	if okCalls != 1 {
		t.Fatalf("ok-вебхук вызван %d раз", okCalls)
	}
	// Ошибка зафиксирована в last_error падающего канала.
	if fs.failed[badCh.ID.String()+"|"+ev.Fingerprint()] == "" {
		t.Error("last_error падающего канала не записан")
	}
}

func TestEngineEmitStoreErrorIsNonFatal(t *testing.T) {
	fs := &fakeChannelStore{tryErr: errors.New("db down"), channels: []store.NotificationChannel{
		webhookChannel(t, "http://127.0.0.1:1/unreachable"),
	}}
	e := &Engine{Store: fs, Sender: NewSender()}
	if n := e.Emit(context.Background(), uuid.New(), testEvent()); n != 0 {
		t.Fatalf("при ошибке store отправок = %d, ожидалось 0", n)
	}
}

func TestEngineNilStoreSender(t *testing.T) {
	e := &Engine{}
	if n := e.Emit(context.Background(), uuid.New(), testEvent()); n != 0 {
		t.Fatalf("пустой движок: отправок = %d, ожидалось 0", n)
	}
}

func TestEventFingerprint(t *testing.T) {
	id := uuid.New()
	ev := Event{Type: "agent.offline", ObjectID: id}
	want := "agent.offline:" + id.String()
	if ev.Fingerprint() != want {
		t.Errorf("fingerprint = %q, ожидалось %q", ev.Fingerprint(), want)
	}
}
