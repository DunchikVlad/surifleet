// API-клиент поверх /api/v1. Auth (чанк 28): сессионный токен в
// localStorage (surifleet_token), Authorization: Bearer; 401 → сброс
// токена + событие surifleet-logout (App покажет форму входа). В
// auth_mode=dev сервера токен не нужен — /auth/me отвечает 200 без него.
const API: string = import.meta.env.VITE_API_BASE ?? "/api/v1";

const TOKEN_KEY = "surifleet_token";
export const getToken = (): string | null => localStorage.getItem(TOKEN_KEY);
export const setToken = (t: string | null) => {
  if (t) localStorage.setItem(TOKEN_KEY, t);
  else localStorage.removeItem(TOKEN_KEY);
};

export class ApiError extends Error { status = 0 }

function authHeaders(): Record<string, string> {
  const t = getToken();
  return t ? { Authorization: "Bearer " + t } : {};
}

function handle401(r: Response) {
  if (r.status === 401 && getToken()) {
    setToken(null);
    window.dispatchEvent(new Event("surifleet-logout"));
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const r = await fetch(API + path, {
    ...init,
    headers: { ...authHeaders(), ...(init?.headers as Record<string, string> | undefined) },
  });
  if (!r.ok) {
    handle401(r);
    let msg = "HTTP " + r.status;
    try {
      const j = await r.json();
      if (j.error?.message) msg += ": " + j.error.message;
    } catch { /* тело не JSON — оставляем HTTP-код */ }
    const e = new ApiError(msg);
    e.status = r.status;
    throw e;
  }
  return r.json() as Promise<T>;
}

export const apiGet = <T,>(path: string) => request<T>(path);
export const apiPost = <T,>(path: string, body?: unknown) =>
  request<T>(path, {
    method: "POST",
    headers: body !== undefined ? { "Content-Type": "application/json" } : {},
    body: body !== undefined ? JSON.stringify(body) : null,
  });

// apiPatch — PATCH с JSON-телом (частичное обновление).
export const apiPatch = <T,>(path: string, body: unknown) =>
  request<T>(path, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });

// apiPut — PUT с JSON-телом (замена ресурса, чанк 61: capabilities).
export const apiPut = <T,>(path: string, body: unknown) =>
  request<T>(path, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });

// apiDelete — DELETE без тела; 204 (без контента) → undefined.
export const apiDelete = async (path: string) => {
  const r = await fetch(API + path, { method: "DELETE", headers: authHeaders() });
  if (!r.ok) {
    handle401(r);
    let msg = "HTTP " + r.status;
    try {
      const j = await r.json();
      if (j.error?.message) msg += ": " + j.error.message;
    } catch { /* тело не JSON — оставляем HTTP-код */ }
    throw new ApiError(msg);
  }
};

// apiPostEx — как apiPost, но возвращает и HTTP-статус (напр. различать
// 201 «создан» и 200 «уже существовал» у идемпотентного POST /rulesets).
export const apiPostEx = async <T,>(path: string, body?: unknown) => {
  const r = await fetch(API + path, {
    method: "POST",
    headers: { ...authHeaders(), ...(body !== undefined ? { "Content-Type": "application/json" } : {}) },
    body: body !== undefined ? JSON.stringify(body) : null,
  });
  if (!r.ok) {
    handle401(r);
    let msg = "HTTP " + r.status;
    try {
      const j = await r.json();
      if (j.error?.message) msg += ": " + j.error.message;
    } catch { /* тело не JSON — оставляем HTTP-код */ }
    throw new ApiError(msg);
  }
  return { status: r.status, body: (await r.json()) as T };
};

// --- auth (чанк 28) ---

export interface AuthTokens {
  access_token: string;
  refresh_token: string;
  token_type: string;
  expires_in: number;
}

export interface CurrentUser {
  user: { email: string; display_name: string; is_break_glass?: boolean };
  roles?: { name: string }[];
  permissions: string[];
}

export const login = (email: string, password: string) =>
  request<AuthTokens>("/auth/login", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email, password }),
  });

export const logout = async () => {
  const t = getToken();
  if (t) {
    try {
      await request<unknown>("/auth/logout", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ refresh_token: t }),
      });
    } catch { /* отзыв на сервере best-effort */ }
  }
  setToken(null);
};

// --- типы (соответствуют api/openapi/openapi.yaml, подмножество для UI) ---

export interface Health { status: string; checks?: Record<string, string> }
export interface Version { version: string; commit: string }

export interface FleetCompliance {
  summary: { total_instances: number; by_status: Record<string, number> };
}

export interface Progress { total?: number; succeeded?: number; failed?: number }

export interface Deployment {
  id: string;
  ruleset_version_id: string;
  status: string;
  progress?: Progress;
  created_at?: string;
}

export interface DeployTask {
  instance_id: string;
  wave: number;
  status: string;
  attempts: number;
  max_attempts: number;
  error?: string;
  result?: { loaded_count?: number };
}

export interface Instance {
  id: string;
  host_id?: string;
  name: string;
  suricata_version?: string;
  config_path?: string;
  rules_dir?: string;
  log_dir?: string;
  capture_interfaces?: string[];
  systemd_unit?: string;
  updated_at?: string;
}

export interface FailedRule { sid: number; rev: number; error_text: string }

export interface InstanceState {
  instance_id?: string;
  compliance?: { status?: string; updated_at?: string };
  desired?: {
    ruleset_version_id?: string;
    ruleset_hash?: string;
    calc_version?: number;
    updated_at?: string;
  };
  actual?: {
    ruleset_hash?: string;
    reported_at?: string;
    loaded_count?: number;
    failed_count?: number;
    failed_rules?: FailedRule[];
    last_reload?: {
      action: string;
      success: boolean;
      message?: string;
      finished_at?: string | null;
    };
  };
  diff?: {
    missing_rules?: number[];
    extra_rules?: number[];
    failed_rules?: FailedRule[];
  };
}

// DeployHistoryItem — запись GET /instances/{id}/deploy_history.
export interface DeployHistoryItem {
  deployment_id: string;
  ruleset_version: string;
  initiated_by?: string | null;
  status: string;
  started_at?: string | null;
  finished_at?: string | null;
  result?: string | null;
}

export interface Rule {
  id: string;
  sid: number;
  msg?: string;
  status: string;
  category?: string;
  source_type?: string;
}

export interface Ruleset {
  id: string;
  version: string;
  rule_count: number;
  sha256?: string;
  created_at?: string;
}

export interface Agent {
  id: string;
  host_id?: string;
  hostname?: string;
  status?: string;
}

export interface LogEntry { ts: string; level: string; message: string }

export interface MatrixInstance { instance_id: string; name?: string; hostname?: string }
export interface MatrixRule { sid: number; msg?: string; status?: string }
export interface MatrixCell { sid: number; instance_id: string; status: string }
export interface RulesMatrix {
  instances?: MatrixInstance[];
  rules?: MatrixRule[];
  cells?: MatrixCell[];
  next_rule_cursor?: string | null;
}

export interface Page<T> { items?: T[]; next_cursor?: string | null }

// Ioc — индикатор компрометации (GET /iocs, openapi Ioc).
export interface Ioc {
  id: string;
  type: string;
  value: string;
  score: number;
  status: string;
  source?: string | null;
  expires_at?: string | null;
  created_at?: string;
}

// IocGenerateResult — ответ POST /iocs/generate (openapi IocGenerateResult).
export interface IocGenerateResult {
  swept_expired: number;
  active: number;
  created: number;
  updated: number;
  unchanged: number;
  skipped: { id: string; type: string; value: string; reason: string }[];
  ruleset_id?: string | null;
  ruleset_version?: string;
  ruleset_created?: boolean;
  rules_count?: number;
  deployment_id?: string | null;
}

// Feed — фид IOC (GET /feeds, openapi Feed).
export interface Feed {
  id: string;
  name: string;
  type: string;
  url: string;
  schedule?: string | null;
  enabled: boolean;
  last_sync_at?: string | null;
  last_sync_status?: string | null;
  last_error?: string | null;
  created_at?: string;
}

// FeedRun — запуск синхронизации фида (openapi FeedRun; счётчики
// rules_* и ruleset_version — только в ответе POST /feeds/{id}/sync).
export interface FeedRun {
  id: string;
  feed_id: string;
  status: string;
  started_at?: string;
  finished_at?: string | null;
  imported: number;
  updated: number;
  skipped: number;
  error?: string | null;
  rules_created?: number;
  rules_updated?: number;
  rules_unchanged?: number;
  ruleset_version?: string;
}

// --- пользователи, роли, токены, аудит (чанки 28–30) ---

export interface RoleAssignment {
  role_id: string;
  role_name?: string;
  scope_type?: string;
  cluster_ids?: string[];
}

export interface User {
  id: string;
  email: string;
  display_name: string;
  is_active: boolean;
  is_break_glass: boolean;
  last_login_at?: string | null;
  created_at?: string;
  roles?: RoleAssignment[];
}

export interface Role {
  id: string;
  organization_id?: string | null;
  name: string;
  description?: string | null;
  permissions: string[];
  is_builtin: boolean;
}

export interface ApiToken {
  id: string;
  name: string;
  scopes: string[];
  expires_at?: string | null;
  last_used_at?: string | null;
  created_at?: string;
}

export interface ApiTokenCreated extends ApiToken { token: string }

export interface AuditEntry {
  id: string;
  created_at: string;
  actor_type: string;
  actor_name?: string | null;
  action: string;
  object_type?: string | null;
  object_id?: string | null;
  result: string;
  reason?: string | null;
  ip?: string | null;
  // diff «было→стало» (чанк 37): {before, after} — только изменённые поля.
  diff?: { before?: Record<string, unknown>; after?: Record<string, unknown> } | null;
}

// ChainVerifyResult — ответ GET /audit_log/verify (чанк 38).
export interface ChainVerifyResult {
  checked: number;
  chained: number;
  ok: boolean;
  broken_at?: string;
  reason?: string;
}

// PERMS — каталог разрешений (мультивыбор в формах ролей/токенов).
export const PERMS = [
  "fleet.read", "hosts.read", "hosts.write", "agents.read",
  "rules.read", "rules.write", "rules.deploy",
  "ioc.read", "ioc.write", "feeds.read", "feeds.write",
  "users.read", "users.write", "tokens.read", "tokens.write",
  "roles.read", "roles.write", "audit.read",
  "sso.read", "sso.write",
] as const;

// --- SSO-провайдеры (чанк 35) ---

// SsoProviderPublic — публичная запись формы входа (GET /auth/sso/providers).
export interface SsoProviderPublic { id: string; name: string; type: string }

// OIDCConfig — конфигурация OIDC-провайдера (client_secret — writeOnly).
export interface OIDCConfig {
  issuer_url: string;
  client_id: string;
  client_secret?: string;
  redirect_url: string;
  scopes?: string[];
}

// SsoProvider — SSO-провайдер (GET /sso_providers; client_secret не отдаётся).
export interface SsoProvider {
  id: string;
  organization_id: string;
  name: string;
  type: string;
  config: OIDCConfig;
  group_role_mapping?: Record<string, string[]>;
  enabled: boolean;
  created_at?: string;
  updated_at?: string;
}

// listSsoProvidersPublic — включённые OIDC-провайдеры для кнопки «Войти через SSO».
export const listSsoProvidersPublic = () =>
  apiGet<Page<SsoProviderPublic>>("/auth/sso/providers");
