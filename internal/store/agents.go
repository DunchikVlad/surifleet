package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Agent — строка таблицы agents (агент SuriFleet, 1:1 к хосту).
type Agent struct {
	ID              uuid.UUID  `json:"id"`
	HostID          uuid.UUID  `json:"host_id"`
	AgentVersion    *string    `json:"agent_version"`
	ProtocolVersion *string    `json:"protocol_version"`
	Status          string     `json:"status"`
	LastSeenAt      *time.Time `json:"last_seen_at"`
	CertSerial      *string    `json:"cert_serial"`
	CertExpiresAt   *time.Time `json:"cert_expires_at"`
	EnrolledAt      *time.Time `json:"enrolled_at"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

const agentColumns = `id, host_id, agent_version, protocol_version, status, last_seen_at,
	cert_serial, cert_expires_at, enrolled_at, created_at, updated_at`

// AgentsRepo — операции с таблицей agents (enrollment, presence).
type AgentsRepo struct {
	pool *pgxpool.Pool
}

func scanAgent(row pgx.Row) (Agent, error) {
	var a Agent
	err := row.Scan(&a.ID, &a.HostID, &a.AgentVersion, &a.ProtocolVersion, &a.Status,
		&a.LastSeenAt, &a.CertSerial, &a.CertExpiresAt, &a.EnrolledAt, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

// GetByID возвращает агента по id. Нет записи → ErrNotFound.
func (r *AgentsRepo) GetByID(ctx context.Context, id uuid.UUID) (Agent, error) {
	a, err := scanAgent(r.pool.QueryRow(ctx,
		`SELECT `+agentColumns+` FROM agents WHERE id = $1`, id))
	if err != nil {
		return a, translate(err)
	}
	return a, nil
}

// GetByHostID возвращает агента хоста (1:1). Нет записи → ErrNotFound.
func (r *AgentsRepo) GetByHostID(ctx context.Context, tx pgx.Tx, hostID uuid.UUID) (Agent, error) {
	var a Agent
	var err error
	if tx != nil {
		a, err = scanAgent(tx.QueryRow(ctx, `SELECT `+agentColumns+` FROM agents WHERE host_id = $1`, hostID))
	} else {
		a, err = scanAgent(r.pool.QueryRow(ctx, `SELECT `+agentColumns+` FROM agents WHERE host_id = $1`, hostID))
	}
	if err != nil {
		return a, translate(err)
	}
	return a, nil
}

// CreateEnrolled создаёт агента при enrollment (в транзакции tx):
// id задаётся явно — он должен совпадать с CN выданного сертификата;
// status 'offline', cert_serial/срок из выданного сертификата.
// Дубль по host_id → ErrConflict.
func (r *AgentsRepo) CreateEnrolled(ctx context.Context, tx pgx.Tx, id, hostID uuid.UUID, agentVersion, serial string, certExpiresAt time.Time) (Agent, error) {
	a, err := scanAgent(tx.QueryRow(ctx,
		`INSERT INTO agents (id, host_id, agent_version, protocol_version, status, cert_serial, cert_expires_at, enrolled_at)
		 VALUES ($1, $2, $3, '1', 'offline', $4, $5, now())
		 RETURNING `+agentColumns,
		id, hostID, agentVersion, serial, certExpiresAt))
	if err != nil {
		return a, translate(err)
	}
	return a, nil
}

// SetStatus обновляет статус/last_seen_at агента и при смене статуса пишет
// запись в agent_state_history (heartbeat в PG не пишется — только смены
// состояния, docs/architecture.md §5.4). Атомарно, в одной транзакции.
// Нет записи → ErrNotFound.
func (r *AgentsRepo) SetStatus(ctx context.Context, id uuid.UUID, status string, details map[string]any) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return translate(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var prev string
	err = tx.QueryRow(ctx, `SELECT status FROM agents WHERE id = $1 FOR UPDATE`, id).Scan(&prev)
	if err != nil {
		return translate(err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE agents SET status = $2, last_seen_at = now(), updated_at = now() WHERE id = $1`,
		id, status); err != nil {
		return translate(err)
	}
	if prev != status {
		if _, err := tx.Exec(ctx,
			`INSERT INTO agent_state_history (agent_id, previous_status, status, details)
			 VALUES ($1, $2, $3, $4)`,
			id, prev, status, details); err != nil {
			return translate(err)
		}
	}
	return translate(tx.Commit(ctx))
}

// TouchLastSeen обновляет last_seen_at без смены статуса и без истории
// (вызывается Hub'ом не чаще раза в минуту — редкий «пульс» для UI).
func (r *AgentsRepo) TouchLastSeen(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE agents SET last_seen_at = now() WHERE id = $1`, id)
	return translate(err)
}

// FindHostByHostname — переиспользование карточки хоста при enrollment:
// хост ищется по (cluster_id, hostname). Нет записи → ErrNotFound.
func (r *HostsRepo) FindHostByHostname(ctx context.Context, tx pgx.Tx, clusterID uuid.UUID, hostname string) (Host, error) {
	var h Host
	var err error
	const q = `SELECT ` + hostColumns + ` FROM hosts WHERE cluster_id = $1 AND hostname = $2`
	if tx != nil {
		err = h.scanFrom(tx.QueryRow(ctx, q, clusterID, hostname))
	} else {
		err = h.scanFrom(r.pool.QueryRow(ctx, q, clusterID, hostname))
	}
	if err != nil {
		return h, translate(err)
	}
	return h, nil
}

// scanFrom — общий сканер строки hosts.
func (h *Host) scanFrom(row pgx.Row) error {
	return row.Scan(&h.ID, &h.ClusterID, &h.Hostname, &h.IPAddresses, &h.OS, &h.Labels, &h.CreatedAt, &h.UpdatedAt)
}

// CreateHostTx — создание хоста в транзакции (enrollment).
func (r *HostsRepo) CreateHostTx(ctx context.Context, tx pgx.Tx, in HostInput) (Host, error) {
	if in.IPAddresses == nil {
		in.IPAddresses = []string{}
	}
	if in.Labels == nil {
		in.Labels = map[string]string{}
	}
	var h Host
	err := h.scanFrom(tx.QueryRow(ctx,
		`INSERT INTO hosts (cluster_id, hostname, ip_addresses, os, labels)
		 VALUES ($1, $2, $3::inet[], $4, $5)
		 RETURNING `+hostColumns,
		in.ClusterID, in.Hostname, in.IPAddresses, in.OS, in.Labels))
	if err != nil {
		return h, translate(err)
	}
	return h, nil
}
