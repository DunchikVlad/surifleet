import React from "react";
import { apiGet, apiPost, apiPatch, apiDelete, User, Role, Page } from "../api";
import { ErrorBox, fmtTime, short } from "../components";

// Users — вкладка «Пользователи» (чанк 30): список, создание с ролью,
// вкл/откл, revoke sessions, удаление.
export default function Users(_: { active: boolean }) {
  const [items, setItems] = React.useState<User[]>([]);
  const [roles, setRoles] = React.useState<Role[]>([]);
  const [err, setErr] = React.useState<unknown>(null);
  const [q, setQ] = React.useState("");
  // форма создания
  const [email, setEmail] = React.useState("");
  const [name, setName] = React.useState("");
  const [password, setPassword] = React.useState("");
  const [roleID, setRoleID] = React.useState("");
  const [breakGlass, setBreakGlass] = React.useState(false);
  const [busy, setBusy] = React.useState(false);

  const load = React.useCallback(async () => {
    try {
      const u = await apiGet<Page<User>>("/users?limit=100" + (q.trim() ? "&q=" + encodeURIComponent(q.trim()) : ""));
      setItems(u.items ?? []);
      setErr(null);
    } catch (e) { setErr(e); }
  }, [q]);

  React.useEffect(() => { load(); }, [load]);
  React.useEffect(() => {
    apiGet<Page<Role>>("/roles").then(p => setRoles(p.items ?? [])).catch(() => {});
  }, []);

  const add = async () => {
    setBusy(true);
    try {
      const body: Record<string, unknown> = {
        email: email.trim(), display_name: name.trim(), password,
        is_break_glass: breakGlass,
        roles: roleID ? [{ role_id: roleID }] : [],
      };
      await apiPost("/users", body);
      setEmail(""); setName(""); setPassword(""); setRoleID(""); setBreakGlass(false);
      await load();
    } catch (e) { setErr(e); } finally { setBusy(false); }
  };

  const toggle = async (u: User) => {
    try { await apiPatch(`/users/${u.id}`, { is_active: !u.is_active }); await load(); }
    catch (e) { setErr(e); }
  };
  const revoke = async (u: User) => {
    try { await apiPost(`/users/${u.id}/revoke_sessions`); } catch (e) { setErr(e); }
  };
  const remove = async (u: User) => {
    if (!confirm(`Удалить пользователя ${u.email}?`)) return;
    try { await apiDelete(`/users/${u.id}`); await load(); } catch (e) { setErr(e); }
  };

  return (
    <>
      <h2>Пользователи</h2>
      <ErrorBox error={err} />
      <div className="panel">
        <input placeholder="email" value={email} onChange={e => setEmail(e.target.value)} />{" "}
        <input placeholder="имя" value={name} onChange={e => setName(e.target.value)} />{" "}
        <input placeholder="пароль (≥8)" type="password" value={password} onChange={e => setPassword(e.target.value)} />{" "}
        <select value={roleID} onChange={e => setRoleID(e.target.value)} title="роль (можно назначить позже)">
          <option value="">— без роли —</option>
          {roles.map(r => <option key={r.id} value={r.id}>{r.name}</option>)}
        </select>{" "}
        <label className="muted" title="локальный администратор вне SSO (break-glass)">
          <input type="checkbox" checked={breakGlass} onChange={e => setBreakGlass(e.target.checked)} /> break-glass
        </label>{" "}
        <button className="btn primary" disabled={busy || !email.trim() || !name.trim() || password.length < 8} onClick={add}>
          Создать
        </button>
      </div>
      <p>
        <input placeholder="поиск по email/имени…" value={q} onChange={e => setQ(e.target.value)} style={{ minWidth: "18em" }} />
      </p>
      <table>
        <thead>
          <tr><th>Email</th><th>Имя</th><th>Роли</th><th>Вход</th><th>Статус</th><th></th></tr>
        </thead>
        <tbody>
          {items.map(u => (
            <tr key={u.id}>
              <td>{u.email}{u.is_break_glass && <> <span className="badge badge-warn">break-glass</span></>}</td>
              <td>{u.display_name}</td>
              <td className="muted">{(u.roles ?? []).map(r => r.role_name || short(r.role_id)).join(", ") || "—"}</td>
              <td className="muted">{fmtTime(u.last_login_at ?? undefined)}</td>
              <td>
                <button className={"btn" + (u.is_active ? "" : " primary")} onClick={() => toggle(u)}>
                  {u.is_active ? "active" : "off"}
                </button>
              </td>
              <td style={{ whiteSpace: "nowrap" }}>
                <button className="btn" title="принудительный logout всех сессий" onClick={() => revoke(u)}>сессии ✕</button>{" "}
                <button className="btn" onClick={() => remove(u)}>удалить</button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <p className="muted">
        Роли — на вкладке «Роли»; последний break-glass администратор неудаляем (409).
      </p>
    </>
  );
}
