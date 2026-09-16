import React from "react";
import {
  apiGet, apiPost, Deployment, DeployTask, Instance, Page, Ruleset,
} from "../api";
import { Badge, ErrorBox, fmtTime, Progress, short } from "../components";

function TaskList({ depId }: { depId: string }) {
  const [tasks, setTasks] = React.useState<DeployTask[] | null>(null);
  const [err, setErr] = React.useState<unknown>(null);

  React.useEffect(() => {
    (async () => {
      try {
        const d = await apiGet<Page<DeployTask>>(`/deployments/${depId}/tasks`);
        setTasks(d.items || []);
      } catch (e) { setErr(e); }
    })();
  }, [depId]);

  if (err) return <ErrorBox error={err} />;
  if (!tasks) return <span className="muted">Загрузка задач…</span>;
  return (
    <table>
      <thead>
        <tr><th>Инстанс</th><th>Волна</th><th>Статус</th><th>Попытки</th><th>Результат / ошибка</th></tr>
      </thead>
      <tbody>
        {tasks.map((t, i) => (
          <tr key={i}>
            <td className="muted">{short(t.instance_id)}</td>
            <td>{t.wave}</td>
            <td><Badge status={t.status} /></td>
            <td>{t.attempts}/{t.max_attempts}</td>
            <td className="muted">
              {(t.error || (t.result ? `loaded=${t.result.loaded_count}` : "—")).slice(0, 300)}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function DeploymentRow({
  d, onAction,
}: { d: Deployment; onAction?: (id: string, act: string) => void }) {
  const [open, setOpen] = React.useState(false);
  const acts: { act: string; label: string }[] = [];
  if (onAction) {
    if (d.status === "running") acts.push({ act: "pause", label: "пауза" });
    if (d.status === "paused" || d.status === "failed") acts.push({ act: "resume", label: "resume" });
    if (["pending", "running", "paused"].includes(d.status)) acts.push({ act: "cancel", label: "отмена" });
  }
  return (
    <>
      <tr className="clickable" onClick={() => setOpen(o => !o)}>
        <td className="muted">{short(d.id)}</td>
        <td className="muted">{short(d.ruleset_version_id)}</td>
        <td><Badge status={d.status} /></td>
        <td><Progress p={d.progress} /></td>
        <td className="muted">{fmtTime(d.created_at)}</td>
        <td>
          {acts.map(a => (
            <button
              key={a.act}
              className="btn"
              style={{ marginRight: 6 }}
              onClick={ev => { ev.stopPropagation(); onAction!(d.id, a.act); }}
            >
              {a.label}
            </button>
          ))}
        </td>
      </tr>
      {open && (
        <tr>
          <td colSpan={6}><TaskList depId={d.id} /></td>
        </tr>
      )}
    </>
  );
}

// DeploymentTable используется и на «Обзоре» (без onAction — только просмотр).
export function DeploymentTable({
  items, onAction,
}: { items: Deployment[]; onAction?: (id: string, act: string) => void }) {
  return (
    <table>
      <thead>
        <tr><th>ID</th><th>Ruleset</th><th>Статус</th><th>Прогресс</th><th>Создан</th><th>Действия</th></tr>
      </thead>
      <tbody>
        {items.map(d => <DeploymentRow key={d.id} d={d} onAction={onAction} />)}
      </tbody>
    </table>
  );
}

export default function Deployments({ active }: { active: boolean }) {
  const [items, setItems] = React.useState<Deployment[] | null>(null);
  const [err, setErr] = React.useState<unknown>(null);
  const [rulesets, setRulesets] = React.useState<Ruleset[]>([]);
  const [instances, setInstances] = React.useState<Instance[]>([]);
  const [rsId, setRsId] = React.useState("");
  const [instId, setInstId] = React.useState("");
  const [result, setResult] = React.useState("");

  const load = React.useCallback(async () => {
    try {
      const d = await apiGet<Page<Deployment>>("/deployments?limit=50");
      setItems(d.items || []);
      setErr(null);
    } catch (e) { setErr(e); }
    try {
      const [rs, ins] = await Promise.all([
        apiGet<Page<Ruleset>>("/rulesets?limit=50"),
        apiGet<Page<Instance>>("/instances?limit=100"),
      ]);
      setRulesets(rs.items || []);
      setInstances(ins.items || []);
    } catch { /* форма некритична */ }
  }, []);

  React.useEffect(() => { if (active) load(); }, [active, load]);

  const action = async (id: string, act: string) => {
    if (act === "cancel" && !window.confirm("Отменить деплой " + short(id) + "?")) return;
    try {
      await apiPost(`/deployments/${id}/${act}`);
      load();
    } catch (e) { alert("Действие не выполнено: " + (e as Error).message); }
  };

  const create = async () => {
    if (!rsId || !instId) { setResult("выберите ruleset и инстанс"); return; }
    try {
      const d = await apiPost<Deployment>("/deployments", {
        ruleset_id: rsId,
        targeting: { mode: "specific_instances", instance_ids: [instId] },
        wave: { batch_size: 10, canary: false },
      });
      setResult("деплой " + short(d.id) + " создан");
      load();
    } catch (e) { setResult((e as Error).message); }
  };

  return (
    <>
      <h2>Деплои</h2>
      <ErrorBox error={err} />
      {items && !items.length && <p className="muted">Деплоев нет.</p>}
      {items && items.length > 0 && <DeploymentTable items={items} onAction={action} />}
      <h3>Новый деплой</h3>
      <div className="toolbar">
        <select value={rsId} onChange={e => setRsId(e.target.value)}>
          <option value="">— ruleset —</option>
          {rulesets.map(v => (
            <option key={v.id} value={v.id}>{v.version} · {v.rule_count} правил</option>
          ))}
        </select>
        <select value={instId} onChange={e => setInstId(e.target.value)}>
          <option value="">— инстанс —</option>
          {instances.map(i => (
            <option key={i.id} value={i.id}>{i.name} · {short(i.id)}</option>
          ))}
        </select>
        <button className="btn" onClick={create}>Создать деплой</button>
      </div>
      {result && <p className="muted">{result}</p>}
    </>
  );
}
