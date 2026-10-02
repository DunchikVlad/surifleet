import React from "react";
import { login, setToken } from "../api";

// Login — форма входа (email + пароль, auth_mode=token; чанк 28).
export default function Login({ onDone }: { onDone: () => void }) {
  const [email, setEmail] = React.useState("");
  const [password, setPassword] = React.useState("");
  const [err, setErr] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  const submit = async () => {
    setErr("");
    setBusy(true);
    try {
      const t = await login(email.trim(), password);
      setToken(t.access_token);
      onDone();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <main className="page active" style={{ maxWidth: "26em", margin: "12vh auto" }}>
      <h2>Вход в SuriFleet</h2>
      <div className="panel">
        <p>
          <input
            type="email"
            placeholder="email"
            autoFocus
            value={email}
            onChange={e => setEmail(e.target.value)}
            onKeyDown={e => { if (e.key === "Enter") submit(); }}
            style={{ width: "100%" }}
          />
        </p>
        <p>
          <input
            type="password"
            placeholder="пароль"
            value={password}
            onChange={e => setPassword(e.target.value)}
            onKeyDown={e => { if (e.key === "Enter") submit(); }}
            style={{ width: "100%" }}
          />
        </p>
        {err && <p className="muted" style={{ color: "var(--err, #e66)" }}>{err}</p>}
        <button className="btn primary" disabled={busy || !email || !password} onClick={submit}>
          {busy ? "Вход…" : "Войти"}
        </button>
      </div>
    </main>
  );
}
