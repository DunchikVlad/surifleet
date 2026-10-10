import React from "react";
import {
  Agent, apiGet, apiPost, DeployHistoryItem, Instance, InstanceState, LogEntry, Page,
} from "../api";
import { Badge, ErrorBox, fmtTime, short } from "../components";
import { useCan } from "../perms";

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
  const can = useCan();

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
              <tr><th>IP агента</th><td className="muted">{inst.agent_ip || "offline"}</td></tr>
              <tr><th>Версия Suricata</th><td>{inst.suricata_version || "—"}</td></tr>
              <tr><th>Конфиг</th><td className="muted">{inst.config_path || "—"}</td></tr>
              <tr><th>Каталог правил</th><td className="muted">{inst.rules_dir || "—"}</td></tr>
              <tr><th>Интерфейсы</th><td>{(inst.capture_interfaces || []).join(", ") || "—"}</td></tr>
              <tr><th>Systemd unit</th><td className="muted">{inst.systemd_unit || "—"}</td></tr>
              <tr><th>Сервис Suricata</th><td>{inst.service_state || "—"}{inst.service_pid ? ` (pid ${inst.service_pid})` : ""}</td></tr>
              <tr><th>Сервис агента</th><td>surifleet-agent{inst.agent_pid ? ` (pid ${inst.agent_pid})` : ""}</td></tr>
            </tbody>
          </table>
          {can("hosts.write") && <ServiceActions instanceId={id} onDone={() => {
            // Перечитать карточку: service_state/pid обновятся из heartbeat хаба.
            apiGet<Instance>(`/instances/${id}`).then(setInst).catch(() => {});
          }} />}
          {can("hosts.write") && <LogRotationPanel instanceId={id} />}
          {can("hosts.write") && <PackagesPanel instanceId={id} onChanged={() => {
            apiGet<Instance>(`/instances/${id}`).then(setInst).catch(() => {});
          }} />}
          {agent && can("agents.read") && <BundleButton agentId={agent.id} />}
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

// ServiceActionResult — ответ POST /instances/{id}/service_action (чанк 105).
interface ServiceActionResult {
  task_id: string;
  instance_id: string;
  action: string;
  service_state: string;
}

// ServiceActions — действия над сервисом Suricata инстанса (чанк 105, п. 7 ТЗ
// «действия из UI — перезапуск сервиса»): restart/reload/start/stop через
// агента (на агенте gated capability service_mgmt). Ответ синхронный —
// restart может занять десятки секунд (graceful stop).
function ServiceActions({ instanceId, onDone }: { instanceId: string; onDone: () => void }) {
  const [busy, setBusy] = React.useState<string | null>(null);
  const [result, setResult] = React.useState<string | null>(null);
  const [err, setErr] = React.useState<unknown>(null);

  const run = async (action: string) => {
    if (busy) return;
    if ((action === "stop" || action === "restart") &&
        !window.confirm(`${action === "stop" ? "Остановить" : "Перезапустить"} Suricata на этом инстансе?`)) return;
    setBusy(action); setErr(null); setResult(null);
    try {
      const r = await apiPost<ServiceActionResult>(`/instances/${instanceId}/service_action`, { action });
      setResult(`${action}: выполнено — сервис ${r.service_state || "?"}`);
      onDone();
    } catch (e) { setErr(e); }
    finally { setBusy(null); }
  };

  return (
    <div style={{ marginTop: 8 }}>
      <b>Действия над сервисом:</b>{" "}
      {["restart", "reload", "start", "stop"].map(a => (
        <button key={a} className="btn" disabled={busy !== null} onClick={() => run(a)}
          style={{ marginRight: 6 }}>
          {busy === a ? `${a}…` : a}
        </button>
      ))}
      {result && <span className="muted"> {result}</span>}
      <ErrorBox error={err} />
      <div className="muted" style={{ marginTop: 4, fontSize: 12 }}>
        Требуется capability service_mgmt на хосте; restart может занять до минуты (graceful stop Suricata).
      </div>
    </div>
  );
}

// LogRotationResult — ответ POST /instances/{id}/log_rotation (чанк 111).
interface LogRotationResult {
  task_id: string;
  instance_id: string;
  action: string;
  files: { name: string; size_bytes: number; rotated: boolean }[];
  rotated_count: number;
  freed_bytes: number;
  archived: string[];
}

// LogRotationPanel — ротация логов Suricata инстанса (чанк 111, capability
// log_rotation): отчёт (состав/размеры) или rotate — copytruncate активных
// файлов в log_dir (архив «имя.ГГГГММДД-ЧЧММСС», оригинал усекается).
function LogRotationPanel({ instanceId }: { instanceId: string }) {
  const [busy, setBusy] = React.useState<string | null>(null);
  const [res, setRes] = React.useState<LogRotationResult | null>(null);
  const [err, setErr] = React.useState<unknown>(null);

  const run = async (action: "report" | "rotate") => {
    if (busy) return;
    if (action === "rotate" &&
        !window.confirm("Ротировать логи Suricata на этом инстансе? Активные файлы будут скопированы в архив и усечены.")) return;
    setBusy(action); setErr(null); setRes(null);
    try {
      setRes(await apiPost<LogRotationResult>(`/instances/${instanceId}/log_rotation`, { action }));
    } catch (e) { setErr(e); }
    finally { setBusy(null); }
  };

  return (
    <div style={{ marginTop: 8 }}>
      <b>Ротация логов:</b>{" "}
      <button className="btn" disabled={busy !== null} onClick={() => run("report")} style={{ marginRight: 6 }}>
        {busy === "report" ? "Отчёт…" : "Отчёт"}
      </button>
      <button className="btn" disabled={busy !== null} onClick={() => run("rotate")}>
        {busy === "rotate" ? "Ротирую…" : "Ротировать"}
      </button>
      {res && (
        <div className="muted" style={{ marginTop: 4 }}>
          {res.action === "report" ? "Подлежит ротации" : "Ротировано"}: {res.rotated_count} файлов
          {res.action === "rotate" && res.freed_bytes > 0 &&
            `, освобождено ${(res.freed_bytes / 1024 / 1024).toFixed(1)} МБ`}
          {res.archived && res.archived.length > 0 && <div>Архивы: {res.archived.join(", ")}</div>}
          {res.files && res.files.length > 0 && (
            <div>
              Файлы журнала:{" "}
              {res.files.map(f => `${f.name} (${(f.size_bytes / 1024).toFixed(0)} КБ${f.rotated ? " — ротирован" : ""})`).join("; ")}
            </div>
          )}
        </div>
      )}
      <ErrorBox error={err} />
      <div className="muted" style={{ marginTop: 4, fontSize: 12 }}>
        Требуется capability log_rotation; copytruncate — движок продолжает писать в тот же дескриптор, рестарт не нужен.
      </div>
    </div>
  );
}

// PackagesResult — ответ POST /instances/{id}/packages (чанк 111).
interface PackagesResult {
  task_id: string;
  instance_id: string;
  package: string;
  action: string;
  installed: boolean;
  version: string;
  output: string;
}

// PackagesPanel — управление пакетами suricata/suricata-update на сенсоре
// (чанк 111, capability packages): check/install/update/remove через apt.
function PackagesPanel({ instanceId, onChanged }: { instanceId: string; onChanged: () => void }) {
  const [pkg, setPkg] = React.useState("suricata");
  const [busy, setBusy] = React.useState<string | null>(null);
  const [res, setRes] = React.useState<PackagesResult | null>(null);
  const [err, setErr] = React.useState<unknown>(null);

  const run = async (action: string) => {
    if (busy) return;
    if ((action === "remove" || action === "update") &&
        !window.confirm(`${action === "remove" ? "Удалить" : "Обновить"} пакет ${pkg} на сенсоре?`)) return;
    setBusy(action); setErr(null); setRes(null);
    try {
      const r = await apiPost<PackagesResult>(`/instances/${instanceId}/packages`, { package: pkg, action });
      setRes(r);
      if (action !== "check") onChanged();
    } catch (e) { setErr(e); }
    finally { setBusy(null); }
  };

  return (
    <div style={{ marginTop: 8 }}>
      <b>Пакеты:</b>{" "}
      <select value={pkg} onChange={e => setPkg(e.target.value)} disabled={busy !== null} style={{ marginRight: 6 }}>
        <option value="suricata">suricata</option>
        <option value="suricata-update">suricata-update</option>
      </select>
      {["check", "install", "update", "remove"].map(a => (
        <button key={a} className="btn" disabled={busy !== null} onClick={() => run(a)} style={{ marginRight: 6 }}>
          {busy === a ? `${a}…` : a}
        </button>
      ))}
      {res && (
        <span className="muted">
          {" "}{res.package}: {res.installed ? `установлен (${res.version || "версия ?"})` : "не установлен"}
        </span>
      )}
      <ErrorBox error={err} />
      <div className="muted" style={{ marginTop: 4, fontSize: 12 }}>
        Требуется capability packages; install/update/remove идут через apt и могут занять несколько минут.
      </div>
    </div>
  );
}

// BundleResult — ответ POST /agents/{id}/bundle (чанк 106).
interface BundleResult {
  task_id: string;
  agent_id: string;
  bundle_key: string;
  size_bytes: number;
  download_url: string;
}

// BundleButton — сбор диагностического бандла хоста одной кнопкой
// (чанк 106, п. 7 ТЗ): tar.gz (логи агента/Suricata, конфиги, sysinfo)
// загружается агентом в S3, ответ — presigned ссылка скачивания (TTL 15 мин).
function BundleButton({ agentId }: { agentId: string }) {
  const [busy, setBusy] = React.useState(false);
  const [res, setRes] = React.useState<BundleResult | null>(null);
  const [err, setErr] = React.useState<unknown>(null);

  const run = async () => {
    if (busy) return;
    setBusy(true); setErr(null); setRes(null);
    try {
      setRes(await apiPost<BundleResult>(`/agents/${agentId}/bundle`, {}));
    } catch (e) { setErr(e); }
    finally { setBusy(false); }
  };

  return (
    <div style={{ marginTop: 8 }}>
      <button className="btn" disabled={busy} onClick={run}>
        {busy ? "Собираю бандл…" : "Диагностический бандл"}
      </button>
      {res && (
        <span>
          {" "}<a href={res.download_url} download>Скачать tar.gz</a>{" "}
          <span className="muted">({(res.size_bytes / 1024).toFixed(0)} КБ, ссылка на 15 мин)</span>
        </span>
      )}
      <ErrorBox error={err} />
      <div className="muted" style={{ marginTop: 4, fontSize: 12 }}>
        Логи агента и Suricata (хвосты до 1 МБ), suricata.yaml, системная информация хоста — для тикета в поддержку.
      </div>
    </div>
  );
}
