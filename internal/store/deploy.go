package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DeploymentsRepo — волновые деплои ruleset
// (таблицы deployments, deployment_tasks, deploy_events).
type DeploymentsRepo struct {
	pool *pgxpool.Pool
}

const deploymentColumns = `id, organization_id, ruleset_version_id, deploy_template_id, targeting,
	batch_size, concurrency, canary_size, status, initiated_by,
	started_at, paused_at, finished_at, created_at, updated_at`

func scanDeployment(row pgx.Row) (Deployment, error) {
	var d Deployment
	err := row.Scan(&d.ID, &d.OrganizationID, &d.RulesetVersionID, &d.DeployTemplateID,
		&d.Targeting, &d.BatchSize, &d.Concurrency, &d.CanarySize, &d.Status,
		&d.InitiatedBy, &d.StartedAt, &d.PausedAt, &d.FinishedAt, &d.CreatedAt, &d.UpdatedAt)
	return d, err
}

const taskColumns = `id, deployment_id, instance_id, wave, status, attempts, max_attempts,
	result, error, started_at, finished_at, created_at, updated_at`

func scanTask(row pgx.Row) (DeploymentTask, error) {
	var t DeploymentTask
	err := row.Scan(&t.ID, &t.DeploymentID, &t.InstanceID, &t.Wave, &t.Status,
		&t.Attempts, &t.MaxAttempts, &t.Result, &t.Error,
		&t.StartedAt, &t.FinishedAt, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

// TaskWave — инстанс и номер его волны (для создания задач деплоя).
type TaskWave struct {
	InstanceID uuid.UUID
	Wave       int
}

// Create — транзакционно создаёт деплой, задачи по волнам и событие created.
func (r *DeploymentsRepo) Create(ctx context.Context, d Deployment, waves []TaskWave) (Deployment, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return d, translate(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	created, err := scanDeployment(tx.QueryRow(ctx,
		`INSERT INTO deployments (organization_id, ruleset_version_id, deploy_template_id,
			targeting, batch_size, concurrency, canary_size, initiated_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING `+deploymentColumns,
		d.OrganizationID, d.RulesetVersionID, d.DeployTemplateID,
		d.Targeting, d.BatchSize, d.Concurrency, d.CanarySize, d.InitiatedBy))
	if err != nil {
		return d, translate(err)
	}

	for _, w := range waves {
		if _, err := tx.Exec(ctx,
			`INSERT INTO deployment_tasks (deployment_id, instance_id, wave) VALUES ($1, $2, $3)`,
			created.ID, w.InstanceID, w.Wave); err != nil {
			return d, translate(err)
		}
	}

	if err := eventAddTx(ctx, tx, DeployEvent{
		DeploymentID: created.ID,
		EventType:    "created",
		Message:      strPtr(fmt.Sprintf("деплой создан: %d задач, %d волн", len(waves), maxWave(waves)+1)),
		Details:      json.RawMessage(`{}`),
	}); err != nil {
		return d, translate(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return d, translate(err)
	}
	return created, nil
}

func maxWave(waves []TaskWave) int {
	m := 0
	for _, w := range waves {
		if w.Wave > m {
			m = w.Wave
		}
	}
	return m
}

func strPtr(s string) *string { return &s }

// Get — деплой по id.
func (r *DeploymentsRepo) Get(ctx context.Context, id uuid.UUID) (Deployment, error) {
	d, err := scanDeployment(r.pool.QueryRow(ctx,
		`SELECT `+deploymentColumns+` FROM deployments WHERE id = $1`, id))
	if err != nil {
		return d, translate(err)
	}
	return d, nil
}

// GetProgress — сводка по задачам деплоя.
// Маппинг статусов задач на счётчики openapi: pending = pending;
// running = sent + running; failed = failed + cancelled + skipped.
// current_wave — минимальная волна с незавершёнными задачами (иначе последняя).
func (r *DeploymentsRepo) GetProgress(ctx context.Context, id uuid.UUID) (DeploymentProgress, error) {
	var p DeploymentProgress
	err := r.pool.QueryRow(ctx,
		`SELECT count(*),
			count(*) FILTER (WHERE status = 'pending'),
			count(*) FILTER (WHERE status IN ('sent', 'running')),
			count(*) FILTER (WHERE status = 'succeeded'),
			count(*) FILTER (WHERE status IN ('failed', 'cancelled', 'skipped')),
			COALESCE(
				(SELECT min(wave) FROM deployment_tasks
				 WHERE deployment_id = $1 AND status IN ('pending', 'sent', 'running')),
				(SELECT max(wave) FROM deployment_tasks WHERE deployment_id = $1),
				0)
		 FROM deployment_tasks WHERE deployment_id = $1`, id).
		Scan(&p.Total, &p.Pending, &p.Running, &p.Succeeded, &p.Failed, &p.CurrentWave)
	if err != nil {
		return p, translate(err)
	}
	return p, nil
}

// List — keyset-листинг деплоев организации (фильтр по статусу опционален).
func (r *DeploymentsRepo) List(ctx context.Context, orgID uuid.UUID, status string, cursor uuid.UUID, limit int) ([]Deployment, *string, error) {
	var statusArg *string
	if status != "" {
		statusArg = &status
	}
	var cursorArg *uuid.UUID
	if cursor != uuid.Nil {
		cursorArg = &cursor
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+deploymentColumns+` FROM deployments
		 WHERE organization_id = $1
		   AND ($2::text IS NULL OR status = $2)
		   AND ($3::uuid IS NULL OR id > $3)
		 ORDER BY id LIMIT $4`,
		orgID, statusArg, cursorArg, limit+1)
	if err != nil {
		return nil, nil, translate(err)
	}
	defer rows.Close()

	items := []Deployment{}
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, nil, translate(err)
		}
		items = append(items, d)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, translate(err)
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		c := EncodeCursor(items[len(items)-1].ID)
		next = &c
	}
	return items, next, nil
}

// ListActive — деплои в незавершённых статусах (для восстановления оркестратора
// после рестарта сервера).
func (r *DeploymentsRepo) ListActive(ctx context.Context) ([]Deployment, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+deploymentColumns+` FROM deployments
		 WHERE status IN ('pending', 'running', 'paused') ORDER BY id`)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	items := []Deployment{}
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, translate(err)
		}
		items = append(items, d)
	}
	return items, translate(rows.Err())
}

// SetStatus переводит деплой в новый статус с простановкой временных меток:
// running — started_at (первый раз), сброс paused_at; paused — paused_at;
// completed/failed/cancelled — finished_at.
func (r *DeploymentsRepo) SetStatus(ctx context.Context, id uuid.UUID, status string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE deployments SET status = $2, updated_at = now(),
			started_at  = CASE WHEN $2 = 'running' THEN COALESCE(started_at, now()) ELSE started_at END,
			paused_at   = CASE WHEN $2 = 'paused' THEN now() WHEN $2 = 'running' THEN NULL ELSE paused_at END,
			finished_at = CASE WHEN $2 IN ('completed', 'failed', 'cancelled') THEN now() ELSE finished_at END
		 WHERE id = $1`, id, status)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CancelPendingTasks отменяет все незапущенные задачи деплоя
// (при отмене/фейле деплоя).
func (r *DeploymentsRepo) CancelPendingTasks(ctx context.Context, deploymentID uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE deployment_tasks SET status = 'cancelled', finished_at = now(), updated_at = now()
		 WHERE deployment_id = $1 AND status IN ('pending', 'sent')`, deploymentID)
	return translate(err)
}

// ListTasks — задачи деплоя с фильтрами по статусу и волне (keyset по id).
func (r *DeploymentsRepo) ListTasks(ctx context.Context, deploymentID uuid.UUID, status string, wave *int, cursor uuid.UUID, limit int) ([]DeploymentTask, *string, error) {
	var statusArg *string
	if status != "" {
		statusArg = &status
	}
	var cursorArg *uuid.UUID
	if cursor != uuid.Nil {
		cursorArg = &cursor
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+taskColumns+` FROM deployment_tasks
		 WHERE deployment_id = $1
		   AND ($2::text IS NULL OR status = $2)
		   AND ($3::int IS NULL OR wave = $3)
		   AND ($4::uuid IS NULL OR id > $4)
		 ORDER BY id LIMIT $5`,
		deploymentID, statusArg, wave, cursorArg, limit+1)
	if err != nil {
		return nil, nil, translate(err)
	}
	defer rows.Close()

	items := []DeploymentTask{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, nil, translate(err)
		}
		items = append(items, t)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, translate(err)
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		c := EncodeCursor(items[len(items)-1].ID)
		next = &c
	}
	return items, next, nil
}

// GetTask — задача по id.
func (r *DeploymentsRepo) GetTask(ctx context.Context, id uuid.UUID) (DeploymentTask, error) {
	t, err := scanTask(r.pool.QueryRow(ctx,
		`SELECT `+taskColumns+` FROM deployment_tasks WHERE id = $1`, id))
	if err != nil {
		return t, translate(err)
	}
	return t, nil
}

// TasksOfWave — все задачи указанной волны деплоя.
func (r *DeploymentsRepo) TasksOfWave(ctx context.Context, deploymentID uuid.UUID, wave int) ([]DeploymentTask, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+taskColumns+` FROM deployment_tasks
		 WHERE deployment_id = $1 AND wave = $2 ORDER BY id`, deploymentID, wave)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	items := []DeploymentTask{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, translate(err)
		}
		items = append(items, t)
	}
	return items, translate(rows.Err())
}

// MarkTaskSent — задача отправлена агенту: pending → sent, attempts + 1.
func (r *DeploymentsRepo) MarkTaskSent(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE deployment_tasks SET status = 'sent', attempts = attempts + 1,
			started_at = COALESCE(started_at, now()), updated_at = now()
		 WHERE id = $1 AND status = 'pending'`, id)
	return translate(err)
}

// UpdateTaskResult — финальный результат задачи от агента
// (succeeded/failed + result jsonb + error).
func (r *DeploymentsRepo) UpdateTaskResult(ctx context.Context, id uuid.UUID, status string, result json.RawMessage, errMsg *string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE deployment_tasks SET status = $2, result = $3, error = $4,
			finished_at = now(), updated_at = now()
		 WHERE id = $1`, id, status, result, errMsg)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ResetSentTasks — восстановление после рестарта сервера: задачи в статусе
// sent (ответ мог потеряться) возвращаются в pending для повторной отправки.
func (r *DeploymentsRepo) ResetSentTasks(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		`UPDATE deployment_tasks SET status = 'pending', updated_at = now() WHERE status = 'sent'`)
	if err != nil {
		return 0, translate(err)
	}
	return tag.RowsAffected(), nil
}

// PendingTasksForAgent — неотправленные задачи всех инстансов хоста агента
// (подхват при подключении агента к хабу).
func (r *DeploymentsRepo) PendingTasksForAgent(ctx context.Context, agentID uuid.UUID) ([]DeploymentTask, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT t.`+taskColumns+` FROM deployment_tasks t
		 JOIN deployments d ON d.id = t.deployment_id AND d.status = 'running'
		 JOIN instances i ON i.id = t.instance_id
		 JOIN agents a ON a.host_id = i.host_id
		 WHERE a.id = $1 AND t.status = 'pending'
		 ORDER BY t.id`, agentID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	items := []DeploymentTask{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, translate(err)
		}
		items = append(items, t)
	}
	return items, translate(rows.Err())
}

// MaxWave — максимальный номер волны деплоя (-1, если задач нет).
func (r *DeploymentsRepo) MaxWave(ctx context.Context, deploymentID uuid.UUID) (int, error) {
	var w int
	err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(max(wave), -1) FROM deployment_tasks WHERE deployment_id = $1`,
		deploymentID).Scan(&w)
	if err != nil {
		return 0, translate(err)
	}
	return w, nil
}

// RetryFailedTasks возвращает failed-задачи деплоя в pending (при resume
// после auto-pause: волна с упавшими задачами прогоняется заново).
func (r *DeploymentsRepo) RetryFailedTasks(ctx context.Context, deploymentID uuid.UUID) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		`UPDATE deployment_tasks SET status = 'pending', error = NULL, result = NULL,
			finished_at = NULL, updated_at = now()
		 WHERE deployment_id = $1 AND status = 'failed'`, deploymentID)
	if err != nil {
		return 0, translate(err)
	}
	return tag.RowsAffected(), nil
}

// AgentIDForInstance — агент, обслуживающий хост инстанса.
func (r *DeploymentsRepo) AgentIDForInstance(ctx context.Context, instanceID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx,
		`SELECT a.id FROM agents a
		 JOIN instances i ON i.host_id = a.host_id
		 WHERE i.id = $1`, instanceID).Scan(&id)
	if err != nil {
		return id, translate(err)
	}
	return id, nil
}

// EventAdd — событие в историю деплоя.
func (r *DeploymentsRepo) EventAdd(ctx context.Context, e DeployEvent) error {
	return eventAddTx(ctx, r.pool, e)
}

// eventAddTx — вставка события через Tx или Pool (общий Queryer).
func eventAddTx(ctx context.Context, q interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}, e DeployEvent) error {
	if e.Details == nil {
		e.Details = json.RawMessage(`{}`)
	}
	_, err := q.Exec(ctx,
		`INSERT INTO deploy_events (deployment_id, deployment_task_id, instance_id, event_type, message, details)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		e.DeploymentID, e.DeploymentTaskID, e.InstanceID, e.EventType, e.Message, e.Details)
	return err
}

// EventsByDeployment — последние события деплоя (новые первыми).
func (r *DeploymentsRepo) EventsByDeployment(ctx context.Context, deploymentID uuid.UUID, limit int) ([]DeployEvent, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, created_at, deployment_id, deployment_task_id, instance_id, event_type, message, details
		 FROM deploy_events
		 WHERE deployment_id = $1
		 ORDER BY created_at DESC, id DESC LIMIT $2`, deploymentID, limit)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	items := []DeployEvent{}
	for rows.Next() {
		var e DeployEvent
		if err := rows.Scan(&e.ID, &e.CreatedAt, &e.DeploymentID, &e.DeploymentTaskID,
			&e.InstanceID, &e.EventType, &e.Message, &e.Details); err != nil {
			return nil, translate(err)
		}
		items = append(items, e)
	}
	return items, translate(rows.Err())
}

// DeployHistory — история деплоев инстанса (по задачам, новые первыми).
func (r *DeploymentsRepo) DeployHistory(ctx context.Context, instanceID uuid.UUID, limit int) ([]DeployHistoryItem, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT t.deployment_id, rv.version, u.display_name, t.status, t.started_at, t.finished_at, t.error
		 FROM deployment_tasks t
		 JOIN deployments d ON d.id = t.deployment_id
		 JOIN ruleset_versions rv ON rv.id = d.ruleset_version_id
		 LEFT JOIN users u ON u.id = d.initiated_by
		 WHERE t.instance_id = $1
		 ORDER BY t.created_at DESC, t.id DESC LIMIT $2`, instanceID, limit)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	items := []DeployHistoryItem{}
	for rows.Next() {
		var h DeployHistoryItem
		if err := rows.Scan(&h.DeploymentID, &h.RulesetVersion, &h.InitiatedBy,
			&h.Status, &h.StartedAt, &h.FinishedAt, &h.Result); err != nil {
			return nil, translate(err)
		}
		items = append(items, h)
	}
	return items, translate(rows.Err())
}
