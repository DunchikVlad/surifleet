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
