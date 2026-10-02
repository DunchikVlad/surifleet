import React from "react";
import { apiDelete, apiGet, apiPatch, apiPost, Feed, FeedRun, Page } from "../api";
import { Badge, ErrorBox, fmtTime } from "../components";
import { useCan } from "../perms";

const LIMIT = 50;
const TYPES = ["generic", "et_open", "et_pro", "taxii", "stix", "misp"];

// Результат последнего запуска синка по фиду (для подсветки в таблице).
type SyncResults = Record<string, FeedRun>;

export default function Feeds({ active }: { active: boolean }) {
  const can = useCan();
  const [items, setItems] = React.useState<Feed[]>([]);
  const [cursor, setCursor] = React.useState<string | null>(null);
  const [err, setErr] = React.useState<unknown>(null);
  const [loaded, setLoaded] = React.useState(false);

  // Форма добавления.
  const [fName, setFName] = React.useState("");
  const [fUrl, setFUrl] = React.useState("");
  const [fType, setFType] = React.useState("generic");
  const [fSchedule, setFSchedule] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  // Синк по фидам: busy-флаги и последние результаты.
  const [syncing, setSyncing] = React.useState<Record<string, boolean>>({});
  const [results, setResults] = React.useState<SyncResults>({});

  const load = React.useCallback(async (append: boolean) => {
    let path = `/feeds?limit=${LIMIT}`;
    if (append && cursor) path += `&cursor=${encodeURIComponent(cursor)}`;
    try {
      const d = await apiGet<Page<Feed>>(path);
      setItems(prev => (append ? [...prev, ...(d.items || [])] : d.items || []));
      setCursor(d.next_cursor ?? null);
      setErr(null);
      setLoaded(true);
    } catch (e) { setErr(e); }
  }, [cursor]);

  React.useEffect(() => {
    if (active && !loaded) load(false);
  }, [active, loaded, load]);

  const add = async () => {
    if (!fName.trim() || !fUrl.trim()) return;
    setBusy(true);
    try {
      const body: Record<string, unknown> = {
        name: fName.trim(),
        url: fUrl.trim(),
        type: fType,
      };
      if (fSchedule.trim()) body.schedule = fSchedule.trim();
      await apiPost<Feed>("/feeds", body);
      setFName(""); setFUrl(""); setFSchedule("");
      setLoaded(false);
    } catch (e) { alert("Фид не создан: " + (e as Error).message); }
    setBusy(false);
  };

  const sync = async (f: Feed) => {
    setSyncing(prev => ({ ...prev, [f.id]: true }));
    try {
      const run = await apiPost<FeedRun>(`/feeds/${f.id}/sync`, {});
      setResults(prev => ({ ...prev, [f.id]: run }));
      setLoaded(false); // last_sync_* изменились
    } catch (e) { alert("Синхронизация не запустилась: " + (e as Error).message); }
    setSyncing(prev => ({ ...prev, [f.id]: false }));
  };

  const toggle = async (f: Feed) => {
    try {
      await apiPatch<Feed>(`/feeds/${f.id}`, { enabled: !f.enabled });
      setLoaded(false);
    } catch (e) { alert("Переключение не выполнено: " + (e as Error).message); }
  };

  const remove = async (f: Feed) => {
    if (!confirm(`Удалить фид «${f.name}»? Импортированные IOC останутся.`)) return;
    try {
      await apiDelete(`/feeds/${f.id}`);
      setItems(prev => prev.filter(x => x.id !== f.id));
    } catch (e) { alert("Удаление не выполнено: " + (e as Error).message); }
  };

  return (
    <>
      <h2>Фиды IOC</h2>
      {can("feeds.write") && (
      <div className="toolbar">
        <input
          placeholder="имя фида…"
          value={fName}
          onChange={e => setFName(e.target.value)}
          style={{ width: "14em" }}
        />
        <input
          placeholder="URL фида (http/https)…"
          value={fUrl}
          onChange={e => setFUrl(e.target.value)}
          onKeyDown={e => { if (e.key === "Enter") add(); }}
          style={{ minWidth: "24em" }}
        />
        <select value={fType} onChange={e => setFType(e.target.value)}
          title="синхронизируются: generic (IOC-листы plain/CSV/JSON), et_open/et_pro (фиды правил ET; для et_pro URL можно не задавать — укажите код подписки в credentials через API), taxii (TAXII/STIX: URL — API root или .../collections/{id}/objects/), stix (STIX bundle/JSON по URL), misp (MISP core format: URL — база фида с manifest.json)">
          {TYPES.map(t => <option key={t} value={t}>{t}</option>)}
        </select>
        <input
          placeholder="интервал (1h) или cron…"
          title="авто-синк: длительность Go (1h, 30m) или 5-полевой cron (*/15 * * * *, @daily); пусто — только вручную"
          value={fSchedule}
          onChange={e => setFSchedule(e.target.value)}
          style={{ width: "12em" }}
        />
        <button className="btn" disabled={busy} onClick={add}>Добавить</button>
      </div>
      )}
      <ErrorBox error={err} />
      {loaded && !items.length && !err && <p className="muted">Фидов нет.</p>}
      {items.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>Имя</th><th>URL</th><th>Тип</th><th>Интервал</th>
              <th>Последний синк</th><th>Статус</th><th></th>
            </tr>
          </thead>
          <tbody>
            {items.map(f => {
              const res = results[f.id];
              return (
                <React.Fragment key={f.id}>
                  <tr>
                    <td>
                      {f.name}{" "}
                      {can("feeds.write") && (
                      <button
                        className={"btn" + (f.enabled ? "" : " primary")}
                        title={f.enabled ? "отключить авто/ручной синк" : "включить"}
                        onClick={() => toggle(f)}
                      >
                        {f.enabled ? "on" : "off"}
                      </button>
                      )}
                    </td>
                    <td className="muted" style={{ maxWidth: "26em", overflow: "hidden", textOverflow: "ellipsis" }}>
                      {f.url}
                    </td>
                    <td>{f.type}</td>
                    <td className="muted">{f.schedule || "—"}</td>
                    <td className="muted">{fmtTime(f.last_sync_at ?? undefined)}</td>
                    <td>
                      {f.last_sync_status
                        ? <Badge status={f.last_sync_status === "success" ? "ok" : "err"} />
                        : <span className="muted">—</span>}
                      {f.last_error && (
                        <div className="muted" style={{ fontSize: "0.85em" }} title={f.last_error}>
                          {f.last_error.length > 80 ? f.last_error.slice(0, 80) + "…" : f.last_error}
                        </div>
                      )}
                    </td>
                    <td style={{ whiteSpace: "nowrap" }}>
                      {can("feeds.write") && (
                        <>
                      <button className="btn primary" disabled={syncing[f.id]} onClick={() => sync(f)}>
                        {syncing[f.id] ? "Синк…" : "Синхронизировать"}
                      </button>{" "}
                      <button className="btn" onClick={() => remove(f)}>удалить</button>
                        </>
                      )}
                    </td>
                  </tr>
                  {res && (
                    <tr>
                      <td colSpan={7}>
                        <span className="muted">
                          Синк {fmtTime(res.finished_at ?? undefined)}:{" "}
                          {res.status === "success" ? "успех" : "ошибка"}
                          {" — "}imported {res.imported}, updated {res.updated}, skipped {res.skipped}
                          {res.error ? ` — ${res.error}` : ""}
                          {res.rules_created !== undefined &&
                            ` · правила: +${res.rules_created} ~${res.rules_updated ?? 0} =${res.rules_unchanged ?? 0}`}
                          {res.ruleset_version && ` · ruleset ${res.ruleset_version}`}
                        </span>
                      </td>
                    </tr>
                  )}
                </React.Fragment>
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
      <p className="muted">
        Импортированные фидом IOC — на вкладке «IOC» (источник = имя фида).
        После успешного ручного синка правила из активных IOC пересобираются
        автоматически (ruleset ioc-current-*, без деплоя). Фиды et_open/et_pro
        импортируют правила в репозиторий «Правила»; для et_pro без URL код
        подписки задаётся в поле credentials (через API, просто код).
        Фиды taxii (TAXII 2.x/STIX) импортируют индикаторы в IOC: URL —
        API root сервера или endpoint объектов коллекции
        (.../collections/{"{id}"}/objects/), credentials — "user:pass" (Basic)
        или токен (Bearer). Фиды stix — статический STIX bundle/JSON по URL
        (тот же разбор индикаторов, без TAXII-протокола). Фиды misp —
        MISP core format: URL — база фида (грузятся manifest.json и файлы
        событий), атрибуты to_ids=false пропускаются.
      </p>
    </>
  );
}
