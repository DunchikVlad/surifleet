import React from "react";
import { Agent, apiGet, Page } from "../api";
import { ErrorBox, short, useInterval } from "../components";

// Metrics — вкладка «Метрики» (чанк 33): ряды host.* агента из ClickHouse
// (GET /agents/{id}/metrics), простые SVG-спарклайны без библиотек.

type Point = { ts: string; value: number };
type SeriesResp = { series?: Record<string, Point[]>; minutes?: number };

const NAMES: { name: string; title: string; unit: string; max?: number }[] = [
  { name: "host.cpu_percent", title: "CPU, %", unit: "%", max: 100 },
  { name: "host.mem_bytes", title: "Память", unit: "", },
  { name: "host.disk_used_percent", title: "Диск (логи), %", unit: "%", max: 100 },
];

function fmtVal(name: string, v: number): string {
  if (name === "host.mem_bytes") {
    if (v >= 1 << 30) return (v / (1 << 30)).toFixed(2) + " ГБ";
    return (v / (1 << 20)).toFixed(0) + " МБ";
  }
  return v.toFixed(1);
}

// Spark — мини-график SVG (полилиния по точкам, нормировка по max).
function Spark({ points, max }: { points: Point[]; max?: number }) {
  const W = 560, H = 90;
  if (!points.length) return <div className="muted">нет данных</div>;
  const hi = max ?? Math.max(...points.map(p => p.value), 1);
  const stepX = points.length > 1 ? W / (points.length - 1) : W;
  const pts = points.map((p, i) => `${(i * stepX).toFixed(1)},${(H - (p.value / hi) * (H - 6)).toFixed(1)}`).join(" ");
  const last = points[points.length - 1];
  return (
    <svg width={W} height={H} style={{ background: "rgba(255,255,255,0.03)", borderRadius: 6 }}>
      <polyline points={pts} fill="none" stroke="#4da3ff" strokeWidth="1.5" />
      <text x={W - 6} y={14} textAnchor="end" fill="#dbe4ee" fontSize="12">
        {last.value.toFixed(1)}
      </text>
    </svg>
  );
}

export default function Metrics({ active }: { active: boolean }) {
  const [agents, setAgents] = React.useState<Agent[]>([]);
  const [agentId, setAgentId] = React.useState("");
  const [minutes, setMinutes] = React.useState(60);
  const [data, setData] = React.useState<Record<string, Point[]>>({});
  const [err, setErr] = React.useState<unknown>(null);
  const [updated, setUpdated] = React.useState("");

  React.useEffect(() => {
    if (!active) return;
    (async () => {
      try {
        const d = await apiGet<Page<Agent>>("/agents");
        const list = d.items || [];
        setAgents(list);
        setAgentId(prev => (prev && list.some(a => a.id === prev) ? prev : list[0]?.id || ""));
      } catch { /* агенты некритичны */ }
    })();
  }, [active]);

  const load = React.useCallback(async () => {
    if (!agentId) return;
    try {
      const d = await apiGet<SeriesResp>(`/agents/${agentId}/metrics?minutes=${minutes}`);
      setData(d.series || {});
      setErr(null);
      setUpdated(new Date().toLocaleTimeString("ru-RU"));
    } catch (e) { setErr(e); }
  }, [agentId, minutes]);

  React.useEffect(() => { load(); }, [load]);
  useInterval(load, 30000, active);

  return (
    <>
      <h2>Метрики агента</h2>
      <ErrorBox error={err} />
      <div className="toolbar">
        <select value={agentId} onChange={e => setAgentId(e.target.value)}>
          {agents.length === 0 && <option value="">— агентов нет —</option>}
          {agents.map(a => (
            <option key={a.id} value={a.id}>
              {a.hostname || short(a.host_id)} · {a.status} · {short(a.id)}
            </option>
          ))}
        </select>
        <select value={minutes} onChange={e => setMinutes(Number(e.target.value))}>
          {[15, 60, 180, 720, 1440].map(n => (
            <option key={n} value={n}>{n < 60 ? `${n} мин` : `${n / 60} ч`}</option>
          ))}
        </select>
        <button className="btn" onClick={load}>Обновить</button>
        <span className="muted">{updated && "обновлено " + updated + " · авто 30 с"}</span>
      </div>
      {NAMES.map(({ name, title, max }) => {
        const pts = data[name] || [];
        const last = pts.length ? pts[pts.length - 1].value : null;
        return (
          <div className="panel" key={name} style={{ marginBottom: "1em" }}>
            <p style={{ marginTop: 0 }}>
              <b>{title}</b>{" "}
              <span className="muted">
                {last !== null ? `сейчас: ${fmtVal(name, last)}` : "нет данных"} · точек: {pts.length}
              </span>
            </p>
            <Spark points={pts} max={max} />
          </div>
        );
      })}
      <p className="muted">
        Агент шлёт MetricsBatch каждые 60 с (host.cpu_percent, host.mem_bytes,
        host.disk_used_percent) → ClickHouse surifleet.agent_metrics. Метрики
        Suricata (kernel drops и т.п.) — следующие чанки.
      </p>
    </>
  );
}
