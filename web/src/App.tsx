import React from "react";
import { apiGet, Health, Version } from "./api";
import { Badge, useInterval } from "./components";
import Overview from "./pages/Overview";
import Instances from "./pages/Instances";
import Rules from "./pages/Rules";
import Rulesets from "./pages/Rulesets";
import Deployments from "./pages/Deployments";
import Logs from "./pages/Logs";
import Matrix from "./pages/Matrix";
import Iocs from "./pages/Iocs";

type TabName =
  | "overview" | "instances" | "rules" | "rulesets"
  | "deployments" | "logs" | "matrix" | "iocs";

const TABS: { name: TabName; label: string }[] = [
  { name: "overview", label: "Обзор" },
  { name: "instances", label: "Инстансы" },
  { name: "rules", label: "Правила" },
  { name: "rulesets", label: "Ruleset'ы" },
  { name: "deployments", label: "Деплои" },
  { name: "logs", label: "Логи" },
  { name: "matrix", label: "Матрица" },
  { name: "iocs", label: "IOC" },
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
  matrix: Matrix,
  iocs: Iocs,
};

export default function App() {
  const [tab, setTab] = React.useState<TabName>("overview");
  const [health, setHealth] = React.useState<Health | null>(null);
  const [healthErr, setHealthErr] = React.useState(false);
  const [version, setVersion] = React.useState<Version | null>(null);
  const visited = React.useRef<Set<TabName>>(new Set(["overview"]));

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

  return (
    <>
      <header>
        <div className="brand">Suri<span>Fleet</span></div>
        <div className="hdr-status">
          <span className="muted">{version ? `${version.version} · ${version.commit}` : ""}</span>
          {healthErr
            ? <span className="badge badge-err">недоступен</span>
            : <Badge status={health?.status ?? "muted"} />}
        </div>
      </header>
      <nav>
        {TABS.map(t => (
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
    </>
  );
}
