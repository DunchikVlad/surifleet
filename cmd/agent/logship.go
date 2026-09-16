package main

// Доставка операционных логов агента на сервер (chunk 13c).
//
// captureHandler — обёртка slog.Handler: каждая запись дополнительно
// попадает в logBuffer. logBuffer — ограниченная очередь (ring) последних
// N записей; при переполнении вытесняются самые старые (логи —
// best-effort, локальный файл остаётся источником истины).
// Буфер живёт в main и переживает сессии: записи, накопленные во время
// разрыва, уйдут первым LogBatch после переподключения.

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// logBufferMax — макс. число записей в очереди на отправку.
const logBufferMax = 500

// logEntry — одна запись лога в очереди.
type logEntry struct {
	seq   int64
	ts    time.Time
	level string
	msg   string
	attrs map[string]string
}

// logBuffer — потокобезопасная очередь записей лога на отправку.
type logBuffer struct {
	mu      sync.Mutex
	pending []logEntry
	seq     atomic.Int64
}

func newLogBuffer() *logBuffer { return &logBuffer{} }

// add добавляет запись в хвост очереди (seq присваивается здесь).
func (b *logBuffer) add(e logEntry) {
	e.seq = b.seq.Add(1)
	b.mu.Lock()
	b.pending = append(b.pending, e)
	if len(b.pending) > logBufferMax {
		b.pending = b.pending[len(b.pending)-logBufferMax:]
	}
	b.mu.Unlock()
}

// drain забирает до limit старейших записей, удаляя их из очереди.
func (b *logBuffer) drain(limit int) []logEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.pending) == 0 {
		return nil
	}
	if limit > len(b.pending) {
		limit = len(b.pending)
	}
	out := make([]logEntry, limit)
	copy(out, b.pending[:limit])
	b.pending = b.pending[limit:]
	return out
}

// requeueFront возвращает записи в голову очереди (отправка не удалась).
func (b *logBuffer) requeueFront(entries []logEntry) {
	if len(entries) == 0 {
		return
	}
	b.mu.Lock()
	b.pending = append(entries, b.pending...)
	if len(b.pending) > logBufferMax {
		b.pending = b.pending[len(b.pending)-logBufferMax:]
	}
	b.mu.Unlock()
}

// captureHandler — slog.Handler, дублирующий записи в logBuffer.
type captureHandler struct {
	next slog.Handler
	buf  *logBuffer
}

func (h *captureHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

func (h *captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &captureHandler{next: h.next.WithAttrs(attrs), buf: h.buf}
}

func (h *captureHandler) WithGroup(name string) slog.Handler {
	return &captureHandler{next: h.next.WithGroup(name), buf: h.buf}
}

func (h *captureHandler) Handle(ctx context.Context, r slog.Record) error {
	e := logEntry{ts: r.Time, level: r.Level.String(), msg: r.Message}
	r.Attrs(func(a slog.Attr) bool {
		if e.attrs == nil {
			e.attrs = map[string]string{}
		}
		e.attrs[a.Key] = a.Value.String()
		return true
	})
	h.buf.add(e)
	return h.next.Handle(ctx, r)
}
