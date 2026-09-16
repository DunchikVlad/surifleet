import React from "react";
import { apiGet, apiPostEx, Page, Rule, Ruleset } from "../api";
import { Badge, ErrorBox, fmtTime, short } from "../components";

const LIMIT = 50;

export default function Rulesets({ active }: { active: boolean }) {
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
    if (!version.trim()) { setResult("укажите версию ruleset"); return; }
    if (!selected.size) { setResult("не выбрано ни одного правила"); return; }
    try {
      // Явный rule_ids имеет приоритет над rule_filter (см. buildRuleset).
      const r = await apiPostEx<Ruleset>("/rulesets", {
        version: version.trim(),
        note: note.trim(),
        rule_ids: [...selected.keys()],
      });
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
            <tr><th>Версия</th><th>ID</th><th>Правил</th><th>SHA-256</th><th>Создан</th></tr>
          </thead>
          <tbody>
            {items.map(v => (
              <tr key={v.id}>
                <td><b>{v.version}</b></td>
                <td className="muted">{short(v.id)}</td>
                <td>{v.rule_count}</td>
                <td className="muted">{(v.sha256 || "").slice(0, 16)}…</td>
                <td className="muted">{fmtTime(v.created_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

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
          placeholder="версия, напр. 2026-09-16-01"
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
  );
}
