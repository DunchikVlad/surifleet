import React from "react";
import { apiGet, logout, getToken, Health, Version, CurrentUser } from "./api";
import { Badge, useInterval } from "./components";
import { PermsContext } from "./perms";
import Overview from "./pages/Overview";
import Instances from "./pages/Instances";
import Rules from "./pages/Rules";
import Rulesets from "./pages/Rulesets";
import Deployments from "./pages/Deployments";
import Logs from "./pages/Logs";
import Matrix from "./pages/Matrix";
import Iocs from "./pages/Iocs";
import Feeds from "./pages/Feeds";
import Login from "./pages/Login";
import Users from "./pages/Users";
import Roles from "./pages/Roles";
import Tokens from "./pages/Tokens";
import Audit from "./pages/Audit";
import Metrics from "./pages/Metrics";
import Configs from "./pages/Configs";
import Sso from "./pages/Sso";

type TabName =
  | "overview" | "instances" | "rules" | "rulesets"
  | "deployments" | "logs" | "matrix" | "iocs" | "feeds"
  | "users" | "roles" | "tokens" | "audit" | "metrics" | "configs" | "sso";

// perm — разрешение для показа вкладки (ТЗ: UI скрывает недоступное;
// авторизация всё равно на backend). Пусто — видна всем.
const TABS: { name: TabName; label: string; perm?: string }[] = [
  { name: "overview", label: "Обзор" },
  { name: "instances", label: "Инстансы" },
  { name: "rules", label: "Правила" },
  { name: "rulesets", label: "Ruleset'ы" },
  { name: "deployments", label: "Деплои" },
  { name: "logs", label: "Логи" },
  { name: "metrics", label: "Метрики" },
  { name: "matrix", label: "Матрица" },
  { name: "iocs", label: "IOC" },
  { name: "feeds", label: "Фиды" },
  { name: "configs", label: "Конфигурации", perm: "config.read" },
  { name: "users", label: "Пользователи", perm: "users.read" },
  { name: "roles", label: "Роли", perm: "roles.read" },
  { name: "tokens", label: "Токены", perm: "tokens.read" },
  { name: "sso", label: "SSO", perm: "sso.read" },
  { name: "audit", label: "Аудит", perm: "audit.read" },
];

// Страницы не размонтируем при переключении (display:none), чтобы
// сохранялись фильтры/пагинация — как в ванильном MVP.
const PAGES: Record<TabName, React.ComponentType<{ active: boolean }>> = {
  overview: Overview,
  instances: Instances,
  rules: Rules,
  rulesets: Rulesets,
  deployments: Deployments,
  logs: Logs,
  metrics: Metrics,
  configs: Configs,
  matrix: Matrix,
  iocs: Iocs,
  feeds: Feeds,
  users: Users,
  roles: Roles,
  tokens: Tokens,
  sso: Sso,
  audit: Audit,
};

const hasPerm = (perms: string[] | undefined, p?: string) =>
  !p || !perms || perms.includes("*") || perms.includes(p);

export default function App() {
  const [tab, setTab] = React.useState<TabName>("overview");
  const [health, setHealth] = React.useState<Health | null>(null);
  const [healthErr, setHealthErr] = React.useState(false);
  const [version, setVersion] = React.useState<Version | null>(null);
  // auth: null — проверяем; true — вход есть (token в localStorage или
  // dev-режим); false — показать форму входа.
  const [authed, setAuthed] = React.useState<boolean | null>(null);
  const [me, setMe] = React.useState<CurrentUser | null>(null);
  const visited = React.useRef<Set<TabName>>(new Set(["overview"]));

  const checkAuth = React.useCallback(async () => {
    try {
      setMe(await apiGet<CurrentUser>("/auth/me"));
      setAuthed(true);
    } catch {
      setAuthed(false); // 401: token-режим без валидной сессии → форма входа
    }
  }, []);

  React.useEffect(() => { checkAuth(); }, [checkAuth]);
  React.useEffect(() => {
    const onLogout = () => { setAuthed(false); setMe(null); };
    window.addEventListener("surifleet-logout", onLogout);
    return () => window.removeEventListener("surifleet-logout", onLogout);
  }, []);

  const loadHeader = React.useCallback(async () => {
    try {
      setHealth(await apiGet<Health>("/health"));
      setHealthErr(false);
    } catch {
      setHealthErr(true);
    }
    try {
      setVersion(await apiGet<Version>("/version"));
    } catch { /* версия некритична */ }
  }, []);

  React.useEffect(() => { loadHeader(); }, [loadHeader]);
  useInterval(loadHeader, 15000);

  const select = (t: TabName) => {
    visited.current.add(t);
    setTab(t);
  };

  if (authed === false) {
    return <Login onDone={() => { checkAuth(); }} />;
  }

  return (
    <PermsContext.Provider value={me?.permissions ?? ["*"]}>
      <header>
        <div className="brand">Suri<span>Fleet</span></div>
        <div className="hdr-status">
          <span className="muted">{version ? `${version.version} · ${version.commit}` : ""}</span>
          {me && <span className="muted">{me.user.email}</span>}
          {me && !me.user.display_name?.startsWith("dev-") && getToken() && (
            <button className="btn" onClick={async () => { await logout(); setAuthed(false); setMe(null); }}>
              выйти
            </button>
          )}
          {healthErr
            ? <span className="badge badge-err">недоступен</span>
            : <Badge status={health?.status ?? "muted"} />}
        </div>
      </header>
      <nav>
        {TABS.filter(t => hasPerm(me?.permissions, t.perm)).map(t => (
          <button
            key={t.name}
            className={"tab" + (tab === t.name ? " active" : "")}
            onClick={() => select(t.name)}
          >
            {t.label}
          </button>
        ))}
      </nav>
      <main>
        {(Object.keys(PAGES) as TabName[]).map(name => {
          if (!visited.current.has(name)) return null;
          const Page = PAGES[name];
          return (
            <div key={name} className={"page" + (tab === name ? " active" : "")}>
              <Page active={tab === name} />
            </div>
          );
        })}
      </main>
      <footer className="muted">
        SuriFleet React UI · ванильный MVP остаётся на <a href="/">/</a> · API /api/v1
      </footer>
    </PermsContext.Provider>
  );
}
