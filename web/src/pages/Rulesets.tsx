import React from "react";
import { apiGet, apiPostEx, Page, Rule, Ruleset } from "../api";
import { Badge, ErrorBox, fmtTime, short, SortState, SortTh, sortBy } from "../components";
import { useCan } from "../perms";

const LIMIT = 50;

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
