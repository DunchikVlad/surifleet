// Package config — загрузка конфигурации сервера и агента SuriFleet.
//
// Источники (по возрастанию приоритета): значения по умолчанию → YAML-файл
// (путь из флага --config или env SURIFLEET_CONFIG) → переменные окружения
// с префиксом SURIFLEET_. Вложенность в env задаётся подчёркиваниями:
// SURIFLEET_SERVER_HTTP_ADDR → server.http_addr,
// SURIFLEET_SERVER_S3_ENDPOINT → server.s3.endpoint.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// envPrefix — префикс переменных окружения с переопределениями.
const envPrefix = "SURIFLEET_"

// envConfigPath — имя переменной с путём к файлу конфигурации
// (не является переопределением параметра).
const envConfigPath = "SURIFLEET_CONFIG"

// Duration — time.Duration с поддержкой YAML-строк вида "30s", "5m", "1h".
type Duration time.Duration

// D возвращает значение как time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// UnmarshalYAML реализует yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	if s == "" {
		*d = 0
		return nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("некорректная длительность %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}

// S3Config — параметры S3-совместимого хранилища (MinIO).
// Ключи доступа задаются только через env:
// SURIFLEET_SERVER_S3_ACCESS_KEY / SURIFLEET_SERVER_S3_SECRET_KEY.
type S3Config struct {
	Endpoint string `yaml:"endpoint"` // внутренний адрес (для сервера: upload/stat)
	// PublicEndpoint — адрес, доступный агентам (для подписанных URL;
	// пусто → подпись тем же endpoint).
	PublicEndpoint string `yaml:"public_endpoint"`
	Bucket         string `yaml:"bucket"`
	AccessKey      string `yaml:"access_key"`
	SecretKey      string `yaml:"secret_key"`
	UseSSL         bool   `yaml:"use_ssl"`
}

// ServerConfig — конфигурация сервера (секция server в YAML).
type ServerConfig struct {
	Role          string   `yaml:"role"` // api | hub | all
	HTTPAddr      string   `yaml:"http_addr"`
	GRPCAddr      string   `yaml:"grpc_addr"`
	EnrollAddr    string   `yaml:"enroll_addr"`
	MetricsAddr   string   `yaml:"metrics_addr"`
	CADir         string   `yaml:"ca_dir"`
	HubEndpoints  []string `yaml:"hub_endpoints"` // адреса Hub для агентов (отдаются в Enroll)
	CertSANs      []string `yaml:"cert_sans"`     // SAN серверного сертификата (DNS/IP)
	PostgresDSN   string   `yaml:"postgres_dsn"`
	RedisAddr     string   `yaml:"redis_addr"`
	NatsURL       string   `yaml:"nats_url"`
	ClickHouseDSN string   `yaml:"clickhouse_dsn"`
	// IocSweepInterval — интервал фонового свипера просроченных IOC
	// (active с expires_at < now() → expired). 0 — свипер выключен.
	IocSweepInterval Duration `yaml:"ioc_sweep_interval"`
	// FeedSyncInterval — интервал фонового планировщика авто-синка фидов
	// (фиды с enabled и schedule-длительностью, наступившей по last_sync_at).
	// 0 — планировщик выключен.
	FeedSyncInterval Duration `yaml:"feed_sync_interval"`
	S3               S3Config `yaml:"s3"`
	LogLevel         string   `yaml:"log_level"`
}

// DefaultServer возвращает конфигурацию сервера с дефолтами.
func DefaultServer() *ServerConfig {
	return &ServerConfig{
		Role:             "all",
		HTTPAddr:         ":8080",
		GRPCAddr:         ":8443",
		EnrollAddr:       ":8444",
		MetricsAddr:      ":9090",
		CADir:            "./data/ca",
		HubEndpoints:     []string{"localhost:8443"},
		CertSANs:         []string{"localhost", "127.0.0.1", "::1"},
		IocSweepInterval: Duration(time.Minute),
		FeedSyncInterval: Duration(time.Minute),
		LogLevel:         "info",
		S3: S3Config{
			Endpoint: "localhost:9000",
			Bucket:   "surifleet-rulesets",
		},
	}
}

// Validate проверяет обязательные поля и допустимые значения.
func (c *ServerConfig) Validate() error {
	switch c.Role {
	case "api", "hub", "all":
	default:
		return fmt.Errorf("server.role: недопустимое значение %q (api|hub|all)", c.Role)
	}
	if c.HTTPAddr == "" {
		return fmt.Errorf("server.http_addr: обязательное поле")
	}
	if c.GRPCAddr == "" {
		return fmt.Errorf("server.grpc_addr: обязательное поле")
	}
	if c.EnrollAddr == "" {
		return fmt.Errorf("server.enroll_addr: обязательное поле")
	}
	if c.CADir == "" {
		return fmt.Errorf("server.ca_dir: обязательное поле")
	}
	if len(c.HubEndpoints) == 0 {
		return fmt.Errorf("server.hub_endpoints: нужен хотя бы один адрес Hub для агентов")
	}
	if c.PostgresDSN == "" {
		return fmt.Errorf("server.postgres_dsn: обязательное поле")
	}
	if c.RedisAddr == "" {
		return fmt.Errorf("server.redis_addr: обязательное поле")
	}
	if c.NatsURL == "" {
		return fmt.Errorf("server.nats_url: обязательное поле")
	}
	if c.S3.Endpoint == "" || c.S3.Bucket == "" {
		return fmt.Errorf("server.s3.endpoint и server.s3.bucket: обязательные поля")
	}
	if _, err := ParseLogLevel(c.LogLevel); err != nil {
		return fmt.Errorf("server.log_level: %w", err)
	}
	return nil
}

// AgentConfig — конфигурация агента (секция agent в YAML).
type AgentConfig struct {
	ServerAddr string `yaml:"server_addr"` // Hub (mTLS-стрим), host:port
	EnrollAddr string `yaml:"enroll_addr"` // Enrollment (TLS без client cert), host:port
	JoinToken  string `yaml:"join_token"`  // одноразовый токен (только для первого enrollment)
	CertFile   string `yaml:"cert_file"`   // путь к сертификату mTLS (после enrollment)
	KeyFile    string `yaml:"key_file"`
	CAFile     string `yaml:"ca_file"`
	DataDir    string `yaml:"data_dir"`
	LogFile    string `yaml:"log_file"` // пусто → DataDir/agent.log

	LogMaxSizeMB  int `yaml:"log_max_size_mb"`
	LogMaxBackups int `yaml:"log_max_backups"`
	LogMaxAgeDays int `yaml:"log_max_age_days"`

	BackoffMin          Duration `yaml:"backoff_min"`
	BackoffMax          Duration `yaml:"backoff_max"`
	HeartbeatInterval   Duration `yaml:"heartbeat_interval"`
	StateReportInterval Duration `yaml:"state_report_interval"`
	MetricsInterval     Duration `yaml:"metrics_interval"`

	LogLevel string `yaml:"log_level"`
}

// DefaultAgent возвращает конфигурацию агента с дефолтами.
func DefaultAgent() *AgentConfig {
	return &AgentConfig{
		ServerAddr:          "localhost:8443",
		EnrollAddr:          "localhost:8444",
		DataDir:             "./data",
		LogMaxSizeMB:        100,
		LogMaxBackups:       5,
		LogMaxAgeDays:       30,
		BackoffMin:          Duration(time.Second),
		BackoffMax:          Duration(60 * time.Second),
		HeartbeatInterval:   Duration(30 * time.Second),
		StateReportInterval: Duration(5 * time.Minute),
		MetricsInterval:     Duration(time.Minute),
		LogLevel:            "info",
	}
}

// Validate проверяет обязательные поля.
func (c *AgentConfig) Validate() error {
	if c.ServerAddr == "" {
		return fmt.Errorf("agent.server_addr: обязательное поле")
	}
	if c.DataDir == "" {
		return fmt.Errorf("agent.data_dir: обязательное поле")
	}
	if c.BackoffMin.D() <= 0 {
		return fmt.Errorf("agent.backoff_min: должен быть > 0")
	}
	if c.BackoffMax.D() < c.BackoffMin.D() {
		return fmt.Errorf("agent.backoff_max: должен быть >= backoff_min")
	}
	if _, err := ParseLogLevel(c.LogLevel); err != nil {
		return fmt.Errorf("agent.log_level: %w", err)
	}
	return nil
}

// LoadSection загружает секцию section ("server"/"agent") из YAML-файла path
// в out (предварительно заполненный дефолтами), применяет переопределения
// из env SURIFLEET_* и вызывает Validate, если out её реализует.
// Пустой path — только дефолты и env.
func LoadSection(path, section string, out any) error {
	raw := map[string]any{}
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("чтение файла конфигурации %s: %w", path, err)
		}
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("разбор YAML %s: %w", path, err)
		}
	}

	applyEnvOverrides(raw)

	sub, _ := raw[section].(map[string]any)
	if sub == nil {
		sub = map[string]any{}
	}
	merged, err := yaml.Marshal(sub)
	if err != nil {
		return fmt.Errorf("сборка итоговой конфигурации: %w", err)
	}
	if err := yaml.Unmarshal(merged, out); err != nil {
		return fmt.Errorf("разбор секции %q: %w", section, err)
	}
	if v, ok := out.(interface{ Validate() error }); ok {
		if err := v.Validate(); err != nil {
			return fmt.Errorf("валидация конфигурации: %w", err)
		}
	}
	return nil
}

// applyEnvOverrides применяет переменные SURIFLEET_* к дереву конфигурации.
// Сегменты имени сопоставляются с ключами по правилу наиболее длинного
// совпадения: SURIFLEET_SERVER_HTTP_ADDR → server → http_addr.
func applyEnvOverrides(root map[string]any) {
	for _, kv := range os.Environ() {
		name, val, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(name, envPrefix) || name == envConfigPath {
			continue
		}
		segs := strings.Split(strings.ToLower(strings.TrimPrefix(name, envPrefix)), "_")
		setNested(root, segs, parseEnvValue(val))
	}
}

// setNested устанавливает значение по цепочке сегментов, сопоставляя их
// с существующими ключами (наиболее длинное совпадение, т.к. ключи сами
// содержат подчёркивания: http_addr, log_level).
func setNested(m map[string]any, segs []string, val any) {
	for n := len(segs); n >= 1; n-- {
		key := strings.Join(segs[:n], "_")
		child, ok := m[key]
		if !ok {
			continue
		}
		if n == len(segs) {
			m[key] = val
			return
		}
		if cm, ok := child.(map[string]any); ok {
			setNested(cm, segs[n:], val)
			return
		}
	}
	// Ключ не существует — создаём из всех оставшихся сегментов.
	m[strings.Join(segs, "_")] = val
}

// parseEnvValue приводит строку env к нативному типу (int/float/bool/string),
// чтобы YAML-декодер корректно заполнил типизированные поля.
func parseEnvValue(s string) any {
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	if b, err := strconv.ParseBool(s); err == nil {
		return b
	}
	return s
}

// ParseLogLevel разбирает строковый уровень логирования.
func ParseLogLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "", "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("недопустимый уровень %q (debug|info|warn|error)", s)
	}
}
