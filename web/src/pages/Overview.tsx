import React from "react";
import { apiGet, Deployment, FleetCompliance, Page } from "../api";
import { ErrorBox, useInterval } from "../components";
import { DeploymentTable } from "./Deployments";

const CARD_DEFS: { key: string; label: string; cls: string }[] = [
  { key: "total_instances", label: "всего", cls: "" },
  { key: "in_sync", label: "in sync", cls: "ok" },
  { key: "pending", label: "pending", cls: "warn" },
  { key: "partial", label: "partial", cls: "partial" },
  { key: "drift", label: "drift", cls: "err" },
  { key: "stale", label: "stale", cls: "err" },
];

export default function Overview({ active }: { active: boolean }) {
  const [compliance, setCompliance] = React.useState<FleetCompliance | null>(null);
  const [activeDeps, setActiveDeps] = React.useState<Deployment[]>([]);
  const [err, setErr] = React.useState<unknown>(null);

  const load = React.useCallback(async () => {
    try {
      setCompliance(await apiGet<FleetCompliance>("/fleet/compliance"));
      setErr(null);
    } catch (e) { setErr(e); }
    try {
      const d = await apiGet<Page<Deployment>>("/deployments?limit=50");
      setActiveDeps((d.items || []).filter(x =>
        ["pending", "running", "paused"].includes(x.status)));
    } catch { /* деплои некритичны для обзора */ }
  }, []);

  React.useEffect(() => { if (active) load(); }, [active, load]);
  useInterval(load, 15000, active);

  const by = compliance?.summary.by_status || {};
  return (
    <>
      <h2>
        Соответствие флота
        <button className="btn" onClick={load}>Обновить</button>
      </h2>
      <ErrorBox error={err} />
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
      <h3>Активные деплои</h3>
      {activeDeps.length
        ? <DeploymentTable items={activeDeps} />
        : <p className="muted">Активных деплоев нет.</p>}
    </>
  );
}
