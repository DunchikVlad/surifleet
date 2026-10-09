import React from "react";
import { apiGet, Instance, Page } from "../api";
import { ErrorBox, fmtTime, short } from "../components";
import InstanceDetail from "./InstanceDetail";

// Instances — список инстансов; клик по строке — drill-down на страницу
// инстанса (InstanceDetail): состояние, история деплоев, логи агента.
export default function Instances({ active }: { active: boolean }) {
  const [items, setItems] = React.useState<Instance[] | null>(null);
  const [err, setErr] = React.useState<unknown>(null);
  const [detailId, setDetailId] = React.useState<string | null>(null);

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

  if (detailId) {
    return <InstanceDetail id={detailId} onBack={() => setDetailId(null)} />;
  }

  return (
    <>
      <h2>Инстансы Suricata</h2>
      <ErrorBox error={err} />
      {items && !items.length && <p className="muted">Инстансов нет.</p>}
      {items && items.length > 0 && (
        <table>
          <thead>
            <tr><th>Имя</th><th>ID</th><th>Версия</th><th>IP агента</th><th>Конфиг</th><th>Обновлён</th></tr>
          </thead>
          <tbody>
            {items.map(i => (
              <tr key={i.id} className="clickable" onClick={() => setDetailId(i.id)}>
                <td>{i.name}</td>
                <td className="muted">{short(i.id)}</td>
                <td>{i.suricata_version || "—"}</td>
                <td className="muted">{i.agent_ip || "offline"}</td>
                <td className="muted">{i.config_path}</td>
                <td className="muted">{fmtTime(i.updated_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {items && items.length > 0 && (
        <p className="muted">Клик по строке — страница инстанса: состояние, деплои, логи агента.</p>
      )}
    </>
  );
}
