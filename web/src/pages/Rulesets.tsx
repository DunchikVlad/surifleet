import React from "react";
import { apiGet, apiPost, Page, Ruleset } from "../api";
import { ErrorBox, fmtTime, short } from "../components";

export default function Rulesets({ active }: { active: boolean }) {
  const [items, setItems] = React.useState<Ruleset[] | null>(null);
  const [err, setErr] = React.useState<unknown>(null);
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

  const build = async () => {
    if (!version.trim()) { setResult("укажите версию"); return; }
    try {
      const v = await apiPost<Ruleset>("/rulesets", {
        version: version.trim(),
        note: note.trim(),
        rule_filter: { status: "enabled" },
      });
      setResult(`ruleset ${v.version}: ${v.rule_count} правил (${short(v.id)})`);
      load();
    } catch (e) { setResult((e as Error).message); }
  };

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
      <h3>Собрать новый ruleset (из включённых правил)</h3>
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
        <button className="btn" onClick={build}>Собрать</button>
      </div>
      {result && <p className="muted">{result}</p>}
    </>
  );
}
