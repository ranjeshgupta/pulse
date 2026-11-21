import { useMutation } from "@tanstack/react-query";
import { useRef, useState, type ReactNode } from "react";
import { api, type Channel, type ProviderField } from "../api";
import { ErrorNote, Field } from "../ui";
import { importExamples, type Imported } from "../curlImport";
import {
  AUTH_TYPES, MESSAGE_VARS, RECEIPT_STATUSES, authSecretFields, formatStatuses, newLogin, parseAuth, parseRequest, parseStatuses,
  serializeAuth, serializeRequest, type AuthConfig, type CustomRequest, type Header, type LoginRequest,
} from "../providerAuth";

// The provider form's M5 parts: custom HTTP authentication, the gateway's custom request, and the
// "Test connection" / "Preview request" tools. Secrets are typed in and never shown again.

type Setter = (key: string, value: string) => void;

/** touched tells the dialog around a button that the button changed the form: the dialog's
 * "Discard your changes?" guard only notices typing, so this sends it a typing event from a
 * throwaway input placed next to the button. */
function touched(from: HTMLElement) {
  const probe = document.createElement("input");
  probe.hidden = true;
  from.after(probe);
  probe.dispatchEvent(new Event("input", { bubbles: true }));
  probe.remove();
}

/** ConfigField renders one catalog setting: a select when it has options, a switch, a number, a
 * text area or a line of text. */
export function ConfigField({ f, value, onChange }: { f: ProviderField; value: string; onChange: (v: string) => void }) {
  const label = f.label + (f.required || f.options || f.type === "bool" ? "" : " (optional)"); // a choice always has a value
  if (f.type === "bool") {
    return (
      <div className="field">
        <label className="check" style={{ marginBottom: 2 }}>
          <input type="checkbox" checked={value === "true"} onChange={(e) => onChange(e.target.checked ? "true" : "")} />{f.label}
        </label>
        {f.hint && <small>{f.hint}</small>}
      </div>
    );
  }
  return (
    <Field label={label} hint={f.hint}>
      {f.options ? (
        <select className="select" value={value || f.default || f.options[0]} onChange={(e) => onChange(e.target.value)}>
          {f.options.map((o) => <option key={o} value={o}>{f.option_labels?.[o] ?? o}</option>)}
        </select>
      ) : f.type === "textarea" ? (
        <textarea className="textarea code" rows={3} value={value} placeholder={f.placeholder ?? f.default} onChange={(e) => onChange(e.target.value)} />
      ) : (
        <input className="input" inputMode={f.type === "number" ? "numeric" : undefined} value={value} placeholder={f.placeholder ?? f.default} onChange={(e) => onChange(e.target.value)} />
      )}
    </Field>
  );
}

function SecretInput({ label, value, onChange, saved }: { label: string; value: string; onChange: (v: string) => void; saved: boolean }) {
  return (
    <Field label={label} hint={saved ? "Stored encrypted. Leave empty to keep the saved value." : "Stored encrypted; never shown again."}>
      <input className="input" type="password" autoComplete="off" value={value} onChange={(e) => onChange(e.target.value)} />
    </Field>
  );
}

function HeadersEditor({ headers, onChange, hint }: { headers: Header[]; onChange: (h: Header[]) => void; hint?: string }) {
  const set = (i: number, h: Header) => onChange(headers.map((x, j) => (j === i ? h : x)));
  return (
    <Field label="Headers" hint={hint} group>
      {headers.map((h, i) => (
        <div className="row" key={i} style={{ marginBottom: 6, flexWrap: "nowrap" }}>
          <input className="input" style={{ maxWidth: 200 }} placeholder="Name" aria-label="Header name" value={h.name} onChange={(e) => set(i, { ...h, name: e.target.value })} />
          <input className="input" placeholder="Value" aria-label="Header value" value={h.value} onChange={(e) => set(i, { ...h, value: e.target.value })} />
          <button type="button" className="btn sm ghost" onClick={(e) => { touched(e.currentTarget); onChange(headers.filter((_, j) => j !== i)); }}>Remove</button>
        </div>
      ))}
      <button type="button" className="btn sm" onClick={(e) => { touched(e.currentTarget); onChange([...headers, { name: "", value: "" }]); }}>Add header</button>
    </Field>
  );
}

/** loginSummary says in one sentence what a login request does. */
function loginSummary(l: LoginRequest, inject: { in: string; name?: string; template?: string }): string {
  const names = authSecretFields({ type: "login", login: l }).map((f) => f.label.toLowerCase());
  const where = l.token_from === "header" ? `the ${l.token_path || "?"} header` : l.token_from === "regex" ? "the answer (by a pattern)" : (l.token_path || "?");
  const goes = inject.in === "header" ? `as ${inject.name || "Authorization"}: ${(inject.template || "Bearer {{token}}").replace("{{token}}", "<token>")}` : `in the ${inject.in === "query" ? "query parameter" : "body field"} ${inject.name || "?"}`;
  return `Pulse signs in at the login address${names.length ? ` with ${names.join(" and ")}` : ""}, reads the token from ${where}, and sends it ${goes}. It signs in again when the token expires or is refused.`;
}

/** AuthEditor edits config.auth and the secrets it needs. */
export function AuthEditor({ config, setConfig, secrets, setSecret, saved }: {
  config: Record<string, string>; setConfig: Setter; secrets: Record<string, string>; setSecret: Setter; saved: boolean;
}) {
  const a = parseAuth(config.auth);
  const set = (p: Partial<AuthConfig>) => setConfig("auth", serializeAuth({ ...a, ...p }));
  const login = a.login ?? newLogin();
  const setLogin = (p: Partial<LoginRequest>) => set({ login: { ...login, ...p } });
  const tokenBased = a.type === "oauth2" || a.type === "login" || a.type === "bearer";
  const inject = a.inject ?? { in: "header" as const };
  const injectFields = (
        <div className="grid-2">
          <Field label="Token goes in"><select className="select" value={inject.in} onChange={(e) => set({ inject: { ...inject, in: e.target.value as "header" | "query" | "body" } })}>
            <option value="header">A header</option><option value="query">A query parameter</option><option value="body">A body field</option></select></Field>
          <Field label="Name"><input className="input" value={inject.name ?? ""} placeholder={inject.in === "header" ? "Authorization" : "access_token"} onChange={(e) => set({ inject: { ...inject, name: e.target.value } })} /></Field>
          <Field label="Value template" hint="{{token}} is the token."><input className="input code" value={inject.template ?? ""} placeholder={inject.in === "header" && !inject.name ? "Bearer {{token}}" : "{{token}}"} onChange={(e) => set({ inject: { ...inject, template: e.target.value } })} /></Field>
        </div>
  );
  return (
    <fieldset className="fieldset">
      <legend>Authentication</legend>
      <Field label="Type" hint={AUTH_TYPES.find((t) => t.id === a.type)?.hint}>
        <select className="select" value={a.type} onChange={(e) => set({ type: e.target.value as AuthConfig["type"], login: e.target.value === "login" ? login : a.login })}>
          {AUTH_TYPES.map((t) => <option key={t.id} value={t.id}>{t.label}</option>)}
        </select>
      </Field>
      {a.type === "api_key" && (
        <div className="grid-2">
          <Field label="Goes in"><select className="select" value={a.key_in ?? "header"} onChange={(e) => set({ key_in: e.target.value as "header" | "query" })}><option value="header">Header</option><option value="query">Query parameter</option></select></Field>
          <Field label="Name"><input className="input" value={a.key_name ?? ""} placeholder={a.key_in === "query" ? "api_key" : "X-API-Key"} onChange={(e) => set({ key_name: e.target.value })} /></Field>
        </div>
      )}
      {a.type === "oauth2" && (
        <>
          <Field label="Token URL" hint="Changing its host clears the saved secrets."><input className="input" value={a.token_url ?? ""} placeholder="https://auth.example.com/oauth/token" onChange={(e) => set({ token_url: e.target.value })} /></Field>
          <div className="grid-2">
            <Field label="Client ID"><input className="input" value={a.client_id ?? ""} onChange={(e) => set({ client_id: e.target.value })} /></Field>
            <Field label="Client secret goes"><select className="select" value={a.client_auth ?? "body"} onChange={(e) => set({ client_auth: e.target.value as "body" | "basic" })}><option value="body">In the form body</option><option value="basic">As basic auth</option></select></Field>
            <Field label="Scope (optional)"><input className="input" value={a.scope ?? ""} onChange={(e) => set({ scope: e.target.value })} /></Field>
            <Field label="Audience (optional)"><input className="input" value={a.audience ?? ""} onChange={(e) => set({ audience: e.target.value })} /></Field>
          </div>
        </>
      )}
      {a.type === "login" && (
        <>
          <div className="row" style={{ flexWrap: "nowrap", alignItems: "flex-end" }}>
            <Field label="Method"><select className="select" value={login.method ?? "POST"} onChange={(e) => setLogin({ method: e.target.value })}>{["POST", "GET", "PUT"].map((m) => <option key={m}>{m}</option>)}</select></Field>
            <div style={{ flex: 1 }}><Field label="Login URL" hint="Changing its host clears the saved secrets."><input className="input" value={login.url} placeholder="https://api.example.com/auth/login" onChange={(e) => setLogin({ url: e.target.value })} /></Field></div>
          </div>
          <p className="small" style={{ margin: "0 0 8px" }}>{loginSummary(login, inject)}</p>
          <details className="pf-more">
          <summary>Login details</summary>
          <HeadersEditor headers={login.headers ?? []} onChange={(headers) => setLogin({ headers })} hint="Values may use {{secrets.name}}." />
          <Field label="Content type"><select className="select" value={login.content_type ?? "application/json"} onChange={(e) => setLogin({ content_type: e.target.value })}>
            {["application/json", "application/x-www-form-urlencoded", "text/plain"].map((c) => <option key={c}>{c}</option>)}</select></Field>
          <Field label="Body" hint="Name secrets as {{secrets.name}}: each gets its own field below, stored encrypted. Values are escaped for the content type.">
            <textarea className="textarea code" rows={3} value={login.body ?? ""} onChange={(e) => setLogin({ body: e.target.value })} />
          </Field>
          <div className="grid-2">
            <Field label="Read the token from"><select className="select" value={login.token_from} onChange={(e) => setLogin({ token_from: e.target.value as LoginRequest["token_from"] })}>
              <option value="json">JSON path</option><option value="header">A response header</option><option value="regex">A regular expression</option></select></Field>
            <Field label={login.token_from === "header" ? "Header name" : login.token_from === "regex" ? "Expression (first group)" : "JSON path"} hint={login.token_from === "json" ? "e.g. data.access_token or items[0].token" : undefined}>
              <input className="input code" value={login.token_path} onChange={(e) => setLogin({ token_path: e.target.value })} /></Field>
            <Field label="Expiry"><select className="select" value={login.expiry_from || "none"} onChange={(e) => setLogin({ expiry_from: e.target.value as LoginRequest["expiry_from"] })}>
              <option value="none">Unknown (1 hour, renewed on 401)</option><option value="json">From a JSON path</option><option value="fixed">Fixed lifetime</option></select></Field>
            {login.expiry_from === "json" && <Field label="Expiry JSON path" hint="Seconds left, a Unix time or an ISO date."><input className="input code" value={login.expiry_path ?? ""} onChange={(e) => setLogin({ expiry_path: e.target.value })} /></Field>}
            {login.expiry_from === "fixed" && <Field label="Lifetime (seconds)"><input className="input" type="number" min={10} value={login.expiry_seconds ?? 3600} onChange={(e) => setLogin({ expiry_seconds: Number(e.target.value) })} /></Field>}
          </div>
          {injectFields}
          </details>
        </>
      )}
      {tokenBased && a.type !== "login" && injectFields}
      {authSecretFields(a).map((f) => <SecretInput key={f.key} label={f.label} saved={saved} value={secrets[f.key] ?? ""} onChange={(v) => setSecret(f.key, v)} />)}
    </fieldset>
  );
}

// ---- the custom HTTP gateway, step by step ----

/** PasteExamples: paste the gateway's curl or Postman examples (send, and its login if it has one)
 * and Pulse fills the steps below; the notes say what's left to check. */
export function PasteExamples({ onApply }: { onApply: (r: Imported) => void }) {
  const [send, setSend] = useState("");
  const [login, setLogin] = useState("");
  const [answer, setAnswer] = useState("");
  const [result, setResult] = useState<{ notes: string[] } | { error: string } | null>(null);
  const fill = () => {
    const r = importExamples(send, login, answer);
    setResult(r);
    if (!("error" in r)) onApply(r);
  };
  return (
    <details className="pf-paste" open>
      <summary><strong>Have examples from the gateway's docs or Postman?</strong> Paste them and Pulse fills in the steps below.</summary>
      <Field label="Send-message request" hint="The curl or postman request command for sending one message.">
        <textarea className="textarea code" rows={5} value={send} onChange={(e) => setSend(e.target.value)} placeholder={"curl --location 'https://api.example.com/v1/message' \\\n--header 'Authorization: Bearer …' \\\n--data '{\"to\": \"…\", \"text\": \"…\"}'"} spellCheck={false} />
      </Field>
      <div className="grid-2">
        <Field label="Login request (optional)" hint="If the gateway first gives you a token from a login address.">
          <textarea className="textarea code" rows={4} value={login} onChange={(e) => setLogin(e.target.value)} placeholder={"curl 'https://api.example.com/auth/login' --data '{\"username\": \"…\", \"password\": \"…\"}'"} spellCheck={false} />
        </Field>
        <Field label="Login answer (optional)" hint="An example of what the login returns, so Pulse finds the token.">
          <textarea className="textarea code" rows={4} value={answer} onChange={(e) => setAnswer(e.target.value)} placeholder={'{"JWTAUTH": "eyJ…", "user_data": {…}}'} spellCheck={false} />
        </Field>
      </div>
      <div className="row" style={{ alignItems: "center" }}>
        <button type="button" className="btn" disabled={!send.trim()} onClick={fill}>Fill in the form</button>
        <span className="xs muted">Passwords, keys and tokens in the examples go into the secret fields (stored encrypted).</span>
      </div>
      {result && "error" in result && <div className="alert error" style={{ marginTop: 10 }}>{result.error}</div>}
      {result && !("error" in result) && (
        <div className="alert ok" style={{ marginTop: 10 }}>
          Filled in. Check the steps below{result.notes.length ? ":" : ", then Preview request."}
          {result.notes.length > 0 && <ul style={{ margin: "6px 0 0", paddingLeft: 18 }}>{result.notes.map((n) => <li key={n}>{n}</li>)}</ul>}
        </div>
      )}
    </details>
  );
}

/** Step: one numbered part of the gateway form. */
export function Step({ n, title, hint, children }: { n: number; title: string; hint?: ReactNode; children: ReactNode }) {
  return (
    <section className="pf-step" aria-label={title}>
      <h4><span className="pf-step-n">{n}</span>{title}</h4>
      {hint && <p className="xs muted pf-step-hint">{hint}</p>}
      {children}
    </section>
  );
}

/** GatewayModeChoice: does the gateway take Pulse's own request, or does Pulse adapt to its API. */
export function GatewayModeChoice({ mode, onChange }: { mode: string; onChange: (m: "standard" | "custom") => void }) {
  const custom = mode === "custom";
  return (
    <div className="choice-cards" role="radiogroup" aria-label="How Pulse calls your gateway">
      <button type="button" role="radio" aria-checked={!custom} className={!custom ? "on" : ""} onClick={(e) => { if (custom) touched(e.currentTarget); onChange("standard"); }}>
        <strong>Your developers build an endpoint for Pulse</strong>
        <span>Pulse posts each message as JSON in its own format, signed with a secret. Simplest: you only enter the address and the secret.</span>
      </button>
      <button type="button" role="radio" aria-checked={custom} className={custom ? "on" : ""} onClick={(e) => { if (!custom) touched(e.currentTarget); onChange("custom"); }}>
        <strong>Connect to a gateway's existing API</strong>
        <span>Your SMS or messaging service already has an API. You tell Pulse where to send, how to sign in and what to send.</span>
      </button>
    </div>
  );
}

const useRequest = (channel: Channel, config: Record<string, string>, setConfig: Setter) => {
  const r = parseRequest(config.request, channel);
  return [r, (p: Partial<CustomRequest>) => setConfig("request", serializeRequest({ ...r, ...p }))] as const;
};

/** RequestTarget: the custom request's method and address. */
export function RequestTarget({ channel, config, setConfig }: { channel: Channel; config: Record<string, string>; setConfig: Setter }) {
  const [r, set] = useRequest(channel, config, setConfig);
  return (
    <div className="pf-target">
      <Field label="Method">
        <select className="select" value={r.method} onChange={(e) => set({ method: e.target.value })}>{["POST", "PUT", "PATCH", "GET"].map((m) => <option key={m}>{m}</option>)}</select>
      </Field>
      <Field label="Address (URL)" hint="From your gateway's API documentation, e.g. https://api.example.com/v1/sms/send. Changing its host later clears the saved secrets.">
        <input className="input code" value={r.url} placeholder="https://api.example.com/v1/sms/send" onChange={(e) => set({ url: e.target.value })} />
      </Field>
    </div>
  );
}

// The placeholders offered as one-click inserts in the body, by channel.
const INSERTS: Record<string, [string, string][]> = {
  email: [["message.to", "Email address"], ["message.subject", "Subject"], ["message.html", "HTML"], ["message.text", "Plain text"], ["contact.first_name", "First name"], ["message.id", "Pulse message ID"]],
  other: [["message.to", "Phone or address"], ["message.text", "Message text"], ["contact.first_name", "First name"], ["message.sender_id", "Sender ID"], ["message.dlt_template_id", "DLT template ID"], ["message.id", "Pulse message ID"]],
};

/** RequestBody: what Pulse sends (content type, body with one-click placeholders, extra headers). */
export function RequestBody({ channel, config, setConfig }: { channel: Channel; config: Record<string, string>; setConfig: Setter }) {
  const [r, set] = useRequest(channel, config, setConfig);
  const area = useRef<HTMLTextAreaElement>(null);
  const insert = (v: string) => {
    const el = area.current;
    const body = r.body ?? "";
    const text = `{{${v}}}`;
    const at = el ? el.selectionStart : body.length;
    const to = el ? el.selectionEnd : body.length;
    set({ body: body.slice(0, at) + text + body.slice(to) });
    requestAnimationFrame(() => { el?.focus(); el?.setSelectionRange(at + text.length, at + text.length); });
  };
  const headers = r.headers ?? [];
  return (
    <>
      <Field label="Format">
        <select className="select" value={r.content_type ?? "application/json"} onChange={(e) => set({ content_type: e.target.value })}>
          {CONTENT_TYPES.map(([c, label]) => <option key={c} value={c}>{label}</option>)}
        </select>
      </Field>
      <Field label="Body" hint="Copy the example from your gateway's documentation and put placeholders where each message's details go.">
        <textarea ref={area} className="textarea code" rows={5} value={r.body ?? ""} onChange={(e) => set({ body: e.target.value })} />
      </Field>
      <div className="pf-inserts" aria-label="Insert a placeholder">
        <span className="xs muted">Insert:</span>
        {(INSERTS[channel] ?? INSERTS.other).map(([v, label]) => (
          <button type="button" key={v} className="btn sm" title={`{{${v}}}`} onClick={(e) => { touched(e.currentTarget); insert(v); }}>{label}</button>
        ))}
      </div>
      <details className="pf-more">
        <summary>How placeholders work</summary>
        <p className="xs muted">
          <code>{"{{message.x}}"}</code> is the message being sent ({MESSAGE_VARS.slice(0, 8).join(", ")}…), <code>{"{{contact.x}}"}</code> a contact field,
          {" "}<code>{"{{event.x}}"}</code> the event that triggered it and <code>{"{{campaign.x}}"}</code> the campaign. Pulse escapes each value for the format:
          in JSON write <code>"text": "{"{{message.text}}"}"</code> with the quotes, and add <code>| json</code> for a list; form values are encoded for you.
        </p>
      </details>
      <details className="pf-more" open={headers.length > 0 || undefined}>
        <summary>Extra headers{headers.length ? ` (${headers.length})` : " (optional)"}</summary>
        <HeadersEditor headers={headers} onChange={(h) => set({ headers: h })} hint="Only if the gateway asks for one. Sign-in headers are added by step 2." />
      </details>
    </>
  );
}

/** RequestResult: when the gateway's answer means "sent", and where its message ID is. */
export function RequestResult({ channel, config, setConfig }: { channel: Channel; config: Record<string, string>; setConfig: Setter }) {
  const [r, set] = useRequest(channel, config, setConfig);
  const min = r.success.status_min ?? 200;
  const max = r.success.status_max ?? 299;
  const custom = min !== 200 || max !== 299 || !!r.success.json_path;
  return (
    <>
      <p className="small" style={{ margin: "0 0 10px" }}>
        Sent when the gateway answers with status <strong>{min}–{max}</strong>
        {r.success.json_path ? <> and <code>{r.success.json_path}</code> {r.success.json_value ? <>is <code>{r.success.json_value}</code></> : "is present and not false"}</> : null}.
      </p>
      <details className="pf-more" open={custom || undefined}>
        <summary>Change this</summary>
        <p className="xs muted">Some gateways answer 200 even on failure and put the result in the answer, e.g. <code>{'{"status": "success"}'}</code>: then check that field too.</p>
        <div className="grid-2">
          <Field label="Status from"><input className="input" type="number" value={min} onChange={(e) => set({ success: { ...r.success, status_min: Number(e.target.value), status_max: max } })} /></Field>
          <Field label="to"><input className="input" type="number" value={max} onChange={(e) => set({ success: { ...r.success, status_max: Number(e.target.value), status_min: min } })} /></Field>
          <Field label="And this field in the answer (optional)" hint="e.g. status or data.result"><input className="input code" value={r.success.json_path ?? ""} onChange={(e) => set({ success: { ...r.success, json_path: e.target.value } })} /></Field>
          <Field label="Equals (optional)" hint="Empty: present and not false."><input className="input" value={r.success.json_value ?? ""} onChange={(e) => set({ success: { ...r.success, json_value: e.target.value } })} /></Field>
        </div>
      </details>
      <Field label="The gateway's message ID is at (optional)" hint="The field in the answer holding the gateway's own ID for the message, e.g. id or data.message_id. Needed to match delivery reports.">
        <input className="input code" value={r.id_path ?? ""} placeholder="id" onChange={(e) => set({ id_path: e.target.value })} />
      </Field>
    </>
  );
}

/** ReceiptMapping: how delivery reports the gateway posts back are read. */
export function ReceiptMapping({ channel, config, setConfig, webhookURL }: { channel: Channel; config: Record<string, string>; setConfig: Setter; webhookURL?: string }) {
  const [r, set] = useRequest(channel, config, setConfig);
  const [statusText, setStatusText] = useState<string | null>(null);
  const rc = r.receipt;
  const statuses = parseStatuses(statusText ?? formatStatuses(rc?.statuses));
  return (
    <>
      <label className="check">
        <input type="checkbox" checked={!!rc} onChange={(e) => set({ receipt: e.target.checked ? { id_path: "id", status_path: "status", statuses: { delivered: "delivered", read: "read", failed: "failed" }, verify: "query", verify_name: "token" } : undefined })} />
        The gateway sends delivery reports
      </label>
      {rc && (
        <>
          <p className="xs muted" style={{ marginTop: -6 }}>
            Give the gateway {webhookURL ? <code>{webhookURL}</code> : "this provider's delivery-report address (shown after saving)"} with the receipt token added as below. Pulse reads each report like this:
          </p>
          <div className="grid-2">
            <Field label="Message ID at"><input className="input code" value={rc.id_path} onChange={(e) => set({ receipt: { ...rc, id_path: e.target.value } })} /></Field>
            <Field label="Status at"><input className="input code" value={rc.status_path} onChange={(e) => set({ receipt: { ...rc, status_path: e.target.value } })} /></Field>
          </div>
          <Field label="Status values" hint={`One per line: the gateway's word = ${RECEIPT_STATUSES.join(" | ")}. Others are ignored.${statuses.errors.length ? " Not understood: " + statuses.errors.join("; ") : ""}`}>
            <textarea className="textarea code" rows={3} value={statusText ?? formatStatuses(rc.statuses)}
              onChange={(e) => { setStatusText(e.target.value); set({ receipt: { ...rc, statuses: parseStatuses(e.target.value).statuses } }); }} />
          </Field>
          <div className="grid-2">
            <Field label="Reports prove they're genuine with" hint="The receipt token below. Reports without it are refused.">
              <select className="select" value={rc.verify} onChange={(e) => set({ receipt: { ...rc, verify: e.target.value as "header" | "query" | "hmac" } })}>
                <option value="query">The token in the address (?token=…)</option><option value="header">The token in a header</option><option value="hmac">An HMAC-SHA256 of the body in a header</option></select>
            </Field>
            <Field label={rc.verify === "query" ? "Parameter name" : "Header name"}><input className="input" value={rc.verify_name} onChange={(e) => set({ receipt: { ...rc, verify_name: e.target.value } })} /></Field>
          </div>
          <details className="pf-more" open={!!(rc.items_path || rc.reason_path || rc.code_path) || undefined}>
            <summary>More fields (optional)</summary>
            <div className="grid-2">
              <Field label="List of reports at" hint="When one post carries several."><input className="input code" value={rc.items_path ?? ""} onChange={(e) => set({ receipt: { ...rc, items_path: e.target.value } })} /></Field>
              <Field label="Failure reason at"><input className="input code" value={rc.reason_path ?? ""} onChange={(e) => set({ receipt: { ...rc, reason_path: e.target.value } })} /></Field>
              <Field label="Error code at"><input className="input code" value={rc.code_path ?? ""} onChange={(e) => set({ receipt: { ...rc, code_path: e.target.value } })} /></Field>
            </div>
          </details>
        </>
      )}
    </>
  );
}

const CONTENT_TYPES: [string, string][] = [
  ["application/json", "JSON (application/json)"], ["application/x-www-form-urlencoded", "Form fields (application/x-www-form-urlencoded)"],
  ["text/plain", "Plain text"], ["application/xml", "XML"],
];

type Draft = { provider_id?: number; provider: string; config: Record<string, string>; secrets: Record<string, string> };
type AuthResult = { ok: boolean; result?: { type: string; token?: string; expires?: string; static: boolean; server?: string; logged_in?: boolean }; error?: string; meaning?: string };
type PreviewResult = { method: string; url: string; headers: Record<string, string>; body: string };

async function postRaw<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(path, { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json", "X-Pulse-CSRF": "1" }, body: JSON.stringify(body) });
  const data = await res.json();
  if (!res.ok && !("ok" in data)) throw new Error(data.error ?? `HTTP ${res.status}`);
  return data as T;
}

/** ProviderTools: "Test connection" (when the provider has custom auth) and "Preview request". */
export function ProviderTools({ draft, canAuth, canPreview }: { draft: Draft; canAuth: boolean; canPreview: boolean }) {
  const test = useMutation({ mutationFn: () => postRaw<AuthResult>("/v1/channels/providers/check-auth", draft) });
  const preview = useMutation({ mutationFn: () => api.post<PreviewResult>("/v1/channels/providers/preview", draft) });
  if (!canAuth && !canPreview) return null;
  return (
    <div className="provider-tools" style={{ margin: "10px 0 14px" }}>
      <div className="row">
        {canAuth && <button className="btn sm" disabled={test.isPending} onClick={() => test.mutate()}>Test connection</button>}
        {canPreview && <button className="btn sm" disabled={preview.isPending} onClick={() => preview.mutate()}>Preview request</button>}
        <span className="xs muted">Uses the settings above; empty secrets use the saved ones.</span>
      </div>
      <ErrorNote error={test.error ?? preview.error} />
      {test.data?.ok && test.data.result && (
        <div className="alert ok" style={{ marginTop: 8 }}>
          {test.data.result.server ? <>{test.data.result.logged_in ? "Logged in to" : "Connected to"} <code>{test.data.result.server}</code>. Use <strong>Send test</strong> to check a real message arrives.</>
            : test.data.result.static ? "Nothing to log in to: the credentials are sent with each request." : <>Logged in. Token <code>{test.data.result.token}</code>{test.data.result.expires ? <>, expires {new Date(test.data.result.expires).toLocaleString()}</> : null}.</>}
        </div>
      )}
      {test.data && !test.data.ok && <div className="alert error" style={{ marginTop: 8 }}><strong>{test.data.meaning}.</strong><br />{test.data.error}</div>}
      {preview.data && (
        <pre className="code-block" style={{ marginTop: 8, whiteSpace: "pre-wrap", wordBreak: "break-all", maxHeight: 280, overflow: "auto" }}>
          {`${preview.data.method} ${preview.data.url}\n${Object.entries(preview.data.headers).sort().map(([k, v]) => `${k}: ${v}`).join("\n")}\n\n${preview.data.body}`}
        </pre>
      )}
    </div>
  );
}
