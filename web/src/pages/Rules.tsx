import React from "react";
import { apiGet, apiPost, getToken, Page, Rule } from "../api";
import { Badge, ErrorBox, SortState, SortTh, sortBy } from "../components";
import { useCan } from "../perms";

const LIMIT = 50;

// ParsedRule — структурная логика из POST /rules/validate (чанк 72).
interface ParsedRule {
  action?: string;
  protocol?: string;
  src_addr?: string;
  src_port?: string;
  direction?: string;
  dst_addr?: string;
  dst_port?: string;
  sid?: number;
  rev?: number;
  msg?: string;
  classtype?: string;
  priority?: number;
  reference?: string[];
  metadata?: string[];
}

interface LineError { line: number; reason: string }

interface RuleRevisionItem {
  revision: number;
  raw: string;
  created_at?: string;
}

// RuleEditor — редактор содержимого правила (чанк 74, 1E п.1) вместо
// prompt'а «ред.»: raw из последней ревизии, «Проверить» (валидация
// парсером + показ логики), «Сохранить» (новая ревизия, чанк 73).
function RuleEditor({ rule, onClose, onSaved }: {
  rule: Rule;
  onClose: () => void;
  onSaved: (msg: string) => void;
}) {
  const [raw, setRaw] = React.useState("");
  const [loaded, setLoaded] = React.useState(false);
  const [check, setCheck] = React.useState<{ ok: boolean; rules: ParsedRule[]; errors: LineError[] } | null>(null);
  const [busy, setBusy] = React.useState(false);
  const [err, setErr] = React.useState<unknown>(null);
  // Проверка suricata -T через агента (чанк 75).
  const [instances, setInstances] = React.useState<{ id: string; name: string; hostname?: string }[]>([]);
  const [agentInstID, setAgentInstID] = React.useState("");
  const [agentCheck, setAgentCheck] = React.useState<{ ok: boolean; loaded_count?: number; error?: string } | null>(null);

  React.useEffect(() => {
    (async () => {
      try {
        const d = await apiGet<{ items?: RuleRevisionItem[] }>(`/rules/${rule.id}/revisions?limit=1`);
        setRaw(d.items?.[0]?.raw ?? "");
        setLoaded(true);
      } catch (e) { setErr(e); }
    })();
    apiGet<{ items?: { id: string; name: string; hostname?: string }[] }>("/instances?limit=100")
      .then(d => setInstances(d.items || []))
      .catch(() => {});
  }, [rule.id]);

  // validateOnAgent — suricata -T кандидата на выбранном инстансе (чанк 75).
  const validateOnAgent = async () => {
    if (!agentInstID) return;
    setBusy(true);
    try {
      const d = await apiPost<{ ok: boolean; loaded_count?: number; error?: string }>(
        "/rules/validate_agent", { raw, instance_id: agentInstID });
      setAgentCheck(d);
    } catch (e) { setErr(e); } finally { setBusy(false); }
  };

  const validate = async () => {
    setBusy(true);
    try {
      const d = await apiPost<{ ok: boolean; rules: ParsedRule[]; errors: LineError[] }>(
        "/rules/validate", { rules: raw });
      setCheck(d);
    } catch (e) { setErr(e); } finally { setBusy(false); }
  };

  const save = async () => {
    setBusy(true);
    try {
      const d = await apiPost<{ revision: number; state: string }>(`/rules/${rule.id}/revisions`, { raw });
      onSaved(`сохранена ревизия ${d.revision} (${d.state}) правила sid ${rule.sid}`);
    } catch (e) { setErr(e); } finally { setBusy(false); }
  };

  const p = check?.rules?.[0];

  return (
    <div className="panel">
      <h3>Редактор правила sid {rule.sid}</h3>
      <ErrorBox error={err} />
      {!loaded && !err && <p className="muted">загрузка текущей ревизии…</p>}
      <textarea value={raw} onChange={e => { setRaw(e.target.value); setCheck(null); }}
        placeholder={'alert http $HOME_NET any -> $EXTERNAL_NET any (msg:"…"; sid:…; rev:…)'}
        style={{ width: "100%", minHeight: "6em", fontFamily: "monospace" }} />
      <p>
        <button className="btn" disabled={busy || !raw.trim()} onClick={validate}>Проверить</button>{" "}
        <button className="btn primary" disabled={busy || !raw.trim()} onClick={save}>Сохранить ревизию</button>{" "}
        <button className="btn" onClick={onClose}>Закрыть</button>
      </p>
      {instances.length > 0 && (
        <p className="muted">
          suricata -T на агенте:{" "}
          <select value={agentInstID} onChange={e => { setAgentInstID(e.target.value); setAgentCheck(null); }}>
            <option value="">— инстанс —</option>
            {instances.map(i => (
              <option key={i.id} value={i.id}>{i.hostname ? i.hostname + " · " : ""}{i.name}</option>
            ))}
          </select>{" "}
          <button className="btn" disabled={busy || !raw.trim() || !agentInstID} onClick={validateOnAgent}>
            Проверить на агенте
          </button>
          {agentCheck && (
            <span>
              {" "}→ {agentCheck.ok
                ? `✓ конфигурация загружена (правил в кандидате: ${agentCheck.loaded_count ?? 0})`
                : `✗ ${agentCheck.error || "валидация не пройдена"}`}
            </span>
          )}
        </p>
      )}
      {check && (
        <>
          {check.errors.length > 0 && (
            <p className="muted">ошибки: {check.errors.map(e => `строка ${e.line}: ${e.reason}`).join("; ")}</p>
          )}
          {p && (
            <table>
              <tbody>
                <tr><td className="muted">логика</td>
                  <td><b>{p.action}</b> {p.protocol} {p.src_addr}:{p.src_port} {p.direction} {p.dst_addr}:{p.dst_port}</td></tr>
                <tr><td className="muted">msg</td><td>{p.msg || "—"}</td></tr>
                <tr><td className="muted">classtype / priority</td>
                  <td>{p.classtype || "—"}{p.priority ? ` / prio ${p.priority}` : ""}</td></tr>
                <tr><td className="muted">rev</td><td>{p.rev ?? 1}</td></tr>
                {(p.reference?.length ?? 0) > 0 && (
                  <tr><td className="muted">reference</td><td>{p.reference!.join("; ")}</td></tr>
                )}
                {(p.metadata?.length ?? 0) > 0 && (
                  <tr><td className="muted">metadata</td><td>{p.metadata!.join("; ")}</td></tr>
                )}
              </tbody>
            </table>
          )}
          {check.ok && <p className="muted">✓ разбор чистый (suricata -T через агента — отдельный шаг)</p>}
        </>
      )}
    </div>
  );
}

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

  // Редактор содержимого (чанк 74) — открывается кнопкой «ред.».
  const [editing, setEditing] = React.useState<Rule | null>(null);
  const [editorMsg, setEditorMsg] = React.useState("");

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
      {editorMsg && <p className="muted">{editorMsg}</p>}
      {editing && (
        <RuleEditor
          rule={editing}
          onClose={() => setEditing(null)}
          onSaved={(m) => { setEditorMsg(m); setEditing(null); setLoaded(false); }}
        />
      )}
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
                      {" "}<button className="btn" title="редактор правила (raw + проверка + ревизия)" onClick={() => { setEditorMsg(""); setEditing(r); }}>ред.</button>
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
