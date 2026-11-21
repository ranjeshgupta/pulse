import { useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { own } from "../safe";
import { api } from "../api";
import { HEARTBEAT, PulseTrace } from "../PulseTrace";
import { Logo } from "../Shell";
import { CodeField, RecoveryCodes, SetupApp } from "../TwoFactorUI";
import { codeReady } from "../twoFactor";
import { ErrorNote, Field } from "../ui";

const SSO_ERRORS: Record<string, string> = {
  unknown_workspace: "That workspace doesn't exist. Check the workspace ID.",
  sso_unavailable: "Single sign-on isn't set up for this workspace yet. Sign in with your password, or ask an admin.",
  sso_expired: "The sign-in took too long or was interrupted. Start again.",
  sso_denied: "Sign-in was cancelled at your identity provider.",
  sso_failed: "Your identity provider's response couldn't be verified. Try again, or ask an admin to check the SSO settings.",
  sso_no_verified_email: "Your identity provider didn't share a verified email address.",
  sso_not_provisioned: "You don't have access to this workspace yet. Ask an admin to add you.",
};

function AuthLayout({ children }: { children: React.ReactNode }) {
  return (
    <div className="auth">
      <section className="auth-form">
        <div className="brand" style={{ color: "var(--ink)", padding: 0 }}><Logo /> Pulse</div>
        {children}
      </section>
      <aside className="auth-side" aria-hidden>
        <PulseTrace values={HEARTBEAT} />
        <blockquote>Contacts move into segments and campaigns the moment they act, not hours later.</blockquote>
      </aside>
    </div>
  );
}

export function Login() {
  const [params] = useSearchParams();
  const nav = useNavigate();
  const qc = useQueryClient();
  const [workspace, setWorkspace] = useState(localStorage.getItem("pulse.workspace") ?? "");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<unknown>(params.get("error") ? new Error(own(SSO_ERRORS, params.get("error")) ?? "Sign-in failed.") : null);
  const [busy, setBusy] = useState(false);
  // Two-step sign-in: after the password, a code from the app ("code"), or setting the app up
  // first when the workspace requires it ("setup"). The server holds the half-finished sign-in
  // in a short-lived cookie.
  const [step, setStep] = useState<"password" | "code" | "setup">("password");
  const [code, setCode] = useState("");
  const [codes, setCodes] = useState<string[] | null>(null);

  async function signedIn() {
    localStorage.setItem("pulse.workspace", workspace);
    await qc.invalidateQueries({ queryKey: ["me"] });
    nav(params.get("next") ?? "/", { replace: true });
  }

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const res = await api.post<{ two_factor?: "code" | "setup" }>("/v1/auth/login", { workspace, email, password });
      if (res.two_factor) {
        setPassword("");
        setStep(res.two_factor);
        return;
      }
      await signedIn();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  async function verify(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.post("/v1/auth/two-factor/verify", { code });
      await signedIn();
    } catch (err) {
      setError(err);
      setCode("");
    } finally {
      setBusy(false);
    }
  }

  function startOver() {
    setStep("password");
    setCode("");
    setError(null);
  }

  if (step === "code") {
    return (
      <AuthLayout>
        <h1>Enter your code</h1>
        <p>Open your authenticator app and type the six-digit code it shows for Pulse.</p>
        <ErrorNote error={error} />
        <form onSubmit={verify}>
          <CodeField value={code} onChange={setCode} recovery />
          <button className="btn primary block" disabled={busy || !codeReady(code)}>{busy ? "Checking…" : "Sign in"}</button>
        </form>
        <p className="small muted" style={{ marginTop: 22 }}>
          Lost your phone? Type one of your recovery codes instead, or ask an admin to reset your two-step sign-in. <button type="button" className="btn ghost sm" onClick={startOver}>Start again</button>
        </p>
      </AuthLayout>
    );
  }

  if (step === "setup") {
    return (
      <AuthLayout>
        <h1>Set up two-step sign-in</h1>
        <p>Your workspace asks you to confirm each sign-in with a code from an authenticator app on your phone. Setting it up takes a minute.</p>
        {codes ? (
          <RecoveryCodes codes={codes} workspace={workspace} email={email} onDone={signedIn} doneLabel="Open Pulse" />
        ) : (
          <>
            <ErrorNote error={error} />
            <SetupApp base="/v1/auth" onDone={setCodes} onCancel={startOver} />
          </>
        )}
      </AuthLayout>
    );
  }

  async function sso() {
    if (!workspace) {
      setError(new Error("Enter your workspace ID first."));
      return;
    }
    const info = await api.get<{ enabled: boolean }>(`/v1/auth/sso?workspace=${encodeURIComponent(workspace)}`);
    if (!info.enabled) {
      setError(new Error(SSO_ERRORS.sso_unavailable));
      return;
    }
    localStorage.setItem("pulse.workspace", workspace);
    window.location.assign(`/v1/auth/sso/start?workspace=${encodeURIComponent(workspace)}`);
  }

  return (
    <AuthLayout>
      <h1>Sign in</h1>
      <p>Use your workspace ID, the short name in your workspace's address.</p>
      <ErrorNote error={error} />
      <form onSubmit={submit}>
        <Field label="Workspace ID"><input className="input" value={workspace} onChange={(e) => setWorkspace(e.target.value.trim().toLowerCase())} autoComplete="organization" required /></Field>
        <Field label="Email"><input className="input" type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="email" required /></Field>
        <Field label="Password"><input className="input" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" required /></Field>
        <button className="btn primary block" disabled={busy}>{busy ? "Signing in…" : "Sign in"}</button>
      </form>
      <div className="divider">or</div>
      <button className="btn block" onClick={sso} type="button">Sign in with SSO</button>
      <p className="small muted" style={{ marginTop: 22 }}>New to Pulse? <Link to="/signup">Create a workspace</Link></p>
    </AuthLayout>
  );
}

export function Signup() {
  const nav = useNavigate();
  const qc = useQueryClient();
  const [form, setForm] = useState({ workspace: "", name: "", email: "", password: "" });
  const [error, setError] = useState<unknown>(null);
  const [created, setCreated] = useState<{ api_key: string; workspace: { slug: string } } | null>(null);
  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) => setForm({ ...form, [k]: e.target.value });

  async function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      const res = await api.post<{ api_key: string; workspace: { slug: string } }>("/v1/auth/signup", form);
      localStorage.setItem("pulse.workspace", res.workspace.slug);
      setCreated(res);
    } catch (err) {
      setError(err);
    }
  }

  if (created) {
    return (
      <AuthLayout>
        <h1>Your workspace is ready</h1>
        <p>Your workspace ID is <strong>{created.workspace.slug}</strong>. Teammates use it to sign in.</p>
        <Field label="Server API key" hint="Copy it now: it's shown only once. Servers use it to send events. You can rotate it later in Sign-in & keys.">
          <input className="input code" readOnly value={created.api_key} onFocus={(e) => e.target.select()} />
        </Field>
        <button className="btn primary block" onClick={async () => { await qc.invalidateQueries({ queryKey: ["me"] }); nav("/", { replace: true }); }}>Open Pulse</button>
      </AuthLayout>
    );
  }

  return (
    <AuthLayout>
      <h1>Create a workspace</h1>
      <p>You'll be its admin, and can invite your team afterwards.</p>
      <ErrorNote error={error} />
      <form onSubmit={submit}>
        <Field label="Company or team name"><input className="input" value={form.workspace} onChange={set("workspace")} required /></Field>
        <Field label="Your name"><input className="input" value={form.name} onChange={set("name")} autoComplete="name" /></Field>
        <Field label="Work email"><input className="input" type="email" value={form.email} onChange={set("email")} autoComplete="email" required /></Field>
        <Field label="Password" hint="At least 10 characters."><input className="input" type="password" value={form.password} onChange={set("password")} autoComplete="new-password" minLength={10} required /></Field>
        <button className="btn primary block">Create workspace</button>
      </form>
      <p className="small muted" style={{ marginTop: 22 }}>Already have one? <Link to="/login">Sign in</Link></p>
    </AuthLayout>
  );
}
