import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState, type FormEvent } from "react";
import { MIN_PASSWORD, passwordChangeError, passwordProblem, type PasswordForm } from "../accountSecurity";
import { api, ApiError, type TwoFactorSetup, type TwoFactorStatus } from "../api";
import { useMe } from "../auth";
import { ask, useEditGuard } from "../guard";
import { CodeOrPassword, RecoveryCodes, SetupApp } from "../TwoFactorUI";
import { ErrorNote, Field, PageHead, timeAgo, useToast } from "../ui";
import { roleName, useRoles } from "./Roles";

/** Account is "Your account": who you are, your password, where you're signed in and your two-step
 * sign-in. Everyone signed in can open it. */
export function Account() {
  const me = useMe().data;
  const roles = useRoles().data;
  // A guard for the password form: leaving the page with something typed there asks first.
  const guard = useEditGuard();
  return (
    <div className="page">
      <PageHead title="Your account" intro={<>Your details and how you sign in. Jump to <a href="#password">password</a>, <a href="#sessions">where you're signed in</a> or <a href="#two-step">two-step sign-in</a>.</>} />
      <section className="panel">
        <div className="panel-head"><h3>You</h3></div>
        <div className="panel-body">
          <dl className="kv" style={{ gridTemplateColumns: "160px 1fr" }}>
            <dt>Name</dt><dd>{me?.user?.name || <span className="faint">not set</span>}</dd>
            <dt>Email</dt><dd>{me?.user?.email}</dd>
            <dt>Workspace</dt><dd>{me?.workspace.name} (<code>{me?.workspace.slug}</code>)</dd>
            <dt>Role</dt><dd>{roleName(me?.role, roles)}</dd>
          </dl>
        </div>
      </section>
      <PasswordPanel guard={guard} />
      <SessionsPanel guard={guard} />
      <TwoFactorPanel />
    </div>
  );
}

type Guard = ReturnType<typeof useEditGuard>;
const EMPTY: PasswordForm = { current: "", next: "", confirm: "" };

/** PasswordPanel changes the password; the server then signs out every other device. */
function PasswordPanel({ guard }: { guard: Guard }) {
  const toast = useToast();
  const tf = useQuery({ queryKey: ["two-factor"], queryFn: () => api.get<TwoFactorStatus>("/v1/account/two-factor"), retry: false });
  const [f, setF] = useState<PasswordForm>(EMPTY);
  const [tried, setTried] = useState(false);
  const dirty = f.current !== "" || f.next !== "" || f.confirm !== "";
  const { markDirty, saved } = guard;
  useEffect(() => { if (dirty) markDirty(); else saved(); }, [dirty, markDirty, saved]);
  const change = useMutation({
    mutationFn: () => api.post("/v1/account/password", { current_password: f.current, new_password: f.next }),
    onSuccess: () => {
      saved();
      setF(EMPTY);
      setTried(false);
      toast({ text: "Password changed. Other devices were signed out." });
    },
  });
  const problem = passwordProblem(f);
  const err = change.error instanceof ApiError ? new Error(passwordChangeError(change.error.status, change.error.message)) : change.error;
  const set = (k: keyof PasswordForm) => (e: React.ChangeEvent<HTMLInputElement>) => { change.reset(); setF({ ...f, [k]: e.target.value }); };

  function submit(e: FormEvent) {
    e.preventDefault();
    setTried(true);
    if (!problem) change.mutate();
  }

  // Only someone who signs in with single sign-on has no password here.
  if (tf.data && !tf.data.password) {
    return (
      <section className="panel section" id="password">
        <div className="panel-head"><h3>Password</h3></div>
        <div className="panel-body">
          <p className="small muted">You sign in with single sign-on (SSO), so your company's identity provider manages your password. There's no Pulse password to change.</p>
        </div>
      </section>
    );
  }
  // Show what's missing only once the person tried to save, or for the confirm field once typed in.
  const shown = tried ? problem : f.confirm && f.next && f.confirm !== f.next && f.confirm.length >= f.next.length ? "The two new passwords don't match." : null;
  return (
    <section className="panel section" id="password">
      <div className="panel-head"><h3>Password</h3></div>
      <div className="panel-body">
        <p className="small muted" style={{ marginBottom: 14, maxWidth: "70ch" }}>
          Changing your password signs you out on every other device and browser. You stay signed in here.
        </p>
        <ErrorNote error={err} />
        <form onSubmit={submit} style={{ maxWidth: 420 }}>
          <Field label="Current password"><input className="input" type="password" autoComplete="current-password" value={f.current} onChange={set("current")} /></Field>
          <Field label="New password" hint={`At least ${MIN_PASSWORD} characters.`}><input className="input" type="password" autoComplete="new-password" value={f.next} onChange={set("next")} /></Field>
          <Field label="New password again"><input className="input" type="password" autoComplete="new-password" value={f.confirm} onChange={set("confirm")} /></Field>
          {shown && <p className="small" role="alert" style={{ color: "var(--danger)", marginTop: -4, marginBottom: 12 }}>{shown}</p>}
          <div className="row" style={{ gap: 8 }}>
            <button className="btn primary" disabled={change.isPending || !dirty}>{change.isPending ? "Changing…" : "Change password"}</button>
            {dirty && <button type="button" className="btn" disabled={change.isPending}
              onClick={() => void ask({ title: "Clear what you typed?", body: "The password fields are emptied; nothing is changed.", confirmText: "Clear", cancelText: "Keep editing", danger: true })
                .then((ok) => { if (ok) { change.reset(); setF(EMPTY); setTried(false); } })}>Cancel</button>}
          </div>
        </form>
      </div>
    </section>
  );
}

/** SessionsPanel: "Sign out everywhere" ends every session, this one too. */
function SessionsPanel({ guard }: { guard: Guard }) {
  const qc = useQueryClient();
  const revoke = useMutation({
    mutationFn: () => api.post("/v1/account/sessions/revoke"),
    onSuccess: () => {
      guard.saved(); // nothing left to protect: this session has ended too
      qc.clear();
      window.location.assign("/login");
    },
  });
  async function signOutEverywhere() {
    const ok = await ask({
      title: "Sign out everywhere?",
      body: "Every browser and device signed in to your account is signed out, this one too. You'll need to sign in again." + (guard.isDirty() ? " What you typed in the password form is lost." : ""),
      confirmText: "Sign out everywhere", danger: true,
    });
    if (ok) revoke.mutate();
  }
  return (
    <section className="panel section" id="sessions">
      <div className="panel-head"><h3>Where you're signed in</h3></div>
      <div className="panel-body">
        <p className="small muted" style={{ marginBottom: 14, maxWidth: "70ch" }}>
          Pulse signs you out automatically after 7 days without use, and after 14 days at the latest. Left yourself signed in on a shared or lost computer? Sign out everywhere.
        </p>
        <ErrorNote error={revoke.error} />
        <button className="btn danger" disabled={revoke.isPending} onClick={() => void signOutEverywhere()}>{revoke.isPending ? "Signing out…" : "Sign out everywhere"}</button>
      </div>
    </section>
  );
}

type Mode = "idle" | "setup" | "codes" | "off" | "new-codes" | "new-phone";

function TwoFactorPanel() {
  const me = useMe().data;
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({ queryKey: ["two-factor"], queryFn: () => api.get<TwoFactorStatus>("/v1/account/two-factor") });
  const [mode, setMode] = useState<Mode>("idle");
  const [codes, setCodes] = useState<string[]>([]);
  const [started, setStarted] = useState<TwoFactorSetup | undefined>();
  const refresh = () => { qc.invalidateQueries({ queryKey: ["two-factor"] }); qc.invalidateQueries({ queryKey: ["users"] }); };
  const off = useMutation({
    mutationFn: (body: { code?: string; password?: string }) => api.post("/v1/account/two-factor/disable", body),
    onSuccess: () => { refresh(); setMode("idle"); toast({ text: "Two-step sign-in is off" }); },
  });
  const renew = useMutation({
    mutationFn: (body: { code?: string; password?: string }) => api.post<{ recovery_codes: string[] }>("/v1/account/two-factor/recovery-codes", body),
    onSuccess: (r) => { refresh(); setCodes(r.recovery_codes); setMode("codes"); },
  });
  // A new phone replaces the secret in use, so it starts with a current code or the password.
  const newPhone = useMutation({
    mutationFn: (body: { code?: string; password?: string }) => api.post<TwoFactorSetup>("/v1/account/two-factor/setup", body),
    onSuccess: (r) => { setStarted(r); setMode("setup"); },
  });
  const v = q.data;
  const ws = me?.workspace.slug ?? "";
  const email = me?.user?.email ?? "";

  let body: React.ReactNode = null;
  if (v && !v.password) {
    body = <p className="small muted">You sign in with single sign-on (SSO), so your company's identity provider decides about extra sign-in steps. Two-step sign-in here is for signing in with a password.</p>;
  } else if (v && !v.available) {
    body = <p className="small muted">Two-step sign-in isn't available on this server yet: it needs the server's secret key. Ask whoever runs Pulse for you.</p>;
  } else if (mode === "codes") {
    body = <RecoveryCodes codes={codes} workspace={ws} email={email} onDone={() => { setCodes([]); setMode("idle"); }} />;
  } else if (mode === "setup") {
    body = <SetupApp base="/v1/account" initial={started} onDone={(c) => { refresh(); setStarted(undefined); setCodes(c); setMode("codes"); toast({ text: "Two-step sign-in is on" }); }} onCancel={() => { setStarted(undefined); setMode("idle"); }} />;
  } else if (mode === "new-phone") {
    body = (
      <>
        <p className="small" style={{ marginBottom: 12 }}>To move two-step sign-in to a new phone, first confirm it's you: enter a code from your current app (or a recovery code), or your password. Your current phone keeps working until the new one is set up.</p>
        <ErrorNote error={newPhone.error} />
        <CodeOrPassword hasPassword busy={newPhone.isPending} label="Continue" onSubmit={(b) => newPhone.mutate(b)} />
        <button className="btn ghost sm" style={{ marginTop: 8 }} onClick={() => { newPhone.reset(); setMode("idle"); }}>Cancel</button>
      </>
    );
  } else if (mode === "off") {
    body = (
      <>
        <p className="small" style={{ marginBottom: 12 }}>To turn two-step sign-in off, enter a code from your app (or a recovery code), or your password.</p>
        <ErrorNote error={off.error} />
        <CodeOrPassword hasPassword busy={off.isPending} label="Turn off" onSubmit={(b) => off.mutate(b)} />
        <button className="btn ghost sm" style={{ marginTop: 8 }} onClick={() => { off.reset(); setMode("idle"); }}>Cancel</button>
      </>
    );
  } else if (mode === "new-codes") {
    body = (
      <>
        <p className="small" style={{ marginBottom: 12 }}>New recovery codes replace the old ones, which stop working. Confirm with a code from your app, or your password.</p>
        <ErrorNote error={renew.error} />
        <CodeOrPassword hasPassword busy={renew.isPending} label="Make new codes" onSubmit={(b) => renew.mutate(b)} />
        <button className="btn ghost sm" style={{ marginTop: 8 }} onClick={() => { renew.reset(); setMode("idle"); }}>Cancel</button>
      </>
    );
  } else if (v?.enabled) {
    body = (
      <>
        <p className="small" style={{ marginBottom: 12 }}>
          After your password, Pulse asks for the code your authenticator app shows. Turned on {timeAgo(v.enabled_at ?? undefined)}.{" "}
          You have <strong>{v.recovery_codes_left}</strong> unused recovery code{v.recovery_codes_left === 1 ? "" : "s"}.
        </p>
        {v.recovery_codes_left <= 3 && <div className="alert warn">You're running low on recovery codes. Make new ones and keep them safe.</div>}
        <div className="row" style={{ gap: 8 }}>
          <button className="btn" onClick={() => setMode("new-phone")}>Use a new phone</button>
          <button className="btn" onClick={() => setMode("new-codes")}>New recovery codes</button>
          {!v.required && <button className="btn danger" onClick={() => setMode("off")}>Turn off</button>}
        </div>
        {v.required && <p className="xs muted" style={{ marginTop: 10 }}>Your workspace requires two-step sign-in, so it can't be turned off.</p>}
      </>
    );
  } else if (v) {
    body = (
      <>
        {v.required && <div className="alert warn">Your workspace requires two-step sign-in. Set it up now, or you'll be asked to the next time you sign in.</div>}
        <SetupApp base="/v1/account" onDone={(c) => { refresh(); setCodes(c); setMode("codes"); toast({ text: "Two-step sign-in is on" }); }} />
      </>
    );
  }

  return (
    <section className="panel section" id="two-step">
      <div className="panel-head">
        <h3>Two-step sign-in</h3>
        {v && (v.enabled ? <span className="badge ok">On</span> : <span className="badge">Off</span>)}
      </div>
      <div className="panel-body">
        <p className="small muted" style={{ marginBottom: 14, maxWidth: "70ch" }}>
          Protects your account even if someone learns your password: signing in also needs a code from an app on your phone (Google Authenticator, Microsoft Authenticator, Authy, 1Password and others).
        </p>
        <ErrorNote error={q.error} />
        {body}
      </div>
    </section>
  );
}
