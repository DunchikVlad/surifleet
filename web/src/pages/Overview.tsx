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

// ClusterDashboard — ответ GET /clusters/{id}/dashboard (чанк 87).
interface ClusterDashboard {
  cluster: { id: string; name: string };
  agents: { total: number; by_status: Record<string, number> };
  instances: { total: number; by_status: Record<string, number> };
  hosts: {
    host_id: string; hostname: string;
    agent_id?: string | null; agent_status?: string | null;
    last_seen_at?: string | null;
    instances: number; by_compliance?: Record<string, number>;
  }[];
}

interface ClusterRef { id: string; name: string }

// agentBadge — CSS-класс бейджа статуса агента.
const agentBadge = (s?: string | null) =>
  !s ? "badge-info" : s === "online" ? "badge-ok" : s === "offline" || s === "error" ? "badge-err" : "badge-warn";

// complianceBadge — CSS-класс бейджа compliance-статуса инстанса.
const complianceBadge = (s: string) =>
  s === "in_sync" ? "badge-ok" : s === "drift" || s === "stale" ? "badge-err" : s === "pending" ? "badge-warn" : "badge-info";

// HostDashboard — ответ GET /hosts/{id}/dashboard (чанк 88).
interface HostDashboard {
  host: {
    id: string; hostname: string; ip_addresses: string[]; os?: string | null;
    cluster_id: string; cluster_name: string;
  };
  agent?: {
    id: string; status: string; agent_version?: string | null;
    last_seen_at?: string | null; cert_expires_at?: string | null; agent_ip?: string;
  } | null;
  instances: {
    id: string; name: string; suricata_version?: string | null; systemd_unit?: string | null;
    compliance_status: string; compliance_updated_at?: string | null;
    service_state?: string; service_pid?: number;
  }[];
}

// HostDashPanel — дашборд хоста (чанк 88): карточка агента и таблица
// инстансов с compliance и живым состоянием сервисов.
function HostDashPanel({ hostID, onClose }: { hostID: string; onClose: () => void }) {
  const [d, setD] = React.useState<HostDashboard | null>(null);
  const [err, setErr] = React.useState<unknown>(null);

  const load = React.useCallback(async () => {
    if (!hostID) return;
    try {
      setD(await apiGet<HostDashboard>(`/hosts/${hostID}/dashboard`));
      setErr(null);
    } catch (e) { setErr(e); }
  }, [hostID]);

  React.useEffect(() => { load(); }, [load]);
  useInterval(load, 15000, !!hostID);

  if (err) return <ErrorBox error={err} />;
  if (!d) return <p className="muted">загрузка…</p>;

  return (
    <div className="panel">
      <h3>
        Хост «{d.host.hostname}»{" "}
        <button className="btn" onClick={onClose}>закрыть</button>
      </h3>
      <p className="muted">
        Кластер {d.host.cluster_name}
        {d.host.ip_addresses.length > 0 && " · IP: " + d.host.ip_addresses.join(", ")}
        {d.host.os && " · " + d.host.os}
      </p>
      {d.agent ? (
        <p>
          Агент: <span className={agentBadge(d.agent.status)}>{d.agent.status}</span>{" "}
          <span className="muted">
            {d.agent.agent_version && "v" + d.agent.agent_version + " · "}
            {d.agent.agent_ip && "IP " + d.agent.agent_ip + " · "}
            виден {d.agent.last_seen_at ? fmtTime(d.agent.last_seen_at) : "ни разу"}
            {d.agent.cert_expires_at && " · серт. до " + fmtTime(d.agent.cert_expires_at)}
          </span>
        </p>
      ) : (
        <p className="muted">Агент не зарегистрирован — онбординг не пройден.</p>
      )}
      {d.instances.length > 0 ? (
        <table>
          <thead><tr><th>Инстанс</th><th>Suricata</th><th>Compliance</th><th>Сервис</th></tr></thead>
          <tbody>
            {d.instances.map(i => (
              <tr key={i.id}>
                <td><b>{i.name}</b>{i.systemd_unit && <span className="muted"> ({i.systemd_unit})</span>}</td>
                <td className="muted">{i.suricata_version || "—"}</td>
                <td>
                  <span className={complianceBadge(i.compliance_status)}>{i.compliance_status}</span>{" "}
                  {i.compliance_updated_at && <span className="muted">{fmtTime(i.compliance_updated_at)}</span>}
                </td>
                <td>
                  {i.service_state
                    ? <span className={i.service_state === "active" ? "badge-ok" : "badge-err"}>{i.service_state}</span>
                    : <span className="muted">—</span>}
                  {i.service_pid ? <span className="muted"> pid {i.service_pid}</span> : null}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : (
        <p className="muted">Инстансов нет.</p>
      )}
    </div>
  );
}

// ClusterDashPanel — дашборд выбранного кластера (чанк 87): те же
// карточки, что у флота, + таблица хостов; клик по хосту раскрывает
// дашборд хоста (чанк 88).
function ClusterDashPanel({ clusterID }: { clusterID: string }) {
  const [d, setD] = React.useState<ClusterDashboard | null>(null);
  const [err, setErr] = React.useState<unknown>(null);
  const [hostID, setHostID] = React.useState("");

  const load = React.useCallback(async () => {
    if (!clusterID) { setD(null); setHostID(""); return; }
    try {
      setD(await apiGet<ClusterDashboard>(`/clusters/${clusterID}/dashboard`));
      setErr(null);
    } catch (e) { setErr(e); setD(null); }
  }, [clusterID]);

  React.useEffect(() => { setHostID(""); load(); }, [load]);
  useInterval(load, 15000, !!clusterID);

  if (!clusterID) return null;
  if (err) return <ErrorBox error={err} />;
  if (!d) return <p className="muted">загрузка…</p>;

  return (
    <div className="panel">
      <h3>Кластер «{d.cluster.name}»</h3>
      <div className="cards">
        <div className="card"><div className="num">{d.agents.total}</div><div className="lbl">агентов</div></div>
        <div className="card ok"><div className="num">{d.agents.by_status["online"] ?? 0}</div><div className="lbl">online</div></div>
        <div className="card err"><div className="num">{d.agents.by_status["offline"] ?? 0}</div><div className="lbl">offline</div></div>
        <div className="card"><div className="num">{d.instances.total}</div><div className="lbl">инстансов</div></div>
        <div className="card ok"><div className="num">{d.instances.by_status["in_sync"] ?? 0}</div><div className="lbl">in sync</div></div>
        <div className="card err"><div className="num">
          {(d.instances.by_status["drift"] ?? 0) + (d.instances.by_status["stale"] ?? 0)}
        </div><div className="lbl">drift+stale</div></div>
      </div>
      {d.hosts.length > 0 && (
        <table>
          <thead><tr><th>Хост</th><th>Агент</th><th>Виден</th><th>Инстансы</th><th>Compliance</th></tr></thead>
          <tbody>
            {d.hosts.map(h => (
              <tr key={h.host_id}>
                <td>
                  <button className="btn" title="дашборд хоста" onClick={() => setHostID(hostID === h.host_id ? "" : h.host_id)}>
                    {h.hostname}
                  </button>
                </td>
                <td>{h.agent_status
                  ? <span className={agentBadge(h.agent_status)}>{h.agent_status}</span>
                  : <span className="muted">нет агента</span>}</td>
                <td className="muted">{h.last_seen_at ? fmtTime(h.last_seen_at) : "—"}</td>
                <td className="muted">{h.instances}</td>
                <td className="muted">
                  {h.by_compliance && Object.keys(h.by_compliance).length > 0
                    ? Object.entries(h.by_compliance).map(([k, v]) => `${k}:${v}`).join(" ")
                    : "—"}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {hostID && <HostDashPanel hostID={hostID} onClose={() => setHostID("")} />}
    </div>
  );
}

export default function Overview({ active }: { active: boolean }) {
  const [compliance, setCompliance] = React.useState<FleetCompliance | null>(null);
  const [dash, setDash] = React.useState<FleetDashboard | null>(null);
  const [activeDeps, setActiveDeps] = React.useState<Deployment[]>([]);
  const [clusters, setClusters] = React.useState<ClusterRef[]>([]);
  const [clusterID, setClusterID] = React.useState("");
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
  React.useEffect(() => {
    if (!active) return;
    apiGet<Page<ClusterRef>>("/clusters?limit=100")
      .then(d => setClusters(d.items || []))
      .catch(() => {});
  }, [active]);
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
      {clusters.length > 0 && (
        <>
          <h3>
            Дашборд кластера:{" "}
            <select value={clusterID} onChange={e => setClusterID(e.target.value)}>
              <option value="">— выбрать —</option>
              {clusters.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}
            </select>
          </h3>
          <ClusterDashPanel clusterID={clusterID} />
        </>
      )}
    </>
  );
}
