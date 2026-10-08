import React from "react";

export const short = (id?: string | null) => (id || "").slice(0, 8);
export const fmtTime = (t?: string) => (t ? new Date(t).toLocaleString("ru-RU") : "—");

export function Badge({ status }: { status?: string }) {
  const st = status || "muted";
  return <span className={`badge badge-${st}`}>{st}</span>;
}

export function ErrorBox({ error }: { error: unknown }) {
  if (!error) return null;
  const msg = error instanceof Error ? error.message : String(error);
  return <div className="error-box">{msg}</div>;
}

export function Progress({ p }: { p?: { total?: number; succeeded?: number; failed?: number } }) {
  const total = p?.total ?? 0;
  const ok = p?.succeeded ?? 0;
  const failed = p?.failed ?? 0;
  const pct = total ? Math.round((100 * ok) / total) : 0;
  return (
    <>
      <span className="progress">
        <i className={failed ? "has-failed" : ""} style={{ width: `${pct}%` }} />
      </span>{" "}
      <span className="muted">
        {ok}/{total}
        {failed ? ` · failed ${failed}` : ""}
      </span>
    </>
  );
}

export function useInterval(cb: () => void, ms: number, active = true) {
  React.useEffect(() => {
    if (!active) return;
    const t = setInterval(cb, ms);
    return () => clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ms, active]);
}

// --- сортировка таблиц (чанк 53): кликабельные заголовки колонок ---

export interface SortState { key: string; dir: 1 | -1 }

// sortBy — клиентская сортировка: acc возвращает значение колонки
// (строки — localeCompare, числа/даты — численно, undefined — в конец).
export function sortBy<T>(items: T[], sort: SortState, acc: (t: T, key: string) => string | number | undefined): T[] {
  const out = [...items];
  out.sort((a, b) => {
    const va = acc(a, sort.key);
    const vb = acc(b, sort.key);
    if (va === undefined && vb === undefined) return 0;
    if (va === undefined) return 1;
    if (vb === undefined) return -1;
    let c: number;
    if (typeof va === "number" && typeof vb === "number") c = va - vb;
    else c = String(va).localeCompare(String(vb), "ru");
    return c * sort.dir;
  });
  return out;
}

// SortTh — <th> с переключением сортировки (▲/▼).
export function SortTh({ label, k, sort, onSort }: {
  label: string; k: string; sort: SortState; onSort: (s: SortState) => void;
}) {
  const active = sort.key === k;
  return (
    <th
      style={{ cursor: "pointer", userSelect: "none", whiteSpace: "nowrap" }}
      title="сортировка"
      onClick={() => onSort({ key: k, dir: active ? (sort.dir === 1 ? -1 : 1) : 1 })}
    >
      {label} {active ? (sort.dir === 1 ? "▲" : "▼") : ""}
    </th>
  );
}
