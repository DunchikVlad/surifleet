package store

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Organization — тенант (таблица organizations).
type Organization struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"`
	Description *string   `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// OrganizationInput — создание организации (openapi OrganizationInput).
type OrganizationInput struct {
	Name        string  `json:"name"`
	Slug        string  `json:"slug"`
	Description *string `json:"description"`
}

// OrganizationPatch — частичное обновление (openapi OrganizationUpdateInput):
// nil-поле означает «не изменять». Slug по контракту API иммутабелен.
type OrganizationPatch struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

// Cluster — логическая группа хостов организации (таблица clusters).
type Cluster struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"organization_id"`
	Name           string    `json:"name"`
	Description    *string   `json:"description"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ClusterInput — создание кластера (openapi ClusterInput).
type ClusterInput struct {
	OrganizationID uuid.UUID `json:"organization_id"`
	Name           string    `json:"name"`
	Description    *string   `json:"description"`
}

// ClusterPatch — частичное обновление кластера (openapi ClusterUpdateInput).
type ClusterPatch struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

// Host — машина с агентом (таблица hosts).
type Host struct {
	ID          uuid.UUID         `json:"id"`
	ClusterID   uuid.UUID         `json:"cluster_id"`
	Hostname    string            `json:"hostname"`
	IPAddresses []string          `json:"ip_addresses"`
	OS          *string           `json:"os"`
	Labels      map[string]string `json:"labels"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// HostInput — ручная регистрация хоста (openapi HostInput).
type HostInput struct {
	ClusterID   uuid.UUID         `json:"cluster_id"`
	Hostname    string            `json:"hostname"`
	IPAddresses []string          `json:"ip_addresses"`
	OS          *string           `json:"os"`
	Labels      map[string]string `json:"labels"`
}

// HostPatch — частичное обновление хоста (openapi HostUpdateInput).
type HostPatch struct {
	Hostname *string           `json:"hostname"`
	Labels   map[string]string `json:"labels"`
}

// Instance — инстанс Suricata на хосте (таблица instances):
// свой suricata.yaml, каталоги правил/логов, интерфейсы захвата, systemd-юнит.
// organization_id не хранится — вычисляется через host→cluster→org.
type Instance struct {
	ID                uuid.UUID `json:"id"`
	HostID            uuid.UUID `json:"host_id"`
	Name              string    `json:"name"`
	ConfigPath        string    `json:"config_path"`
	RulesDir          string    `json:"rules_dir"`
	LogDir            string    `json:"log_dir"`
	CaptureInterfaces []string  `json:"capture_interfaces"`
	SuricataVersion   *string   `json:"suricata_version"`
	SystemdUnit       *string   `json:"systemd_unit"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// InstanceInput — ручное создание инстанса (openapi InstanceInput).
type InstanceInput struct {
	HostID            uuid.UUID `json:"host_id"`
	Name              string    `json:"name"`
	ConfigPath        string    `json:"config_path"`
	RulesDir          string    `json:"rules_dir"`
	LogDir            string    `json:"log_dir"`
	CaptureInterfaces []string  `json:"capture_interfaces"`
	SystemdUnit       *string   `json:"systemd_unit"`
}

// InstancePatch — частичное обновление инстанса (openapi InstanceUpdateInput):
// nil-поле означает «не изменять».
type InstancePatch struct {
	Name              *string  `json:"name"`
	ConfigPath        *string  `json:"config_path"`
	RulesDir          *string  `json:"rules_dir"`
	LogDir            *string  `json:"log_dir"`
	CaptureInterfaces []string `json:"capture_interfaces"`
	SystemdUnit       *string  `json:"systemd_unit"`
}

// InstanceUpsertInput — подтверждённый инстанс из DiscoveryReport
// (confirm_discovery): upsert по (host_id, name). build_flags из отчёта
// в instances не хранится (остаётся в hosts.discovery).
type InstanceUpsertInput struct {
	Name              string   `json:"name"`
	ConfigPath        string   `json:"config_path"`
	RulesDir          string   `json:"rules_dir"`
	LogDir            string   `json:"log_dir"`
	CaptureInterfaces []string `json:"capture_interfaces"`
	SuricataVersion   string   `json:"suricata_version"`
	BuildFlags        string   `json:"build_flags"`
	SystemdUnit       string   `json:"systemd_unit"`
}

// Rule — правило мастер-репозитория (таблица rules; по sid в рамках орг.).
// priority/threshold/status/tags — тюнинг аналитика: импорт их НЕ перетирает.
type Rule struct {
	ID             uuid.UUID       `json:"id"`
	OrganizationID uuid.UUID       `json:"organization_id"`
	SID            int64           `json:"sid"`
	Msg            *string         `json:"msg"`
	Category       *string         `json:"category"`
	Tags           []string        `json:"tags"`
	Status         string          `json:"status"`
	Priority       *int            `json:"priority"`
	Threshold      json.RawMessage `json:"threshold"`
	SourceType     string          `json:"source_type"`
	FeedID         *uuid.UUID      `json:"feed_id"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// RulePatch — тюнинг аналитика (openapi RuleUpdateInput): nil — «не менять».
type RulePatch struct {
	Category  *string         `json:"category"`
	Tags      []string        `json:"tags"`
	Status    *string         `json:"status"`
	Priority  *int            `json:"priority"`
	Threshold json.RawMessage `json:"threshold"`
}

// RuleFilter — фильтры списка/bulk (openapi RuleFilter + sid/q для list).
type RuleFilter struct {
	Status   string
	Category string
	Tag      string
	Source   string
	FeedID   uuid.UUID
	SID      int64
	Q        string // подстрока по msg (только list)
}

// RuleRevision — версия правила (таблица rule_revisions): sid + revision +
// raw + sha256; parsed — разобранные поля (classtype, reference, ...).
type RuleRevision struct {
	ID        uuid.UUID       `json:"id"`
	RuleID    uuid.UUID       `json:"rule_id"`
	SID       int64           `json:"sid"`
	Revision  int             `json:"revision"`
	Raw       string          `json:"raw"`
	Hash      string          `json:"hash"`
	Parsed    json.RawMessage `json:"parsed,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

// RulesetVersion — версия ruleset (таблица ruleset_versions):
// sha256 — content-addressed блоб в S3 (s3_key), manifest — состав.
type RulesetVersion struct {
	ID             uuid.UUID       `json:"id"`
	OrganizationID uuid.UUID       `json:"organization_id"`
	Version        string          `json:"version"`
	SHA256         string          `json:"sha256"`
	S3Key          string          `json:"s3_key"`
	Manifest       json.RawMessage `json:"manifest,omitempty"`
	RuleCount      int             `json:"rule_count"`
	CreatedBy      *uuid.UUID      `json:"created_by"`
	CreatedAt      time.Time       `json:"created_at"`
}

// Deployment — волновой деплой ruleset (таблица deployments).
type Deployment struct {
	ID               uuid.UUID       `json:"id"`
	OrganizationID   uuid.UUID       `json:"organization_id"`
	RulesetVersionID uuid.UUID       `json:"ruleset_version_id"`
	DeployTemplateID *uuid.UUID      `json:"deploy_template_id"`
	Targeting        json.RawMessage `json:"targeting"`
	BatchSize        int             `json:"batch_size"`
	Concurrency      int             `json:"concurrency"`
	CanarySize       int             `json:"canary_size"`
	Status           string          `json:"status"`
	InitiatedBy      *uuid.UUID      `json:"initiated_by"`
	StartedAt        *time.Time      `json:"started_at"`
	PausedAt         *time.Time      `json:"paused_at"`
	FinishedAt       *time.Time      `json:"finished_at"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

// DeploymentProgress — сводка по задачам деплоя (openapi DeploymentProgress).
type DeploymentProgress struct {
	Total       int `json:"total"`
	Pending     int `json:"pending"`
	Running     int `json:"running"`
	Succeeded   int `json:"succeeded"`
	Failed      int `json:"failed"`
	CurrentWave int `json:"current_wave"`
}

// DeploymentTask — задача деплоя на один инстанс (таблица deployment_tasks).
type DeploymentTask struct {
	ID           uuid.UUID       `json:"id"`
	DeploymentID uuid.UUID       `json:"deployment_id"`
	InstanceID   uuid.UUID       `json:"instance_id"`
	Wave         int             `json:"wave"`
	Status       string          `json:"status"`
	Attempts     int             `json:"attempts"`
	MaxAttempts  int             `json:"max_attempts"`
	Result       json.RawMessage `json:"result,omitempty"`
	Error        *string         `json:"error"`
	StartedAt    *time.Time      `json:"started_at"`
	FinishedAt   *time.Time      `json:"finished_at"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// DesiredState — целевое состояние инстанса (таблица desired_state).
type DesiredState struct {
	InstanceID       uuid.UUID       `json:"instance_id"`
	RulesetVersionID uuid.UUID       `json:"ruleset_version_id"`
	ComputedRules    json.RawMessage `json:"computed_rules"`
	CalcVersion      int64           `json:"calc_version"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

// ActualState — фактическое состояние инстанса (таблица actual_state).
type ActualState struct {
	InstanceID       uuid.UUID       `json:"instance_id"`
	RulesetHash      *string         `json:"ruleset_hash"`
	LoadedRules      json.RawMessage `json:"loaded_rules"`
	FailedRules      json.RawMessage `json:"failed_rules"`
	LastReloadResult json.RawMessage `json:"last_reload_result"`
	ReportedAt       *time.Time      `json:"reported_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

// InstanceCompliance — текущая сводка соответствия (таблица instance_compliance).
type InstanceCompliance struct {
	InstanceID uuid.UUID       `json:"instance_id"`
	Status     string          `json:"status"`
	Details    json.RawMessage `json:"details"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// DeployEvent — событие деплоя (таблица deploy_events, партиционирована).
type DeployEvent struct {
	ID               uuid.UUID       `json:"id"`
	CreatedAt        time.Time       `json:"created_at"`
	DeploymentID     uuid.UUID       `json:"deployment_id"`
	DeploymentTaskID *uuid.UUID      `json:"deployment_task_id"`
	InstanceID       *uuid.UUID      `json:"instance_id"`
	EventType        string          `json:"event_type"`
	Message          *string         `json:"message"`
	Details          json.RawMessage `json:"details"`
}

// DeployHistoryItem — строка истории деплоев инстанса (openapi DeployHistoryItem).
type DeployHistoryItem struct {
	DeploymentID   uuid.UUID  `json:"deployment_id"`
	RulesetVersion string     `json:"ruleset_version"`
	InitiatedBy    *string    `json:"initiated_by"`
	Status         string     `json:"status"`
	StartedAt      *time.Time `json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at"`
	Result         *string    `json:"result"`
}
