// Package notify — отправка уведомлений в каналы webhook/Telegram
// (чанк 83, пр. 2 roadmap; п. 5.5 ТЗ). Без очереди и дедупликации —
// синхронная отправка с таймаутом; движок событий с дедупликацией —
// следующий чанк.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Message — одно уведомление для отправки в канал.
type Message struct {
	// Title — короткий заголовок («Агент offline», «Тестовое уведомление»).
	Title string `json:"title"`
	// Text — тело сообщения (plain text; в Telegram уходит как есть).
	Text string `json:"text"`
	// Severity — info | warning | critical (для webhook-потребителей).
	Severity string `json:"severity,omitempty"`
	// Fields — произвольные структурные данные (только webhook).
	Fields map[string]any `json:"fields,omitempty"`
}

// WebhookConfig — config канала типа webhook.
type WebhookConfig struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

// TelegramConfig — config канала типа telegram. BotToken — секрет
// (writeOnly в API, наружу не отдаётся).
type TelegramConfig struct {
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

// Sender — синхронный отправитель с общим HTTP-клиентом (таймаут 10 с).
type Sender struct {
	HTTP *http.Client
	// TelegramAPIBase — базовый URL Bot API (пусто — https://api.telegram.org);
	// переопределяется в тестах (httptest).
	TelegramAPIBase string
}

// NewSender — отправитель с дефолтным клиентом (10 с на запрос).
func NewSender() *Sender {
	return &Sender{HTTP: &http.Client{Timeout: 10 * time.Second}}
}

func (s *Sender) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// maxBody — предел чтения тела ответа при ошибке (чтобы не раздувать лог).
const maxBody = 4 << 10

// SendWebhook — POST JSON в произвольный webhook. Тело — Message + шапка
// события; не-2xx → ошибка со статусом и фрагментом тела.
func (s *Sender) SendWebhook(ctx context.Context, cfg WebhookConfig, m Message) error {
	if cfg.URL == "" {
		return fmt.Errorf("webhook: пустой url")
	}
	body, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("webhook: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return fmt.Errorf("webhook: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		frag, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		return fmt.Errorf("webhook: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(frag)))
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
	return nil
}

// SendTelegram — сообщение через Telegram Bot API (sendMessage).
// Ответ ok=false → ошибка с description.
func (s *Sender) SendTelegram(ctx context.Context, cfg TelegramConfig, m Message) error {
	if cfg.BotToken == "" || cfg.ChatID == "" {
		return fmt.Errorf("telegram: пустой bot_token/chat_id")
	}
	text := m.Title
	if m.Text != "" {
		if text != "" {
			text += "\n"
		}
		text += m.Text
	}
	form := url.Values{"chat_id": {cfg.ChatID}, "text": {text}}
	base := s.TelegramAPIBase
	if base == "" {
		base = "https://api.telegram.org"
	}
	apiURL := strings.TrimSuffix(base, "/") + "/bot" + cfg.BotToken + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("telegram: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.client().Do(req)
	if err != nil {
		return fmt.Errorf("telegram: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	var tr struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(raw, &tr); err != nil {
		return fmt.Errorf("telegram: HTTP %d, не-JSON ответ: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if !tr.OK {
		return fmt.Errorf("telegram: %s", tr.Description)
	}
	return nil
}
