import React from "react";
import { apiGet, apiPost, Page } from "../api";
import { ErrorBox, fmtTime, short, SortState, SortTh, sortBy } from "../components";
import { useCan } from "../perms";

// Configs — вкладка «Конфигурации» (чанки 54/56, план 1B): версии
// suricata.yaml — создание из текста или загрузка фактического yaml с
// сенсора («как на хосте», GET /instances/{id}/config/current), просмотр,
// редактирование версии, деплой на инстанс (deploy_config: бэкап →
// suricata -T → рестарт; validate_only — только проверка).
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
  const [busy, setBusy] = React.useState(false);

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

  const deploy = async (id: string) => {
    if (!instID) { setResult("выберите инстанс"); return; }
    setBusy(true);
    try {
      const r = await apiPost<{ task_id: string; version: string }>(`/config_versions/${id}/deploy`, {
        instance_id: instID, validate_only: validateOnly,
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
      <p className="muted">
        Деплой: бэкап текущего файла → запись → suricata -T (откат при ошибке) →
        restart сервиса. Требует capability «config» на хосте. Результат задачи —
        в логах агента (вкладка «Логи») и аудите.
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
