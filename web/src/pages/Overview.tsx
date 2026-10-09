import React from "react";
import { apiGet, Deployment, FleetCompliance, Page } from "../api";
import { ErrorBox, fmtTime, useInterval } from "../components";
import { DeploymentTable } from "./Deployments";

const CARD_DEFS: { key: string; label: string; cls: string }[] = [
  { key: "total_instances", label: "всего", cls: "" },
  { key: "in_sync", label: "in sync", cls: "ok" },
  { key: "pending", label: "pending", cls: "warn" },
  { key: "partial", label: "partial", cls: "partial" },
  { key: "drift", label: "drift", cls: "err" },
  { key: "stale", label: "stale", cls: "err" },
];

// FleetDashboard — ответ GET /fleet/dashboard (чанк 86, план 1C).
interface FleetDashboard {
  agents: { total: number; by_status: Record<string, number> };
  instances: { total: number; by_status: Record<string, number> };
  deployments_24h: { total: number; by_status: Record<string, number> };
  offline_agents: { agent_id: string; hostname: string; cluster: string; last_seen_at?: string | null }[];
}

// AGENT_CARDS — карточки здоровья агентов (порядок отображения).
const AGENT_CARDS: { key: string; label: string; cls: string }[] = [
  { key: "total", label: "всего агентов", cls: "" },
  { key: "online", label: "online", cls: "ok" },
  { key: "offline", label: "offline", cls: "err" },
  { key: "degraded", label: "degraded", cls: "warn" },
  { key: "updating", label: "updating", cls: "warn" },
  { key: "error", label: "error", cls: "err" },
];

export default function Overview({ active }: { active: boolean }) {
  const [compliance, setCompliance] = React.useState<FleetCompliance | null>(null);
  const [dash, setDash] = React.useState<FleetDashboard | null>(null);
  const [activeDeps, setActiveDeps] = React.useState<Deployment[]>([]);
  const [err, setErr] = React.useState<unknown>(null);

  const load = React.useCallback(async () => {
    try {
      setCompliance(await apiGet<FleetCompliance>("/fleet/compliance"));
      setErr(null);
    } catch (e) { setErr(e); }
    try {
      setDash(await apiGet<FleetDashboard>("/fleet/dashboard"));
    } catch { /* дашборд некритичен для обзора */ }
    try {
      const d = await apiGet<Page<Deployment>>("/deployments?limit=50");
      setActiveDeps((d.items || []).filter(x =>
        ["pending", "running", "paused"].includes(x.status)));
    } catch { /* деплои некритичны для обзора */ }
  }, []);

  React.useEffect(() => { if (active) load(); }, [active, load]);
  useInterval(load, 15000, active);

  const by = compliance?.summary.by_status || {};
  const dBy = dash?.deployments_24h.by_status || {};
  return (
    <>
      <h2>
        Соответствие флота
        <button className="btn" onClick={load}>Обновить</button>
      </h2>
      <ErrorBox error={err} />
      {dash && (
        <>
          <h3>Агенты</h3>
          <div className="cards">
            {AGENT_CARDS.map(c => (
              <div key={c.key} className={"card " + c.cls}>
                <div className="num">
                  {c.key === "total" ? dash.agents.total : (dash.agents.by_status[c.key] ?? 0)}
                </div>
                <div className="lbl">{c.label}</div>
              </div>
            ))}
          </div>
          {dash.offline_agents.length > 0 && (
            <p className="muted">
              Offline: {dash.offline_agents.map(a =>
                `${a.hostname} (${a.cluster}${a.last_seen_at ? ", виден " + fmtTime(a.last_seen_at) : ", не виден ни разу"})`).join("; ")}
            </p>
          )}
        </>
      )}
      <h3>Инстансы</h3>
      <div className="cards">
        {CARD_DEFS.map(c => (
          <div key={c.key} className={"card " + c.cls}>
            <div className="num">
              {c.key === "total_instances"
                ? compliance?.summary.total_instances ?? 0
                : by[c.key] ?? 0}
            </div>
            <div className="lbl">{c.label}</div>
          </div>
        ))}
      </div>
      {dash && dash.deployments_24h.total > 0 && (
        <p className="muted">
          Деплои за 24 ч: {dash.deployments_24h.total}
          {Object.entries(dBy).map(([k, v]) => ` · ${k}: ${v}`).join("")}
        </p>
      )}
      <h3>Активные деплои</h3>
      {activeDeps.length
        ? <DeploymentTable items={activeDeps} />
        : <p className="muted">Активных деплоев нет.</p>}
    </>
  );
}
