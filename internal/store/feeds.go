package store

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// FeedsRepo — репозиторий фидов IOC/правил (таблицы feeds и feed_runs,
// миграции 000001 + 000005).
type FeedsRepo struct {
	pool *pgxpool.Pool
}

const feedColumns = `id, organization_id, name, type, url, schedule, credentials_ref,
	enabled, last_sync_at, last_sync_status, last_error, created_at, updated_at`

func scanFeed(row pgx.Row) (Feed, error) {
	var f Feed
	err := row.Scan(&f.ID, &f.OrganizationID, &f.Name, &f.Type, &f.URL, &f.Schedule,
		&f.CredentialsRef, &f.Enabled, &f.LastSyncAt, &f.LastSyncStatus, &f.LastError,
		&f.CreatedAt, &f.UpdatedAt)
	return f, err
}

// Create — подключение фида (POST /feeds). Дубль (org, name) → ErrConflict.
func (r *FeedsRepo) Create(ctx context.Context, orgID uuid.UUID, in FeedInput) (Feed, error) {
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	feed, err := scanFeed(r.pool.QueryRow(ctx,
		`INSERT INTO feeds (organization_id, name, type, url, schedule, credentials_ref, enabled)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING `+feedColumns,
		orgID, in.Name, in.Type, in.URL, in.Schedule, in.Credentials, enabled))
	if err != nil {
		return Feed{}, translate(err)
	}
	return feed, nil
}

// Get возвращает фид по id. Нет записи → ErrNotFound.
func (r *FeedsRepo) Get(ctx context.Context, id uuid.UUID) (Feed, error) {
	feed, err := scanFeed(r.pool.QueryRow(ctx,
		`SELECT `+feedColumns+` FROM feeds WHERE id = $1`, id))
	if err != nil {
		return Feed{}, translate(err)
	}
	return feed, nil
}

// Update — частичное обновление (name/url/schedule/credentials/enabled);
// nil-поле — «не менять». Нет записи → ErrNotFound.
func (r *FeedsRepo) Update(ctx context.Context, id uuid.UUID, p FeedPatch) (Feed, error) {
	feed, err := scanFeed(r.pool.QueryRow(ctx,
		`UPDATE feeds
		 SET name = COALESCE($2, name),
		     url = COALESCE($3, url),
		     schedule = COALESCE($4, schedule),
		     credentials_ref = COALESCE($5, credentials_ref),
		     enabled = COALESCE($6, enabled),
		     updated_at = now()
		 WHERE id = $1
		 RETURNING `+feedColumns,
		id, p.Name, p.URL, p.Schedule, p.Credentials, p.Enabled))
	if err != nil {
		return Feed{}, translate(err)
	}
	return feed, nil
}

// Delete — жёсткое удаление фида. Импортированные IOC/правила остаются
// (iocs.feed_id / rules.feed_id → NULL по FK ON DELETE SET NULL),
// история feed_runs удаляется каскадом. Нет записи → ErrNotFound.
func (r *FeedsRepo) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM feeds WHERE id = $1`, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// List — keyset-листинг фидов организации с фильтром по типу.
func (r *FeedsRepo) List(ctx context.Context, orgID uuid.UUID, feedType string, cursor uuid.UUID, limit int) ([]Feed, *string, error) {
	args := []any{orgID}
	where := "organization_id = $1"
	if feedType != "" {
		args = append(args, feedType)
		where += fmt.Sprintf(" AND type = $%d", len(args))
	}

	var cursorArg *uuid.UUID
	if cursor != uuid.Nil {
		cursorArg = &cursor
	}
	args = append(args, cursorArg, limit+1)
	rows, err := r.pool.Query(ctx,
		fmt.Sprintf(`SELECT %s FROM feeds
		 WHERE %s AND ($%d::uuid IS NULL OR id > $%d)
		 ORDER BY id LIMIT $%d`,
			feedColumns, where, len(args)-1, len(args)-1, len(args)),
		args...)
	if err != nil {
		return nil, nil, translate(err)
	}
	defer rows.Close()

	items := []Feed{}
	for rows.Next() {
		feed, err := scanFeed(rows)
		if err != nil {
			return nil, nil, translate(err)
		}
		items = append(items, feed)
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

// ListScheduled — все включённые фиды с заданным schedule (все организации;
// для фонового планировщика авто-синка, который сам фильтрует «пора ли»).
func (r *FeedsRepo) ListScheduled(ctx context.Context) ([]Feed, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+feedColumns+` FROM feeds
		 WHERE enabled AND schedule IS NOT NULL AND schedule <> ''
		 ORDER BY id`)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	items := []Feed{}
	for rows.Next() {
		feed, err := scanFeed(rows)
		if err != nil {
			return nil, translate(err)
		}
		items = append(items, feed)
	}
	return items, translate(rows.Err())
}

// MarkSync фиксирует результат синхронизации на карточке фида:
// last_sync_at = now(), last_sync_status, last_error (nil — очистить).
func (r *FeedsRepo) MarkSync(ctx context.Context, id uuid.UUID, status string, syncErr *string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE feeds
		 SET last_sync_at = now(), last_sync_status = $2, last_error = $3, updated_at = now()
		 WHERE id = $1`,
		id, status, syncErr)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- feed_runs: история запусков синхронизации ---

const feedRunColumns = `id, feed_id, status, started_at, finished_at, imported, updated, skipped, error`

func scanFeedRun(row pgx.Row) (FeedRun, error) {
	var fr FeedRun
	err := row.Scan(&fr.ID, &fr.FeedID, &fr.Status, &fr.StartedAt, &fr.FinishedAt,
		&fr.Imported, &fr.Updated, &fr.Skipped, &fr.Error)
	return fr, err
}

// CreateRun открывает запуск синхронизации (status=running).
func (r *FeedsRepo) CreateRun(ctx context.Context, feedID uuid.UUID) (FeedRun, error) {
	run, err := scanFeedRun(r.pool.QueryRow(ctx,
		`INSERT INTO feed_runs (feed_id, status) VALUES ($1, 'running')
		 RETURNING `+feedRunColumns, feedID))
	if err != nil {
		return FeedRun{}, translate(err)
	}
	return run, nil
}

// FinishRun закрывает запуск: статус success|failed, счётчики, текст ошибки.
func (r *FeedsRepo) FinishRun(ctx context.Context, runID uuid.UUID, status string, imported, updated, skipped int, runErr *string) (FeedRun, error) {
	run, err := scanFeedRun(r.pool.QueryRow(ctx,
		`UPDATE feed_runs
		 SET status = $2, finished_at = now(), imported = $3, updated = $4, skipped = $5, error = $6
		 WHERE id = $1
		 RETURNING `+feedRunColumns,
		runID, status, imported, updated, skipped, runErr))
	if err != nil {
		return FeedRun{}, translate(err)
	}
	return run, nil
}

// feedRunCursor — композитный keyset-курсор истории запусков
// (started_at DESC, id DESC): base64url "<unixnano>|<uuid>".
func encodeRunCursor(r FeedRun) string {
	return base64.RawURLEncoding.EncodeToString(
		[]byte(strconv.FormatInt(r.StartedAt.UnixNano(), 10) + "|" + r.ID.String()))
}

func decodeRunCursor(s string) (time.Time, uuid.UUID, error) {
	if s == "" {
		return time.Time{}, uuid.Nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("некорректный курсор: %w", err)
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, uuid.Nil, fmt.Errorf("некорректный курсор")
	}
	ns, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("некорректный курсор: %w", err)
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("некорректный курсор: %w", err)
	}
	return time.Unix(0, ns).UTC(), id, nil
}

// ListRuns — история запусков фида, свежие первыми (keyset по
// (started_at, id) DESC). Пустой курсор — первая страница.
func (r *FeedsRepo) ListRuns(ctx context.Context, feedID uuid.UUID, cursor string, limit int) ([]FeedRun, *string, error) {
	ts, id, err := decodeRunCursor(cursor)
	if err != nil {
		return nil, nil, err
	}

	var tsArg *time.Time
	var idArg *uuid.UUID
	if cursor != "" {
		tsArg, idArg = &ts, &id
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+feedRunColumns+` FROM feed_runs
		 WHERE feed_id = $1
		   AND ($2::timestamptz IS NULL OR (started_at, id) < ($2, $3::uuid))
		 ORDER BY started_at DESC, id DESC
		 LIMIT $4`,
		feedID, tsArg, idArg, limit+1)
	if err != nil {
		return nil, nil, translate(err)
	}
	defer rows.Close()

	items := []FeedRun{}
	for rows.Next() {
		run, err := scanFeedRun(rows)
		if err != nil {
			return nil, nil, translate(err)
		}
		items = append(items, run)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, translate(err)
	}

	var next *string
	if len(items) > limit {
		items = items[:limit]
		c := encodeRunCursor(items[len(items)-1])
		next = &c
	}
	return items, next, nil
}
