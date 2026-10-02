import React from "react";
import { apiDelete, apiGet, apiPost, Ioc, IocGenerateResult, Page } from "../api";
import { Badge, ErrorBox, fmtTime } from "../components";
import { useCan } from "../perms";

const LIMIT = 50;
const TYPES = ["ip", "domain", "url", "md5", "sha1", "sha256", "email"];
const STATUSES = ["active", "under_review", "expired", "revoked"];

// Ссылка на внешний lookup (ТЗ 5.2: VirusTotal/ipinfo).
function extLink(i: Ioc): { href: string; label: string } | null {
  if (i.type === "ip") return { href: `https://www.virustotal.com/gui/ip-address/${i.value}`, label: "VT" };
  if (i.type === "domain") return { href: `https://www.virustotal.com/gui/domain/${i.value}`, label: "VT" };
  if (["md5", "sha1", "sha256"].includes(i.type)) return { href: `https://www.virustotal.com/gui/file/${i.value}`, label: "VT" };
  if (i.type === "url") return { href: `https://www.virustotal.com/gui/url/${btoa(i.value).replace(/=+$/, "")}`, label: "VT" };
  return null;
}

export default function Iocs({ active }: { active: boolean }) {
  const can = useCan();
  const [items, setItems] = React.useState<Ioc[]>([]);
  const [cursor, setCursor] = React.useState<string | null>(null);
  const [type, setType] = React.useState("");
  const [status, setStatus] = React.useState("");
  const [q, setQ] = React.useState("");
  const [err, setErr] = React.useState<unknown>(null);
  const [loaded, setLoaded] = React.useState(false);

  // Форма добавления.
  const [fType, setFType] = React.useState("ip");
  const [fValue, setFValue] = React.useState("");
  const [fScore, setFScore] = React.useState("80");
  const [fSource, setFSource] = React.useState("manual");
  const [fExpires, setFExpires] = React.useState(""); // datetime-local
  const [busy, setBusy] = React.useState(false);

  // Генерация Suricata-правил из IOC (POST /iocs/generate).
  const [genBusy, setGenBusy] = React.useState(false);
  const [genResult, setGenResult] = React.useState<IocGenerateResult | null>(null);
  const [genErr, setGenErr] = React.useState<unknown>(null);

  const generate = async () => {
    setGenBusy(true);
    setGenErr(null);
    try {
      const r = await apiPost<IocGenerateResult>("/iocs/generate", {});
      setGenResult(r);
      if (r.swept_expired > 0) setLoaded(false); // статусы могли измениться
    } catch (e) { setGenErr(e); }
    setGenBusy(false);
  };

  const load = React.useCallback(async (append: boolean) => {
    let path = `/iocs?limit=${LIMIT}`;
    if (type) path += `&type=${encodeURIComponent(type)}`;
    if (status) path += `&status=${encodeURIComponent(status)}`;
    if (q.trim()) path += `&q=${encodeURIComponent(q.trim())}`;
    if (append && cursor) path += `&cursor=${encodeURIComponent(cursor)}`;
    try {
      const d = await apiGet<Page<Ioc>>(path);
      setItems(prev => (append ? [...prev, ...(d.items || [])] : d.items || []));
      setCursor(d.next_cursor ?? null);
      setErr(null);
      setLoaded(true);
    } catch (e) { setErr(e); }
  }, [type, status, q, cursor]);

  React.useEffect(() => {
    if (active && !loaded) load(false);
  }, [active, loaded, load]);

  const add = async () => {
    if (!fValue.trim()) return;
    setBusy(true);
    try {
      const body: Record<string, unknown> = {
        type: fType,
        value: fValue.trim(),
        score: parseInt(fScore, 10) || 0,
      };
      if (fSource.trim()) body.source = fSource.trim();
      if (fExpires) body.expires_at = new Date(fExpires).toISOString();
      const ioc = await apiPost<Ioc>("/iocs", body);
      setItems(prev => [ioc, ...prev]);
      setFValue("");
    } catch (e) { alert("IOC не создан: " + (e as Error).message); }
    setBusy(false);
  };

  const remove = async (id: string) => {
    try {
      await apiDelete(`/iocs/${id}`);
      setItems(prev => prev.filter(i => i.id !== id));
    } catch (e) { alert("Удаление не выполнено: " + (e as Error).message); }
  };

  return (
    <>
      <h2>IOC / Threat Intel</h2>
      {can("ioc.write") && (
      <div className="toolbar">
        <select value={fType} onChange={e => setFType(e.target.value)}>
          {TYPES.map(t => <option key={t} value={t}>{t}</option>)}
        </select>
        <input
          placeholder="значение IOC…"
          value={fValue}
          onChange={e => setFValue(e.target.value)}
          onKeyDown={e => { if (e.key === "Enter") add(); }}
          style={{ minWidth: "22em" }}
        />
        <input
          type="number" min={0} max={100} title="уверенность (score 0..100)"
          value={fScore}
          onChange={e => setFScore(e.target.value)}
          style={{ width: "6em" }}
        />
        <input
          placeholder="источник"
          value={fSource}
          onChange={e => setFSource(e.target.value)}
          style={{ width: "9em" }}
        />
        <input
          type="datetime-local" title="истекает (необязательно)"
          value={fExpires}
          onChange={e => setFExpires(e.target.value)}
        />
        <button className="btn" disabled={busy} onClick={add}>Добавить</button>
      </div>
      )}
      <div className="toolbar">
        <select value={type} onChange={e => { setType(e.target.value); setLoaded(false); }}>
          <option value="">все типы</option>
          {TYPES.map(t => <option key={t} value={t}>{t}</option>)}
        </select>
        <select value={status} onChange={e => { setStatus(e.target.value); setLoaded(false); }}>
          <option value="">все статусы</option>
          {STATUSES.map(s => <option key={s} value={s}>{s}</option>)}
        </select>
        <input
          placeholder="поиск по значению…"
          value={q}
          onChange={e => setQ(e.target.value)}
          onKeyDown={e => { if (e.key === "Enter") setLoaded(false); }}
        />
        <button className="btn" onClick={() => setLoaded(false)}>Найти</button>
        <span style={{ flex: 1 }} />
        {can("ioc.write") && (
        <button className="btn primary" disabled={genBusy} onClick={generate}
          title="Сгенерировать Suricata-правила из активных IOC (sid 8800000+, ruleset ioc-current)">
          {genBusy ? "Генерация…" : "Сгенерировать правила"}
        </button>
        )}
      </div>
      {genErr && <ErrorBox error={genErr} />}
      {genResult && (
        <div className="card">
          <b>Генерация правил из IOC:</b>{" "}
          активных {genResult.active}, погашено просроченных {genResult.swept_expired},{" "}
          создано {genResult.created}, обновлено {genResult.updated}, без изменений {genResult.unchanged}
          {genResult.skipped.length > 0 && <> , пропущено {genResult.skipped.length}
            {" "}({genResult.skipped.map(s => `${s.type}:${s.value} — ${s.reason}`).join("; ")})
          </>}
          {genResult.ruleset_id && (
            <> — ruleset <b>{genResult.ruleset_version}</b> ({genResult.rules_count} правил,{" "}
              {genResult.ruleset_created ? "новая версия" : "состав не изменился"},{" "}
              id <code>{genResult.ruleset_id}</code>)
            </>
          )}
          {!genResult.ruleset_id && <> — правил нет, ruleset не собран</>}
          {genResult.deployment_id && <> — деплой <code>{genResult.deployment_id}</code></>}
        </div>
      )}
      <ErrorBox error={err} />
      {loaded && !items.length && !err && <p className="muted">IOC по фильтру нет.</p>}
      {items.length > 0 && (
        <table>
          <thead>
            <tr><th>Тип</th><th>Значение</th><th>Score</th><th>Статус</th><th>Источник</th><th>Истекает</th><th>Создан</th><th></th></tr>
          </thead>
          <tbody>
            {items.map(i => {
              const ext = extLink(i);
              return (
                <tr key={i.id}>
                  <td>{i.type}</td>
                  <td>
                    {i.value}{" "}
                    {ext && <a href={ext.href} target="_blank" rel="noreferrer" title="VirusTotal">{ext.label}</a>}
                  </td>
                  <td>{i.score}</td>
                  <td><Badge status={i.status} /></td>
                  <td className="muted">{i.source || "—"}</td>
                  <td className="muted">{i.expires_at ? fmtTime(i.expires_at) : "—"}</td>
                  <td className="muted">{fmtTime(i.created_at)}</td>
                  <td>
                    {can("ioc.write") &&
                      <button className="btn" onClick={() => remove(i.id)}>удалить</button>}
                  </td>
                </tr>
              );
            })}
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
