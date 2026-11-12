import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { api, type Channel, type ProviderField } from "../api";
import { ErrorNote, Field } from "../ui";
import {
  AUTH_TYPES, MESSAGE_VARS, RECEIPT_STATUSES, authSecretFields, formatStatuses, newLogin, parseAuth, parseRequest, parseStatuses,
  serializeAuth, serializeRequest, type AuthConfig, type CustomRequest, type Header, type LoginRequest,
} from "../providerAuth";

// The provider form's M5 parts: custom HTTP authentication, the gateway's custom request, and the
// "Test connection" / "Preview request" tools. Secrets are typed in and never shown again.

type Setter = (key: string, value: string) => void;

/** ConfigField renders one catalog setting: a select when it has options, else an input. */
export function ConfigField({ f, value, onChange }: { f: ProviderField; value: string; onChange: (v: string) => void }) {
  const label = f.label + (f.required ? "" : " (optional)");
  return (
    <Field label={label} hint={f.hint}>
      {f.options ? (
        <select className="select" value={value || f.default || f.options[0]} onChange={(e) => onChange(e.target.value)}>
          {f.options.map((o) => <option key={o} value={o}>{f.option_labels?.[o] ?? o}</option>)}
        </select>
      ) : (
        <input className="input" value={value} placeholder={f.placeholder ?? f.default} onChange={(e) => onChange(e.target.value)} />
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
          <button className="btn sm ghost" onClick={() => onChange(headers.filter((_, j) => j !== i))}>Remove</button>
        </div>
      ))}
      <button className="btn sm" onClick={() => onChange([...headers, { name: "", value: "" }])}>Add header</button>
    </Field>
  );
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
        </>
      )}
      {tokenBased && (
        <div className="grid-2">
          <Field label="Token goes in"><select className="select" value={inject.in} onChange={(e) => set({ inject: { ...inject, in: e.target.value as "header" | "query" | "body" } })}>
            <option value="header">A header</option><option value="query">A query parameter</option><option value="body">A body field</option></select></Field>
          <Field label="Name"><input className="input" value={inject.name ?? ""} placeholder={inject.in === "header" ? "Authorization" : "access_token"} onChange={(e) => set({ inject: { ...inject, name: e.target.value } })} /></Field>
          <Field label="Value template" hint="{{token}} is the token."><input className="input code" value={inject.template ?? ""} placeholder={inject.in === "header" && !inject.name ? "Bearer {{token}}" : "{{token}}"} onChange={(e) => set({ inject: { ...inject, template: e.target.value } })} /></Field>
        </div>
      )}
      {authSecretFields(a).map((f) => <SecretInput key={f.key} label={f.label} saved={saved} value={secrets[f.key] ?? ""} onChange={(v) => setSecret(f.key, v)} />)}
    </fieldset>
  );
}

/** CustomRequestEditor edits config.request (the gateway's custom-request mode). */
export function CustomRequestEditor({ channel, config, setConfig }: { channel: Channel; config: Record<string, string>; setConfig: Setter }) {
  const r = parseRequest(config.request, channel);
  const set = (p: Partial<CustomRequest>) => setConfig("request", serializeRequest({ ...r, ...p }));
  const [statusText, setStatusText] = useState<string | null>(null);
  const rc = r.receipt;
  const statuses = parseStatuses(statusText ?? formatStatuses(rc?.statuses));
  return (
    <fieldset className="fieldset">
      <legend>Request</legend>
      <p className="xs muted" style={{ marginBottom: 10 }}>
        Templates take {"{{contact.x}}"}, {"{{event.x}}"}, {"{{campaign.x}}"} and {"{{message.x}}"} ({MESSAGE_VARS.slice(0, 8).join(", ")}…). In a JSON body every value is
        JSON-escaped (write <code>"text": "{"{{message.text}}"}"</code>; use <code>| json</code> for lists); form and query values are percent-encoded. The URL's host is fixed.
      </p>
      <div className="row" style={{ flexWrap: "nowrap", alignItems: "flex-end" }}>
        <Field label="Method"><select className="select" value={r.method} onChange={(e) => set({ method: e.target.value })}>{["POST", "PUT", "PATCH", "GET"].map((m) => <option key={m}>{m}</option>)}</select></Field>
        <div style={{ flex: 1 }}><Field label="URL" hint="Changing its host clears the saved secrets."><input className="input code" value={r.url} onChange={(e) => set({ url: e.target.value })} /></Field></div>
      </div>
      <HeadersEditor headers={r.headers ?? []} onChange={(headers) => set({ headers })} />
      <Field label="Content type"><select className="select" value={r.content_type ?? "application/json"} onChange={(e) => set({ content_type: e.target.value })}>
        {["application/json", "application/x-www-form-urlencoded", "text/plain", "application/xml"].map((c) => <option key={c}>{c}</option>)}</select></Field>
      <Field label="Body"><textarea className="textarea code" rows={5} value={r.body ?? ""} onChange={(e) => set({ body: e.target.value })} /></Field>
      <h4 className="small" style={{ margin: "8px 0" }}>Success and message ID</h4>
      <div className="grid-2">
        <Field label="Success status from"><input className="input" type="number" value={r.success.status_min ?? 200} onChange={(e) => set({ success: { ...r.success, status_min: Number(e.target.value), status_max: r.success.status_max ?? 299 } })} /></Field>
        <Field label="to"><input className="input" type="number" value={r.success.status_max ?? 299} onChange={(e) => set({ success: { ...r.success, status_max: Number(e.target.value), status_min: r.success.status_min ?? 200 } })} /></Field>
        <Field label="And the answer's JSON path (optional)" hint="e.g. status"><input className="input code" value={r.success.json_path ?? ""} onChange={(e) => set({ success: { ...r.success, json_path: e.target.value } })} /></Field>
        <Field label="Equals (optional)" hint="Empty: present and not false."><input className="input" value={r.success.json_value ?? ""} onChange={(e) => set({ success: { ...r.success, json_value: e.target.value } })} /></Field>
        <Field label="Provider message ID at (JSON path)" hint="Matches delivery receipts to the message."><input className="input code" value={r.id_path ?? ""} onChange={(e) => set({ id_path: e.target.value })} /></Field>
      </div>
      <label className="check"><input type="checkbox" checked={!!rc} onChange={(e) => set({ receipt: e.target.checked ? { id_path: "id", status_path: "status", statuses: { delivered: "delivered", read: "read", failed: "failed" }, verify: "query", verify_name: "token" } : undefined })} />The provider posts delivery receipts to this provider's webhook URL</label>
      {rc && (
        <div className="grid-2">
          <Field label="List of receipts at (optional)" hint="When one post carries several."><input className="input code" value={rc.items_path ?? ""} onChange={(e) => set({ receipt: { ...rc, items_path: e.target.value } })} /></Field>
          <Field label="Message ID at"><input className="input code" value={rc.id_path} onChange={(e) => set({ receipt: { ...rc, id_path: e.target.value } })} /></Field>
          <Field label="Status at"><input className="input code" value={rc.status_path} onChange={(e) => set({ receipt: { ...rc, status_path: e.target.value } })} /></Field>
          <Field label="Failure reason at (optional)"><input className="input code" value={rc.reason_path ?? ""} onChange={(e) => set({ receipt: { ...rc, reason_path: e.target.value } })} /></Field>
          <Field label="Error code at (optional)"><input className="input code" value={rc.code_path ?? ""} onChange={(e) => set({ receipt: { ...rc, code_path: e.target.value } })} /></Field>
          <Field label="Status values" hint={`One per line: provider value = ${RECEIPT_STATUSES.join(" | ")}. Others are ignored.${statuses.errors.length ? " Not understood: " + statuses.errors.join("; ") : ""}`}>
            <textarea className="textarea code" rows={4} value={statusText ?? formatStatuses(rc.statuses)}
              onChange={(e) => { setStatusText(e.target.value); set({ receipt: { ...rc, statuses: parseStatuses(e.target.value).statuses } }); }} />
          </Field>
          <Field label="Receipts prove themselves with" hint="The receipt token (a secret below). Receipts without it are refused.">
            <select className="select" value={rc.verify} onChange={(e) => set({ receipt: { ...rc, verify: e.target.value as "header" | "query" | "hmac" } })}>
              <option value="query">The token in a query parameter</option><option value="header">The token in a header</option><option value="hmac">An HMAC-SHA256 of the body in a header</option></select>
          </Field>
          <Field label={rc.verify === "query" ? "Parameter name" : "Header name"}><input className="input" value={rc.verify_name} onChange={(e) => set({ receipt: { ...rc, verify_name: e.target.value } })} /></Field>
        </div>
      )}
    </fieldset>
  );
}

type Draft = { provider_id?: number; provider: string; config: Record<string, string>; secrets: Record<string, string> };
type AuthResult = { ok: boolean; result?: { type: string; token?: string; expires?: string; static: boolean }; error?: string; meaning?: string };
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
          {test.data.result.static ? "Nothing to log in to: the credentials are sent with each request." : <>Logged in. Token <code>{test.data.result.token}</code>{test.data.result.expires ? <>, expires {new Date(test.data.result.expires).toLocaleString()}</> : null}.</>}
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
