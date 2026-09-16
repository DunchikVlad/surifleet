// API-клиент поверх /api/v1 (DevAuth-заглушка — без заголовков авторизации).
// Base URL: в dev — vite proxy /api → стенд; в prod — тот же origin, что отдал /app/.
const API: string = import.meta.env.VITE_API_BASE ?? "/api/v1";

export class ApiError extends Error {}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const r = await fetch(API + path, init);
  if (!r.ok) {
    let msg = "HTTP " + r.status;
    try {
      const j = await r.json();
      if (j.error?.message) msg += ": " + j.error.message;
    } catch { /* тело не JSON — оставляем HTTP-код */ }
    throw new ApiError(msg);
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

// apiPostEx — как apiPost, но возвращает и HTTP-статус (напр. различать
// 201 «создан» и 200 «уже существовал» у идемпотентного POST /rulesets).
export const apiPostEx = async <T,>(path: string, body?: unknown) => {
  const r = await fetch(API + path, {
    method: "POST",
    headers: body !== undefined ? { "Content-Type": "application/json" } : {},
    body: body !== undefined ? JSON.stringify(body) : null,
  });
  if (!r.ok) {
    let msg = "HTTP " + r.status;
    try {
      const j = await r.json();
      if (j.error?.message) msg += ": " + j.error.message;
    } catch { /* тело не JSON — оставляем HTTP-код */ }
    throw new ApiError(msg);
  }
  return { status: r.status, body: (await r.json()) as T };
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
