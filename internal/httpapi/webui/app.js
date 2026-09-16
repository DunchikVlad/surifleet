// SuriFleet MVP UI — ванильный JS поверх /api/v1 (DevAuth: без заголовков).
const API = "/api/v1";

async function api(path) {
  const r = await fetch(API + path);
  if (!r.ok) {
    let msg = "HTTP " + r.status;
    try { const j = await r.json(); if (j.error) msg += ": " + j.error.message; } catch {}
    throw new Error(msg);
  }
  return r.json();
}

async function apiPOST(path, body) {
  const r = await fetch(API + path, {
    method: "POST",
    headers: body ? { "Content-Type": "application/json" } : {},
    body: body ? JSON.stringify(body) : null,
  });
  if (!r.ok) {
    let msg = "HTTP " + r.status;
    try { const j = await r.json(); if (j.error) msg += ": " + j.error.message; } catch {}
    throw new Error(msg);
  }
  return r.json();
}

const esc = s => String(s ?? "").replace(/[&<>"']/g,
  c => ({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[c]));
const short = id => (id || "").slice(0, 8);
const badge = st => `<span class="badge badge-${esc(st)}">${esc(st)}</span>`;
const fmtTime = t => t ? new Date(t).toLocaleString("ru-RU") : "—";

// --- вкладки ---
document.querySelectorAll(".tab").forEach(btn => {
  btn.addEventListener("click", () => {
    document.querySelectorAll(".tab").forEach(b => b.classList.remove("active"));
    document.querySelectorAll(".page").forEach(p => p.classList.remove("active"));
    btn.classList.add("active");
    document.getElementById("tab-" + btn.dataset.tab).classList.add("active");
    loadTab(btn.dataset.tab);
  });
});

function loadTab(name) {
  ({ overview: loadOverview, instances: loadInstances, rules: loadRules,
     rulesets: loadRulesets, deployments: loadDeployments, logs: loadLogs })[name]?.();
}

// --- шапка: health + version ---
async function loadHeader() {
  try {
    const h = await api("/health");
    const el = document.getElementById("health");
    el.textContent = h.status;
    el.className = "badge badge-" + (h.status === "ok" ? "ok" : "warn");
  } catch { document.getElementById("health").textContent = "недоступен"; }
  try {
    const v = await api("/version");
    document.getElementById("version").textContent = v.version + " · " + v.commit;
  } catch {}
}

// --- Обзор: compliance флота + активные деплои ---
async function loadOverview() {
  const box = document.getElementById("compliance-cards");
  try {
    const c = await api("/fleet/compliance");
    const by = c.summary.by_status || {};
    const defs = [
      ["total_instances", "всего", ""], ["in_sync", "in sync", "ok"],
      ["pending", "pending", "warn"], ["partial", "partial", "partial"],
      ["drift", "drift", "err"], ["stale", "stale", "err"],
    ];
    box.innerHTML = defs.map(([k, lbl, cls]) => {
      const v = k === "total_instances" ? (c.summary.total_instances ?? 0) : (by[k] ?? 0);
      return `<div class="card ${cls}"><div class="num">${v}</div><div class="lbl">${lbl}</div></div>`;
    }).join("");
  } catch (e) { box.innerHTML = `<div class="error-box">${esc(e.message)}</div>`; }

  try {
    const d = await api("/deployments?limit=50");
    const active = (d.items || []).filter(x => ["pending", "running", "paused"].includes(x.status));
    document.getElementById("active-deployments").innerHTML = active.length
      ? renderDeployments(active) : '<p class="muted">Активных деплоев нет.</p>';
  } catch {}
}

// --- Инстансы + состояние ---
async function loadInstances() {
  const box = document.getElementById("instances-list");
  try {
    const d = await api("/instances?limit=100");
    if (!d.items?.length) { box.innerHTML = '<p class="muted">Инстансов нет.</p>'; return; }
    box.innerHTML = `<table><thead><tr>
      <th>Имя</th><th>ID</th><th>Версия</th><th>Конфиг</th><th>Обновлён</th></tr></thead>
      <tbody>${d.items.map(i => `<tr class="clickable" data-id="${i.id}">
        <td>${esc(i.name)}</td><td class="muted">${short(i.id)}</td>
        <td>${esc(i.suricata_version || "—")}</td><td class="muted">${esc(i.config_path)}</td>
        <td class="muted">${fmtTime(i.updated_at)}</td></tr>`).join("")}</tbody></table>`;
    box.querySelectorAll("tr.clickable").forEach(tr =>
      tr.addEventListener("click", () => loadInstanceState(tr.dataset.id)));
  } catch (e) { box.innerHTML = `<div class="error-box">${esc(e.message)}</div>`; }
}

async function loadInstanceState(id) {
  const det = document.getElementById("instance-detail");
  det.classList.remove("hidden");
  det.innerHTML = "<p class='muted'>Загрузка состояния…</p>";
  try {
    const s = await api(`/instances/${id}/state`);
    det.innerHTML = `<h3>Состояние инстанса ${short(id)}</h3>
      <p>Compliance: ${badge(s.compliance?.status || "unknown")}
         <span class="muted">· обновлено ${fmtTime(s.compliance?.updated_at)}</span></p>
      <p>Desired: <code>${esc((s.desired?.ruleset_hash || "—").slice(0, 16))}…</code>
         · Actual: <code>${esc((s.actual?.ruleset_hash || "—").slice(0, 16))}…</code>
         · загружено ${s.actual?.loaded_count ?? "—"}, не загрузилось ${s.actual?.failed_count ?? "—"}</p>
      <pre>${esc(JSON.stringify(s.diff || {}, null, 2))}</pre>`;
  } catch (e) { det.innerHTML = `<div class="error-box">${esc(e.message)}</div>`; }
}

// --- Правила ---
let rulesCursor = null;
async function loadRules(append) {
  const box = document.getElementById("rules-list");
  const more = document.getElementById("rules-more");
  if (!append) { rulesCursor = null; box.innerHTML = ""; }
  const st = document.getElementById("rules-status").value;
  const q = document.getElementById("rules-q").value.trim();
  let path = `/rules?limit=50`;
  if (st) path += `&status=${encodeURIComponent(st)}`;
  if (q) path += `&q=${encodeURIComponent(q)}`;
  if (rulesCursor) path += `&cursor=${encodeURIComponent(rulesCursor)}`;
  try {
    const d = await api(path);
    const rows = (d.items || []).map(r => `<tr>
      <td>${r.sid}</td><td>${esc(r.msg || "")}</td>
      <td>${badge(r.status)}</td><td class="muted">${esc(r.category || "—")}</td>
      <td class="muted">${esc(r.source_type)}</td>
      <td>${r.status === "enabled"
        ? `<button class="btn rule-toggle" data-id="${r.id}" data-to="disable">откл.</button>`
        : r.status === "disabled"
          ? `<button class="btn rule-toggle" data-id="${r.id}" data-to="enable">вкл.</button>`
          : ""}</td></tr>`).join("");
    if (append) box.querySelector("tbody")?.insertAdjacentHTML("beforeend", rows);
    else box.innerHTML = `<table><thead><tr>
      <th>SID</th><th>Сообщение</th><th>Статус</th><th>Категория</th><th>Источник</th><th></th>
      </tr></thead><tbody>${rows}</tbody></table>`;
    box.querySelectorAll(".rule-toggle:not([data-bound])").forEach(b => {
      b.dataset.bound = "1";
      b.addEventListener("click", () => ruleToggle(b.dataset.id, b.dataset.to));
    });
    rulesCursor = d.next_cursor;
    more.classList.toggle("hidden", !rulesCursor);
  } catch (e) { box.innerHTML = `<div class="error-box">${esc(e.message)}</div>`; }
}

// --- Ruleset'ы ---
async function loadRulesets() {
  const box = document.getElementById("rulesets-list");
  try {
    const d = await api("/rulesets?limit=50");
    if (!d.items?.length) { box.innerHTML = '<p class="muted">Ruleset\'ов нет.</p>'; return; }
    box.innerHTML = `<table><thead><tr>
      <th>Версия</th><th>ID</th><th>Правил</th><th>SHA-256</th><th>Создан</th></tr></thead>
      <tbody>${d.items.map(v => `<tr>
        <td><b>${esc(v.version)}</b></td><td class="muted">${short(v.id)}</td>
        <td>${v.rule_count}</td><td class="muted">${esc((v.sha256 || "").slice(0, 16))}…</td>
        <td class="muted">${fmtTime(v.created_at)}</td></tr>`).join("")}</tbody></table>`;
  } catch (e) { box.innerHTML = `<div class="error-box">${esc(e.message)}</div>`; }
}

// --- Деплои ---
function renderDeployments(items) {
  const actions = d => {
    const b = [];
    if (d.status === "running") b.push(`<button class="btn dep-act" data-id="${d.id}" data-act="pause">пауза</button>`);
    if (d.status === "paused" || d.status === "failed") b.push(`<button class="btn dep-act" data-id="${d.id}" data-act="resume">resume</button>`);
    if (["pending", "running", "paused"].includes(d.status)) b.push(`<button class="btn dep-act" data-id="${d.id}" data-act="cancel">отмена</button>`);
    return b.join(" ");
  };
  return `<table><thead><tr>
    <th>ID</th><th>Ruleset</th><th>Статус</th><th>Прогресс</th><th>Создан</th><th>Действия</th></tr></thead>
    <tbody>${items.map(d => {
      const p = d.progress || {};
      const pct = p.total ? Math.round(100 * (p.succeeded || 0) / p.total) : 0;
      return `<tr class="clickable dep" data-id="${d.id}">
        <td class="muted">${short(d.id)}</td><td class="muted">${short(d.ruleset_version_id)}</td>
        <td>${badge(d.status)}</td>
        <td><span class="progress"><i class="${p.failed ? "has-failed" : ""}" style="width:${pct}%"></i></span>
            <span class="muted"> ${p.succeeded || 0}/${p.total || 0}${p.failed ? " · failed " + p.failed : ""}</span></td>
        <td class="muted">${fmtTime(d.created_at)}</td>
        <td>${actions(d)}</td></tr>
        <tr class="hidden tasks" id="tasks-${d.id}"><td colspan="6"></td></tr>`;
    }).join("")}</tbody></table>`;
}

async function loadDeployments() {
  const box = document.getElementById("deployments-list");
  try {
    const d = await api("/deployments?limit=50");
    box.innerHTML = d.items?.length
      ? renderDeployments(d.items) : '<p class="muted">Деплоев нет.</p>';
    box.querySelectorAll("tr.dep").forEach(tr =>
      tr.addEventListener("click", () => toggleTasks(tr.dataset.id)));
    box.querySelectorAll(".dep-act").forEach(b =>
      b.addEventListener("click", async ev => {
        ev.stopPropagation();
        await deploymentAction(b.dataset.id, b.dataset.act);
      }));
  } catch (e) { box.innerHTML = `<div class="error-box">${esc(e.message)}</div>`; }
  fillDeploymentForm();
}

async function deploymentAction(id, act) {
  if (act === "cancel" && !confirm("Отменить деплой " + short(id) + "?")) return;
  try {
    await apiPOST(`/deployments/${id}/${act}`);
    loadDeployments();
  } catch (e) { alert("Действие не выполнено: " + e.message); }
}

async function fillDeploymentForm() {
  const rsSel = document.getElementById("dep-ruleset");
  const inSel = document.getElementById("dep-instance");
  try {
    const [rs, ins] = await Promise.all([
      api("/rulesets?limit=50"), api("/instances?limit=100")]);
    rsSel.innerHTML = (rs.items || []).map(v =>
      `<option value="${v.id}">${esc(v.version)} · ${v.rule_count} правил</option>`).join("");
    inSel.innerHTML = (ins.items || []).map(i =>
      `<option value="${i.id}">${esc(i.name)} · ${short(i.id)}</option>`).join("");
  } catch {}
}

async function createDeployment() {
  const out = document.getElementById("dep-result");
  const rulesetId = document.getElementById("dep-ruleset").value;
  const instanceId = document.getElementById("dep-instance").value;
  if (!rulesetId || !instanceId) { out.textContent = "выберите ruleset и инстанс"; return; }
  try {
    const d = await apiPOST("/deployments", {
      ruleset_id: rulesetId,
      targeting: { mode: "specific_instances", instance_ids: [instanceId] },
      wave: { batch_size: 10, canary: false },
    });
    out.textContent = "деплой " + short(d.id) + " создан";
    loadDeployments();
  } catch (e) { out.textContent = e.message; }
}

async function buildRuleset() {
  const out = document.getElementById("rs-result");
  const version = document.getElementById("rs-version").value.trim();
  const note = document.getElementById("rs-note").value.trim();
  if (!version) { out.textContent = "укажите версию"; return; }
  try {
    const v = await apiPOST("/rulesets", {
      version, note, rule_filter: { status: "enabled" } });
    out.textContent = `ruleset ${v.version}: ${v.rule_count} правил (${short(v.id)})`;
    loadRulesets();
  } catch (e) { out.textContent = e.message; }
}

async function ruleToggle(id, action) {
  try {
    await apiPOST("/rules/bulk", { ids: [id], action });
    loadRules(false);
  } catch (e) { alert("Операция не выполнена: " + e.message); }
}

async function toggleTasks(id) {
  const row = document.getElementById("tasks-" + id);
  if (!row.classList.contains("hidden")) { row.classList.add("hidden"); return; }
  row.classList.remove("hidden");
  const cell = row.querySelector("td");
  cell.innerHTML = "<span class='muted'>Загрузка задач…</span>";
  try {
    const d = await api(`/deployments/${id}/tasks`);
    cell.innerHTML = `<table><thead><tr>
      <th>Инстанс</th><th>Волна</th><th>Статус</th><th>Попытки</th><th>Результат / ошибка</th></tr></thead>
      <tbody>${(d.items || []).map(t => `<tr>
        <td class="muted">${short(t.instance_id)}</td><td>${t.wave}</td>
        <td>${badge(t.status)}</td><td>${t.attempts}/${t.max_attempts}</td>
        <td class="muted">${esc(t.error || (t.result ? `loaded=${t.result.loaded_count}` : "—")).slice(0, 300)}</td>
      </tr>`).join("")}</tbody></table>`;
  } catch (e) { cell.innerHTML = `<div class="error-box">${esc(e.message)}</div>`; }
}

// --- Логи агентов (chunk 13c) ---
async function fillLogsAgents() {
  const sel = document.getElementById("logs-agent");
  const prev = sel.value;
  try {
    const d = await api("/agents");
    sel.innerHTML = (d.items || []).map(a =>
      `<option value="${a.id}">${esc(a.hostname || short(a.host_id))} · ${esc(a.status)} · ${short(a.id)}</option>`).join("");
    if (prev && [...sel.options].some(o => o.value === prev)) sel.value = prev;
  } catch {}
}

async function loadLogs() {
  const box = document.getElementById("logs-list");
  const status = document.getElementById("logs-status");
  if (!document.getElementById("logs-agent").options.length) await fillLogsAgents();
  const agentId = document.getElementById("logs-agent").value;
  if (!agentId) { box.innerHTML = '<p class="muted">Агентов нет.</p>'; return; }
  const limit = document.getElementById("logs-limit").value;
  try {
    const d = await api(`/agents/${agentId}/logs?limit=${limit}`);
    const items = d.items || [];
    status.textContent = "обновлено " + new Date().toLocaleTimeString("ru-RU");
    if (!items.length) { box.innerHTML = '<p class="muted">Записей нет — агент шлёт логи раз в 30 с.</p>'; return; }
    box.innerHTML = `<table><thead><tr>
      <th>Время</th><th>Уровень</th><th>Сообщение</th></tr></thead>
      <tbody>${items.map(e => `<tr>
        <td class="muted" style="white-space:nowrap">${fmtTime(e.ts)}</td>
        <td>${badge(e.level)}</td>
        <td style="word-break:break-all">${esc(e.message)}</td>
      </tr>`).join("")}</tbody></table>`;
  } catch (e) { box.innerHTML = `<div class="error-box">${esc(e.message)}</div>`; }
}

// --- init ---
document.getElementById("refresh-overview").addEventListener("click", loadOverview);
document.getElementById("rules-search").addEventListener("click", () => loadRules(false));
document.getElementById("rules-more").addEventListener("click", () => loadRules(true));
document.getElementById("rules-q").addEventListener("keydown", e => { if (e.key === "Enter") loadRules(false); });
document.getElementById("rs-build").addEventListener("click", buildRuleset);
document.getElementById("dep-create").addEventListener("click", createDeployment);
document.getElementById("logs-refresh").addEventListener("click", loadLogs);
document.getElementById("logs-agent").addEventListener("change", loadLogs);
document.getElementById("logs-limit").addEventListener("change", loadLogs);

loadHeader();
loadOverview();
setInterval(() => {
  if (document.getElementById("tab-overview").classList.contains("active")) loadOverview();
  loadHeader();
}, 15000);
// Логи — чаще (10 с), чтобы было ближе к «живому» хвосту.
setInterval(() => {
  if (document.getElementById("tab-logs").classList.contains("active")
      && document.getElementById("logs-auto").checked) loadLogs();
}, 10000);
