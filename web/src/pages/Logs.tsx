import React from "react";
import { Agent, apiGet, LogEntry, Page } from "../api";
import { Badge, ErrorBox, fmtTime, short, useInterval } from "../components";

export default function Logs({ active }: { active: boolean }) {
  const [agents, setAgents] = React.useState<Agent[]>([]);
  const [agentId, setAgentId] = React.useState("");
  const [limit, setLimit] = React.useState(200);
  const [items, setItems] = React.useState<LogEntry[] | null>(null);
  const [err, setErr] = React.useState<unknown>(null);
  const [auto, setAuto] = React.useState(true);
  const [updated, setUpdated] = React.useState("");
  // Фильтры (чанк 117, п. 5.4 ТЗ «поиск и фильтры по хосту, уровню,
  // периоду, операции»): хост — селектор агента, уровень/подстрока/период
  // — параметры API.
  const [level, setLevel] = React.useState("");
  const [q, setQ] = React.useState("");
  const [qApplied, setQApplied] = React.useState("");
  const [from, setFrom] = React.useState(""); // datetime-local
  const [to, setTo] = React.useState("");

  React.useEffect(() => {
    if (!active) return;
    (async () => {
      try {
        const d = await apiGet<Page<Agent>>("/agents");
        const list = d.items || [];
        setAgents(list);
        setAgentId(prev =>
          prev && list.some(a => a.id === prev) ? prev : list[0]?.id || "");
      } catch { /* агенты некритичны */ }
    })();
  }, [active]);

  const load = React.useCallback(async () => {
    if (!agentId) return;
    try {
      let path = `/agents/${agentId}/logs?limit=${limit}`;
      if (level) path += `&level=${level}`;
      if (qApplied.trim()) path += `&q=${encodeURIComponent(qApplied.trim())}`;
      if (from) path += `&from=${encodeURIComponent(new Date(from).toISOString())}`;
      if (to) path += `&to=${encodeURIComponent(new Date(to).toISOString())}`;
      const d = await apiGet<Page<LogEntry>>(path);
      setItems(d.items || []);
      setErr(null);
      setUpdated(new Date().toLocaleTimeString("ru-RU"));
    } catch (e) { setErr(e); }
  }, [agentId, limit, level, qApplied, from, to]);

  React.useEffect(() => { load(); }, [load]);
  useInterval(load, 10000, active && auto);

  const resetFilters = () => { setLevel(""); setQ(""); setQApplied(""); setFrom(""); setTo(""); };

  return (
    <>
      <h2>Логи агентов</h2>
      <div className="toolbar">
        <select value={agentId} onChange={e => setAgentId(e.target.value)}>
          {agents.length === 0 && <option value="">— агентов нет —</option>}
          {agents.map(a => (
            <option key={a.id} value={a.id}>
              {a.hostname || short(a.host_id)} · {a.status} · {short(a.id)}
            </option>
          ))}
        </select>
        <select value={limit} onChange={e => setLimit(Number(e.target.value))}>
          {[50, 100, 200, 500, 1000].map(n => <option key={n} value={n}>{n}</option>)}
        </select>
        <button className="btn" onClick={load}>Обновить</button>
        <label className="muted" style={{ display: "flex", alignItems: "center", gap: 6 }}>
          <input type="checkbox" checked={auto} onChange={e => setAuto(e.target.checked)} />
          авто (10 с)
        </label>
        <span className="muted">{updated && "обновлено " + updated}</span>
      </div>
      <div className="toolbar">
        <select value={level} onChange={e => setLevel(e.target.value)} title="уровень">
          <option value="">все уровни</option>
          {["debug", "info", "warn", "error"].map(l => <option key={l} value={l}>{l}</option>)}
        </select>
        <input
          placeholder="поиск по тексту…"
          value={q}
          onChange={e => setQ(e.target.value)}
          onKeyDown={e => { if (e.key === "Enter") setQApplied(q); }}
          style={{ minWidth: "16em" }}
        />
        <button className="btn" onClick={() => setQApplied(q)}>Найти</button>
        <input type="datetime-local" title="период: с" value={from}
          onChange={e => setFrom(e.target.value)} />
        <span className="muted">—</span>
        <input type="datetime-local" title="период: по" value={to}
          onChange={e => setTo(e.target.value)} />
        <button className="btn" onClick={resetFilters}>Сбросить</button>
      </div>
      <ErrorBox error={err} />
      {items && !items.length && !err && (
        <p className="muted">Записей нет — агент шлёт логи раз в 30 с.</p>
      )}
      {items && items.length > 0 && (
        <table>
          <thead>
            <tr><th>Время</th><th>Уровень</th><th>Сообщение</th></tr>
          </thead>
          <tbody>
            {items.map((e, i) => (
              <tr key={i}>
                <td className="muted" style={{ whiteSpace: "nowrap" }}>{fmtTime(e.ts)}</td>
                <td><Badge status={e.level} /></td>
                <td style={{ wordBreak: "break-all" }}>{e.message}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}
