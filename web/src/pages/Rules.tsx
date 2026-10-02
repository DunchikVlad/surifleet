import React from "react";
import { apiGet, apiPost, Page, Rule } from "../api";
import { Badge, ErrorBox } from "../components";
import { useCan } from "../perms";

const LIMIT = 50;

export default function Rules({ active }: { active: boolean }) {
  const can = useCan();
  const [items, setItems] = React.useState<Rule[]>([]);
  const [cursor, setCursor] = React.useState<string | null>(null);
  const [status, setStatus] = React.useState("");
  const [q, setQ] = React.useState("");
  const [err, setErr] = React.useState<unknown>(null);
  const [loaded, setLoaded] = React.useState(false);

  const load = React.useCallback(async (append: boolean) => {
    let path = `/rules?limit=${LIMIT}`;
    if (status) path += `&status=${encodeURIComponent(status)}`;
    if (q.trim()) path += `&q=${encodeURIComponent(q.trim())}`;
    if (append && cursor) path += `&cursor=${encodeURIComponent(cursor)}`;
    try {
      const d = await apiGet<Page<Rule>>(path);
      setItems(prev => (append ? [...prev, ...(d.items || [])] : d.items || []));
      setCursor(d.next_cursor ?? null);
      setErr(null);
      setLoaded(true);
    } catch (e) { setErr(e); }
  }, [status, q, cursor]);

  React.useEffect(() => {
    if (active && !loaded) load(false);
  }, [active, loaded, load]);

  const toggle = async (id: string, action: "enable" | "disable") => {
    try {
      await apiPost("/rules/bulk", { ids: [id], action });
      setItems(prev => prev.map(r =>
        r.id === id ? { ...r, status: action === "enable" ? "enabled" : "disabled" } : r));
    } catch (e) { alert("Операция не выполнена: " + (e as Error).message); }
  };

  return (
    <>
      <h2>Правила</h2>
      <div className="toolbar">
        <select value={status} onChange={e => { setStatus(e.target.value); setLoaded(false); }}>
          <option value="">все статусы</option>
          <option value="enabled">enabled</option>
          <option value="disabled">disabled</option>
          <option value="under_review">under_review</option>
        </select>
        <input
          placeholder="поиск по sid / сообщению…"
          value={q}
          onChange={e => setQ(e.target.value)}
          onKeyDown={e => { if (e.key === "Enter") { setLoaded(false); } }}
        />
        <button className="btn" onClick={() => setLoaded(false)}>Найти</button>
      </div>
      <ErrorBox error={err} />
      {loaded && !items.length && !err && <p className="muted">Правил по фильтру нет.</p>}
      {items.length > 0 && (
        <table>
          <thead>
            <tr><th>SID</th><th>Сообщение</th><th>Статус</th><th>Категория</th><th>Источник</th><th></th></tr>
          </thead>
          <tbody>
            {items.map(r => (
              <tr key={r.id}>
                <td>{r.sid}</td>
                <td>{r.msg || ""}</td>
                <td><Badge status={r.status} /></td>
                <td className="muted">{r.category || "—"}</td>
                <td className="muted">{r.source_type}</td>
                <td>
                  {can("rules.write") && r.status === "enabled" &&
                    <button className="btn" onClick={() => toggle(r.id, "disable")}>откл.</button>}
                  {can("rules.write") && r.status === "disabled" &&
                    <button className="btn" onClick={() => toggle(r.id, "enable")}>вкл.</button>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {cursor && (
        <p>
          <button className="btn" onClick={() => load(true)}>Ещё {LIMIT}…</button>{" "}
          <span className="muted">показано {items.length}</span>
        </p>
      )}
    </>
  );
}
