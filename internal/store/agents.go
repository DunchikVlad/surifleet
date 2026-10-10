package store

import (
	"context"
	"encoding/json"
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

// AgentListItem — агент с hostname хоста (списки в UI/API, chunk 13c).
type AgentListItem struct {
	Agent
	Hostname string `json:"hostname"`
}

// List возвращает всех агентов (с hostname хоста), свежие первыми.
func (r *AgentsRepo) List(ctx context.Context) ([]AgentListItem, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT a.id, a.host_id, a.agent_version, a.protocol_version, a.status, a.last_seen_at,
		        a.cert_serial, a.cert_expires_at, a.enrolled_at, a.created_at, a.updated_at,
		        h.hostname
		 FROM agents a JOIN hosts h ON h.id = a.host_id
		 ORDER BY a.last_seen_at DESC NULLS LAST`)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	var out []AgentListItem
	for rows.Next() {
		var it AgentListItem
		if err := rows.Scan(&it.ID, &it.HostID, &it.AgentVersion, &it.ProtocolVersion, &it.Status,
			&it.LastSeenAt, &it.CertSerial, &it.CertExpiresAt, &it.EnrolledAt, &it.CreatedAt, &it.UpdatedAt,
			&it.Hostname); err != nil {
			return nil, translate(err)
		}
		out = append(out, it)
	}
	return out, translate(rows.Err())
}

// CountByStatus — число агентов по статусам (дашборд флота, чанк 86).
func (r *AgentsRepo) CountByStatus(ctx context.Context) (map[string]int, error) {
	rows, err := r.pool.Query(ctx, `SELECT status, count(*) FROM agents GROUP BY status`)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, translate(err)
		}
		out[st] = n
	}
	return out, translate(rows.Err())
}

// CountByStatusForCluster — число агентов по статусам в кластере
// (дашборд кластера, чанк 87).
func (r *AgentsRepo) CountByStatusForCluster(ctx context.Context, clusterID uuid.UUID) (map[string]int, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT a.status, count(*) FROM agents a
		 JOIN hosts h ON h.id = a.host_id
		 WHERE h.cluster_id = $1 GROUP BY a.status`, clusterID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, translate(err)
		}
		out[st] = n
	}
	return out, translate(rows.Err())
}

// HostAgentStatus — строка дашборда кластера (чанк 87): хост + статус
// его агента + число инстансов + compliance-статусы инстансов хоста.
type HostAgentStatus struct {
	HostID       uuid.UUID      `json:"host_id"`
	Hostname     string         `json:"hostname"`
	AgentID      *uuid.UUID     `json:"agent_id"`
	AgentStatus  *string        `json:"agent_status"`
	LastSeenAt   *time.Time     `json:"last_seen_at"`
	Instances    int            `json:"instances"`
	ByCompliance map[string]int `json:"by_compliance,omitempty"`
}

// HostsWithAgentStatus — хосты кластера с агентами и числом инстансов
// (дашборд кластера, чанк 87). Агент может отсутствовать (NULL) —
// онбординг не пройден.
func (r *AgentsRepo) HostsWithAgentStatus(ctx context.Context, clusterID uuid.UUID) ([]HostAgentStatus, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT h.id, h.hostname, a.id, a.status, a.last_seen_at,
		        (SELECT count(*) FROM instances i WHERE i.host_id = h.id) AS inst_n,
		        (SELECT COALESCE(jsonb_object_agg(st, n), '{}'::jsonb) FROM
		          (SELECT COALESCE(c.status, 'pending') AS st, count(*) AS n
		           FROM instances i
		           LEFT JOIN instance_compliance c ON c.instance_id = i.id
		           WHERE i.host_id = h.id GROUP BY 1) s) AS by_comp
		 FROM hosts h
		 LEFT JOIN agents a ON a.host_id = h.id
		 WHERE h.cluster_id = $1
		 ORDER BY h.hostname`, clusterID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []HostAgentStatus{}
	for rows.Next() {
		var h HostAgentStatus
		var byComp []byte
		if err := rows.Scan(&h.HostID, &h.Hostname, &h.AgentID, &h.AgentStatus, &h.LastSeenAt, &h.Instances, &byComp); err != nil {
			return nil, translate(err)
		}
		if len(byComp) > 0 {
			_ = json.Unmarshal(byComp, &h.ByCompliance)
		}
		out = append(out, h)
	}
	return out, translate(rows.Err())
}

// OfflineAgentBrief — краткая карточка offline-агента для дашборда
// (чанк 86): hostname + кластер + фактическое время последнего heartbeat.
type OfflineAgentBrief struct {
	AgentID    uuid.UUID  `json:"agent_id"`
	Hostname   string     `json:"hostname"`
	Cluster    string     `json:"cluster"`
	LastSeenAt *time.Time `json:"last_seen_at"`
}

// ListOffline — offline-агенты, давно не виденные первыми (лимит —
// разумный максимум для дашборда).
func (r *AgentsRepo) ListOffline(ctx context.Context, limit int) ([]OfflineAgentBrief, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT a.id, h.hostname, c.name, a.last_seen_at
		 FROM agents a
		 JOIN hosts h ON h.id = a.host_id
		 JOIN clusters c ON c.id = h.cluster_id
		 WHERE a.status <> 'online'
		 ORDER BY a.last_seen_at ASC NULLS FIRST
		 LIMIT $1`, limit)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []OfflineAgentBrief{}
	for rows.Next() {
		var b OfflineAgentBrief
		if err := rows.Scan(&b.AgentID, &b.Hostname, &b.Cluster, &b.LastSeenAt); err != nil {
			return nil, translate(err)
		}
		out = append(out, b)
	}
	return out, translate(rows.Err())
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
// запись в agent_state_history (heartbeat пишет last_seen_at троттлированно
// через TouchLastSeen, в историю попадают только смены состояния — §5.4). Атомарно, в одной транзакции.
// Возвращает true, если статус реально сменился (prev != status) — хаб по
// этому флаку публикует события переходов (KI-2, чанк 103).
// Нет записи → ErrNotFound.
func (r *AgentsRepo) SetStatus(ctx context.Context, id uuid.UUID, status string, details map[string]any) (bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, translate(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var prev string
	err = tx.QueryRow(ctx, `SELECT status FROM agents WHERE id = $1 FOR UPDATE`, id).Scan(&prev)
	if err != nil {
		return false, translate(err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE agents SET status = $2, last_seen_at = now(), updated_at = now() WHERE id = $1`,
		id, status); err != nil {
		return false, translate(err)
	}
	if prev != status {
		if _, err := tx.Exec(ctx,
			`INSERT INTO agent_state_history (agent_id, previous_status, status, details)
			 VALUES ($1, $2, $3, $4)`,
			id, prev, status, details); err != nil {
			return false, translate(err)
		}
	}
	return prev != status, translate(tx.Commit(ctx))
}

// HeartbeatPulse — пульс heartbeat (chunk 23): обновляет last_seen_at. Если
// агент был 'offline' (погашен свипером, но стрим на самом деле жив —
// размороженный процесс, заживший TCP), возвращает его в 'online' с записью
// в историю (reason heartbeat-resumed); статусы degraded/updating/error
// heartbeat не трогает. Возвращает true, если статус восстановлен из offline.
func (r *AgentsRepo) HeartbeatPulse(ctx context.Context, id uuid.UUID) (bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, translate(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var prev string
	err = tx.QueryRow(ctx, `SELECT status FROM agents WHERE id = $1 FOR UPDATE`, id).Scan(&prev)
	if err != nil {
		return false, translate(err)
	}
	revived := prev == "offline"
	newStatus := prev
	if revived {
		newStatus = "online"
	}
	if _, err := tx.Exec(ctx,
		`UPDATE agents SET status = $2, last_seen_at = now(), updated_at = now() WHERE id = $1`,
		id, newStatus); err != nil {
		return false, translate(err)
	}
	if revived {
		if _, err := tx.Exec(ctx,
			`INSERT INTO agent_state_history (agent_id, previous_status, status, details)
			 VALUES ($1, 'offline', 'online', $2)`,
			id, map[string]any{"reason": "heartbeat-resumed"}); err != nil {
			return false, translate(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, translate(err)
	}
	return revived, nil
}

// SweepStaleOnline переводит в offline агентов со статусом 'online', чей
// last_seen_at старше before (или NULL — никогда не виден). last_seen_at при
// этом НЕ перезаписывается (это фактическое время последнего heartbeat, а не
// момент детекта). Каждая смена пишется в agent_state_history с reason
// heartbeat-timeout. Возвращает id погашенных агентов.
func (r *AgentsRepo) SweepStaleOnline(ctx context.Context, before time.Time) ([]uuid.UUID, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, translate(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx,
		`SELECT id, last_seen_at FROM agents
		 WHERE status = 'online' AND (last_seen_at IS NULL OR last_seen_at < $1)
		 FOR UPDATE`, before)
	if err != nil {
		return nil, translate(err)
	}
	type staleAgent struct {
		id         uuid.UUID
		lastSeenAt *time.Time
	}
	var stale []staleAgent
	for rows.Next() {
		var sa staleAgent
		if err := rows.Scan(&sa.id, &sa.lastSeenAt); err != nil {
			rows.Close()
			return nil, translate(err)
		}
		stale = append(stale, sa)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, translate(err)
	}

	ids := make([]uuid.UUID, 0, len(stale))
	for _, sa := range stale {
		if _, err := tx.Exec(ctx,
			`UPDATE agents SET status = 'offline', updated_at = now() WHERE id = $1`, sa.id); err != nil {
			return nil, translate(err)
		}
		var lastSeen string
		if sa.lastSeenAt != nil {
			lastSeen = sa.lastSeenAt.UTC().Format(time.RFC3339)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO agent_state_history (agent_id, previous_status, status, details)
			 VALUES ($1, 'online', 'offline', $2)`,
			sa.id, map[string]any{"reason": "heartbeat-timeout", "last_seen_at": lastSeen}); err != nil {
			return nil, translate(err)
		}
		ids = append(ids, sa.id)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, translate(err)
	}
	return ids, nil
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
