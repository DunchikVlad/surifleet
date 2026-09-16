import React from "react";
import { apiGet, Instance, InstanceState, Page } from "../api";
import { Badge, ErrorBox, fmtTime, short } from "../components";

export default function Instances({ active }: { active: boolean }) {
  const [items, setItems] = React.useState<Instance[] | null>(null);
  const [err, setErr] = React.useState<unknown>(null);
  const [state, setState] = React.useState<InstanceState | null>(null);
  const [stateId, setStateId] = React.useState<string | null>(null);
  const [stateErr, setStateErr] = React.useState<unknown>(null);

  React.useEffect(() => {
    if (!active) return;
    (async () => {
      try {
        const d = await apiGet<Page<Instance>>("/instances?limit=100");
        setItems(d.items || []);
        setErr(null);
      } catch (e) { setErr(e); }
    })();
  }, [active]);

  const loadState = async (id: string) => {
    setStateId(id);
    setState(null);
    setStateErr(null);
    try {
      setState(await apiGet<InstanceState>(`/instances/${id}/state`));
    } catch (e) { setStateErr(e); }
  };

  return (
    <>
      <h2>Инстансы Suricata</h2>
      <ErrorBox error={err} />
      {items && !items.length && <p className="muted">Инстансов нет.</p>}
      {items && items.length > 0 && (
        <table>
          <thead>
            <tr><th>Имя</th><th>ID</th><th>Версия</th><th>Конфиг</th><th>Обновлён</th></tr>
          </thead>
          <tbody>
            {items.map(i => (
              <tr key={i.id} className="clickable" onClick={() => loadState(i.id)}>
                <td>{i.name}</td>
                <td className="muted">{short(i.id)}</td>
                <td>{i.suricata_version || "—"}</td>
                <td className="muted">{i.config_path}</td>
                <td className="muted">{fmtTime(i.updated_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {stateId && (
        <div className="detail">
          <h3>Состояние инстанса {short(stateId)}</h3>
          <ErrorBox error={stateErr} />
          {!state && !stateErr && <p className="muted">Загрузка состояния…</p>}
          {state && (
            <>
              <p>
                Compliance: <Badge status={state.compliance?.status || "unknown"} />{" "}
                <span className="muted">· обновлено {fmtTime(state.compliance?.updated_at)}</span>
              </p>
              <p>
                Desired: <code>{(state.desired?.ruleset_hash || "—").slice(0, 16)}…</code> ·
                Actual: <code>{(state.actual?.ruleset_hash || "—").slice(0, 16)}…</code> ·
                загружено {state.actual?.loaded_count ?? "—"}, не загрузилось{" "}
                {state.actual?.failed_count ?? "—"}
              </p>
              <pre>{JSON.stringify(state.diff || {}, null, 2)}</pre>
            </>
          )}
        </div>
      )}
    </>
  );
}
