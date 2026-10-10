import React from "react";
import { apiDelete, apiGet, apiPatch, apiPost, apiPostEx, Page, Rule, Ruleset } from "../api";
import { Badge, ErrorBox, fmtTime, short, SortState, SortTh, sortBy } from "../components";
import { useCan } from "../perms";

const LIMIT = 50;

// AutoRuleset — авто-обновляемый набор (чанк 93).
interface AutoRuleset {
  id: string;
  name: string;
  enabled: boolean;
  include_suriupdate: boolean;
  include_ioc: boolean;
  include_manual: boolean;
  include_feeds: boolean;
  exclude_sids: number[];
  include_sources?: string[];
  schedule_enabled?: boolean;
  schedule_time?: string | null;
  schedule_interval_minutes?: number | null;
  targeting: { mode?: string; instance_ids?: string[] };
  last_built_at?: string;
  last_ruleset_version_id?: string;
}

// schedLabel — человекочитаемое расписание пересборки (таблица).
const schedLabel = (a: AutoRuleset): string => {
  if (!a.schedule_enabled) return "—";
  const iv = a.schedule_interval_minutes;
  if (iv) {
    if (iv % 60 === 0 && iv >= 60) return `каждые ${iv / 60} ч`;
    return `каждые ${iv} мин`;
  }
  return a.schedule_time ? `ежедневно ${a.schedule_time}` : "—";
};

// AutoRulesetsPanel — авто-ruleset'ы: состав по происхождению (suricata-
// update + IOC + ручные), exclude_sids — запрет на деплой, таргетинг
// агентов, пересборка по кнопке (и автоматически после suricata-update).
function AutoRulesetsPanel() {
  const can = useCan();
  const [items, setItems] = React.useState<AutoRuleset[]>([]);
  const [instances, setInstances] = React.useState<{ id: string; name: string; hostname?: string }[]>([]);
  const [err, setErr] = React.useState<unknown>(null);
  const [msg, setMsg] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [name, setName] = React.useState("");
  const [incSU, setIncSU] = React.useState(true);
  const [incIoc, setIncIoc] = React.useState(true);
  const [incMan, setIncMan] = React.useState(true);
  const [exclude, setExclude] = React.useState("");
  const [sources, setSources] = React.useState<string[]>([]);
  const [allSources, setAllSources] = React.useState<string[]>([]);
  const [schedEn, setSchedEn] = React.useState(false);
  const [schedMode, setSchedMode] = React.useState<"daily" | "minutes" | "hours">("daily");
  const [schedTime, setSchedTime] = React.useState("03:00");
  const [schedEvery, setSchedEvery] = React.useState(30);
  const [mode, setMode] = React.useState("all_clusters");
  const [selInst, setSelInst] = React.useState<string[]>([]);

  const load = () =>
    apiGet<{ items?: AutoRuleset[] }>("/auto_rulesets")
      .then(d => setItems(d.items || []))
      .catch(() => {});

  React.useEffect(() => {
    load();
    apiGet<{ items?: { id: string; name: string; hostname?: string }[] }>("/instances?limit=100")
      .then(d => {
        setInstances(d.items || []);
        const first = (d.items || [])[0];
        if (first) {
          apiGet<{ items?: { name: string }[] }>(`/instances/${first.id}/suricata_update/sources`)
            .then(x => setAllSources((x.items || []).map(y => y.name)))
            .catch(() => {});
        }
      })
      .catch(() => {});
  }, []);

  const add = async () => {
    setBusy(true);
    try {
      const targeting: Record<string, unknown> =
        mode === "specific_instances"
          ? { mode: "specific_instances", instance_ids: selInst }
          : { mode: "all_clusters" };
      const body: Record<string, unknown> = {
        name: name.trim(), targeting,
        include_suriupdate: incSU, include_ioc: incIoc, include_manual: incMan,
        include_sources: sources,
        schedule_enabled: schedEn,
        schedule_time: schedEn && schedMode === "daily" ? schedTime : "",
        schedule_interval_minutes: schedEn && schedMode !== "daily"
          ? (schedMode === "minutes" ? schedEvery : schedEvery * 60) : null,
        exclude_sids: exclude.split(",").map(x => Number(x.trim())).filter(x => Number.isFinite(x) && x > 0),
      };
      await apiPost("/auto_rulesets", body);
      setMsg(`авто-ruleset ${name.trim()} создан`);
      setName(""); setExclude("");
      await load();
    } catch (e) { setErr(e); } finally { setBusy(false); }
  };

  const rebuild = async (a: AutoRuleset) => {
    setBusy(true);
    try {
      const r = await apiPost<{ skipped: boolean; ruleset_version?: string; instances?: number }>(
        `/auto_rulesets/${a.id}/rebuild`, {});
      setMsg(r.skipped
        ? `${a.name}: пересборка пропущена (пустой состав/нет целей)`
        : `${a.name}: версия ${r.ruleset_version}, инстансов: ${r.instances} — деплой пошёл`);
      await load();
    } catch (e) { setErr(e); } finally { setBusy(false); }
  };

  const toggleEnabled = async (a: AutoRuleset) => {
    try {
      await apiPatch(`/auto_rulesets/${a.id}`, { name: a.name, targeting: a.targeting, enabled: !a.enabled });
      await load();
    } catch (e) { setErr(e); }
  };

  const del = async (a: AutoRuleset) => {
    if (!confirm(`Удалить авто-ruleset ${a.name}?`)) return;
    try { await apiDelete(`/auto_rulesets/${a.id}`); await load(); } catch (e) { setErr(e); }
  };

  return (
    <div className="panel">
      <h3>Авто-обновляемые ruleset'ы (suricata-update + IOC + ручные)</h3>
      <ErrorBox error={err} />
      {msg && <p className="muted">{msg}</p>}
      {items.length > 0 && (
        <table>
          <thead><tr><th>Имя</th><th>Состав</th><th>Запрет (sid)</th><th>Расписание</th><th>Последняя сборка</th><th></th></tr></thead>
          <tbody>
            {items.map(a => (
              <tr key={a.id}>
                <td><b>{a.name}</b>{!a.enabled && <span className="muted"> (выкл)</span>}</td>
                <td className="muted">
                  {[a.include_suriupdate && "suriupdate", a.include_ioc && "ioc", a.include_manual && "manual", a.include_feeds && "feeds"]
                    .filter(Boolean).join(" + ")}
                </td>
                <td className="muted">{(a.exclude_sids || []).length || "—"}</td>
                <td className="muted">{schedLabel(a)}</td>
                <td className="muted">{a.last_built_at ? fmtTime(a.last_built_at) : "не собирался"}</td>
                <td style={{ whiteSpace: "nowrap" }}>
                  {can("rules.write") && <>
                    <button className="btn primary" disabled={busy || !a.enabled} onClick={() => rebuild(a)}>пересобрать</button>{" "}
                    <button className="btn" disabled={busy} onClick={() => toggleEnabled(a)}>{a.enabled ? "выкл" : "вкл"}</button>{" "}
                    <button className="btn" disabled={busy} onClick={() => del(a)}>×</button>
                  </>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {can("rules.write") && (
        <>
          <p className="muted">
            Новый:{" "}
            <input placeholder="имя" value={name} onChange={e => setName(e.target.value)} />{" "}
            состав:{" "}
            <label><input type="checkbox" checked={incSU} onChange={e => setIncSU(e.target.checked)} /> suricata-update</label>{" "}
            <label><input type="checkbox" checked={incIoc} onChange={e => setIncIoc(e.target.checked)} /> IOC</label>{" "}
            <label><input type="checkbox" checked={incMan} onChange={e => setIncMan(e.target.checked)} /> ручные</label>{" "}
            таргетинг:{" "}
            <select value={mode} onChange={e => setMode(e.target.value)}>
              <option value="all_clusters">все кластеры</option>
              <option value="specific_instances">выбранные инстансы</option>
            </select>{" "}
            <button className="btn primary" disabled={busy || !name.trim() || (mode === "specific_instances" && selInst.length === 0)} onClick={add}>
              Создать
            </button>
          </p>
          {mode === "specific_instances" && (
            <p className="muted">
              {instances.map(i => (
                <label key={i.id} style={{ marginRight: "1em" }}>
                  <input type="checkbox" checked={selInst.includes(i.id)}
                    onChange={() => setSelInst(prev => prev.includes(i.id) ? prev.filter(x => x !== i.id) : [...prev, i.id])} />{" "}
                  {i.hostname ? i.hostname + " · " : ""}{i.name}
                </label>
              ))}
            </p>
          )}
          {allSources.length > 0 && (
            <p className="muted">
              Источники suricata-update (пусто = все):{" "}
              {allSources.map(nm => (
                <label key={nm} style={{ marginRight: "1em" }}>
                  <input type="checkbox" checked={sources.includes(nm)}
                    onChange={() => setSources(prev => prev.includes(nm) ? prev.filter(x => x !== nm) : [...prev, nm])} />{" "}
                  {nm}
                </label>
              ))}
            </p>
          )}
          <p className="muted">
            Расписание пересборки:{" "}
            <label>
              <input type="checkbox" checked={schedEn} onChange={e => setSchedEn(e.target.checked)} />{" "}
              авто
            </label>{" "}
            <label>
              <input type="radio" name="schedmode" disabled={!schedEn} checked={schedMode === "daily"}
                onChange={() => setSchedMode("daily")} /> ежедневно
            </label>{" "}
            <input type="time" value={schedTime} disabled={!schedEn || schedMode !== "daily"}
              onChange={e => setSchedTime(e.target.value)} />{" "}
            <label>
              <input type="radio" name="schedmode" disabled={!schedEn} checked={schedMode === "minutes"}
                onChange={() => setSchedMode("minutes")} /> каждые
            </label>{" "}
            <input type="number" min={1} max={10080} value={schedEvery} style={{ width: "5em" }}
              disabled={!schedEn || schedMode === "daily"}
              onChange={e => setSchedEvery(Math.max(1, Number(e.target.value) || 1))} />{" "}
            <select value={schedMode === "hours" ? "hours" : "minutes"} disabled={!schedEn || schedMode === "daily"}
              onChange={e => setSchedMode(e.target.value as "minutes" | "hours")}>
              <option value="minutes">минут</option>
              <option value="hours">часов</option>
            </select>{" "}
            — после сборки набор сразу раскатывается на таргетинг
          </p>
          <p className="muted">
            Запрет на деплой (sid через запятую — не попадут в набор):{" "}
            <input placeholder="напр. 2030692, 9000001" value={exclude} onChange={e => setExclude(e.target.value)} style={{ minWidth: "18em" }} />
          </p>
        </>
      )}
      <p className="muted">
        Пересборка автоматически запускается после каждого импорта suricata-update
        (включённые наборы с составом suricata-update): новая версия ruleset →
        волновой деплой на таргетинг.
      </p>
    </div>
  );
}

export default function Rulesets({ active }: { active: boolean }) {

  const can = useCan();
  const [items, setItems] = React.useState<Ruleset[] | null>(null);
  const [err, setErr] = React.useState<unknown>(null);

  // --- конструктор: выбор правил чекбоксами ---
  const [rules, setRules] = React.useState<Rule[]>([]);
  const [cursor, setCursor] = React.useState<string | null>(null);
  const [status, setStatus] = React.useState("enabled");
  const [q, setQ] = React.useState("");
  const [rulesErr, setRulesErr] = React.useState<unknown>(null);
  const [rulesLoaded, setRulesLoaded] = React.useState(false);
  // Выбранные правила накапливаются между страницами/поиском: id → rule.
  const [selected, setSelected] = React.useState<Map<string, Rule>>(new Map());

  const [version, setVersion] = React.useState("");
  const [note, setNote] = React.useState("");
  const [result, setResult] = React.useState("");
  // Состав ruleset'а (drill-down по клику на версию, чанк 50).
  const [expanded, setExpanded] = React.useState<string | null>(null);
  const toggleExpand = (id: string) => setExpanded(prev => (prev === id ? null : id));
  const [sort, setSort] = React.useState<SortState>({ key: "created_at", dir: -1 });

  // download — скачивание версии ruleset'а файлом .rules (чанк 71):
  // сырое скачивание через fetch (Content-Disposition attachment).
  const download = async (v: Ruleset) => {
    try {
      const base = import.meta.env.VITE_API_BASE ?? "/api/v1";
      const r = await fetch(`${base}/rulesets/${v.id}/download`, {
        headers: { Authorization: "Bearer " + (localStorage.getItem("surifleet_token") || "") },
      });
      if (!r.ok) throw new Error("HTTP " + r.status);
      const a = document.createElement("a");
      a.href = URL.createObjectURL(await r.blob());
      a.download = `surifleet-ruleset-${v.version}.rules`;
      a.click();
      URL.revokeObjectURL(a.href);
    } catch (e) { setErr(e); }
  };

  const load = React.useCallback(async () => {
    try {
      const d = await apiGet<Page<Ruleset>>("/rulesets?limit=50");
      setItems(d.items || []);
      setErr(null);
    } catch (e) { setErr(e); }
  }, []);

  React.useEffect(() => { if (active) load(); }, [active, load]);

  const loadRules = React.useCallback(async (append: boolean) => {
    let path = `/rules?limit=${LIMIT}`;
    if (status) path += `&status=${encodeURIComponent(status)}`;
    if (q.trim()) path += `&q=${encodeURIComponent(q.trim())}`;
    if (append && cursor) path += `&cursor=${encodeURIComponent(cursor)}`;
    try {
      const d = await apiGet<Page<Rule>>(path);
      setRules(prev => (append ? [...prev, ...(d.items || [])] : d.items || []));
      setCursor(d.next_cursor ?? null);
      setRulesErr(null);
      setRulesLoaded(true);
    } catch (e) { setRulesErr(e); }
  }, [status, q, cursor]);

  React.useEffect(() => {
    if (active && !rulesLoaded) loadRules(false);
  }, [active, rulesLoaded, loadRules]);

  const toggle = (r: Rule, on: boolean) => {
    setSelected(prev => {
      const next = new Map(prev);
      if (on) next.set(r.id, r);
      else next.delete(r.id);
      return next;
    });
  };

  const build = async () => {
    if (!selected.size) { setResult("не выбрано ни одного правила"); return; }
    try {
      // Явный rule_ids имеет приоритет над rule_filter (см. buildRuleset);
      // пустая версия → автоинкремент vN на сервере (чанк 50).
      const body: Record<string, unknown> = {
        note: note.trim(),
        rule_ids: [...selected.keys()],
      };
      if (version.trim()) body.version = version.trim();
      const r = await apiPostEx<Ruleset>("/rulesets", body);
      const v = r.body;
      setResult(
        (r.status === 201
          ? `создан ruleset ${v.version}`
          : `ruleset с таким составом уже существует (версия ${v.version})`) +
        `: ${v.rule_count} правил, id ${short(v.id)}, sha256 ${(v.sha256 || "").slice(0, 16)}…`
      );
      load();
    } catch (e) { setResult((e as Error).message); }
  };

  const selList = [...selected.values()].sort((a, b) => a.sid - b.sid);

  return (
    <>
      <h2>Ruleset'ы</h2>
      <AutoRulesetsPanel />
      <ErrorBox error={err} />
      {items && !items.length && <p className="muted">Ruleset'ов нет.</p>}
      {items && items.length > 0 && (
        <table>
          <thead>
            <tr>
              <SortTh label="Версия" k="version" sort={sort} onSort={setSort} />
              <th>ID</th>
              <SortTh label="Правил" k="rule_count" sort={sort} onSort={setSort} />
              <th>SHA-256</th>
              <SortTh label="Создан" k="created_at" sort={sort} onSort={setSort} />
              <th></th>
            </tr>
          </thead>
          <tbody>
            {sortBy(items, sort, (v, k) => {
              switch (k) {
                case "version": return v.version;
                case "rule_count": return v.rule_count;
                case "created_at": return v.created_at;
                default: return undefined;
              }
            }).map(v => (
              <React.Fragment key={v.id}>
                <tr onClick={() => toggleExpand(v.id)} style={{ cursor: "pointer" }} title="показать состав">
                  <td><b>{v.version}</b></td>
                  <td className="muted">{short(v.id)}</td>
                  <td>{v.rule_count}</td>
                  <td className="muted">{(v.sha256 || "").slice(0, 16)}…</td>
                  <td className="muted">{fmtTime(v.created_at)}</td>
                  <td>
                    <button className="btn" title="скачать .rules"
                      onClick={(e) => { e.stopPropagation(); download(v); }}>
                      скачать
                    </button>
                  </td>
                </tr>
                {expanded === v.id && (
                  <tr>
                    <td colSpan={6}>
                      <RulesetRules id={v.id} version={v.version} />
                    </td>
                  </tr>
                )}
              </React.Fragment>
            ))}
          </tbody>
        </table>
      )}

      {can("rules.write") && (
        <>
      <h3>Конструктор ruleset'а — сборка из выбранных правил</h3>
      <div className="toolbar">
        <select value={status} onChange={e => { setStatus(e.target.value); setRulesLoaded(false); }}>
          <option value="enabled">enabled</option>
          <option value="disabled">disabled</option>
          <option value="under_review">under_review</option>
          <option value="">все статусы</option>
        </select>
        <input
          placeholder="поиск по sid / сообщению…"
          value={q}
          onChange={e => setQ(e.target.value)}
          onKeyDown={e => { if (e.key === "Enter") setRulesLoaded(false); }}
        />
        <button className="btn" onClick={() => setRulesLoaded(false)}>Найти</button>
        <span className="muted">выбрано: <b>{selected.size}</b></span>
        {selected.size > 0 && (
          <button className="btn" onClick={() => setSelected(new Map())}>снять выбор</button>
        )}
      </div>
      <ErrorBox error={rulesErr} />
      {rulesLoaded && !rules.length && !rulesErr && <p className="muted">Правил по фильтру нет.</p>}
      {rules.length > 0 && (
        <table>
          <thead>
            <tr><th></th><th>SID</th><th>Сообщение</th><th>Статус</th><th>Категория</th></tr>
          </thead>
          <tbody>
            {rules.map(r => (
              <tr key={r.id}>
                <td>
                  <input
                    type="checkbox"
                    checked={selected.has(r.id)}
                    onChange={e => toggle(r, e.target.checked)}
                  />
                </td>
                <td>{r.sid}</td>
                <td>{r.msg || ""}</td>
                <td><Badge status={r.status} /></td>
                <td className="muted">{r.category || "—"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {cursor && (
        <p>
          <button className="btn" onClick={() => loadRules(true)}>Ещё {LIMIT}…</button>{" "}
          <span className="muted">показано {rules.length}</span>
        </p>
      )}

      {selList.length > 0 && (
        <div className="detail">
          <h3 style={{ marginTop: 0 }}>Выбранные правила ({selList.length})</h3>
          <p>
            {selList.map(r => (
              <span key={r.id} className="chip">
                {r.sid} {r.msg ? `· ${r.msg.slice(0, 40)}` : ""}
                <button
                  className="chip-x"
                  title="снять"
                  onClick={() => toggle(r, false)}
                >×</button>
              </span>
            ))}
          </p>
        </div>
      )}

      <h3>Собрать ruleset из выбранных</h3>
      <div className="toolbar">
        <input
          placeholder="версия/тег (пусто → авто vN)"
          title="пусто — автоинкремент v<N+1> по организации; непусто — произвольный тег"
          value={version}
          onChange={e => setVersion(e.target.value)}
          style={{ maxWidth: 220 }}
        />
        <input
          placeholder="комментарий (необязательно)"
          value={note}
          onChange={e => setNote(e.target.value)}
        />
        <button className="btn" onClick={build} disabled={!selected.size}>
          Собрать из выбранных ({selected.size})
        </button>
      </div>
      <p className="muted">
        API идемпотентен по содержимому: тот же состав правил вернёт существующую версию (200),
        новый — создаст (201).
      </p>
      {result && <p className="muted">{result}</p>}
        </>
      )}
    </>
  );
}

// RulesetRules — состав ruleset'а (drill-down, чанк 50):
// GET /rulesets/{id}/rules — sid/rev/msg/status каждого правила версии.
function RulesetRules({ id, version }: { id: string; version: string }) {
  const [rules, setRules] = React.useState<{ sid: number; rev: number; msg?: string; status?: string }[] | null>(null);
  const [err, setErr] = React.useState<unknown>(null);

  React.useEffect(() => {
    (async () => {
      try {
        const d = await apiGet<{ items: { sid: number; rev: number; msg?: string; status?: string }[] }>(`/rulesets/${id}/rules`);
        setRules(d.items || []);
      } catch (e) { setErr(e); }
    })();
  }, [id]);

  if (err) return <ErrorBox error={err} />;
  if (!rules) return <span className="muted">загрузка состава…</span>;
  return (
    <div className="panel">
      <p className="muted">Состав ruleset'а <b>{version}</b> — {rules.length} правил:</p>
      <table>
        <thead>
          <tr><th>SID</th><th>rev</th><th>msg</th><th>статус сейчас</th></tr>
        </thead>
        <tbody>
          {rules.map(r => (
            <tr key={r.sid}>
              <td>{r.sid}</td>
              <td className="muted">{r.rev}</td>
              <td>{r.msg || <span className="muted">—</span>}</td>
              <td>{r.status ? <Badge status={r.status} /> : <span className="muted">нет в репозитории</span>}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
