package store

import (
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
