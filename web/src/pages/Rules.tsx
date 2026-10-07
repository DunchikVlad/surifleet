import React from "react";
import { apiGet, apiPost, apiPatch, Page, Rule } from "../api";
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

  // Клонирование правила (чанк 51): новый sid 9000xxx + msg (копия).
  const clone = async (r: Rule) => {
    const msg = prompt("msg клона (sid будет выдан из диапазона 9000xxx):", (r.msg || "") + " (копия)");
    if (msg === null) return;
    try {
      const created = await apiPost<Rule>(`/rules/${r.id}/clone`, { msg });
      alert(`Создан клон: sid ${created.sid} (under_review)`);
      setLoaded(false);
    } catch (e) { alert("Клонирование не удалось: " + (e as Error).message); }
  };

  // Правка msg (PATCH /rules/{id}).
  const editMsg = async (r: Rule) => {
    const msg = prompt("Новое msg правила:", r.msg || "");
    if (msg === null || !msg.trim()) return;
    try {
      await apiPatch(`/rules/${r.id}`, { msg: msg.trim() });
      setItems(prev => prev.map(x => (x.id === r.id ? { ...x, msg: msg.trim() } : x)));
    } catch (e) { alert("Правка не удалась: " + (e as Error).message); }
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
                <td style={{ whiteSpace: "nowrap" }}>
                  {can("rules.write") && r.status === "enabled" &&
                    <button className="btn" onClick={() => toggle(r.id, "disable")}>откл.</button>}
                  {can("rules.write") && r.status === "disabled" &&
                    <button className="btn" onClick={() => toggle(r.id, "enable")}>вкл.</button>}
                  {can("rules.write") && (
                    <>
                      {" "}<button className="btn" title="правка msg" onClick={() => editMsg(r)}>ред.</button>
                      {" "}<button className="btn" title="клон с новым sid (9000xxx)" onClick={() => clone(r)}>клон</button>
                    </>
                  )}
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
