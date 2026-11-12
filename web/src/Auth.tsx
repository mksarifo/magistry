import { useCallback, useEffect, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import {
  ArrowRight,
  Box,
  LoaderCircle,
  LockKeyhole,
  ShieldCheck,
} from "lucide-react";
import { api, json } from "./api";
export type Session = {
  authenticated: boolean;
  setupRequired: boolean;
  username: string;
  managed: boolean;
};

export function Root({
  render,
}: {
  render: (session: Session, logout: () => void) => ReactNode;
}) {
  const [session, setSession] = useState<Session | null>(null);
  const [error, setError] = useState("");
  const load = useCallback(async () => {
    setError("");
    try {
      setSession(await api<Session>("auth/session"));
    } catch (err) {
      setError((err as Error).message);
    }
  }, []);
  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => {
    const expired = () =>
      setSession((current) =>
        current ? { ...current, authenticated: false } : current,
      );
    window.addEventListener("auth-expired", expired);
    return () => window.removeEventListener("auth-expired", expired);
  }, []);
  if (!session)
    return (
      <div className="auth-loading" role="status">
        {error ? (
          <>
            <p>{error}</p>
            <button className="secondary" onClick={() => void load()}>
              Try again
            </button>
          </>
        ) : (
          <>
            <LoaderCircle size={25} className="spin" />
            <p>Connecting to Magistry…</p>
          </>
        )}
      </div>
    );
  if (!session.authenticated)
    return <AuthScreen session={session} onSuccess={setSession} />;
  return (
    <>
      {error && (
        <div role="alert" className="alert">
          {error}
        </div>
      )}
      {render(session, () => {
        void api("auth/logout", { method: "POST" })
          .then(() => setSession({ ...session, authenticated: false }))
          .catch((err) => setError(err.message));
      })}
    </>
  );
}

function AuthScreen({
  session,
  onSuccess,
}: {
  session: Session;
  onSuccess: (session: Session) => void;
}) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const setup = session.setupRequired;
  async function submit(event: FormEvent) {
    event.preventDefault();
    setError("");
    if (setup && password !== confirmation) {
      setError("Passwords do not match.");
      return;
    }
    setBusy(true);
    try {
      onSuccess(
        await api<Session>(
          `auth/${setup ? "setup" : "login"}`,
          json("POST", { username, password }),
        ),
      );
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="auth-page">
      <a href="/" className="brand">
        <span className="brand-mark">
          <Box size={29} />
        </span>
        magistry<span className="brand-period">.</span>
      </a>
      <div className="auth-form-wrap">
        <span className="auth-symbol">
          <LockKeyhole size={25} strokeWidth={1.6} />
        </span>
        <div className="eyebrow">YOUR PRIVATE REGISTRY</div>
        <h1>{setup ? "Make it yours." : "Welcome back."}</h1>
        <p>
          {setup
            ? "Create an administrator account to secure your images."
            : "Sign in to manage your container images and storage."}
        </p>
        <form onSubmit={(event) => void submit(event)}>
          <label>
            Username
            <input
              autoFocus
              required
              value={username}
              onChange={(event) => setUsername(event.target.value)}
              autoComplete="username"
              minLength={setup ? 3 : 1}
              maxLength={setup ? 32 : 128}
              pattern={setup ? "[a-zA-Z0-9][a-zA-Z0-9_.-]{2,31}" : undefined}
            />
          </label>
          <label>
            Password
            <input
              type="password"
              required
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              autoComplete={setup ? "new-password" : "current-password"}
              minLength={setup ? 12 : 1}
              maxLength={256}
            />
            {setup && <small>Use at least 12 characters.</small>}
          </label>
          {setup && (
            <label>
              Confirm password
              <input
                type="password"
                required
                value={confirmation}
                onChange={(event) => setConfirmation(event.target.value)}
                autoComplete="new-password"
                minLength={12}
                maxLength={256}
              />
            </label>
          )}
          {error && (
            <p role="alert" className="form-error">
              {error}
            </p>
          )}
          <button className="primary" disabled={busy}>
            {busy ? (
              <LoaderCircle className="spin" size={16} />
            ) : (
              <ArrowRight size={16} />
            )}
            {setup ? "Create administrator" : "Sign in"}
          </button>
        </form>
        <div className="auth-note">
          <ShieldCheck size={16} />
          <span>
            Use this account with <code>docker login</code> too.
            <br />
            Your credentials and images stay on your server.
          </span>
        </div>
      </div>
      <footer>Magistry · Self-hosted container registry</footer>
    </div>
  );
}
