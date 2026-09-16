// Package chlogs — минимальный клиент ClickHouse по HTTP-интерфейсу
// (chunk 13c): операционные логи агентов в таблице surifleet.agent_logs.
//
// DSN: http(s)://host:port[/database] либо clickhouse://host:port[/database]
// (native-протокол: порты 9000/9900 автоматически заменяются на HTTP 8123).
// Аутентификация — пользователь default без пароля (dev-стенд).
package chlogs

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

// defaultHTTPPort — HTTP-порт, подставляемый вместо native 9000/9900.
const defaultHTTPPort = "8123"

// Client — клиент ClickHouse (HTTP).
type Client struct {
	base string // http://host:port
	db   string // база (по умолчанию surifleet)
	hc   *http.Client
}

// AgentLogRow — строка таблицы surifleet.agent_logs.
type AgentLogRow struct {
	AgentID    string `json:"agent_id"`
	InstanceID string `json:"instance_id"`
	Ts         string `json:"ts"` // "2006-01-02 15:04:05.000" (UTC)
	Level      string `json:"level"`
	Message    string `json:"message"`
}

// tsLayout — формат DateTime64(3) в JSONEachRow.
const tsLayout = "2006-01-02 15:04:05.000"

// New разбирает DSN и возвращает клиента.
func New(dsn string) (*Client, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("clickhouse dsn: %w", err)
	}
	switch u.Scheme {
	case "http", "https":
		// как есть
	case "clickhouse":
		// Native-DSN → HTTP: меняем схему и native-порт на HTTP.
		u.Scheme = "http"
		if p := u.Port(); p == "9000" || p == "9900" || p == "" {
			u.Host = u.Hostname() + ":" + defaultHTTPPort
		}
	default:
		return nil, fmt.Errorf("clickhouse dsn: неподдерживаемая схема %q (http|https|clickhouse)", u.Scheme)
	}
	db := strings.TrimPrefix(u.Path, "/")
	if db == "" {
		db = "surifleet"
	}
	return &Client{
		base: fmt.Sprintf("%s://%s", u.Scheme, u.Host),
		db:   db,
		hc:   &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// exec выполняет запрос без результата (DDL/INSERT).
func (c *Client) exec(ctx context.Context, query string, body []byte) error {
	u := c.base + "/?query=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("clickhouse HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// EnsureTable создаёт базу и таблицу agent_logs, если их нет (старт сервера).
func (c *Client) EnsureTable(ctx context.Context) error {
	if err := c.exec(ctx, "CREATE DATABASE IF NOT EXISTS "+c.db, nil); err != nil {
		return fmt.Errorf("create database: %w", err)
	}
	ddl := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.agent_logs (
  agent_id String,
  instance_id String,
  ts DateTime64(3, 'UTC'),
  level LowCardinality(String),
  message String
) ENGINE = MergeTree ORDER BY (agent_id, ts)`, c.db)
	if err := c.exec(ctx, ddl, nil); err != nil {
		return fmt.Errorf("create table: %w", err)
	}
	return nil
}

// InsertAgentLogs вставляет батч записей (JSONEachRow).
func (c *Client) InsertAgentLogs(ctx context.Context, rows []AgentLogRow) error {
	if len(rows) == 0 {
		return nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			return fmt.Errorf("encode row: %w", err)
		}
	}
	q := fmt.Sprintf("INSERT INTO %s.agent_logs (agent_id, instance_id, ts, level, message) FORMAT JSONEachRow", c.db)
	return c.exec(ctx, q, buf.Bytes())
}

// AgentLogs возвращает последние limit записей агента (ts DESC).
func (c *Client) AgentLogs(ctx context.Context, agentID string, limit int) ([]AgentLogRow, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	q := fmt.Sprintf(`SELECT agent_id, instance_id, ts, level, message
FROM %s.agent_logs WHERE agent_id = %s ORDER BY ts DESC LIMIT %d FORMAT JSONEachRow`,
		c.db, quoteString(agentID), limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/?query="+url.QueryEscape(q), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("clickhouse HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var rows []AgentLogRow
	dec := json.NewDecoder(resp.Body)
	for dec.More() {
		var r AgentLogRow
		if err := dec.Decode(&r); err != nil {
			return rows, fmt.Errorf("decode row: %w", err)
		}
		rows = append(rows, r)
	}
	return rows, nil
}

// FormatTS переводит время в формат DateTime64(3) UTC для вставки.
func FormatTS(t time.Time) string { return t.UTC().Format(tsLayout) }

// ParseTS переводит строку DateTime64 из ClickHouse обратно во время (UTC).
func ParseTS(s string) (time.Time, error) {
	return time.ParseInLocation(tsLayout, s, time.UTC)
}

// quoteString — экранирование строкового литерала ClickHouse.
func quoteString(s string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `'`, `\'`) + "'"
}
