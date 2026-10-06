package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Пользователи, роли, сессии, аудит (чанк 28 — auth/RBAC)
// ---------------------------------------------------------------------------

// User — пользователь (таблица users; openapi User).
type User struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID uuid.UUID  `json:"organization_id"`
	Email          string     `json:"email"`
	DisplayName    string     `json:"display_name"`
	PasswordHash   *string    `json:"-"` // никогда наружу
	ProviderID     *uuid.UUID `json:"provider_id"`
	IsBreakGlass   bool       `json:"is_break_glass"`
	IsActive       bool       `json:"is_active"`
	LastLoginAt    *time.Time `json:"last_login_at"`
	CreatedAt      time.Time  `json:"created_at"`
	// Roles — назначения ролей (заполняется отдельным запросом).
	Roles []UserRoleAssignment `json:"roles,omitempty"`
}

// UserRoleAssignment — назначение роли пользователю (user_roles + имя роли).
type UserRoleAssignment struct {
	RoleID     uuid.UUID   `json:"role_id"`
	RoleName   string      `json:"role_name"`
	ScopeType  string      `json:"scope_type"`
	ClusterIDs []uuid.UUID `json:"cluster_ids,omitempty"`
}

// Role — роль (встроенная: organization_id IS NULL; или кастомная).
type Role struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID *uuid.UUID `json:"organization_id"`
	Name           string     `json:"name"`
	Description    *string    `json:"description"`
	Permissions    []string   `json:"permissions"`
	IsBuiltin      bool       `json:"is_builtin"`
	CreatedAt      time.Time  `json:"created_at"`
}

// Session — сессия (refresh-токен; MVP: токен сессии = access, хранится хэш).
type Session struct {
	ID        uuid.UUID  `json:"id"`
	UserID    uuid.UUID  `json:"user_id"`
	UserAgent *string    `json:"user_agent,omitempty"`
	IP        *string    `json:"ip,omitempty"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// RoleAssignmentInput — назначение роли при создании/обновлении пользователя.
type RoleAssignmentInput struct {
	RoleID     uuid.UUID   `json:"role_id"`
	ScopeType  string      `json:"scope_type"`
	ClusterIDs []uuid.UUID `json:"cluster_ids"`
}

// UserCreateInput — создание локального пользователя.
type UserCreateInput struct {
	Email        string
	DisplayName  string
	PasswordHash string
	IsBreakGlass bool
	Roles        []RoleAssignmentInput
}

// UserPatch — частичное обновление пользователя; nil — «не менять».
// Roles != nil — полная замена назначений.
type UserPatch struct {
	DisplayName  *string
	IsActive     *bool
	PasswordHash *string
	Roles        *[]RoleAssignmentInput
}

// UsersRepo — операции с users/user_roles.
type UsersRepo struct {
	pool *pgxpool.Pool
}

const userColumns = `id, organization_id, email, display_name, password_hash, provider_id,
	is_break_glass, is_active, last_login_at, created_at`

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.OrganizationID, &u.Email, &u.DisplayName, &u.PasswordHash,
		&u.ProviderID, &u.IsBreakGlass, &u.IsActive, &u.LastLoginAt, &u.CreatedAt)
	return u, err
}

// userRoles подгружает назначения ролей пользователя (с именем роли).
func (r *UsersRepo) userRoles(ctx context.Context, userID uuid.UUID) ([]UserRoleAssignment, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT ur.role_id, ro.name, ur.scope_type, ur.cluster_ids
		 FROM user_roles ur JOIN roles ro ON ro.id = ur.role_id
		 WHERE ur.user_id = $1 ORDER BY ro.name`, userID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []UserRoleAssignment{}
	for rows.Next() {
		var a UserRoleAssignment
		if err := rows.Scan(&a.RoleID, &a.RoleName, &a.ScopeType, &a.ClusterIDs); err != nil {
			return nil, translate(err)
		}
		out = append(out, a)
	}
	return out, translate(rows.Err())
}

// insertRolesTx — вставка назначений ролей в транзакции.
func insertRolesTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, roles []RoleAssignmentInput) error {
	for _, ra := range roles {
		scope := ra.ScopeType
		if scope == "" {
			scope = "organization"
		}
		clusters := ra.ClusterIDs
		if clusters == nil {
			clusters = []uuid.UUID{}
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO user_roles (user_id, role_id, scope_type, cluster_ids)
			 VALUES ($1, $2, $3, $4)`, userID, ra.RoleID, scope, clusters); err != nil {
			return translate(err)
		}
	}
	return nil
}

// Create создаёт локального пользователя с назначениями ролей (в tx).
// Дубль (org, email) → ErrConflict.
func (r *UsersRepo) Create(ctx context.Context, orgID uuid.UUID, in UserCreateInput) (User, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return User{}, translate(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	u, err := scanUser(tx.QueryRow(ctx,
		`INSERT INTO users (organization_id, email, display_name, password_hash, is_break_glass)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING `+userColumns,
		orgID, in.Email, in.DisplayName, in.PasswordHash, in.IsBreakGlass))
	if err != nil {
		return User{}, translate(err)
	}
	if err := insertRolesTx(ctx, tx, u.ID, in.Roles); err != nil {
		return User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, translate(err)
	}
	u.Roles, err = r.userRoles(ctx, u.ID)
	return u, err
}

// GetByID возвращает пользователя с ролями. Нет записи → ErrNotFound.
func (r *UsersRepo) GetByID(ctx context.Context, id uuid.UUID) (User, error) {
	u, err := scanUser(r.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = $1`, id))
	if err != nil {
		return u, translate(err)
	}
	u.Roles, err = r.userRoles(ctx, u.ID)
	return u, err
}

// GetByEmail — пользователь по email организации (для login; включает
// password_hash). Нет записи → ErrNotFound.
func (r *UsersRepo) GetByEmail(ctx context.Context, orgID uuid.UUID, email string) (User, error) {
	u, err := scanUser(r.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE organization_id = $1 AND lower(email) = lower($2)`,
		orgID, email))
	if err != nil {
		return u, translate(err)
	}
	return u, nil
}

// List — keyset-листинг пользователей организации с поиском по email/имени
// и фильтром активности.
func (r *UsersRepo) List(ctx context.Context, orgID uuid.UUID, q string, isActive *bool, cursor uuid.UUID, limit int) ([]User, *string, error) {
	args := []any{orgID}
	where := "organization_id = $1"
	if q != "" {
		args = append(args, "%"+q+"%")
		where += fmt.Sprintf(" AND (email ILIKE $%d OR display_name ILIKE $%d)", len(args), len(args))
	}
	if isActive != nil {
		args = append(args, *isActive)
		where += fmt.Sprintf(" AND is_active = $%d", len(args))
	}
	var cursorArg *uuid.UUID
	if cursor != uuid.Nil {
		cursorArg = &cursor
	}
	args = append(args, cursorArg, limit+1)
	rows, err := r.pool.Query(ctx,
		fmt.Sprintf(`SELECT %s FROM users
		 WHERE %s AND ($%d::uuid IS NULL OR id > $%d)
		 ORDER BY id LIMIT $%d`, userColumns, where, len(args)-1, len(args)-1, len(args)),
		args...)
	if err != nil {
		return nil, nil, translate(err)
	}
	defer rows.Close()
	items := []User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.OrganizationID, &u.Email, &u.DisplayName, &u.PasswordHash,
			&u.ProviderID, &u.IsBreakGlass, &u.IsActive, &u.LastLoginAt, &u.CreatedAt); err != nil {
			return nil, nil, translate(err)
		}
		u.PasswordHash = nil
		items = append(items, u)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, translate(err)
	}
	var next *string
	if len(items) > limit {
		s := items[limit-1].ID.String()
		next = &s
		items = items[:limit]
	}
	return items, next, nil
}

// Update — частичное обновление пользователя; Roles != nil — полная замена
// назначений (в tx). Нет записи → ErrNotFound.
func (r *UsersRepo) Update(ctx context.Context, id uuid.UUID, p UserPatch) (User, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return User{}, translate(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	u, err := scanUser(tx.QueryRow(ctx,
		`UPDATE users
		 SET display_name = COALESCE($2, display_name),
		     is_active = COALESCE($3, is_active),
		     password_hash = COALESCE($4, password_hash),
		     updated_at = now()
		 WHERE id = $1
		 RETURNING `+userColumns,
		id, p.DisplayName, p.IsActive, p.PasswordHash))
	if err != nil {
		return User{}, translate(err)
	}
	if p.Roles != nil {
		if _, err := tx.Exec(ctx, `DELETE FROM user_roles WHERE user_id = $1`, id); err != nil {
			return User{}, translate(err)
		}
		if err := insertRolesTx(ctx, tx, id, *p.Roles); err != nil {
			return User{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, translate(err)
	}
	u.PasswordHash = nil
	u.Roles, err = r.userRoles(ctx, u.ID)
	return u, err
}

// Delete удаляет пользователя. Последний break-glass пользователь
// организации неудаляем (ErrConflict).
func (r *UsersRepo) Delete(ctx context.Context, id uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return translate(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orgID uuid.UUID
	var breakGlass bool
	err = tx.QueryRow(ctx,
		`SELECT organization_id, is_break_glass FROM users WHERE id = $1 FOR UPDATE`, id).
		Scan(&orgID, &breakGlass)
	if err != nil {
		return translate(err)
	}
	if breakGlass {
		var n int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM users WHERE organization_id = $1 AND is_break_glass AND is_active`,
			orgID).Scan(&n); err != nil {
			return translate(err)
		}
		if n <= 1 {
			return ErrConflict // последний break-glass администратор неудаляем
		}
	}
	tag, err := tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return translate(tx.Commit(ctx))
}

// TouchLogin фиксирует момент входа.
func (r *UsersRepo) TouchLogin(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE users SET last_login_at = now(), updated_at = now() WHERE id = $1`, id)
	return translate(err)
}

// CountActiveBreakGlass — число активных break-glass пользователей (bootstrap).
func (r *UsersRepo) CountActiveBreakGlass(ctx context.Context, orgID uuid.UUID) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE organization_id = $1 AND is_break_glass AND is_active`,
		orgID).Scan(&n)
	return n, translate(err)
}

// ClusterScope — scoping пользователя по кластерам (чанк 43, п. 8 ТЗ:
// «аналитик видит/меняет только свои кластеры»). Семантика MVP:
//   - есть хотя бы одно назначение scope_type='organization' → restricted=false
//     (доступна вся организация, clusterIDs пуст);
//   - иначе restricted=true и clusterIDs — объединение cluster_ids всех
//     назначений scope_type='clusters' (может быть пустым — ничего не видит).
func (r *UsersRepo) ClusterScope(ctx context.Context, userID uuid.UUID) (clusterIDs []uuid.UUID, restricted bool, err error) {
	rows, err := r.pool.Query(ctx,
		`SELECT scope_type, cluster_ids FROM user_roles WHERE user_id = $1`, userID)
	if err != nil {
		return nil, false, translate(err)
	}
	defer rows.Close()
	seen := map[uuid.UUID]bool{}
	clusterIDs = []uuid.UUID{}
	for rows.Next() {
		var scope string
		var ids []uuid.UUID
		if err := rows.Scan(&scope, &ids); err != nil {
			return nil, false, translate(err)
		}
		if scope == "organization" {
			return nil, false, translate(rows.Err()) // org-scoped → без ограничений
		}
		restricted = true
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				clusterIDs = append(clusterIDs, id)
			}
		}
	}
	return clusterIDs, restricted, translate(rows.Err())
}

// ---------------------------------------------------------------------------
// JIT-провижининг SSO-пользователей (чанк 35)
// ---------------------------------------------------------------------------

// GetByExternalID — пользователь по (provider_id, external_id) (уже JIT-создан
// через этот IdP). Нет записи → ErrNotFound.
func (r *UsersRepo) GetByExternalID(ctx context.Context, providerID uuid.UUID, externalID string) (User, error) {
	u, err := scanUser(r.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE provider_id = $1 AND external_id = $2`,
		providerID, externalID))
	if err != nil {
		return u, translate(err)
	}
	u.Roles, err = r.userRoles(ctx, u.ID)
	return u, err
}

// LinkExternal привязывает существующего (локального) пользователя к IdP
// (provider_id + external_id) — дедупликация JIT по email. Возвращает
// обновлённого пользователя с ролями.
func (r *UsersRepo) LinkExternal(ctx context.Context, id, providerID uuid.UUID, externalID string) (User, error) {
	u, err := scanUser(r.pool.QueryRow(ctx,
		`UPDATE users SET provider_id=$2, external_id=$3, updated_at=now() WHERE id=$1
		 RETURNING `+userColumns, id, providerID, externalID))
	if err != nil {
		return u, translate(err)
	}
	u.PasswordHash = nil
	u.Roles, err = r.userRoles(ctx, u.ID)
	return u, err
}

// CreateExternal создаёт JIT-пользователя из SSO (без пароля) с ролями (в tx).
// Дубль (org,email) или (provider,external) → ErrConflict.
func (r *UsersRepo) CreateExternal(ctx context.Context, orgID, providerID uuid.UUID, externalID, email, displayName string, roles []RoleAssignmentInput) (User, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return User{}, translate(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	u, err := scanUser(tx.QueryRow(ctx,
		`INSERT INTO users (organization_id, external_id, provider_id, email, display_name, password_hash, is_break_glass)
		 VALUES ($1,$2,$3,$4,$5,NULL,false)
		 RETURNING `+userColumns,
		orgID, externalID, providerID, email, displayName))
	if err != nil {
		return User{}, translate(err)
	}
	if err := insertRolesTx(ctx, tx, u.ID, roles); err != nil {
		return User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, translate(err)
	}
	u.Roles, err = r.userRoles(ctx, u.ID)
	return u, err
}

// SetRoles — полная замена назначений ролей пользователя (в tx). Пустой
// срез — снять все роли (JIT: пользователь больше ни в одной группе IdP).
func (r *UsersRepo) SetRoles(ctx context.Context, userID uuid.UUID, roles []RoleAssignmentInput) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return translate(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM user_roles WHERE user_id = $1`, userID); err != nil {
		return translate(err)
	}
	if err := insertRolesTx(ctx, tx, userID, roles); err != nil {
		return err
	}
	return translate(tx.Commit(ctx))
}

// ---------------------------------------------------------------------------

// RolesRepo — роли (встроенные глобальные + кастомные организации).
type RolesRepo struct {
	pool *pgxpool.Pool
}

const roleColumns = `id, organization_id, name, description, permissions, is_builtin, created_at`

func scanRole(row pgx.Row) (Role, error) {
	var ro Role
	err := row.Scan(&ro.ID, &ro.OrganizationID, &ro.Name, &ro.Description, &ro.Permissions, &ro.IsBuiltin, &ro.CreatedAt)
	return ro, err
}

// List — встроенные роли + кастомные организации, встроенные первыми.
func (r *RolesRepo) List(ctx context.Context, orgID uuid.UUID) ([]Role, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+roleColumns+` FROM roles
		 WHERE organization_id IS NULL OR organization_id = $1
		 ORDER BY is_builtin DESC, name`, orgID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []Role{}
	for rows.Next() {
		var ro Role
		if err := rows.Scan(&ro.ID, &ro.OrganizationID, &ro.Name, &ro.Description, &ro.Permissions, &ro.IsBuiltin, &ro.CreatedAt); err != nil {
			return nil, translate(err)
		}
		out = append(out, ro)
	}
	return out, translate(rows.Err())
}

// GetByID возвращает роль по id. Нет записи → ErrNotFound.
func (r *RolesRepo) GetByID(ctx context.Context, id uuid.UUID) (Role, error) {
	ro, err := scanRole(r.pool.QueryRow(ctx,
		`SELECT `+roleColumns+` FROM roles WHERE id = $1`, id))
	if err != nil {
		return ro, translate(err)
	}
	return ro, nil
}

// BuiltinByName — встроенная роль по имени (bootstrap). Нет → ErrNotFound.
func (r *RolesRepo) BuiltinByName(ctx context.Context, name string) (Role, error) {
	ro, err := scanRole(r.pool.QueryRow(ctx,
		`SELECT `+roleColumns+` FROM roles WHERE organization_id IS NULL AND name = $1`, name))
	if err != nil {
		return ro, translate(err)
	}
	return ro, nil
}

// Create — кастомная роль организации. Дубль имени → ErrConflict.
func (r *RolesRepo) Create(ctx context.Context, orgID uuid.UUID, name string, description *string, permissions []string) (Role, error) {
	ro, err := scanRole(r.pool.QueryRow(ctx,
		`INSERT INTO roles (organization_id, name, description, permissions, is_builtin)
		 VALUES ($1, $2, $3, $4, false)
		 RETURNING `+roleColumns,
		orgID, name, description, permissions))
	if err != nil {
		return ro, translate(err)
	}
	return ro, nil
}

// Update — частичное обновление кастомной роли (встроенные неизменяемы —
// ErrConflict). Нет записи → ErrNotFound.
func (r *RolesRepo) Update(ctx context.Context, id uuid.UUID, name, description *string, permissions []string) (Role, error) {
	ro, err := scanRole(r.pool.QueryRow(ctx,
		`UPDATE roles
		 SET name = COALESCE($2, name),
		     description = COALESCE($3, description),
		     permissions = COALESCE($4, permissions),
		     updated_at = now()
		 WHERE id = $1 AND is_builtin = false
		 RETURNING `+roleColumns,
		id, name, description, permissions))
	if err == pgx.ErrNoRows {
		// либо нет записи, либо встроенная — различаем
		if _, err2 := r.GetByID(ctx, id); err2 == nil {
			return ro, ErrConflict // встроенная роль неизменяема
		}
		return ro, ErrNotFound
	}
	if err != nil {
		return ro, translate(err)
	}
	return ro, nil
}

// Delete — удаление кастомной роли (встроенная → ErrConflict).
func (r *RolesRepo) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM roles WHERE id = $1 AND is_builtin = false`, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		if _, err2 := r.GetByID(ctx, id); err2 == nil {
			return ErrConflict
		}
		return ErrNotFound
	}
	return nil
}

// PermissionsForUser — итоговый набор разрешений пользователя (объединение
// по всем назначенным ролям; scoping по кластерам — следующие чанки).
func (r *RolesRepo) PermissionsForUser(ctx context.Context, userID uuid.UUID) ([]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT DISTINCT unnest(ro.permissions)
		 FROM user_roles ur JOIN roles ro ON ro.id = ur.role_id
		 WHERE ur.user_id = $1`, userID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, translate(err)
		}
		out = append(out, p)
	}
	return out, translate(rows.Err())
}

// ---------------------------------------------------------------------------

// SessionsRepo — сессии (хранится SHA-256 хэш токена).
type SessionsRepo struct {
	pool *pgxpool.Pool
}

// Create создаёт сессию с хэшем токена.
func (r *SessionsRepo) Create(ctx context.Context, userID uuid.UUID, tokenHash string, ua, ip *string, expiresAt time.Time) (Session, error) {
	var s Session
	err := r.pool.QueryRow(ctx,
		`INSERT INTO sessions (user_id, refresh_token_hash, user_agent, ip, expires_at)
		 VALUES ($1, $2, $3, $4::inet, $5)
		 RETURNING id, user_id, user_agent, ip::text, expires_at, revoked_at, created_at`,
		userID, tokenHash, ua, ip, expiresAt).
		Scan(&s.ID, &s.UserID, &s.UserAgent, &s.IP, &s.ExpiresAt, &s.RevokedAt, &s.CreatedAt)
	if err != nil {
		return s, translate(err)
	}
	return s, nil
}

// GetValidByHash — активная (не отозванная, не истёкшая) сессия по хэшу
// токена. Нет записи → ErrNotFound.
func (r *SessionsRepo) GetValidByHash(ctx context.Context, tokenHash string) (Session, error) {
	var s Session
	err := r.pool.QueryRow(ctx,
		`SELECT id, user_id, user_agent, ip::text, expires_at, revoked_at, created_at
		 FROM sessions
		 WHERE refresh_token_hash = $1 AND revoked_at IS NULL AND expires_at > now()`,
		tokenHash).
		Scan(&s.ID, &s.UserID, &s.UserAgent, &s.IP, &s.ExpiresAt, &s.RevokedAt, &s.CreatedAt)
	if err != nil {
		return s, translate(err)
	}
	return s, nil
}

// Revoke отзывает сессию (идемпотентно).
func (r *SessionsRepo) Revoke(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE sessions SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, id)
	return translate(err)
}

// RevokeAllForUser — принудительный logout всех сессий пользователя.
// Возвращает число отозванных.
func (r *SessionsRepo) RevokeAllForUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		`UPDATE sessions SET revoked_at = now()
		 WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	if err != nil {
		return 0, translate(err)
	}
	return tag.RowsAffected(), nil
}

// ---------------------------------------------------------------------------

// AuditEntry — запись audit_log (append-only; FK нет — записи переживают
// удаление акторов).
type AuditEntry struct {
	OrganizationID *uuid.UUID
	ActorType      string // user | api_token | system | agent
	ActorUserID    *uuid.UUID
	ActorSessionID *uuid.UUID
	ActorAPIKeyID  *uuid.UUID
	ActorName      string
	IP             *string
	UserAgent      *string
	Action         string // auth.login, users.create, ...
	ObjectType     *string
	ObjectID       *uuid.UUID
	ObjectName     *string
	Result         string // success | denied | error
	Reason         *string
	// Diff — «было → стало» для изменений (чанк 37): {"before":{...},"after":{...}}
	// (только изменившиеся поля; секреты исключаются — см. auditdiff.Compute).
	Diff json.RawMessage
}

// AuditRepo — запись в audit_log (чтение — List для GET /audit_log).
// HashChain=true (server.audit_hash_chain) — записи связываются цепочкой
// хэшей prev_hash→hash (чанк 38); false — обычная вставка.
type AuditRepo struct {
	pool      *pgxpool.Pool
	HashChain bool
}

// Log пишет запись аудита (best-effort вызывающего кода — ошибки
// логируются выше). При включённой цепочке хэшей — logChained.
func (r *AuditRepo) Log(ctx context.Context, e AuditEntry) error {
	if r.HashChain {
		return r.logChained(ctx, e)
	}
	if e.ActorType == "" {
		e.ActorType = "user"
	}
	if e.Result == "" {
		e.Result = "success"
	}
	var ip pgtype.Text
	if e.IP != nil {
		ip = pgtype.Text{String: *e.IP, Valid: true}
	}
	_, err := r.pool.Exec(ctx,
		`INSERT INTO audit_log (organization_id, actor_type, actor_user_id, actor_session_id,
		 actor_api_token_id, actor_name, ip, user_agent, action, object_type, object_id, object_name, result, reason, diff)
		 VALUES ($1, $2, $3, $4, $5, $6, $7::inet, $8, $9, $10, $11, $12, $13, $14, $15)`,
		e.OrganizationID, e.ActorType, e.ActorUserID, e.ActorSessionID,
		e.ActorAPIKeyID, e.ActorName, ip, e.UserAgent, e.Action, e.ObjectType, e.ObjectID, e.ObjectName,
		e.Result, e.Reason, nullableJSON(e.Diff))
	return translate(err)
}

// AuditListItem — запись аудита для API (openapi AuditLogEntry).
type AuditListItem struct {
	ID         uuid.UUID       `json:"id"`
	CreatedAt  time.Time       `json:"created_at"`
	ActorType  string          `json:"actor_type"`
	ActorName  *string         `json:"actor_name"`
	Action     string          `json:"action"`
	ObjectType *string         `json:"object_type"`
	ObjectID   *uuid.UUID      `json:"object_id"`
	Result     string          `json:"result"`
	Reason     *string         `json:"reason"`
	IP         *string         `json:"ip"`
	Diff       json.RawMessage `json:"diff,omitempty"` // «было→стало» (чанк 37)
}

// List — keyset-листинг аудита организации (свежие первыми), фильтр по
// actor/action. Курсор — (created_at, id) DESC.
func (r *AuditRepo) List(ctx context.Context, orgID uuid.UUID, actionPrefix string, cursorCreatedAt *time.Time, cursorID *uuid.UUID, limit int) ([]AuditListItem, *string, error) {
	args := []any{orgID}
	where := "(organization_id = $1 OR organization_id IS NULL)"
	if actionPrefix != "" {
		args = append(args, actionPrefix+"%")
		where += fmt.Sprintf(" AND action LIKE $%d", len(args))
	}
	if cursorCreatedAt != nil && cursorID != nil {
		args = append(args, *cursorCreatedAt, *cursorID)
		where += fmt.Sprintf(" AND (created_at, id) < ($%d, $%d)", len(args)-1, len(args))
	}
	args = append(args, limit+1)
	rows, err := r.pool.Query(ctx,
		fmt.Sprintf(`SELECT id, created_at, actor_type, actor_name, action, object_type, object_id, result, reason, ip::text, diff
		 FROM audit_log WHERE %s
		 ORDER BY created_at DESC, id DESC LIMIT $%d`, where, len(args)),
		args...)
	if err != nil {
		return nil, nil, translate(err)
	}
	defer rows.Close()
	items := []AuditListItem{}
	for rows.Next() {
		var it AuditListItem
		if err := rows.Scan(&it.ID, &it.CreatedAt, &it.ActorType, &it.ActorName, &it.Action,
			&it.ObjectType, &it.ObjectID, &it.Result, &it.Reason, &it.IP, &it.Diff); err != nil {
			return nil, nil, translate(err)
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, translate(err)
	}
	var next *string
	if len(items) > limit {
		last := items[limit-1]
		s := fmt.Sprintf("%s,%s", last.CreatedAt.UTC().Format(time.RFC3339Nano), last.ID)
		next = &s
		items = items[:limit]
	}
	return items, next, nil
}
