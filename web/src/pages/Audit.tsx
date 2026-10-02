import React from "react";
import { apiGet, AuditEntry, Page } from "../api";
import { ErrorBox, fmtTime, short } from "../components";

const LIMIT = 50;

// Audit — вкладка «Аудит» (чанк 30): журнал действий, свежие первыми,
// фильтр по префиксу action, дозагрузка.
export default function Audit(_: { active: boolean }) {
  const [items, setItems] = React.useState<AuditEntry[]>([]);
  const [cursor, setCursor] = React.useState<string | null>(null);
  const [action, setAction] = React.useState("");
  const [err, setErr] = React.useState<unknown>(null);

  const load = React.useCallback(async (more = false) => {
    try {
      let path = `/audit_log?limit=${LIMIT}`;
      if (action.trim()) path += "&action=" + encodeURIComponent(action.trim());
      if (more && cursor) path += "&cursor=" + encodeURIComponent(cursor);
      const p = await apiGet<Page<AuditEntry>>(path);
      setItems(prev => (more ? [...prev, ...(p.items ?? [])] : (p.items ?? [])));
      setCursor(p.next_cursor ?? null);
      setErr(null);
    } catch (e) { setErr(e); }
  }, [action, cursor]);

  React.useEffect(() => { load(); }, [load]);

  return (
    <>
      <h2>Аудит</h2>
      <ErrorBox error={err} />
      <p>
        <input
          placeholder="фильтр action (напр. auth., users., tokens.)"
          value={action}
          onChange={e => setAction(e.target.value)}
          style={{ minWidth: "22em" }}
        />{" "}
        <span className="muted">append-only; запись недоступна для удаления через API</span>
      </p>
      <table>
        <thead>
          <tr><th>Время</th><th>Актор</th><th>Действие</th><th>Объект</th><th>Результат</th><th>IP</th></tr>
        </thead>
        <tbody>
          {items.map(e => (
            <tr key={e.id}>
              <td className="muted">{fmtTime(e.created_at)}</td>
              <td>{e.actor_name ?? "—"} <span className="muted">({e.actor_type})</span></td>
              <td>{e.action}</td>
              <td className="muted">{e.object_type ? `${e.object_type}:${short(e.object_id)}` : "—"}</td>
              <td>
                <span className={`badge badge-${e.result === "success" ? "ok" : "err"}`}>{e.result}</span>
                {e.reason && <div className="muted" style={{ fontSize: "0.85em" }}>{e.reason}</div>}
              </td>
              <td className="muted">{e.ip ?? "—"}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {cursor && (
        <p><button className="btn" onClick={() => load(true)}>Ещё {LIMIT}…</button>{" "}
          <span className="muted">показано {items.length}</span></p>
      )}
    </>
  );
}
