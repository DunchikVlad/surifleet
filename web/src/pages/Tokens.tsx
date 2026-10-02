import React from "react";
import { apiGet, apiPost, apiDelete, ApiToken, ApiTokenCreated, Page, PERMS } from "../api";
import { ErrorBox, fmtTime } from "../components";
import { useCan } from "../perms";

// Tokens — вкладка «Токены» (чанк 30): выпуск (значение один раз),
// листинг, отзыв.
export default function Tokens(_: { active: boolean }) {
  const can = useCan();
  const [items, setItems] = React.useState<ApiToken[]>([]);
  const [err, setErr] = React.useState<unknown>(null);
  const [name, setName] = React.useState("");
  const [expires, setExpires] = React.useState("");
  const [sel, setSel] = React.useState<Set<string>>(new Set(["fleet.read"]));
  const [created, setCreated] = React.useState<ApiTokenCreated | null>(null);
  const [busy, setBusy] = React.useState(false);

  const load = React.useCallback(async () => {
    try {
      const p = await apiGet<Page<ApiToken>>("/api_tokens?limit=100");
      setItems(p.items ?? []);
      setErr(null);
    } catch (e) { setErr(e); }
  }, []);
  React.useEffect(() => { load(); }, [load]);

  const flip = (p: string) => {
    const s = new Set(sel);
    if (s.has(p)) s.delete(p); else s.add(p);
    setSel(s);
  };

  const add = async () => {
    setBusy(true);
    try {
      const body: Record<string, unknown> = { name: name.trim(), scopes: [...sel] };
      if (expires) body.expires_at = new Date(expires).toISOString();
      const t = await apiPost<ApiTokenCreated>("/api_tokens", body);
      setCreated(t);
      setName(""); setExpires("");
      await load();
    } catch (e) { setErr(e); } finally { setBusy(false); }
  };

  const revoke = async (t: ApiToken) => {
    if (!confirm(`Отозвать токен ${t.name}? Он сразу перестанет работать.`)) return;
    try { await apiDelete(`/api_tokens/${t.id}`); await load(); } catch (e) { setErr(e); }
  };

  return (
    <>
      <h2>API-токены</h2>
      <ErrorBox error={err} />
      {can("tokens.write") && (
      <div className="panel">
        <p>
          <input placeholder="имя токена (напр. ci-deploy)" value={name} onChange={e => setName(e.target.value)} style={{ minWidth: "16em" }} />{" "}
          <input type="datetime-local" value={expires} onChange={e => setExpires(e.target.value)} title="истечение (необязательно)" />{" "}
          <button className="btn primary" disabled={busy || !name.trim() || sel.size === 0} onClick={add}>Выпустить</button>
        </p>
        <p>
          {PERMS.map(p => (
            <label key={p} className="muted" style={{ marginRight: "0.9em", whiteSpace: "nowrap" }}>
              <input type="checkbox" checked={sel.has(p)} onChange={() => flip(p)} /> {p}
            </label>
          ))}
        </p>
      </div>
      )}
      {created && (
        <div className="panel" style={{ borderColor: "var(--ok, #4a4)" }}>
          <p><b>Токен выпущен — скопируйте сейчас, больше не показывается:</b></p>
          <p><code style={{ userSelect: "all", wordBreak: "break-all" }}>{created.token}</code></p>
          <p className="muted">Использование: <code>curl -H "X-API-Key: &lt;token&gt;" …</code> · права = scopes токена.</p>
          <button className="btn" onClick={() => setCreated(null)}>скрыл</button>
        </div>
      )}
      <table>
        <thead>
          <tr><th>Имя</th><th>Scopes</th><th>Истекает</th><th>Использован</th><th></th></tr>
        </thead>
        <tbody>
          {items.map(t => (
            <tr key={t.id}>
              <td>{t.name}</td>
              <td className="muted">{t.scopes.join(" ")}</td>
              <td className="muted">{fmtTime(t.expires_at ?? undefined)}</td>
              <td className="muted">{fmtTime(t.last_used_at ?? undefined)}</td>
              <td>
                {can("tokens.write") &&
                  <button className="btn" onClick={() => revoke(t)}>отозвать</button>}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <p className="muted">Отозванные токены скрываются из списка, но остаются в аудите.</p>
    </>
  );
}
