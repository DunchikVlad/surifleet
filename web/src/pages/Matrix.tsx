import React from "react";
import {
  apiGet, MatrixInstance, MatrixRule, RulesMatrix,
} from "../api";
import { Badge, ErrorBox } from "../components";

const LIMIT = 50;

const LEGEND: { st: string; label: string }[] = [
  { st: "loaded", label: "loaded — загружено движком" },
  { st: "failed", label: "failed — ошибка загрузки" },
  { st: "missing", label: "missing — в desired, нет в actual" },
  { st: "extra", label: "extra — есть в actual, нет в desired" },
  { st: "none", label: "— вне контекста инстанса" },
];

export default function Matrix({ active }: { active: boolean }) {
  const [rules, setRules] = React.useState<MatrixRule[]>([]);
  const [instances, setInstances] = React.useState<MatrixInstance[]>([]);
  const [cells, setCells] = React.useState<Record<string, string>>({});
  const [cursor, setCursor] = React.useState<string | null>(null);
  const [cellStatus, setCellStatus] = React.useState("");
  const [sid, setSid] = React.useState("");
  const [err, setErr] = React.useState<unknown>(null);
  const [loaded, setLoaded] = React.useState(false);

  // Фильтры в ref, чтобы load(append) не пересоздавался на каждый ввод.
  const filters = React.useRef({ cellStatus, sid });
  filters.current = { cellStatus, sid };
  const cursorRef = React.useRef<string | null>(null);

  const load = React.useCallback(async (append: boolean) => {
    const f = filters.current;
    let path = `/matrix/rules?limit=${LIMIT}`;
    if (f.cellStatus) path += `&cell_status=${encodeURIComponent(f.cellStatus)}`;
    if (f.sid.trim()) path += `&sid=${encodeURIComponent(f.sid.trim())}`;
    if (append && cursorRef.current) {
      path += `&rule_cursor=${encodeURIComponent(cursorRef.current)}`;
    }
    try {
      const d = await apiGet<RulesMatrix>(path);
      if (!append) {
        setRules(d.rules || []);
        setInstances(d.instances || []);
        const map: Record<string, string> = {};
        (d.cells || []).forEach(c => { map[c.sid + "|" + c.instance_id] = c.status; });
        setCells(map);
      } else {
        setRules(prev => [...prev, ...(d.rules || [])]);
        setInstances(prev => {
          const next = [...prev];
          (d.instances || []).forEach(i => {
            if (!next.some(x => x.instance_id === i.instance_id)) next.push(i);
          });
          return next;
        });
        setCells(prev => {
          const next = { ...prev };
          (d.cells || []).forEach(c => { next[c.sid + "|" + c.instance_id] = c.status; });
          return next;
        });
      }
      cursorRef.current = d.next_rule_cursor ?? null;
      setCursor(d.next_rule_cursor ?? null);
      setErr(null);
      setLoaded(true);
    } catch (e) { setErr(e); }
  }, []);

  React.useEffect(() => {
    if (active && !loaded) load(false);
  }, [active, loaded, load]);

  const refresh = () => { cursorRef.current = null; load(false); };

  // Сводка по ячейкам текущей страницы.
  const stats = React.useMemo(() => {
    const s: Record<string, number> = {};
    Object.values(cells).forEach(v => { s[v] = (s[v] || 0) + 1; });
    return s;
  }, [cells]);

  return (
    <>
      <h2>
        Матрица «правила × инстансы»
        <span className="matrix-legend">
          {LEGEND.map(l => (
            <span key={l.st} title={l.label}>
              <i className={`mx-cell mx-${l.st}`} /> {l.st}
            </span>
          ))}
        </span>
      </h2>
      <div className="toolbar">
        <select
          value={cellStatus}
          onChange={e => { setCellStatus(e.target.value); setLoaded(false); cursorRef.current = null; }}
        >
          <option value="">все ячейки</option>
          <option value="loaded">loaded</option>
          <option value="failed">failed</option>
          <option value="missing">missing</option>
          <option value="extra">extra</option>
        </select>
        <input
          placeholder="фильтр по sid…"
          value={sid}
          onChange={e => setSid(e.target.value)}
          onKeyDown={e => { if (e.key === "Enter") { cursorRef.current = null; setLoaded(false); } }}
        />
        <button className="btn" onClick={() => { cursorRef.current = null; setLoaded(false); }}>
          Найти
        </button>
        <button className="btn" onClick={refresh}>Обновить</button>
        <span className="muted">
          {Object.entries(stats).map(([k, v]) => `${k}:${v}`).join(" · ")}
        </span>
      </div>
      <ErrorBox error={err} />
      {loaded && !rules.length && !err && <p className="muted">Правил по фильтру нет.</p>}
      {loaded && rules.length > 0 && !instances.length && <p className="muted">Инстансов нет.</p>}
      {rules.length > 0 && instances.length > 0 && (
        <div className="mx-wrap">
          <table className="mx-table">
            <thead>
              <tr>
                <th>SID</th><th>Сообщение</th><th>Статус</th>
                {instances.map(i => (
                  <th key={i.instance_id} className="mx-inst" title={`${i.name} · ${i.instance_id}`}>
                    {i.hostname}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rules.map(r => (
                <tr key={r.sid}>
                  <td className="mx-sid">{r.sid}</td>
                  <td className="mx-msg" title={r.msg || ""}>{r.msg || ""}</td>
                  <td><Badge status={r.status} /></td>
                  {instances.map(i => {
                    const st = cells[r.sid + "|" + i.instance_id] || "none";
                    return (
                      <td key={i.instance_id} className="mx">
                        <i
                          className={`mx-cell mx-${st}`}
                          title={`sid ${r.sid} · ${i.hostname}: ${st === "none" ? "—" : st}`}
                        />
                      </td>
                    );
                  })}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {cursor && (
        <p>
          <button className="btn" onClick={() => load(true)}>Ещё {LIMIT} правил…</button>{" "}
          <span className="muted">показано {rules.length}</span>
        </p>
      )}
    </>
  );
}
