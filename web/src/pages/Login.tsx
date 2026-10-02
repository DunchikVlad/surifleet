import React from "react";
import { login, setToken, listSsoProvidersPublic, SsoProviderPublic } from "../api";

// Login — форма входа (email + пароль, auth_mode=token; чанк 28) + вход
// через SSO (OIDC; чанк 35): если на сервере есть включённые OIDC-провайдеры,
// показываются кнопки «Войти через <name>» (полный редирект на IdP).
export default function Login({ onDone }: { onDone: () => void }) {
  const [email, setEmail] = React.useState("");
  const [password, setPassword] = React.useState("");
  const [err, setErr] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [sso, setSso] = React.useState<SsoProviderPublic[]>([]);

  // Включённые OIDC-провайдеры (публичный эндпоинт, без токена).
  React.useEffect(() => {
    listSsoProvidersPublic()
      .then(p => setSso(p.items ?? []))
      .catch(() => setSso([])); // SSO необязателен — без него просто форма
  }, []);

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

        {sso.length > 0 && (
          <>
            <hr style={{ margin: "1em 0", opacity: 0.3 }} />
            <p className="muted" style={{ marginBottom: "0.4em" }}>или через SSO:</p>
            {sso.map(p => (
              <p key={p.id} style={{ margin: "0.3em 0" }}>
                <a
                  className="btn"
                  style={{ display: "block", textAlign: "center" }}
                  href={`/api/v1/auth/sso/${p.id}/login`}
                >
                  Войти через {p.name}
                </a>
              </p>
            ))}
          </>
        )}
      </div>
    </main>
  );
}
