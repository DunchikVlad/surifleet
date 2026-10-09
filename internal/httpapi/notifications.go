// Каналы уведомлений webhook/Telegram (чанк 83, пр. 2 roadmap; п. 5.5 ТЗ):
// CRUD /notification_channels + POST /{id}/test (живая проверка канала).
// bot_token telegram-канала — writeOnly (в ответах обнуляется, Public).
// Движок событий с дедупликацией — следующий чанк.
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/surifleet/surifleet/internal/notify"
	"github.com/surifleet/surifleet/internal/store"
)

// notificationChannelInput — POST /notification_channels.
type notificationChannelInput struct {
	Name    string          `json:"name"`
	Type    string          `json:"type"`
	Config  json.RawMessage `json:"config"`
	Enabled *bool           `json:"enabled"`
}

var notificationChannelTypes = map[string]bool{"webhook": true, "telegram": true}

// validate — базовая валидация + валидация config по типу канала.
func (in *notificationChannelInput) validate(forCreate bool) fieldErrors {
	var fe fieldErrors
	if forCreate {
		if in.Name = strings.TrimSpace(in.Name); in.Name == "" {
			fe["name"] = "обязательное поле"
		}
		if !notificationChannelTypes[in.Type] {
			fe["type"] = "webhook | telegram"
		}
	} else if in.Type != "" && !notificationChannelTypes[in.Type] {
		fe["type"] = "webhook | telegram"
	}
	if len(in.Config) > 0 {
		if err := validateChannelConfig(in.Type, in.Config); err != nil {
			fe["config"] = err.Error()
		}
	}
	return fe
}

// validateChannelConfig — строгая проверка config по типу: webhook —
// {url http/https}; telegram — {bot_token, chat_id} (при создании оба
// обязательны; при PATCH через mergeConfig обязательность обеспечивает
// существующая запись).
func validateChannelConfig(typ string, raw json.RawMessage) error {
	switch typ {
	case "webhook":
		var c notify.WebhookConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			return errors.New("не JSON: " + err.Error())
		}
		u, err := url.Parse(c.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("url должен быть http(s)://…")
		}
	case "telegram":
		var c notify.TelegramConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			return errors.New("не JSON: " + err.Error())
		}
		if strings.TrimSpace(c.BotToken) == "" {
			return errors.New("bot_token обязателен")
		}
		if strings.TrimSpace(c.ChatID) == "" {
			return errors.New("chat_id обязателен")
		}
	}
	return nil
}

// createNotificationChannel — POST /notification_channels (notifications.write).
func (h *handlers) createNotificationChannel(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	var in notificationChannelInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if fe := in.validate(true); len(fe) > 0 {
		writeValidation(w, fe)
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	c, err := h.d.Store.NotificationChannels.Create(r.Context(), orgID, in.Name, in.Type, in.Config, enabled)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	objType := "notification_channel"
	h.audit(r, identityFrom(r.Context()), "notification_channels.create", &objType, &c.ID, "success", "канал "+c.Name+" ("+c.Type+")")
	writeJSON(w, http.StatusCreated, c.Public())
}

// listNotificationChannels — GET /notification_channels (notifications.read).
func (h *handlers) listNotificationChannels(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	cursor, limit, ok := parsePage(w, r)
	if !ok {
		return
	}
	items, next, err := h.d.Store.NotificationChannels.List(r.Context(), orgID, cursor, limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]store.NotificationChannel, len(items))
	for i, c := range items {
		out[i] = c.Public()
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "next_cursor": next})
}

// getNotificationChannel — GET /notification_channels/{id} (notifications.read).
func (h *handlers) getNotificationChannel(w http.ResponseWriter, r *http.Request) {
	c, ok := h.channelScoped(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, c.Public())
}

// updateNotificationChannel — PATCH /notification_channels/{id}
// (notifications.write). Пустой bot_token в config telegram — «не менять»
// (сохраняется старый; как client_secret у SSO).
func (h *handlers) updateNotificationChannel(w http.ResponseWriter, r *http.Request) {
	cur, ok := h.channelScoped(w, r)
	if !ok {
		return
	}
	var in notificationChannelInput
	if !decodeJSON(w, r, &in) {
		return
	}
	// Тип менять нельзя — config привязан к типу (иначе рассинхрон).
	if in.Type != "" && in.Type != cur.Type {
		writeValidation(w, fieldErrors{"type": "тип канала менять нельзя"})
		return
	}
	if fe := in.validate(false); len(fe) > 0 {
		writeValidation(w, fe)
		return
	}
	// telegram: пустой bot_token в PATCH — сохранить старый.
	if len(in.Config) > 0 && cur.Type == "telegram" {
		var cfg notify.TelegramConfig
		if err := json.Unmarshal(in.Config, &cfg); err == nil && strings.TrimSpace(cfg.BotToken) == "" {
			cfg.BotToken = cur.BotToken()
			if merged, err := json.Marshal(cfg); err == nil {
				in.Config = merged
			}
		}
	}
	var name *string
	if in.Name != "" {
		name = &in.Name
	}
	c, err := h.d.Store.NotificationChannels.Update(r.Context(), cur.ID, name, in.Config, in.Enabled)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	objType := "notification_channel"
	h.audit(r, identityFrom(r.Context()), "notification_channels.update", &objType, &c.ID, "success", "канал "+c.Name)
	writeJSON(w, http.StatusOK, c.Public())
}

// deleteNotificationChannel — DELETE /notification_channels/{id}
// (notifications.write).
func (h *handlers) deleteNotificationChannel(w http.ResponseWriter, r *http.Request) {
	cur, ok := h.channelScoped(w, r)
	if !ok {
		return
	}
	if err := h.d.Store.NotificationChannels.Delete(r.Context(), cur.ID); err != nil {
		writeStoreError(w, err)
		return
	}
	objType := "notification_channel"
	h.audit(r, identityFrom(r.Context()), "notification_channels.delete", &objType, &cur.ID, "success", "канал "+cur.Name)
	w.WriteHeader(http.StatusNoContent)
}

// testNotificationChannel — POST /notification_channels/{id}/test
// (notifications.write): живая проверка канала — тестовое сообщение
// через notify.Sender. 200 {ok:true} | 502 {error}.
func (h *handlers) testNotificationChannel(w http.ResponseWriter, r *http.Request) {
	c, ok := h.channelScoped(w, r)
	if !ok {
		return
	}
	if h.d.Notify == nil {
		writeError(w, http.StatusServiceUnavailable, CodeInternal, "отправка уведомлений не настроена на сервере", nil)
		return
	}
	msg := notify.Message{
		Title:    "SuriFleet: тестовое уведомление",
		Text:     "Канал «" + c.Name + "» (" + c.Type + ") настроен верно.",
		Severity: "info",
		Fields:   map[string]any{"channel_id": c.ID.String(), "channel_name": c.Name, "type": c.Type},
	}
	var err error
	switch c.Type {
	case "webhook":
		var cfg notify.WebhookConfig
		if e := json.Unmarshal(c.Config, &cfg); e != nil {
			err = e
			break
		}
		err = h.d.Notify.SendWebhook(r.Context(), cfg, msg)
	case "telegram":
		var cfg notify.TelegramConfig
		if e := json.Unmarshal(c.Config, &cfg); e != nil {
			err = e
			break
		}
		err = h.d.Notify.SendTelegram(r.Context(), cfg, msg)
	}
	if err != nil {
		objType := "notification_channel"
		h.audit(r, identityFrom(r.Context()), "notification_channels.test", &objType, &c.ID, "error", err.Error())
		writeError(w, http.StatusBadGateway, CodeInternal, "отправка не удалась: "+err.Error(), nil)
		return
	}
	objType := "notification_channel"
	h.audit(r, identityFrom(r.Context()), "notification_channels.test", &objType, &c.ID, "success", "канал "+c.Name)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// channelScoped — канал по {id} с проверкой принадлежности организации
// (чужой → 404, как прочие сущности org-scope).
func (h *handlers) channelScoped(w http.ResponseWriter, r *http.Request) (store.NotificationChannel, bool) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return store.NotificationChannel{}, false
	}
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return store.NotificationChannel{}, false
	}
	c, err := h.d.Store.NotificationChannels.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return store.NotificationChannel{}, false
	}
	if c.OrganizationID != orgID {
		writeError(w, http.StatusNotFound, CodeNotFound, "ресурс не найден", nil)
		return store.NotificationChannel{}, false
	}
	return c, true
}
