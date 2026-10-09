// Движок уведомлений с дедупликацией (чанк 84, пр. 2 «уведомления с
// дедупликацией», п. 7 ТЗ «уведомления с дедупликацией и эскалацией»).
// Emit — точка входа для источников событий (хаб: offline/online
// агентов): выбирает включённые каналы организации, подавляет повторы в
// окне дедупликации (notification_deliveries, атомарный upsert в PG —
// конкурентные эмиттеры не задублируют) и отправляет через Sender.
package notify

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/surifleet/surifleet/internal/store"
)

// Event — событие-уведомление от источника (хаб, оркестратор).
type Event struct {
	// Type — тип события (agent.offline, agent.online, …): вместе с
	// ObjectID образует fingerprint дедупликации.
	Type string
	// ObjectID — объект события (agent_id и т.п.).
	ObjectID uuid.UUID
	// Title/Text/Severity — содержимое сообщения (см. Message).
	Title    string
	Text     string
	Severity string
	// Fields — структурные данные (только webhook).
	Fields map[string]any
}

// ChannelStore — часть store.Store, нужная движку (интерфейс для
// юнит-тестов без живой БД).
type ChannelStore interface {
	ListEnabled(ctx context.Context, orgID uuid.UUID) ([]store.NotificationChannel, error)
	TryDelivery(ctx context.Context, channelID uuid.UUID, fingerprint string, window time.Duration) (bool, error)
	FailDelivery(ctx context.Context, channelID uuid.UUID, fingerprint, errText string) error
}

// channelStoreAdapter — адаптер *store.Store к ChannelStore
// (репозиторий — поле структуры, методы сами не поднимаются).
type channelStoreAdapter struct{ st *store.Store }

func (a channelStoreAdapter) ListEnabled(ctx context.Context, orgID uuid.UUID) ([]store.NotificationChannel, error) {
	return a.st.NotificationChannels.ListEnabled(ctx, orgID)
}
func (a channelStoreAdapter) TryDelivery(ctx context.Context, channelID uuid.UUID, fingerprint string, window time.Duration) (bool, error) {
	return a.st.NotificationChannels.TryDelivery(ctx, channelID, fingerprint, window)
}
func (a channelStoreAdapter) FailDelivery(ctx context.Context, channelID uuid.UUID, fingerprint, errText string) error {
	return a.st.NotificationChannels.FailDelivery(ctx, channelID, fingerprint, errText)
}

// Engine — движок: store (каналы + дедуп) + sender (доставка).
type Engine struct {
	Store  ChannelStore
	Sender *Sender
	Log    *slog.Logger
	// DedupWindow — окно дедупликации одного события на канал
	// (default 10 мин, если 0).
	DedupWindow time.Duration
	// SendTimeout — предел на одну отправку (default 15 с).
	SendTimeout time.Duration
}

// NewEngine — движок с дефолтами (окно 10 мин, таймаут 15 с).
func NewEngine(st *store.Store, sender *Sender, log *slog.Logger) *Engine {
	if log == nil {
		log = slog.Default()
	}
	return &Engine{Store: channelStoreAdapter{st}, Sender: sender, Log: log, DedupWindow: 10 * time.Minute, SendTimeout: 15 * time.Second}
}

func (e *Engine) dedupWindow() time.Duration {
	if e.DedupWindow > 0 {
		return e.DedupWindow
	}
	return 10 * time.Minute
}

func (e *Engine) sendTimeout() time.Duration {
	if e.SendTimeout > 0 {
		return e.SendTimeout
	}
	return 15 * time.Second
}

// Fingerprint — ключ дедупликации события («type:object_id»).
func (ev Event) Fingerprint() string {
	return ev.Type + ":" + ev.ObjectID.String()
}

// logger — логгер движка (nil → slog.Default).
func (e *Engine) logger() *slog.Logger {
	if e.Log != nil {
		return e.Log
	}
	return slog.Default()
}

// Emit — обработать событие: отправить во все включённые каналы
// организации с дедупликацией. Синхронно по каналам (мало: единицы),
// ошибки одного канала не мешают остальным. Возвращает число реальных
// отправок (подавленные дедупом не считаются).
func (e *Engine) Emit(ctx context.Context, orgID uuid.UUID, ev Event) int {
	if e.Store == nil || e.Sender == nil {
		return 0
	}
	log := e.logger()
	channels, err := e.Store.ListEnabled(ctx, orgID)
	if err != nil {
		log.Error("notify: список каналов", "event", ev.Type, "err", err)
		return 0
	}
	sent := 0
	fp := ev.Fingerprint()
	msg := Message{Title: ev.Title, Text: ev.Text, Severity: ev.Severity, Fields: ev.Fields}
	for _, ch := range channels {
		ok, err := e.Store.TryDelivery(ctx, ch.ID, fp, e.dedupWindow())
		if err != nil {
			log.Error("notify: дедуп", "channel", ch.Name, "event", ev.Type, "err", err)
			continue
		}
		if !ok {
			log.Debug("notify: подавлено дедупликацией", "channel", ch.Name, "fingerprint", fp)
			continue
		}
		if err := e.deliver(ctx, ch, msg); err != nil {
			log.Warn("notify: отправка не удалась", "channel", ch.Name, "type", ch.Type,
				"event", ev.Type, "err", err)
			if fe := e.Store.FailDelivery(ctx, ch.ID, fp, err.Error()); fe != nil {
				log.Error("notify: запись last_error", "channel", ch.Name, "err", fe)
			}
			continue
		}
		log.Info("notify: отправлено", "channel", ch.Name, "type", ch.Type, "event", ev.Type,
			"object_id", ev.ObjectID)
		sent++
	}
	return sent
}

// deliver — одна отправка в канал с таймаутом.
func (e *Engine) deliver(ctx context.Context, ch store.NotificationChannel, msg Message) error {
	ctx, cancel := context.WithTimeout(ctx, e.sendTimeout())
	defer cancel()
	switch ch.Type {
	case "webhook":
		var cfg WebhookConfig
		if err := json.Unmarshal(ch.Config, &cfg); err != nil {
			return err
		}
		return e.Sender.SendWebhook(ctx, cfg, msg)
	case "telegram":
		var cfg TelegramConfig
		if err := json.Unmarshal(ch.Config, &cfg); err != nil {
			return err
		}
		return e.Sender.SendTelegram(ctx, cfg, msg)
	default:
		return nil
	}
}

// EmitAsync — fire-and-forget обёртка Emit для горячих путей (стримы
// агентов): отправка в фоне с отдельным контекстом (родительский ctx
// стрима умрёт раньше доставки).
func (e *Engine) EmitAsync(orgID uuid.UUID, ev Event) {
	if e == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), e.sendTimeout()+5*time.Second)
		defer cancel()
		e.Emit(ctx, orgID, ev)
	}()
}
