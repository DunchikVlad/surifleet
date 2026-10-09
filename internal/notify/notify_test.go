package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendWebhookOK(t *testing.T) {
	var gotBody []byte
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotHeader = r.Header.Get("X-Custom")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := NewSender()
	err := s.SendWebhook(context.Background(),
		WebhookConfig{URL: srv.URL, Headers: map[string]string{"X-Custom": "v1"}},
		Message{Title: "Тест", Text: "тело", Severity: "info"})
	if err != nil {
		t.Fatalf("SendWebhook: %v", err)
	}
	if gotHeader != "v1" {
		t.Errorf("заголовок X-Custom не дошёл: %q", gotHeader)
	}
	var m map[string]any
	if err := json.Unmarshal(gotBody, &m); err != nil {
		t.Fatalf("тело не JSON: %v", err)
	}
	if m["title"] != "Тест" || m["severity"] != "info" {
		t.Errorf("поля сообщения: %v", m)
	}
}

func TestSendWebhookHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("bad gateway body"))
	}))
	defer srv.Close()
	err := NewSender().SendWebhook(context.Background(), WebhookConfig{URL: srv.URL}, Message{Title: "x"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Fatalf("ожидалась ошибка HTTP 502, получено: %v", err)
	}
}

func TestSendWebhookEmptyURL(t *testing.T) {
	if err := NewSender().SendWebhook(context.Background(), WebhookConfig{}, Message{}); err == nil {
		t.Fatal("пустой url — ожидалась ошибка")
	}
}

func TestSendTelegramOK(t *testing.T) {
	var gotPath, gotToken, gotChat, gotText string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = r.ParseForm()
		gotChat = r.Form.Get("chat_id")
		gotText = r.Form.Get("text")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	s := NewSender()
	s.TelegramAPIBase = srv.URL
	gotToken = "123:ABC"
	err := s.SendTelegram(context.Background(),
		TelegramConfig{BotToken: gotToken, ChatID: "-1001"},
		Message{Title: "Заголовок", Text: "текст"})
	if err != nil {
		t.Fatalf("SendTelegram: %v", err)
	}
	if !strings.Contains(gotPath, "/bot"+gotToken+"/sendMessage") {
		t.Errorf("путь запроса: %q", gotPath)
	}
	if gotChat != "-1001" {
		t.Errorf("chat_id: %q", gotChat)
	}
	if gotText != "Заголовок\nтекст" {
		t.Errorf("text: %q vs want %q", gotText, "Заголовок\nтекст")
	}
}

func TestSendTelegramAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "description": "chat not found"})
	}))
	defer srv.Close()
	s := NewSender()
	s.TelegramAPIBase = srv.URL
	err := s.SendTelegram(context.Background(), TelegramConfig{BotToken: "t", ChatID: "c"}, Message{Title: "x"})
	if err == nil || !strings.Contains(err.Error(), "chat not found") {
		t.Fatalf("ожидалась ошибка 'chat not found', получено: %v", err)
	}
}

func TestSendTelegramEmptyConfig(t *testing.T) {
	if err := NewSender().SendTelegram(context.Background(), TelegramConfig{}, Message{}); err == nil {
		t.Fatal("пустой конфиг — ожидалась ошибка")
	}
}
