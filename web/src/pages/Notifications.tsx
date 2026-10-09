import React from "react";
import { apiDelete, apiGet, apiPatch, apiPost, Page } from "../api";
import { ErrorBox, fmtTime } from "../components";
import { useCan } from "../perms";

// Notifications — вкладка «Уведомления» (чанк 85, пр. 2): каналы
// webhook/Telegram (CRUD + тест-отправка + вкл/выкл). Backend — чанки
// 83 (CRUD /notification_channels + /test) и 84 (движок дедупликации,
// переходы online/offline агентов).

interface NotificationChannel {
  id: string;
  name: string;
  type: string; // webhook | telegram
  config: Record<string, unknown>;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export default function Notifications({ active }: { active: boolean }) {
  const can = useCan();
  const [items, setItems] = React.useState<NotificationChannel[]>([]);
  const [err, setErr] = React.useState<unknown>(null);
  const [msg, setMsg] = React.useState("");
  const [loaded, setLoaded] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  // форма создания
  const [fName, setFName] = React.useState("");
  const [fType, setFType] = React.useState("webhook");
  const [fUrl, setFUrl] = React.useState("");
  const [fToken, setFToken] = React.useState("");
  const [fChatID, setFChatID] = React.useState("");
  // редактирование (только имя)
  const [editID, setEditID] = React.useState("");
  const [editName, setEditName] = React.useState("");

  const load = React.useCallback(async () => {
    try {
      const d = await apiGet<Page<NotificationChannel>>("/notification_channels?limit=100");
      setItems(d.items || []);
      setErr(null);
      setLoaded(true);
    } catch (e) { setErr(e); }
  }, []);

  React.useEffect(() => {
    if (active && !loaded) load();
  }, [active, loaded, load]);

  const add = async () => {
    const name = fName.trim();
    if (!name) return;
    let config: Record<string, unknown>;
    if (fType === "webhook") {
      if (!fUrl.trim()) { setMsg("укажите URL webhook'а"); return; }
      config = { url: fUrl.trim() };
    } else {
      if (!fToken.trim() || !fChatID.trim()) { setMsg("укажите bot_token и chat_id"); return; }
      config = { bot_token: fToken.trim(), chat_id: fChatID.trim() };
    }
    setBusy(true);
    try {
      await apiPost<NotificationChannel>("/notification_channels", { name, type: fType, config });
      setMsg("канал создан — нажмите «Тест» для живой проверки");
      setFName(""); setFUrl(""); setFToken(""); setFChatID("");
      await load();
    } catch (e) { setErr(e); } finally { setBusy(false); }
  };

  const toggle = async (c: NotificationChannel) => {
    try {
      await apiPatch<NotificationChannel>(`/notification_channels/${c.id}`, { enabled: !c.enabled });
      await load();
    } catch (e) { setErr(e); }
  };

  const rename = async (c: NotificationChannel) => {
    const name = editName.trim();
    if (!name || name === c.name) { setEditID(""); return; }
    try {
      await apiPatch<NotificationChannel>(`/notification_channels/${c.id}`, { name });
      setEditID("");
      await load();
    } catch (e) { setErr(e); }
  };

  const remove = async (c: NotificationChannel) => {
    if (!confirm(`Удалить канал «${c.name}»? Уведомления в него прекратятся.`)) return;
    try {
      await apiDelete(`/notification_channels/${c.id}`);
      setItems(prev => prev.filter(x => x.id !== c.id));
    } catch (e) { setErr(e); }
  };

  const test = async (c: NotificationChannel) => {
    setBusy(true);
    try {
      await apiPost<{ ok: boolean }>(`/notification_channels/${c.id}/test`, {});
      setMsg(`тестовое уведомление в «${c.name}» отправлено`);
    } catch (e) { setErr(e); setMsg(""); } finally { setBusy(false); }
  };

  // configHint — короткое описание конфигурации канала (без секретов;
  // bot_token наружу и не приходит — writeOnly).
  const configHint = (c: NotificationChannel) => {
    if (c.type === "webhook") return String(c.config?.url ?? "");
    if (c.type === "telegram") return "chat_id: " + String(c.config?.chat_id ?? "");
    return "";
  };

  return (
    <>
      <h2>Каналы уведомлений</h2>
      <p className="muted">
        События online/offline агентов рассылаются во включённые каналы с дедупликацией
        (одно событие — не чаще, чем раз в 10 минут на канал).
      </p>
      <ErrorBox error={err} />
      {msg && <p className="muted">{msg}</p>}

      {can("notifications.write") && (
        <div className="panel">
          <p>
            Новый канал:{" "}
            <input placeholder="имя" value={fName} onChange={e => setFName(e.target.value)} style={{ width: "12em" }} />{" "}
            <select value={fType} onChange={e => setFType(e.target.value)}>
              <option value="webhook">webhook</option>
              <option value="telegram">telegram</option>
            </select>{" "}
            {fType === "webhook" && (
              <input placeholder="https://example.org/hook" value={fUrl} onChange={e => setFUrl(e.target.value)} style={{ width: "24em" }} />
            )}
            {fType === "telegram" && (
              <>
                <input placeholder="bot_token (123:ABC…)" value={fToken} onChange={e => setFToken(e.target.value)} style={{ width: "18em" }} />{" "}
                <input placeholder="chat_id (-100…)" value={fChatID} onChange={e => setFChatID(e.target.value)} style={{ width: "10em" }} />
              </>
            )}{" "}
            <button className="btn primary" disabled={busy || !fName.trim()} onClick={add}>Создать</button>
          </p>
          <p className="muted">
            webhook: POST JSON {"{title, text, severity, fields}"} на URL; telegram: сообщение через Bot API.
            bot_token хранится на сервере и наружу не отдаётся.
          </p>
        </div>
      )}

      {items.length > 0 && (
        <table>
          <thead>
            <tr><th>Имя</th><th>Тип</th><th>Куда</th><th>Статус</th><th>Обновлён</th><th></th></tr>
          </thead>
          <tbody>
            {items.map(c => (
              <tr key={c.id}>
                <td>
                  {editID === c.id ? (
                    <>
                      <input value={editName} onChange={e => setEditName(e.target.value)} />{" "}
                      <button className="btn" onClick={() => rename(c)}>ок</button>{" "}
                      <button className="btn" onClick={() => setEditID("")}>отмена</button>
                    </>
                  ) : (
                    <b>{c.name}</b>
                  )}
                </td>
                <td className="muted">{c.type}</td>
                <td className="muted" style={{ maxWidth: "26em", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                  {configHint(c)}
                </td>
                <td>{c.enabled ? <span className="badge-ok">включён</span> : <span className="badge-err">выключен</span>}</td>
                <td className="muted">{fmtTime(c.updated_at)}</td>
                <td style={{ whiteSpace: "nowrap" }}>
                  {can("notifications.write") && (
                    <>
                      <button className="btn" disabled={busy} title="тестовое уведомление в канал" onClick={() => test(c)}>Тест</button>{" "}
                      <button className="btn" onClick={() => { setEditID(c.id); setEditName(c.name); }}>ред.</button>{" "}
                      <button className="btn" onClick={() => toggle(c)}>{c.enabled ? "выкл." : "вкл."}</button>{" "}
                      <button className="btn" onClick={() => remove(c)}>удалить</button>
                    </>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {loaded && items.length === 0 && <p className="muted">Каналов нет — создайте первый выше.</p>}
    </>
  );
}
