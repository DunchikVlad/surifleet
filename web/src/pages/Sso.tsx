import React from "react";
import {
  apiGet, apiPost, apiPatch, apiDelete,
  Page, SsoProvider, Role,
} from "../api";
import { ErrorBox } from "../components";
import { useCan } from "../perms";

// Sso — вкладка «SSO» (чанк 35): OIDC-провайдеры — список, создание,
// вкл/откл, удаление; маппинг групп IdP → роли системы (JSON).
export default function Sso(_: { active: boolean }) {
  const can = useCan();
  const [items, setItems] = React.useState<SsoProvider[]>([]);
  const [roles, setRoles] = React.useState<Role[]>([]);
  const [err, setErr] = React.useState<unknown>(null);
  // форма создания
  const [name, setName] = React.useState("");
  const [issuer, setIssuer] = React.useState("");
  const [clientID, setClientID] = React.useState("");
  const [secret, setSecret] = React.useState("");
  const [redirect, setRedirect] = React.useState("");
  const [scopes, setScopes] = React.useState("");
  const [mapping, setMapping] = React.useState("{}");
  const [busy, setBusy] = React.useState(false);

  const load = React.useCallback(async () => {
    try {
      const p = await apiGet<Page<SsoProvider>>("/sso_providers?limit=100");
      setItems(p.items ?? []);
      setErr(null);
    } catch (e) { setErr(e); }
  }, []);

  React.useEffect(() => { load(); }, [load]);
  React.useEffect(() => {
    apiGet<Page<Role>>("/roles").then(p => setRoles(p.items ?? [])).catch(() => {});
  }, []);

  const add = async () => {
    let grm: Record<string, string[]>;
    try {
      grm = mapping.trim() ? JSON.parse(mapping) : {};
    } catch {
      setErr(new Error("маппинг групп — некорректный JSON"));
      return;
    }
    setBusy(true);
    try {
      await apiPost("/sso_providers", {
        name: name.trim(),
        type: "oidc",
        config: {
          issuer_url: issuer.trim(),
          client_id: clientID.trim(),
          client_secret: secret,
          redirect_url: redirect.trim(),
          scopes: scopes.trim() ? scopes.trim().split(/\s+/) : undefined,
        },
        group_role_mapping: grm,
        enabled: true,
      });
      setName(""); setIssuer(""); setClientID(""); setSecret(""); setRedirect(""); setScopes(""); setMapping("{}");
      await load();
    } catch (e) { setErr(e); } finally { setBusy(false); }
  };

  const toggle = async (p: SsoProvider) => {
    try { await apiPatch(`/sso_providers/${p.id}`, { enabled: !p.enabled }); await load(); }
    catch (e) { setErr(e); }
  };
  const remove = async (p: SsoProvider) => {
    if (!confirm(`Удалить SSO-провайдер «${p.name}»? JIT-пользователи сохранятся, но войти через SSO не смогут.`)) return;
    try { await apiDelete(`/sso_providers/${p.id}`); await load(); } catch (e) { setErr(e); }
  };

  return (
    <>
      <h2>SSO-провайдеры (OIDC)</h2>
      <ErrorBox error={err} />
      {can("sso.write") && (
        <div className="panel">
          <div><input placeholder="название (напр. Keycloak)" value={name} onChange={e => setName(e.target.value)} style={{ minWidth: "16em" }} /></div>
          <div><input placeholder="issuer_url (https://idp/.../realm)" value={issuer} onChange={e => setIssuer(e.target.value)} style={{ minWidth: "34em" }} /></div>
          <div>
            <input placeholder="client_id" value={clientID} onChange={e => setClientID(e.target.value)} style={{ minWidth: "16em" }} />{" "}
            <input placeholder="client_secret" type="password" value={secret} onChange={e => setSecret(e.target.value)} style={{ minWidth: "16em" }} />
          </div>
          <div><input placeholder="redirect_url (https://<сервер>/api/v1/auth/sso/callback)" value={redirect} onChange={e => setRedirect(e.target.value)} style={{ minWidth: "34em" }} /></div>
          <div><input placeholder="scopes (через пробел; пусто = openid email profile)" value={scopes} onChange={e => setScopes(e.target.value)} style={{ minWidth: "34em" }} /></div>
          <div>
            <textarea
              placeholder='маппинг групп → роли: {"sec-admins": ["<role_uuid>"]}'
              value={mapping}
              onChange={e => setMapping(e.target.value)}
              rows={3}
              style={{ minWidth: "34em", fontFamily: "monospace" }}
              title={"Доступные role_id:\n" + roles.map(r => `${r.name} = ${r.id}`).join("\n")}
            />
          </div>
          <button className="btn primary" disabled={busy || !name.trim() || !issuer.trim() || !clientID.trim() || !redirect.trim()} onClick={add}>
            Создать провайдер
          </button>
        </div>
      )}
      <table>
        <thead>
          <tr><th>Название</th><th>Тип</th><th>Issuer</th><th>Client ID</th><th>Маппинг</th><th>Статус</th><th></th></tr>
        </thead>
        <tbody>
          {items.map(p => (
            <tr key={p.id}>
              <td>{p.name}</td>
              <td className="muted">{p.type}</td>
              <td className="muted" title={p.config?.issuer_url}>{(p.config?.issuer_url ?? "").slice(0, 40)}</td>
              <td className="muted">{p.config?.client_id}</td>
              <td className="muted" title={JSON.stringify(p.group_role_mapping ?? {})}>
                {Object.keys(p.group_role_mapping ?? {}).length} групп
              </td>
              <td>
                {can("sso.write")
                  ? <button className={"btn" + (p.enabled ? "" : " primary")} onClick={() => toggle(p)}>{p.enabled ? "on" : "off"}</button>
                  : (p.enabled ? "on" : "off")}
              </td>
              <td>
                {can("sso.write") && <button className="btn" onClick={() => remove(p)}>удалить</button>}
              </td>
            </tr>
          ))}
          {items.length === 0 && <tr><td colSpan={7} className="muted">провайдеров нет — вход только локальный (или break-glass)</td></tr>}
        </tbody>
      </table>
      <p className="muted">
        Redirect URI, который нужно зарегистрировать в IdP: <code>/api/v1/auth/sso/callback</code> (полный URL — как redirect_url провайдера).
        client_secret в ответах не отдаётся (writeOnly). Вход — кнопка «Войти через SSO» на форме входа.
      </p>
      <details className="muted">
        <summary>role_id для маппинга групп</summary>
        <ul>
          {roles.map(r => <li key={r.id}><code>{r.name}</code> = <code>{r.id}</code></li>)}
        </ul>
      </details>
    </>
  );
}
