// Управление пользователями, ролями и аудит-логом (чанк 28).
package httpapi

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/surifleet/surifleet/internal/authn"
	"github.com/surifleet/surifleet/internal/store"
)

var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// userInput — POST /users (openapi UserInput).
type userInput struct {
	Email        string                      `json:"email"`
	DisplayName  string                      `json:"display_name"`
	Password     string                      `json:"password"`
	IsBreakGlass bool                        `json:"is_break_glass"`
	Roles        []store.RoleAssignmentInput `json:"roles"`
}

// userPatchInput — PATCH /users/{id} (openapi UserUpdateInput + password).
type userPatchInput struct {
	DisplayName *string                      `json:"display_name"`
	IsActive    *bool                        `json:"is_active"`
	Password    *string                      `json:"password"`
	Roles       *[]store.RoleAssignmentInput `json:"roles"`
}

// validateRoleAssignments — роли существуют и доступны организации
// (встроенные или кастомные этой org).
func (h *handlers) validateRoleAssignments(r *http.Request, orgID uuid.UUID, fe fieldErrors, ras []store.RoleAssignmentInput) {
	for i, ra := range ras {
		ro, err := h.d.Store.Roles.GetByID(r.Context(), ra.RoleID)
		if err != nil {
			fe.add("roles", "роль не найдена: "+ra.RoleID.String())
			continue
		}
		if ro.OrganizationID != nil && *ro.OrganizationID != orgID {
			fe.add("roles", "роль "+ro.Name+" принадлежит другой организации")
			continue
		}
		if ra.ScopeType == "clusters" && len(ra.ClusterIDs) == 0 {
			fe.add("roles", "scope_type=clusters требует непустой cluster_ids")
		}
		_ = i
	}
}

// listUsers — GET /users[?q&is_active].
func (h *handlers) listUsers(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	cursor, limit, ok := parsePage(w, r)
	if !ok {
		return
	}
	var isActive *bool
	if s := r.URL.Query().Get("is_active"); s != "" {
		b, err := strconv.ParseBool(s)
		if err != nil {
			writeValidation(w, fieldErrors{"is_active": "true|false"})
			return
		}
		isActive = &b
	}
	items, next, err := h.d.Store.Users.List(r.Context(), orgID, r.URL.Query().Get("q"), isActive, cursor, limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page[store.User]{Items: items, NextCursor: next})
}

// createUser — POST /users: локальный пользователь с ролями (чанк 28).
func (h *handlers) createUser(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	var in userInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Email = strings.TrimSpace(in.Email)
	fe := fieldErrors{}
	if !emailRe.MatchString(in.Email) {
		fe.add("email", "некорректный email")
	}
	if strings.TrimSpace(in.DisplayName) == "" {
		fe.add("display_name", "обязательное непустое поле")
	}
	if len(in.Password) < 8 {
		fe.add("password", "минимум 8 символов")
	}
	h.validateRoleAssignments(r, orgID, fe, in.Roles)
	if fe.any() {
		writeValidation(w, fe)
		return
	}
	hash, err := authn.HashPassword(in.Password)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	u, err := h.d.Store.Users.Create(r.Context(), orgID, store.UserCreateInput{
		Email: in.Email, DisplayName: strings.TrimSpace(in.DisplayName),
		PasswordHash: hash, IsBreakGlass: in.IsBreakGlass, Roles: in.Roles,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	objType, objID := "user", u.ID
	id := identityFrom(r.Context())
	h.audit(r, id, "users.create", &objType, &objID, "success", "")
	writeJSON(w, http.StatusCreated, u)
}

// getUser — GET /users/{id}.
func (h *handlers) getUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	u, err := h.d.Store.Users.GetByID(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	u.PasswordHash = nil
	writeJSON(w, http.StatusOK, u)
}

// updateUser — PATCH /users/{id}: активность, имя, пароль, замена ролей.
func (h *handlers) updateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	var in userPatchInput
	if !decodeJSON(w, r, &in) {
		return
	}
	fe := fieldErrors{}
	if in.DisplayName != nil && strings.TrimSpace(*in.DisplayName) == "" {
		fe.add("display_name", "непустое поле")
	}
	if in.Password != nil && len(*in.Password) < 8 {
		fe.add("password", "минимум 8 символов")
	}
	if in.Roles != nil {
		h.validateRoleAssignments(r, orgID, fe, *in.Roles)
	}
	if fe.any() {
		writeValidation(w, fe)
		return
	}
	p := store.UserPatch{DisplayName: in.DisplayName, IsActive: in.IsActive, Roles: in.Roles}
	if in.Password != nil {
		hash, err := authn.HashPassword(*in.Password)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		p.PasswordHash = &hash
	}
	u, err := h.d.Store.Users.Update(r.Context(), id, p)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	objType := "user"
	actor := identityFrom(r.Context())
	h.audit(r, actor, "users.update", &objType, &id, "success", "")
	writeJSON(w, http.StatusOK, u)
}

// deleteUser — DELETE /users/{id}: удаление (последний break-glass — 409);
// сессии удаляются каскадом.
func (h *handlers) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	if actor := identityFrom(r.Context()); actor != nil && actor.UserID == id {
		writeError(w, http.StatusConflict, CodeConflict, "нельзя удалить самого себя", nil)
		return
	}
	if err := h.d.Store.Users.Delete(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	objType := "user"
	h.audit(r, identityFrom(r.Context()), "users.delete", &objType, &id, "success", "")
	w.WriteHeader(http.StatusNoContent)
}

// revokeUserSessions — POST /users/{id}/revoke_sessions: принудительный
// logout всех сессий пользователя.
func (h *handlers) revokeUserSessions(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	if _, err := h.d.Store.Users.GetByID(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	n, err := h.d.Store.Sessions.RevokeAllForUser(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	objType := "user"
	reason := "отозвано сессий: " + strconv.FormatInt(n, 10)
	h.audit(r, identityFrom(r.Context()), "users.revoke_sessions", &objType, &id, "success", reason)
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Роли
// ---------------------------------------------------------------------------

// listRoles — GET /roles: встроенные + кастомные организации.
func (h *handlers) listRoles(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	items, err := h.d.Store.Roles.List(r.Context(), orgID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page[store.Role]{Items: items, NextCursor: nil})
}

// roleInput — POST/PATCH /roles (openapi RoleInput).
type roleInput struct {
	Name        *string  `json:"name"`
	Description *string  `json:"description"`
	Permissions []string `json:"permissions"`
}

func validateRoleInput(fe fieldErrors, name *string, permissions []string, requireName bool) {
	if requireName && (name == nil || strings.TrimSpace(*name) == "") {
		fe.add("name", "обязательное непустое поле")
	}
	if name != nil && strings.TrimSpace(*name) == "" {
		fe.add("name", "непустое поле")
	}
	if permissions == nil {
		if requireName {
			fe.add("permissions", "обязательное поле")
		}
		return
	}
	for _, p := range permissions {
		if !validPermission(p) {
			fe.add("permissions", "неизвестное разрешение: "+p)
		}
	}
}

// createRole — POST /roles: кастомная роль организации.
func (h *handlers) createRole(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	var in roleInput
	if !decodeJSON(w, r, &in) {
		return
	}
	fe := fieldErrors{}
	validateRoleInput(fe, in.Name, in.Permissions, true)
	if fe.any() {
		writeValidation(w, fe)
		return
	}
	ro, err := h.d.Store.Roles.Create(r.Context(), orgID, strings.TrimSpace(*in.Name), in.Description, in.Permissions)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	objType, objID := "role", ro.ID
	h.audit(r, identityFrom(r.Context()), "roles.create", &objType, &objID, "success", "")
	writeJSON(w, http.StatusCreated, ro)
}

// getRole — GET /roles/{id}.
func (h *handlers) getRole(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	ro, err := h.d.Store.Roles.GetByID(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ro)
}

// updateRole — PATCH /roles/{id}: только кастомные (встроенная → 409).
func (h *handlers) updateRole(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	var in roleInput
	if !decodeJSON(w, r, &in) {
		return
	}
	fe := fieldErrors{}
	validateRoleInput(fe, in.Name, in.Permissions, false)
	if fe.any() {
		writeValidation(w, fe)
		return
	}
	ro, err := h.d.Store.Roles.Update(r.Context(), id, in.Name, in.Description, in.Permissions)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	objType := "role"
	h.audit(r, identityFrom(r.Context()), "roles.update", &objType, &id, "success", "")
	writeJSON(w, http.StatusOK, ro)
}

// deleteRole — DELETE /roles/{id}: только кастомные (встроенная → 409);
// назначения user_roles удаляются каскадом.
func (h *handlers) deleteRole(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	if err := h.d.Store.Roles.Delete(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	objType := "role"
	h.audit(r, identityFrom(r.Context()), "roles.delete", &objType, &id, "success", "")
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Аудит-лог
// ---------------------------------------------------------------------------

// listAuditLog — GET /audit_log[?action&cursor&limit]: свежие первыми.
// Курсор — "<RFC3339Nano>,<uuid>".
func (h *handlers) listAuditLog(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	limit := defaultLimit
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			writeValidation(w, fieldErrors{"limit": "целое число от 1 до 1000"})
			return
		}
		if n > maxLimit {
			n = maxLimit
		}
		limit = n
	}
	var curTS *time.Time
	var curID *uuid.UUID
	if c := r.URL.Query().Get("cursor"); c != "" {
		tsStr, idStr, ok2 := strings.Cut(c, ",")
		ts, err := time.Parse(time.RFC3339Nano, tsStr)
		uid, err2 := uuid.Parse(idStr)
		if !ok2 || err != nil || err2 != nil {
			writeValidation(w, fieldErrors{"cursor": "формат <RFC3339Nano>,<uuid>"})
			return
		}
		curTS, curID = &ts, &uid
	}
	items, next, err := h.d.Store.Audit.List(r.Context(), orgID, r.URL.Query().Get("action"), curTS, curID, limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page[store.AuditListItem]{Items: items, NextCursor: next})
}
