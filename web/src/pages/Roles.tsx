import React from "react";
import { apiGet, apiPost, apiDelete, Role, Page, PERMS } from "../api";
import { ErrorBox } from "../components";

// Roles — вкладка «Роли» (чанк 30): встроенные (неизменяемы) и кастомные.
export default function Roles(_: { active: boolean }) {
  const [items, setItems] = React.useState<Role[]>([]);
  const [err, setErr] = React.useState<unknown>(null);
  const [name, setName] = React.useState("");
  const [desc, setDesc] = React.useState("");
  const [sel, setSel] = React.useState<Set<string>>(new Set());
  const [busy, setBusy] = React.useState(false);

  const load = React.useCallback(async () => {
    try {
      const p = await apiGet<Page<Role>>("/roles");
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
      await apiPost("/roles", { name: name.trim(), description: desc.trim() || undefined, permissions: [...sel] });
      setName(""); setDesc(""); setSel(new Set());
      await load();
    } catch (e) { setErr(e); } finally { setBusy(false); }
  };

  const remove = async (r: Role) => {
    if (!confirm(`Удалить кастомную роль ${r.name}?`)) return;
    try { await apiDelete(`/roles/${r.id}`); await load(); } catch (e) { setErr(e); }
  };

  return (
    <>
      <h2>Роли</h2>
      <ErrorBox error={err} />
      <div className="panel">
        <p>
          <input placeholder="имя кастомной роли" value={name} onChange={e => setName(e.target.value)} />{" "}
          <input placeholder="описание (необязательно)" value={desc} onChange={e => setDesc(e.target.value)} style={{ minWidth: "18em" }} />{" "}
          <button className="btn primary" disabled={busy || !name.trim() || sel.size === 0} onClick={add}>Создать</button>
        </p>
        <p>
          {PERMS.map(p => (
            <label key={p} className="muted" style={{ marginRight: "0.9em", whiteSpace: "nowrap" }}>
              <input type="checkbox" checked={sel.has(p)} onChange={() => flip(p)} /> {p}
            </label>
          ))}
        </p>
      </div>
      <table>
        <thead>
          <tr><th>Имя</th><th>Тип</th><th>Разрешения</th><th></th></tr>
        </thead>
        <tbody>
          {items.map(r => (
            <tr key={r.id}>
              <td>{r.name}{r.description && <div className="muted" style={{ fontSize: "0.85em" }}>{r.description}</div>}</td>
              <td>{r.is_builtin ? <span className="badge badge-muted">builtin</span> : <span className="badge badge-ok">custom</span>}</td>
              <td className="muted">{r.permissions.join(" ")}</td>
              <td>{!r.is_builtin && <button className="btn" onClick={() => remove(r)}>удалить</button>}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <p className="muted">Встроенные роли (admin/operator/analyst/viewer) неизменяемы и неудаляемы.</p>
    </>
  );
}
