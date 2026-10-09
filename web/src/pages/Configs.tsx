import React from "react";
import { apiGet, apiPost, apiPut, Page } from "../api";
import { ErrorBox, fmtTime, short, SortState, SortTh, sortBy } from "../components";
import { useCan } from "../perms";

// Configs — вкладка «Конфигурации» (чанки 54/56/58, план 1B): версии
// suricata.yaml — создание из текста или загрузка фактического yaml с
// сенсора («как на хосте», GET /instances/{id}/config/current), просмотр,
// редактирование версии, деплой на инстанс (deploy_config: бэкап →
// suricata -T → рестарт; validate_only — только проверка), история
// применений по инстансу (chanк 57) с откатом к последней applied (чанк 58).
// Весь файл под управлением (решение заказчика).

interface ConfigVersion {
  id: string;
  version: string;
  sha256: string;
  note?: string | null;
  created_at?: string;
}

interface Instance {
  id: string;
  name: string;
  hostname?: string;
}

// ConfigDeploy — запись истории применения конфигурации (chanк 57/58).
interface ConfigDeploy {
  id: string;
  instance_id: string;
  config_version: string;
  status: string; // validated | applied | validation_failed | deploy_failed
  validation_output?: string | null;
  reported_at: string;
}

// badgeClass — CSS-класс бейджа по статусу применения.
const badgeClass = (s: string) =>
  s === "applied" ? "badge-ok"
  : s === "validated" ? "badge-info"
  : "badge-err";

// CAPS_KNOWN — каталог capability поэтапной передачи контроля (п. 4 ТЗ).
const CAPS_KNOWN = ["monitoring", "rules", "log_rotation", "service_mgmt", "packages", "config"];

interface Host {
  id: string;
  hostname?: string;
  name?: string;
}

// HostCapsPanel — capability хоста: GET/PUT /hosts/{id}/capabilities
// (чанк 61). Агент применит набор при следующем подключении (HelloAck).
function HostCapsPanel() {
  const can = useCan();
  const [hosts, setHosts] = React.useState<Host[]>([]);
  const [hostID, setHostID] = React.useState("");
  const [caps, setCaps] = React.useState<string[]>([]);
  const [msg, setMsg] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  React.useEffect(() => {
    apiGet<Page<Host>>("/hosts?limit=100")
      .then(d => setHosts(d.items || []))
      .catch(() => {});
  }, []);

  const load = async (id?: string) => {
    const hid = id ?? hostID;
    if (!hid) return;
    setBusy(true);
    try {
      const d = await apiGet<{ capabilities: string[] }>(`/hosts/${hid}/capabilities`);
      setCaps(d.capabilities || []);
    } catch { /* показываем пустой набор */ } finally { setBusy(false); }
  };

  const save = async () => {
    if (!hostID) return;
    setBusy(true);
    try {
      await apiPut(`/hosts/${hostID}/capabilities`, { capabilities: caps });
      setMsg("сохранено — агент применит при следующем подключении (переподключении)");
    } catch { setMsg("ошибка сохранения (нужно право hosts.write)"); } finally { setBusy(false); }
  };

  const toggle = (c: string) =>
    setCaps(prev => prev.includes(c) ? prev.filter(x => x !== c) : [...prev, c]);

  return (
    <div className="panel">
      <p className="muted">
        Capability хоста (поэтапная передача контроля):{" "}
        <select value={hostID} onChange={e => { setHostID(e.target.value); setCaps([]); setMsg(""); if (e.target.value) load(e.target.value); }}>
          <option value="">— выбрать хост —</option>
          {hosts.map(h => (
            <option key={h.id} value={h.id}>{h.hostname || h.name || h.id}</option>
          ))}
        </select>{" "}
        {hostID && can("hosts.write") && (
          <button className="btn primary" disabled={busy} onClick={save}>Сохранить набор</button>
        )}
      </p>
      {hostID && (
        <p>
          {CAPS_KNOWN.map(c => (
            <label key={c} className="muted" style={{ marginRight: "1em" }}>
              <input type="checkbox" disabled={!can("hosts.write")} checked={caps.includes(c)}
                onChange={() => toggle(c)} />{" "}
              <span className={caps.includes(c) ? "" : "muted"}>{c}</span>
            </label>
          ))}
        </p>
      )}
      {msg && <p className="muted">{msg}</p>}
    </div>
  );
}

interface ConfigProfile {
  id: string;
  name: string;
  description?: string | null;
  scope_type: string; // cluster | host | instance
  scope_id: string;
  version: number;
}

interface RenderResult {
  rendered_yaml: string;
  sources: { profile_id: string; name: string; scope_type: string }[];
}

interface Cluster {
  id: string;
  name: string;
}

// ProfilesPanel — профили конфигурации (чанк 69): список, создание,
// preview рендера для инстанса (GET /config_profiles/{id}/render) и
// деплой отрендеренного профиля (POST /{id}/deploy). Наследование
// кластер→хост→инстанс — через parent_id (задаётся в API, в панели —
// базовый сценарий без родителя).
function ProfilesPanel({ instances }: { instances: Instance[] }) {
  const can = useCan();
  const [items, setItems] = React.useState<ConfigProfile[]>([]);
  const [hosts, setHosts] = React.useState<Host[]>([]);
  const [clusters, setClusters] = React.useState<Cluster[]>([]);
  const [err, setErr] = React.useState<unknown>(null);
  const [msg, setMsg] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  // форма создания
  const [name, setName] = React.useState("");
  const [scopeType, setScopeType] = React.useState("cluster");
  const [scopeID, setScopeID] = React.useState("");
  const [content, setContent] = React.useState("");
  // рендер/деплой
  const [profileID, setProfileID] = React.useState("");
  const [targetID, setTargetID] = React.useState("");
  const [rendered, setRendered] = React.useState<RenderResult | null>(null);
  const [validateOnly, setValidateOnly] = React.useState(false);

  const load = React.useCallback(async () => {
    try {
      const d = await apiGet<Page<ConfigProfile>>("/config_profiles?limit=100");
      setItems(d.items || []);
    } catch (e) { setErr(e); }
  }, []);

  React.useEffect(() => {
    load();
    apiGet<Page<Host>>("/hosts?limit=100").then(d => setHosts(d.items || [])).catch(() => {});
    apiGet<Page<Cluster>>("/clusters?limit=100").then(d => setClusters(d.items || [])).catch(() => {});
  }, [load]);

  const scopeOptions = () => {
    if (scopeType === "cluster") return clusters.map(c => ({ id: c.id, label: c.name }));
    if (scopeType === "host") return hosts.map(h => ({ id: h.id, label: h.hostname || h.name || h.id }));
    return instances.map(i => ({ id: i.id, label: (i.hostname ? i.hostname + " · " : "") + i.name }));
  };

  const add = async () => {
    setBusy(true);
    try {
      const p = await apiPost<ConfigProfile>("/config_profiles", {
        name: name.trim(), scope_type: scopeType, scope_id: scopeID, content_yaml: content,
      });
      setMsg(`профиль ${p.name} создан (v${p.version})`);
      setName(""); setScopeID(""); setContent("");
      await load();
    } catch (e) { setErr(e); } finally { setBusy(false); }
  };

  const render = async () => {
    if (!profileID || !targetID) { setMsg("выберите профиль и инстанс-цель"); return; }
    setBusy(true);
    try {
      const d = await apiGet<RenderResult>(`/config_profiles/${profileID}/render?target=${targetID}`);
      setRendered(d);
      setMsg("");
    } catch (e) { setErr(e); setRendered(null); } finally { setBusy(false); }
  };

  const deploy = async () => {
    if (!profileID || !targetID) { setMsg("выберите профиль и инстанс-цель"); return; }
    setBusy(true);
    try {
      const r = await apiPost<{ task_id: string; version: string }>(`/config_profiles/${profileID}/deploy`, {
        instance_id: targetID, validate_only: validateOnly,
      });
      setMsg(`задача ${short(r.task_id)} отправлена (версия ${r.version}` +
        (validateOnly ? ", только валидация" : "") + ") — результат в истории применений");
    } catch (e) { setErr(e); } finally { setBusy(false); }
  };

  return (
    <div className="panel">
      <h3>Профили конфигурации (наследование + переменные)</h3>
      <ErrorBox error={err} />
      {msg && <p className="muted">{msg}</p>}
      {items.length > 0 && (
        <table>
          <thead><tr><th>Имя</th><th>Scope</th><th>Версия</th><th></th></tr></thead>
          <tbody>
            {items.map(p => (
              <tr key={p.id}>
                <td><b>{p.name}</b></td>
                <td className="muted">{p.scope_type}</td>
                <td className="muted">v{p.version}</td>
                <td>
                  <button className="btn" onClick={() => { setProfileID(p.id); setRendered(null); }}>
                    {profileID === p.id ? "✓ выбран" : "выбрать"}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {can("config.write") && (
        <p>
          Новый профиль:{" "}
          <input placeholder="имя" value={name} onChange={e => setName(e.target.value)} />{" "}
          <select value={scopeType} onChange={e => { setScopeType(e.target.value); setScopeID(""); }}>
            <option value="cluster">кластер</option>
            <option value="host">хост</option>
            <option value="instance">инстанс</option>
          </select>{" "}
          <select value={scopeID} onChange={e => setScopeID(e.target.value)}>
            <option value="">— объект —</option>
            {scopeOptions().map(o => <option key={o.id} value={o.id}>{o.label}</option>)}
          </select>{" "}
          <button className="btn primary" disabled={busy || !name.trim() || !scopeID || !content.trim()} onClick={add}>
            Создать
          </button>
        </p>
      )}
      {can("config.write") && (
        <textarea
          placeholder={"content_yaml профиля; переменные {{var}} в кавычках: vars: {iface: enp0s3} + interface: \"{{iface}}\";\nвстроенные: {{instance.name}}, {{host.hostname}}, {{cluster.name}} и др."}
          value={content}
          onChange={e => setContent(e.target.value)}
          style={{ width: "100%", minHeight: "8em", fontFamily: "monospace" }}
        />
      )}
      {items.length > 0 && instances.length > 0 && (
        <p className="muted">
          Рендер/деплой: профиль выбран кнопкой выше; цель —{" "}
          <select value={targetID} onChange={e => { setTargetID(e.target.value); setRendered(null); }}>
            <option value="">— инстанс —</option>
            {instances.map(i => (
              <option key={i.id} value={i.id}>{i.hostname ? i.hostname + " · " : ""}{i.name}</option>
            ))}
          </select>{" "}
          <button className="btn" disabled={busy || !profileID || !targetID} onClick={render}>Предпросмотр рендера</button>
          {can("config.write") && <>{" "}
            <label className="muted">
              <input type="checkbox" checked={validateOnly} onChange={e => setValidateOnly(e.target.checked)} />
              {" "}только валидация
            </label>{" "}
            <button className="btn primary" disabled={busy || !profileID || !targetID} onClick={deploy}>Деплой профиля</button>
          </>}
        </p>
      )}
      {rendered && (
        <>
          <p className="muted">
            Цепочка наследования: {rendered.sources.map(s => `${s.name} (${s.scope_type})`).join(" → ")}
          </p>
          <pre style={{ maxHeight: "24em", overflow: "auto" }}>{rendered.rendered_yaml}</pre>
        </>
      )}
    </div>
  );
}

export default function Configs({ active }: { active: boolean }) {
  const can = useCan();
  const [items, setItems] = React.useState<ConfigVersion[]>([]);
  const [instances, setInstances] = React.useState<Instance[]>([]);
  const [err, setErr] = React.useState<unknown>(null);
  const [result, setResult] = React.useState("");
  const [expanded, setExpanded] = React.useState<string | null>(null);
  const [sort, setSort] = React.useState<SortState>({ key: "created_at", dir: -1 });
  // форма новой версии
  const [version, setVersion] = React.useState("");
  const [note, setNote] = React.useState("");
  const [yaml, setYaml] = React.useState("");
  // деплой
  const [instID, setInstID] = React.useState("");
  const [validateOnly, setValidateOnly] = React.useState(false);
  // редактор «как на хосте» (чанк 56): загрузка фактического yaml с сенсора
  const [srcInstID, setSrcInstID] = React.useState("");
  const [fetching, setFetching] = React.useState(false);
  // история применений по инстансу (чанк 58)
  const [histInstID, setHistInstID] = React.useState("");
  const [history, setHistory] = React.useState<ConfigDeploy[] | null>(null);
  const [busy, setBusy] = React.useState(false);

  const loadHistory = async (inst?: string) => {
    const iid = inst ?? histInstID;
    if (!iid) { setResult("выберите инстанс для истории"); return; }
    setFetching(true);
    try {
      const d = await apiGet<{ items: ConfigDeploy[] }>(`/instances/${iid}/config/history`);
      setHistory(d.items || []);
      setResult("");
    } catch (e) { setErr(e); } finally { setFetching(false); }
  };

  // rollback — деплой последней applied-версии на выбранный инстанс
  // истории (контент версии — из репозитория config_versions).
  const rollback = async () => {
    if (!histInstID) { setResult("выберите инстанс для отката"); return; }
    const lastApplied = (history || []).find(x => x.status === "applied");
    if (!lastApplied) { setResult("нет применённой (applied) версии в истории"); return; }
    const ver = items.find(v => v.version === lastApplied.config_version);
    if (!ver) { setResult(`версия ${lastApplied.config_version} не найдена в репозитории (возможно, создана внешне)`); return; }
    await deploy(ver.id, histInstID);
  };

  // fetchYaml — сырой GET с токеном (content не JSON, читаем текстом).
  const fetchYaml = React.useCallback(async (path: string): Promise<string> => {
    const base = import.meta.env.VITE_API_BASE ?? "/api/v1";
    const r = await fetch(`${base}${path}`, {
      headers: { Authorization: "Bearer " + (localStorage.getItem("surifleet_token") || "") },
    });
    if (!r.ok) throw new Error("HTTP " + r.status + (r.status === 409 ? " (агент offline)" : ""));
    return r.text();
  }, []);

  // fetchFromSensor — фактический suricata.yaml инстанса в редактор.
  const fetchFromSensor = async () => {
    if (!srcInstID) { setResult("выберите инстанс-источник"); return; }
    const inst = instances.find(i => i.id === srcInstID);
    setFetching(true);
    try {
      const text = await fetchYaml(`/instances/${srcInstID}/config/current`);
      setYaml(text);
      setNote(`с сенсора ${inst?.hostname || inst?.name || srcInstID}`);
      setResult(`загружен suricata.yaml с ${inst?.hostname || inst?.name} (${text.length} байт) — отредактируйте и сохраните версией`);
    } catch (e) { setErr(e); } finally { setFetching(false); }
  };

  // toEditor — содержимое версии в форму редактирования.
  const toEditor = async (v: ConfigVersion) => {
    setFetching(true);
    try {
      const text = await fetchYaml(`/config_versions/${v.id}/content`);
      setYaml(text);
      setNote(`на основе ${v.version}`);
      setResult(`версия ${v.version} загружена в редактор`);
    } catch (e) { setErr(e); } finally { setFetching(false); }
  };

  const load = React.useCallback(async () => {
    try {
      const d = await apiGet<Page<ConfigVersion>>("/config_versions?limit=50");
      setItems(d.items || []);
      setErr(null);
    } catch (e) { setErr(e); }
  }, []);
  React.useEffect(() => { if (active) load(); }, [active, load]);

  React.useEffect(() => {
    if (!active) return;
    apiGet<Page<Instance>>("/instances?limit=100")
      .then(d => setInstances(d.items || []))
      .catch(() => {});
  }, [active]);

  const add = async () => {
    setBusy(true);
    try {
      const body: Record<string, unknown> = { yaml, note: note.trim() };
      if (version.trim()) body.version = version.trim();
      const v = await apiPost<ConfigVersion>("/config_versions", body);
      setResult(`сохранена версия ${v.version} (sha256 ${short(v.sha256)}…)`);
      setVersion(""); setNote(""); setYaml("");
      await load();
    } catch (e) { setErr(e); } finally { setBusy(false); }
  };

  const deploy = async (id: string, target?: string) => {
    const tgt = target ?? instID;
    if (!tgt) { setResult("выберите инстанс"); return; }
    setBusy(true);
    try {
      const r = await apiPost<{ task_id: string; version: string }>(`/config_versions/${id}/deploy`, {
        instance_id: tgt, validate_only: validateOnly,
      });
      setResult(`задача ${short(r.task_id)} отправлена (версия ${r.version}` +
        (validateOnly ? ", только валидация" : "") + ") — результат в логах агента");
    } catch (e) { setErr(e); } finally { setBusy(false); }
  };

  return (
    <>
      <h2>Конфигурации Suricata</h2>
      <ErrorBox error={err} />
      {result && <p className="muted">{result}</p>}
      <HostCapsPanel />
      {active && <ProfilesPanel instances={instances} />}

      {can("config.write") && (
        <div className="panel">
          <p>
            Редактор «как на хосте»:{" "}
            <select value={srcInstID} onChange={e => setSrcInstID(e.target.value)}>
              <option value="">— инстанс-источник —</option>
              {instances.map(i => (
                <option key={i.id} value={i.id}>{i.hostname ? i.hostname + " · " : ""}{i.name}</option>
              ))}
            </select>{" "}
            <button className="btn" disabled={fetching || !srcInstID} onClick={fetchFromSensor}>
              {fetching ? "загрузка…" : "Загрузить с сенсора"}
            </button>
          </p>
          <p>
            <input placeholder="версия/тег (пусто → авто cfg-vN)" value={version}
              onChange={e => setVersion(e.target.value)} />{" "}
            <input placeholder="комментарий" value={note}
              onChange={e => setNote(e.target.value)} style={{ minWidth: "16em" }} />{" "}
            <button className="btn primary" disabled={busy || !yaml.trim()} onClick={add}>
              Сохранить версию
            </button>
          </p>
          <textarea
            placeholder="# содержимое suricata.yaml (файл целиком — он будет под управлением; бэкап перед записью)"
            value={yaml}
            onChange={e => setYaml(e.target.value)}
            style={{ width: "100%", minHeight: "14em", fontFamily: "monospace" }}
          />
        </div>
      )}

      {items.length > 0 && (
        <table>
          <thead>
            <tr>
              <SortTh label="Версия" k="version" sort={sort} onSort={setSort} />
              <th>ID</th><th>SHA-256</th><th>Комментарий</th>
              <SortTh label="Создан" k="created_at" sort={sort} onSort={setSort} />
              <th></th>
            </tr>
          </thead>
          <tbody>
            {sortBy(items, sort, (v, k) => {
              switch (k) {
                case "version": return v.version;
                case "created_at": return v.created_at;
                default: return undefined;
              }
            }).map(v => (
              <React.Fragment key={v.id}>
                <tr>
                  <td><b>{v.version}</b></td>
                  <td className="muted">{short(v.id)}</td>
                  <td className="muted">{short(v.sha256)}…</td>
                  <td className="muted">{v.note || "—"}</td>
                  <td className="muted">{fmtTime(v.created_at)}</td>
                  <td style={{ whiteSpace: "nowrap" }}>
                    <button className="btn" onClick={() => setExpanded(expanded === v.id ? null : v.id)}>
                      {expanded === v.id ? "скрыть" : "yaml"}
                    </button>
                    {can("config.write") && <>{" "}<button className="btn" disabled={fetching} onClick={() => toEditor(v)}>в редактор</button></>}
                    {can("config.write") && <>{" "}<button className="btn primary" onClick={() => deploy(v.id)}>деплой</button></>}
                  </td>
                </tr>
                {expanded === v.id && (
                  <tr><td colSpan={6}><ConfigContent id={v.id} /></td></tr>
                )}
              </React.Fragment>
            ))}
          </tbody>
        </table>
      )}
      {can("config.write") && items.length > 0 && (
        <div className="panel">
          <p className="muted">
            Цель деплоя:{" "}
            <select value={instID} onChange={e => setInstID(e.target.value)}>
              <option value="">— выбрать инстанс —</option>
              {instances.map(i => (
                <option key={i.id} value={i.id}>{i.hostname ? i.hostname + " · " : ""}{i.name}</option>
              ))}
            </select>{" "}
            <label className="muted">
              <input type="checkbox" checked={validateOnly} onChange={e => setValidateOnly(e.target.checked)} />
              {" "}только валидация (suricata -T, без применения)
            </label>
          </p>
        </div>
      )}
      {can("config.read") && instances.length > 0 && (
        <div className="panel">
          <p className="muted">
            История применений по инстансу:{" "}
            <select value={histInstID} onChange={e => { setHistInstID(e.target.value); setHistory(null); }}>
              <option value="">— выбрать инстанс —</option>
              {instances.map(i => (
                <option key={i.id} value={i.id}>{i.hostname ? i.hostname + " · " : ""}{i.name}</option>
              ))}
            </select>{" "}
            <button className="btn" disabled={fetching || !histInstID} onClick={() => loadHistory()}>
              {fetching ? "загрузка…" : "Показать"}
            </button>
            {can("config.write") && history !== null && (
              <>{" "}<button className="btn" disabled={busy} onClick={rollback}>Откат к последней applied</button></>
            )}
          </p>
          {history !== null && history.length === 0 && (
            <p className="muted">записей нет — деплоев конфигурации на инстанс не было</p>
          )}
          {history !== null && history.length > 0 && (
            <table>
              <thead>
                <tr><th>Время</th><th>Версия</th><th>Статус</th><th>Вывод валидатора</th></tr>
              </thead>
              <tbody>
                {history.map(x => (
                  <tr key={x.id}>
                    <td className="muted">{fmtTime(x.reported_at)}</td>
                    <td><b>{x.config_version}</b></td>
                    <td><span className={`badge ${badgeClass(x.status)}`}>{x.status}</span></td>
                    <td className="muted" style={{ maxWidth: "34em", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
                        title={x.validation_output || ""}>
                      {(x.validation_output || "—").split("\n").slice(-1)[0]}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}
      <p className="muted">
        Деплой: бэкап текущего файла → запись → suricata -T (откат при ошибке) →
        restart сервиса. Требует capability «config» на хосте. Результат задачи —
        в логах агента (вкладка «Логи»), истории применений и аудите.
      </p>
    </>
  );
}

// ConfigContent — просмотр YAML версии (GET /config_versions/{id}/content).
function ConfigContent({ id }: { id: string }) {
  const [text, setText] = React.useState("");
  const [err, setErr] = React.useState("");
  React.useEffect(() => {
    (async () => {
      try {
        const base = import.meta.env.VITE_API_BASE ?? "/api/v1";
        const r = await fetch(`${base}/config_versions/${id}/content`, {
          headers: { Authorization: "Bearer " + (localStorage.getItem("surifleet_token") || "") },
        });
        if (!r.ok) throw new Error("HTTP " + r.status);
        setText(await r.text());
      } catch (e) { setErr((e as Error).message); }
    })();
  }, [id]);
  if (err) return <span className="muted">ошибка: {err}</span>;
  return <pre style={{ maxHeight: "30em", overflow: "auto" }}>{text || "…"}</pre>;
}
