import React from "react";
import { apiGet, apiPost, getToken, Page, Rule } from "../api";
import { Badge, ErrorBox, fmtTime, SortState, SortTh, sortBy } from "../components";
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

// SourceItem — источник suricata-update (чанк 82).
interface SourceItem { name: string; enabled: boolean; summary?: string }

// UpdateStatus — ответ GET /instances/{id}/suricata_update/status (чанк 112).
interface UpdateStatus {
  available: boolean;
  running?: boolean;
  pid?: number;
  last_result?: string;
  last_run_at?: string;
  last_error?: string;
  index_version?: string;
  index_updated_at?: string;
  suricata_state?: string;
  suricata_pid?: number;
}

// UpdateStatusLine — живой статус suricata-update на сенсоре (чанк 112):
// идёт ли обновление и PID процесса, итог/время последнего запуска,
// версия индекса источников, PID движка Suricata. Опрос 15 с, пока панель
// открыта и инстанс выбран.
function UpdateStatusLine({ instID }: { instID: string }) {
  const [st, setSt] = React.useState<UpdateStatus | null>(null);
  const [err, setErr] = React.useState<unknown>(null);

  React.useEffect(() => {
    if (!instID) { setSt(null); return; }
    let dead = false;
    const load = () => apiGet<UpdateStatus>(`/instances/${instID}/suricata_update/status`)
      .then(d => { if (!dead) { setSt(d); setErr(null); } })
      .catch(e => { if (!dead) setErr(e); });
    load();
    const t = setInterval(load, 15000);
    return () => { dead = true; clearInterval(t); };
  }, [instID]);

  if (!instID) return null;
  if (err) return <p className="muted">Статус suricata-update: недоступен ({String(err)})</p>;
  if (!st) return <p className="muted">Статус suricata-update: загрузка…</p>;
  if (!st.available) {
    return <p className="muted">Статус suricata-update: нет данных — агент offline или обновите агента (чанк 112).</p>;
  }
  const parts: string[] = [];
  parts.push(st.running ? `обновление ИДЁТ (pid ${st.pid})` : "обновление не идёт");
  if (st.last_result) {
    parts.push(`последний прогон: ${st.last_result}${st.last_run_at ? " · " + fmtTime(st.last_run_at) : ""}`);
  } else {
    parts.push("прогонов агента ещё не было");
  }
  if (st.last_error) parts.push(`ошибка: ${st.last_error}`);
  if (st.index_version) {
    parts.push(`индекс источников: v${st.index_version}${st.index_updated_at ? " · " + fmtTime(st.index_updated_at) : ""}`);
  }
  if (st.suricata_pid) {
    parts.push(`движок Suricata: ${st.suricata_state || "?"} (pid ${st.suricata_pid})`);
  }
  return (
    <p className="muted">
      Статус suricata-update: <b>{st.running ? "работает" : "ожидание"}</b> — {parts.join("; ")}
    </p>
  );
}

// UpdateConfigFetch — ответ GET /instances/{id}/update_config (чанк 113).
interface UpdateConfigFetch {
  file: string;
  source_name?: string;
  path: string;
  exists: boolean;
  content: string;
  sha256?: string;
  size_bytes?: number;
}

// UpdateConfigDeploy — ответ POST /instances/{id}/update_config.
interface UpdateConfigDeploy {
  path: string;
  backup?: string;
  sha256: string;
  unchanged: boolean;
}

// UpdateConfigHistoryItem — запись GET .../update_config/history.
interface UpdateConfigHistoryItem {
  id: string;
  file: string;
  path: string;
  sha256: string;
  unchanged: boolean;
  status: string;
  backup?: string | null;
  error?: string | null;
  created_by?: string | null;
  created_at: string;
}

const UPDATE_CONFIG_FILES = [
  { v: "update_yaml", label: "update.yaml" },
  { v: "enable_conf", label: "enable.conf" },
  { v: "disable_conf", label: "disable.conf" },
  { v: "modify_conf", label: "modify.conf" },
  { v: "source_yaml", label: "update/sources/<name>.yaml" },
];

// UpdateConfigEditor — редактор конфигов suricata-update «как на хосте»
// (чанк 113, backlog заказчика): fetch с сенсора → правка → deploy с
// бэкапом; оптимистичная конкурентность через expected_sha256. Права:
// config.read (fetch/история) / config.write (сохранение); на агенте
// gated capability config.
function UpdateConfigEditor({ instID }: { instID: string }) {
  const can = useCan();
  const [file, setFile] = React.useState("update_yaml");
  const [srcName, setSrcName] = React.useState("");
  const [content, setContent] = React.useState<string | null>(null);
  const [sha, setSha] = React.useState("");
  const [exists, setExists] = React.useState(false);
  const [path, setPath] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [msg, setMsg] = React.useState("");
  const [err, setErr] = React.useState<unknown>(null);
  const [hist, setHist] = React.useState<UpdateConfigHistoryItem[] | null>(null);

  const qs = () => `file=${file}` + (file === "source_yaml" ? `&source_name=${encodeURIComponent(srcName.trim())}` : "");

  const load = async () => {
    if (!instID) return;
    if (file === "source_yaml" && !srcName.trim()) { setErr({ message: "укажите имя источника" }); return; }
    setBusy(true); setErr(null); setMsg(""); setHist(null);
    try {
      const d = await apiGet<UpdateConfigFetch>(`/instances/${instID}/update_config?${qs()}`);
      setPath(d.path);
      setExists(d.exists);
      setContent(d.exists ? d.content : "");
      setSha(d.exists ? d.sha256 || "" : "");
      setMsg(d.exists ? `${d.path} — ${(d.size_bytes || 0)} байт` : `${d.path} — файла нет (дефолты suricata-update), сохранение создаст его`);
    } catch (e) { setErr(e); setContent(null); } finally { setBusy(false); }
  };

  const save = async () => {
    if (!instID || content === null) return;
    if (!window.confirm(`Записать ${path || file} на сенсор? Текущее содержимое уйдёт в бэкап (.surifleet-bak-TS).`)) return;
    setBusy(true); setErr(null); setMsg("");
    try {
      const d = await apiPost<UpdateConfigDeploy>(`/instances/${instID}/update_config`, {
        file, source_name: file === "source_yaml" ? srcName.trim() : undefined,
        content, expected_sha256: sha || undefined,
      });
      setMsg(d.unchanged ? "содержимое совпало — запись не потребовалась" :
        `записано: ${d.path}${d.backup ? ` (бэкап ${d.backup})` : ""}, sha ${d.sha256.slice(0, 12)}…`);
      setSha(d.sha256);
      setExists(true);
    } catch (e) { setErr(e); } finally { setBusy(false); }
  };

  const loadHistory = async () => {
    if (!instID) return;
    try {
      const d = await apiGet<{ items?: UpdateConfigHistoryItem[] }>(`/instances/${instID}/update_config/history`);
      setHist(d.items || []);
    } catch (e) { setErr(e); }
  };

  return (
    <div className="muted" style={{ marginTop: 8 }}>
      <b>Конфиги suricata-update:</b>{" "}
      <select value={file} onChange={e => { setFile(e.target.value); setContent(null); setMsg(""); }}
        disabled={busy} style={{ marginRight: 6 }}>
        {UPDATE_CONFIG_FILES.map(f => <option key={f.v} value={f.v}>{f.label}</option>)}
      </select>
      {file === "source_yaml" && (
        <input value={srcName} onChange={e => { setSrcName(e.target.value); setContent(null); }}
          placeholder="имя источника" disabled={busy} style={{ width: 140, marginRight: 6 }} />
      )}
      <button className="btn" disabled={busy || !instID} onClick={load} style={{ marginRight: 6 }}>
        {busy ? "…" : "Загрузить с сенсора"}
      </button>
      {content !== null && can("config.write") && (
        <button className="btn primary" disabled={busy} onClick={save}>Сохранить на сенсоре</button>
      )}{" "}
      <button className="btn" disabled={!instID} onClick={loadHistory}>История</button>
      {msg && <div style={{ marginTop: 4 }}>{msg}</div>}
      <ErrorBox error={err} />
      {content !== null && (
        <textarea value={content} onChange={e => setContent(e.target.value)} disabled={busy}
          rows={12} spellCheck={false}
          style={{ width: "100%", marginTop: 4, fontFamily: "monospace", fontSize: 12 }}
          placeholder={exists ? "" : "# файла нет — дефолты suricata-update; впишите конфиг и сохраните"} />
      )}
      {hist !== null && (
        <div style={{ marginTop: 4 }}>
          {hist.length === 0 && <span>применений ещё не было</span>}
          {hist.length > 0 && (
            <table style={{ width: "100%", fontSize: 12 }}>
              <thead>
                <tr><th>время</th><th>файл</th><th>статус</th><th>sha</th><th>бэкап</th><th>кем</th></tr>
              </thead>
              <tbody>
                {hist.map(x => (
                  <tr key={x.id}>
                    <td>{fmtTime(x.created_at)}</td>
                    <td>{x.file}{x.unchanged ? " (без изменений)" : ""}</td>
                    <td><Badge status={x.status === "applied" ? "ok" : "err"} /></td>
                    <td className="muted">{x.sha256.slice(0, 12)}…</td>
                    <td className="muted">{x.backup || "—"}</td>
                    <td className="muted">{x.created_by || "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}
      <div style={{ marginTop: 4, fontSize: 12 }}>
        Требуется capability config; перед записью — YAML-валидация (*.yaml), бэкап .surifleet-bak-TS (3 шт), рестарт движка не нужен — suricata-update подхватит при следующем прогоне.
      </div>
    </div>
  );
}

// SuricataUpdatePanel — suricata-update по кнопке (чанк 82): список
// источников сенсора (enable/disable чекбоксами), запуск обновления;
// итоговый набор заливается агентом на сервер и импортируется в общий
// список правил (поле import, default true). Чанк 112 — строка живого
// статуса, чанк 113 — редактор конфигов suricata-update.
function SuricataUpdatePanel() {
  const can = useCan();
  const [instances, setInstances] = React.useState<{ id: string; name: string; hostname?: string }[]>([]);
  const [instID, setInstID] = React.useState("");
  const [sources, setSources] = React.useState<SourceItem[] | null>(null);
  const [sel, setSel] = React.useState<Record<string, boolean>>({});
  const [reload, setReload] = React.useState(false);
  const [msg, setMsg] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [err, setErr] = React.useState<unknown>(null);

  React.useEffect(() => {
    apiGet<{ items?: { id: string; name: string; hostname?: string }[] }>("/instances?limit=100")
      .then(d => setInstances(d.items || []))
      .catch(() => {});
  }, []);

  const loadSources = async (id?: string) => {
    const iid = id ?? instID;
    if (!iid) return;
    setBusy(true);
    try {
      const d = await apiGet<{ items?: SourceItem[] }>(`/instances/${iid}/suricata_update/sources`);
      const items = d.items || [];
      setSources(items);
      const m: Record<string, boolean> = {};
      items.forEach(x => { m[x.name] = x.enabled; });
      setSel(m);
      setMsg(`${items.length} источников`);
    } catch (e) { setErr(e); setSources(null); } finally { setBusy(false); }
  };

  const run = async () => {
    if (!instID) { setMsg("выберите инстанс"); return; }
    const enable = (sources || []).filter(x => sel[x.name] && !x.enabled).map(x => x.name);
    const disable = (sources || []).filter(x => !sel[x.name] && x.enabled).map(x => x.name);
    setBusy(true);
    try {
      const r = await apiPost<{ task_id: string; import: boolean }>(`/instances/${instID}/suricata_update`, {
        enable_sources: enable, disable_sources: disable, reload,
      });
      setMsg(`задача ${r.task_id.slice(0, 8)}… запущена` +
        (enable.length ? `, включаются: ${enable.join(", ")}` : "") +
        (disable.length ? `, отключаются: ${disable.join(", ")}` : "") +
        (r.import ? " — итоговый набор будет импортирован в общий список правил" : ""));
      setSources(null);
    } catch (e) { setErr(e); } finally { setBusy(false); }
  };

  return (
    <div className="panel">
      <h3>suricata-update (подписка на источники правил)</h3>
      <ErrorBox error={err} />
      {msg && <p className="muted">{msg}</p>}
      <p className="muted">
        Инстанс:{" "}
        <select value={instID} onChange={e => { setInstID(e.target.value); setSources(null); setMsg(""); }}>
          <option value="">— выбрать —</option>
          {instances.map(i => (
            <option key={i.id} value={i.id}>{i.hostname ? i.hostname + " · " : ""}{i.name}</option>
          ))}
        </select>{" "}
        <button className="btn" disabled={busy || !instID} onClick={() => loadSources()}>
          {busy ? "…" : "Источники"}
        </button>
      </p>
      <UpdateStatusLine instID={instID} />
      {can("config.read") && <UpdateConfigEditor instID={instID} />}
      {sources && (
        <p className="muted">
          {sources.map(x => (
            <label key={x.name} style={{ marginRight: "1em" }} title={x.summary || x.name}>
              <input type="checkbox" checked={!!sel[x.name]}
                onChange={() => setSel(prev => ({ ...prev, [x.name]: !prev[x.name] }))} />{" "}
              {x.name}
            </label>
          ))}
        </p>
      )}
      {can("rules.write") && (
        <p className="muted">
          <label>
            <input type="checkbox" checked={reload} onChange={e => setReload(e.target.checked)} />{" "}
            reload движка после обновления
          </label>{" "}
          <button className="btn primary" disabled={busy || !instID} onClick={run}>
            Запустить suricata-update
          </button>
        </p>
      )}
      <p className="muted">
        Снятые с публикации правила остаются в репозитории (status) — общий список правил
        на вкладке «Правила» пополняется импортом итогового набора.
      </p>
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
      <SuricataUpdatePanel />
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
