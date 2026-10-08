import React from "react";
import { apiGet, apiPost, apiPatch, getToken, Page, Rule } from "../api";
import { Badge, ErrorBox, SortState, SortTh, sortBy } from "../components";
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
  const [sort, setSort] = React.useState<SortState>({ key: "sid", dir: 1 });

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

  // Экспорт правил файлом (чанк 52): POST /rules/export?format=...
  const exportRules = async (format: "text" | "stix" | "dataset") => {
    try {
      const base = import.meta.env.VITE_API_BASE ?? "/api/v1";
      const r = await fetch(`${base}/rules/export?format=${format}`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          ...(getToken() ? { Authorization: "Bearer " + getToken() } : {}),
        },
        body: JSON.stringify({ rule_filter: { status: status || "enabled" } }),
      });
      if (!r.ok) {
        const j = await r.json().catch(() => null);
        throw new Error("HTTP " + r.status + (j?.error?.message ? ": " + j.error.message : ""));
      }
      const blob = await r.blob();
      const a = document.createElement("a");
      a.href = URL.createObjectURL(blob);
      a.download = format === "text" ? "rules.rules" : format === "stix" ? "rules.stix.json" : "dataset.lst";
      a.click();
      URL.revokeObjectURL(a.href);
    } catch (e) { alert("Экспорт не удался: " + (e as Error).message); }
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
        {" "}
        <span className="muted">экспорт:</span>{" "}
        <button className="btn" onClick={() => exportRules("text")}>.rules</button>{" "}
        <button className="btn" onClick={() => exportRules("stix")}>stix</button>{" "}
        <button className="btn" onClick={() => exportRules("dataset")}>dataset</button>
      </div>
      <ErrorBox error={err} />
      {loaded && !items.length && !err && <p className="muted">Правил по фильтру нет.</p>}
      {items.length > 0 && (
        <table>
          <thead>
            <tr>
              <SortTh label="SID" k="sid" sort={sort} onSort={setSort} />
              <SortTh label="Сообщение" k="msg" sort={sort} onSort={setSort} />
              <SortTh label="Статус" k="status" sort={sort} onSort={setSort} />
              <SortTh label="Категория" k="category" sort={sort} onSort={setSort} />
              <SortTh label="Источник" k="source" sort={sort} onSort={setSort} />
              <th></th>
            </tr>
          </thead>
          <tbody>
            {sortBy(items, sort, (r, k) => {
              switch (k) {
                case "sid": return r.sid;
                case "msg": return r.msg;
                case "status": return r.status;
                case "category": return r.category;
                case "source": return r.source_type;
                default: return undefined;
              }
            }).map(r => (
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
