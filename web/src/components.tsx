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
