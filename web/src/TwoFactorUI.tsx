// Two-step sign-in building blocks shared by the sign-in page and Your account: the QR code, the
// setup steps and the recovery codes screen.
import { useMemo, useState, type FormEvent } from "react";
import { api, type TwoFactorSetup } from "./api";
import { codeReady, isAppCode, qrMatrix, recoveryText } from "./twoFactor";
import { ErrorNote, Field, useCopy } from "./ui";

/** QRCode draws text as a QR code (black on white, with the quiet zone apps need). */
export function QRCode({ value, size = 184 }: { value: string; size?: number }) {
  const m = useMemo(() => qrMatrix(value), [value]);
  const n = m.length + 8;
  const path = m.flatMap((row, r) => row.map((dark, c) => (dark ? `M${c + 4} ${r + 4}h1v1h-1z` : ""))).join("");
  return (
    <svg className="qr" width={size} height={size} viewBox={`0 0 ${n} ${n}`} shapeRendering="crispEdges" role="img" aria-label="QR code for your authenticator app">
      <rect width={n} height={n} fill="#fff" />
      <path d={path} fill="#000" />
    </svg>
  );
}

/** CodeField: where people type a code from the app (or a recovery code when allowed). */
export function CodeField({ value, onChange, recovery, label = "Code from the app" }: { value: string; onChange: (v: string) => void; recovery?: boolean; label?: string }) {
  return (
    <Field label={label} hint={recovery ? "Six digits from your authenticator app, or one of your recovery codes." : "The six digits your authenticator app shows for Pulse."}>
      <input className="input code-input" value={value} onChange={(e) => onChange(e.target.value)} autoComplete="one-time-code"
        inputMode={recovery && !isAppCode(value) && /[a-z]/i.test(value) ? "text" : "numeric"} autoFocus spellCheck={false} />
    </Field>
  );
}

/** SetupApp walks through adding Pulse to an authenticator app: base is /v1/account (signed in) or
 * /v1/auth (setting it up while signing in). onDone gets the recovery codes. initial is a setup
 * already started (moving to a new phone starts it with a code or the password). */
export function SetupApp({ base, onDone, onCancel, initial }: { base: "/v1/account" | "/v1/auth"; onDone: (codes: string[]) => void; onCancel?: () => void; initial?: TwoFactorSetup }) {
  const [setup, setSetup] = useState<TwoFactorSetup | null>(initial ?? null);
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [showKey, setShowKey] = useState(false);

  async function start() {
    setBusy(true);
    setError(null);
    try {
      setSetup(await api.post<TwoFactorSetup>(`${base}/two-factor/setup`));
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  }

  async function confirm(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const res = await api.post<{ recovery_codes: string[] }>(`${base}/two-factor/enable`, { code });
      onDone(res.recovery_codes);
    } catch (err) {
      setError(err);
      setCode("");
    } finally {
      setBusy(false);
    }
  }

  if (!setup) {
    return (
      <div>
        <p className="small muted" style={{ marginBottom: 14, maxWidth: "62ch" }}>
          You'll need an authenticator app on your phone, such as Google Authenticator, Microsoft Authenticator, Authy or 1Password. After your password, Pulse will ask for the six-digit code the app shows.
        </p>
        <ErrorNote error={error} />
        <div className="row" style={{ gap: 8 }}>
          <button className="btn primary" disabled={busy} onClick={start}>{busy ? "Starting…" : "Set up the app"}</button>
          {onCancel && <button className="btn" onClick={onCancel}>Cancel</button>}
        </div>
      </div>
    );
  }

  return (
    <form onSubmit={confirm} className="tf-setup">
      <ol className="tf-steps">
        <li>
          <strong>Scan this code with your authenticator app.</strong> In the app, choose to add an account, then scan a QR code.
          <div className="tf-qr"><QRCode value={setup.uri} /></div>
          {showKey ? (
            <div className="small">
              Can't scan it? Choose to enter a setup key, and type:
              <div className="tf-key"><code>{setup.secret}</code></div>
              <span className="xs muted">Account: {setup.account}. Type of key: time based.</span>
            </div>
          ) : (
            <button type="button" className="btn ghost sm" onClick={() => setShowKey(true)}>Can't scan it? Type a key instead</button>
          )}
        </li>
        <li>
          <strong>Enter the code the app shows</strong> for Pulse, to check it's working.
          <ErrorNote error={error} />
          <CodeField value={code} onChange={setCode} />
        </li>
      </ol>
      <div className="row" style={{ gap: 8 }}>
        <button className="btn primary" disabled={busy || !isAppCode(code)}>{busy ? "Checking…" : "Turn on two-step sign-in"}</button>
        {onCancel && <button type="button" className="btn" onClick={onCancel}>Cancel</button>}
      </div>
    </form>
  );
}

/** RecoveryCodes shows new recovery codes once, to copy or download. */
export function RecoveryCodes({ codes, workspace, email, onDone, doneLabel = "Done" }: { codes: string[]; workspace: string; email: string; onDone: () => void; doneLabel?: string }) {
  const copy = useCopy();
  const [saved, setSaved] = useState(false);
  const text = recoveryText(codes, workspace, email);
  const href = useMemo(() => URL.createObjectURL(new Blob([text], { type: "text/plain" })), [text]);
  return (
    <div>
      <div className="alert warn">
        Save these recovery codes now: they're shown only once. If you lose your phone, each code lets you sign in one time instead of a code from the app.
      </div>
      <ul className="tf-codes">{codes.map((c) => <li key={c}><code>{c}</code></li>)}</ul>
      <div className="row" style={{ gap: 8, marginBottom: 14 }}>
        <a className="btn sm" href={href} download="pulse-recovery-codes.txt" onClick={() => setSaved(true)}>Download</a>
        <button type="button" className="btn sm" onClick={() => { copy(text, "Recovery codes copied"); setSaved(true); }}>Copy</button>
      </div>
      <label className="check"><input type="checkbox" checked={saved} onChange={(e) => setSaved(e.target.checked)} />I've saved my recovery codes</label>
      <button className="btn primary" disabled={!saved} onClick={onDone}>{doneLabel}</button>
    </div>
  );
}

/** CodeOrPassword asks for a current code or the password before a change to two-step sign-in. */
export function CodeOrPassword({ hasPassword, onSubmit, busy, label }: { hasPassword: boolean; onSubmit: (body: { code?: string; password?: string }) => void; busy: boolean; label: string }) {
  const [usePassword, setUsePassword] = useState(false);
  const [value, setValue] = useState("");
  const ready = usePassword ? value.length > 0 : codeReady(value);
  return (
    <form onSubmit={(e) => { e.preventDefault(); onSubmit(usePassword ? { password: value } : { code: value }); }}>
      {usePassword ? (
        <Field label="Your password"><input className="input" type="password" autoComplete="current-password" value={value} onChange={(e) => setValue(e.target.value)} autoFocus /></Field>
      ) : (
        <CodeField value={value} onChange={setValue} recovery />
      )}
      <div className="row" style={{ gap: 8 }}>
        <button className="btn primary" disabled={busy || !ready}>{label}</button>
        {hasPassword && (
          <button type="button" className="btn ghost sm" onClick={() => { setUsePassword(!usePassword); setValue(""); }}>
            {usePassword ? "Use a code instead" : "Use your password instead"}
          </button>
        )}
      </div>
    </form>
  );
}
