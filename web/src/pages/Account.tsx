import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, type TwoFactorSetup, type TwoFactorStatus } from "../api";
import { useMe } from "../auth";
import { CodeOrPassword, RecoveryCodes, SetupApp } from "../TwoFactorUI";
import { ErrorNote, PageHead, timeAgo, useToast } from "../ui";
import { roleName, useRoles } from "./Roles";

/** Account is "Your account": who you are, and your two-step sign-in. Everyone signed in can open it. */
export function Account() {
  const me = useMe().data;
  const roles = useRoles().data;
  return (
    <div className="page">
      <PageHead title="Your account" intro="Your details and how you sign in." />
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
      <TwoFactorPanel />
    </div>
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
    <section className="panel section">
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
