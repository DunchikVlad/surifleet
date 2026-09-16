import React from "react";
import {
  Agent, apiGet, DeployHistoryItem, Instance, InstanceState, LogEntry, Page,
} from "../api";
import { Badge, ErrorBox, fmtTime, short } from "../components";

// InstanceDetail — drill-down страница инстанса (требование А ТЗ):
// состояние/compliance, версия ruleset (desired/actual), активные и failed
// правила, история деплоев инстанса и последние логи агента его хоста.
export default function InstanceDetail({ id, onBack }: { id: string; onBack: () => void }) {
  const [inst, setInst] = React.useState<Instance | null>(null);
  const [state, setState] = React.useState<InstanceState | null>(null);
  const [history, setHistory] = React.useState<DeployHistoryItem[] | null>(null);
  const [agent, setAgent] = React.useState<Agent | null>(null);
  const [logs, setLogs] = React.useState<LogEntry[] | null>(null);
  const [err, setErr] = React.useState<unknown>(null);
  const [histErr, setHistErr] = React.useState<unknown>(null);
  const [logsErr, setLogsErr] = React.useState<unknown>(null);

  React.useEffect(() => {
    let dead = false;
    (async () => {
      try {
        const i = await apiGet<Instance>(`/instances/${id}`);
        if (dead) return;
        setInst(i);
        // Агент хоста инстанса → его последние логи.
        try {
          const ag = await apiGet<Page<Agent>>("/agents");
          const a = (ag.items || []).find(x => x.host_id === i.host_id) || null;
          if (dead) return;
          setAgent(a);
          if (a) {
            try {
              const lg = await apiGet<Page<LogEntry>>(`/agents/${a.id}/logs?limit=50`);
              if (!dead) setLogs(lg.items || []);
            } catch (e) { if (!dead) setLogsErr(e); }
          }
        } catch { /* агент/логи некритичны */ }
      } catch (e) { if (!dead) setErr(e); }
      try {
        const s = await apiGet<InstanceState>(`/instances/${id}/state`);
        if (!dead) setState(s);
      } catch (e) { if (!dead) setErr(e); }
      try {
        const hst = await apiGet<Page<DeployHistoryItem>>(`/instances/${id}/deploy_history?limit=20`);
        if (!dead) setHistory(hst.items || []);
      } catch (e) { if (!dead) setHistErr(e); }
    })();
    return () => { dead = true; };
  }, [id]);

  const failed = state?.actual?.failed_rules || state?.diff?.failed_rules || [];
  const missing = state?.diff?.missing_rules || [];
  const extra = state?.diff?.extra_rules || [];

  return (
    <>
      <h2>
        <button className="btn" onClick={onBack}>← к списку</button>
        Инстанс {inst ? inst.name : short(id)}
        {state?.compliance && <Badge status={state.compliance.status} />}
      </h2>
      <ErrorBox error={err} />

      {inst && (
        <div className="detail">
          <h3 style={{ marginTop: 0 }}>Параметры</h3>
          <table>
            <tbody>
              <tr><th>ID</th><td className="muted">{inst.id}</td></tr>
              <tr><th>Хост</th><td className="muted">{inst.host_id || "—"}</td></tr>
              <tr><th>Версия Suricata</th><td>{inst.suricata_version || "—"}</td></tr>
              <tr><th>Конфиг</th><td className="muted">{inst.config_path || "—"}</td></tr>
              <tr><th>Каталог правил</th><td className="muted">{inst.rules_dir || "—"}</td></tr>
              <tr><th>Интерфейсы</th><td>{(inst.capture_interfaces || []).join(", ") || "—"}</td></tr>
              <tr><th>Systemd unit</th><td className="muted">{inst.systemd_unit || "—"}</td></tr>
            </tbody>
          </table>
        </div>
      )}

      {state && (
        <div className="detail">
          <h3 style={{ marginTop: 0 }}>Состояние и соответствие</h3>
          <p>
            Compliance: <Badge status={state.compliance?.status || "unknown"} />{" "}
            <span className="muted">· обновлено {fmtTime(state.compliance?.updated_at)}</span>
          </p>
          <p>
            Desired ruleset:{" "}
            <code>{state.desired?.ruleset_version_id ? short(state.desired.ruleset_version_id) : "—"}</code>{" "}
            hash <code>{state.desired?.ruleset_hash ? state.desired.ruleset_hash.slice(0, 16) + "…" : "—"}</code>
            <br />
            Actual hash: <code>{state.actual?.ruleset_hash ? state.actual.ruleset_hash.slice(0, 16) + "…" : "—"}</code>{" "}
            <span className="muted">· отчёт {fmtTime(state.actual?.reported_at)}</span>
            <br />
            Загружено правил: <b>{state.actual?.loaded_count ?? "—"}</b>, не загрузилось:{" "}
            <b>{state.actual?.failed_count ?? "—"}</b>
          </p>
          {state.actual?.last_reload && (
            <p>
              Последний {state.actual.last_reload.action}:{" "}
              <Badge status={state.actual.last_reload.success ? "ok" : "err"} />{" "}
              <span className="muted">{state.actual.last_reload.message}</span>{" "}
              <span className="muted">· {fmtTime(state.actual.last_reload.finished_at || undefined)}</span>
            </p>
          )}

          {failed.length > 0 && (
            <>
              <h3>Failed правила ({failed.length})</h3>
              <table>
                <thead><tr><th>SID</th><th>Rev</th><th>Ошибка</th></tr></thead>
                <tbody>
                  {failed.map(f => (
                    <tr key={f.sid}>
                      <td>{f.sid}</td>
                      <td className="muted">{f.rev}</td>
                      <td style={{ wordBreak: "break-all" }}>{f.error_text}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </>
          )}
          {missing.length > 0 && (
            <p>Отсутствуют (missing, {missing.length}):{" "}
              <span className="muted">{missing.slice(0, 30).join(", ")}{missing.length > 30 ? "…" : ""}</span>
            </p>
          )}
          {extra.length > 0 && (
            <p>Лишние (extra, {extra.length}):{" "}
              <span className="muted">{extra.slice(0, 30).join(", ")}{extra.length > 30 ? "…" : ""}</span>
            </p>
          )}
        </div>
      )}

      <div className="detail">
        <h3 style={{ marginTop: 0 }}>История деплоев инстанса</h3>
        <ErrorBox error={histErr} />
        {history && !history.length && <p className="muted">Деплоев на этот инстанс не было.</p>}
        {history && history.length > 0 && (
          <table>
            <thead>
              <tr><th>Версия ruleset</th><th>Статус</th><th>Инициатор</th><th>Начало</th><th>Конец</th><th>Результат</th></tr>
            </thead>
            <tbody>
              {history.map((hitem, i) => (
                <tr key={hitem.deployment_id + i}>
                  <td><b>{hitem.ruleset_version}</b> <span className="muted">{short(hitem.deployment_id)}</span></td>
                  <td><Badge status={hitem.status} /></td>
                  <td className="muted">{hitem.initiated_by || "—"}</td>
                  <td className="muted">{fmtTime(hitem.started_at || undefined)}</td>
                  <td className="muted">{fmtTime(hitem.finished_at || undefined)}</td>
                  <td className="muted" style={{ wordBreak: "break-all" }}>{hitem.result || "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div className="detail">
        <h3 style={{ marginTop: 0 }}>
          Логи агента {agent ? `(${short(agent.id)} · ${agent.status})` : ""}
        </h3>
        {!agent && <p className="muted">Агент для хоста инстанса не найден.</p>}
        <ErrorBox error={logsErr} />
        {agent && logs && !logs.length && !logsErr && (
          <p className="muted">Записей нет — агент шлёт логи раз в 30 с.</p>
        )}
        {logs && logs.length > 0 && (
          <table>
            <thead><tr><th>Время</th><th>Уровень</th><th>Сообщение</th></tr></thead>
            <tbody>
              {logs.map((e, i) => (
                <tr key={i}>
                  <td className="muted" style={{ whiteSpace: "nowrap" }}>{fmtTime(e.ts)}</td>
                  <td><Badge status={e.level} /></td>
                  <td style={{ wordBreak: "break-all" }}>{e.message}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </>
  );
}
