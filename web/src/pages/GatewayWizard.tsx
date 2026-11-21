// The custom HTTP gateway ("Connect to a gateway's existing API") as four plain steps, each with a
// Try it: 1. Sign in, 2. Send, 3. Accepted or failed?, 4. Delivery reports (they call us, or we
// ask them). Everything edits config.request / config.auth as before.
import { useMutation } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import { api, type Channel } from "../api";
import {
  formatStatuses, parseRequest, parseStatuses, RECEIPT_STATUS_LABELS, RECEIPT_STATUSES, serializeRequest, statusWordsIn, suggestStatuses,
  type CustomRequest, type ReceiptMapping, type ReceiptStatus,
} from "../providerAuth";
import { ErrorNote, Field } from "../ui";
import { ProviderTools, RequestBody, RequestResult, RequestTarget } from "./ProviderSettings";
import "./gateway-wizard.css";

type Setter = (k: string, v: string) => void;
type Draft = { provider_id?: number; provider: string; config: Record<string, string>; secrets: Record<string, string> };
type Receipt = { provider_message_id: string; status: string; reason?: string; unreachable?: boolean };

export type WizardStep = 1 | 2 | 3 | 4;
export const WIZARD_STEPS: { n: WizardStep; title: string; hint: string }[] = [
  { n: 1, title: "Sign in", hint: "How the gateway knows it's you" },
  { n: 2, title: "Send", hint: "The send-message request" },
  { n: 3, title: "Accepted or failed?", hint: "How Pulse reads the answer" },
  { n: 4, title: "Delivery reports", hint: "Delivered, failed, read" },
];

/** stepDone says whether a step has what it needs (for the ticks in the step bar). */
export function stepDone(n: WizardStep, r: CustomRequest, authType: string): boolean {
  switch (n) {
    case 1: return authType !== "";
    case 2: return /^https:\/\/[^/]+/.test(r.url) && (r.method === "GET" || !!r.body?.trim());
    case 3: return !!r.id_path || r.match_by === "pulse";
    case 4: return !!r.receipt;
  }
}

/** reportMode is how delivery reports arrive: none, a webhook (they call us) or status requests (we ask them). */
export type ReportMode = "none" | "webhook" | "poll";
export function reportMode(r: CustomRequest): ReportMode {
  if (r.poll) return "poll";
  return r.receipt ? "webhook" : "none";
}

export function GatewayWizard({ channel, config, setKey, auth, authType, draft, webhookURL, receiptTokenField, note, paste }: {
  channel: Channel; config: Record<string, string>; setKey: Setter; auth: ReactNode; authType: string; draft: Draft;
  webhookURL?: string; receiptTokenField: ReactNode; note?: ReactNode; paste: ReactNode;
}) {
  const [step, setStep] = useState<WizardStep>(1);
  const r = parseRequest(config.request, channel);
  const set = (p: Partial<CustomRequest>) => setKey("request", serializeRequest({ ...r, ...p }));
  const cur = WIZARD_STEPS[step - 1];
  return (
    <div className="gw">
      {paste}
      <ol className="gw-bar" aria-label="Steps">
        {WIZARD_STEPS.map((s) => {
          const done = stepDone(s.n, r, authType);
          return (
            <li key={s.n}>
              <button type="button" className={`gw-chip${step === s.n ? " on" : ""}${done ? " done" : ""}`} aria-current={step === s.n ? "step" : undefined} onClick={() => setStep(s.n)}>
                <span className="gw-n">{done && step !== s.n ? "✓" : s.n}</span>
                <span><strong>{s.title}</strong><small>{s.hint}</small></span>
              </button>
            </li>
          );
        })}
      </ol>
      <section className="gw-step" aria-label={cur.title}>
        {note}
        {step === 1 && (
          <>
            <p className="small muted gw-lead">As the gateway's documentation says. With <strong>Login, then a token</strong> Pulse calls the login address, picks the token out of the answer, adds it to every send (a header, a query parameter or a body field — you choose), and logs in again when it expires or a send is refused.</p>
            {auth}
            <TryBox title="Try it" hint="Logs in with the details above and shows the token Pulse got.">
              <ProviderTools draft={draft} canAuth canPreview={false} />
            </TryBox>
          </>
        )}
        {step === 2 && (
          <>
            <RequestTarget channel={channel} config={config} setConfig={setKey} />
            <RequestBody channel={channel} config={config} setConfig={setKey} />
            <p className="xs muted">To have delivery reports name the exact campaign and message, put <code>{"{{message.id}}"}</code> (Pulse's message id) or <code>{"{{message.ref}}"}</code> (a signed reference holding the campaign and template) in a field the gateway sends back, such as a client reference or custom data.</p>
            <TryBox title="Try it" hint="Shows the exact request for a sample message, with the sign-in added and secrets hidden. Nothing is sent.">
              <ProviderTools draft={draft} canAuth={false} canPreview />
            </TryBox>
          </>
        )}
        {step === 3 && <AnswerStep r={r} set={set} channel={channel} config={config} setKey={setKey} draft={draft} />}
        {step === 4 && <ReportsStep r={r} set={set} draft={draft} webhookURL={webhookURL} receiptTokenField={receiptTokenField} />}
      </section>
      <div className="gw-nav">
        <button type="button" className="btn" disabled={step === 1} onClick={() => setStep((step - 1) as WizardStep)}>← Back</button>
        <span className="xs muted">Step {step} of 4</span>
        {step < 4
          ? <button type="button" className="btn primary" onClick={() => setStep((step + 1) as WizardStep)}>Next: {WIZARD_STEPS[step].title} →</button>
          : <span className="xs muted">Then Save provider, and Send test for a real message.</span>}
      </div>
    </div>
  );
}

/** offline is the draft for checks that call nothing (pasted answers and reports): the status
 * request, possibly half filled in, isn't needed for them. */
function offline(draft: Draft): Draft {
  try {
    const r = JSON.parse(draft.config.request ?? "{}") as CustomRequest;
    delete r.poll;
    if (r.receipt && !r.receipt.verify) r.receipt = { ...r.receipt, verify: "query", verify_name: "token" };
    return { ...draft, config: { ...draft.config, request: JSON.stringify(r) } };
  } catch {
    return draft;
  }
}

function TryBox({ title, hint, children }: { title: string; hint: string; children: ReactNode }) {
  return (
    <div className="gw-try">
      <div className="gw-try-head"><strong>⚡ {title}</strong><span className="xs muted">{hint}</span></div>
      {children}
    </div>
  );
}

/** AnswerStep: when the answer means accepted, failed or "can't get it here", and where the id is. */
function AnswerStep({ r, set, channel, config, setKey, draft }: {
  r: CustomRequest; set: (p: Partial<CustomRequest>) => void; channel: Channel; config: Record<string, string>; setKey: Setter; draft: Draft;
}) {
  const f = r.failure ?? { json_path: "", values: [] };
  const [values, setValues] = useState((r.failure?.values ?? []).join(", "));
  const [status, setStatus] = useState(200);
  const [body, setBody] = useState("");
  const check = useMutation({
    mutationFn: () => api.post<{ accepted: boolean; provider_message_id?: string; error?: string; fallback?: boolean; warning?: string; matched_by?: string }>(
      "/v1/channels/providers/check-answer", { ...offline(draft), status, body }),
  });
  const fallback = channel === "whatsapp" || channel === "rcs";
  return (
    <>
      <h5 className="gw-h">Accepted</h5>
      <RequestResult channel={channel} config={config} setConfig={setKey} />
      <h5 className="gw-h">Failed</h5>
      <p className="xs muted gw-lead">Some gateways answer 200 even when they refuse a message. Name the field and the words that mean "refused".</p>
      <div className="grid-2">
        <Field label="Failed when this field" hint="e.g. status or error.code"><input className="input code" value={f.json_path} placeholder="status"
          onChange={(e) => set({ failure: { ...f, json_path: e.target.value } })} /></Field>
        <Field label="Is one of" hint="Comma-separated, any case"><input className="input" value={values} placeholder="error, failed, rejected"
          onChange={(e) => { setValues(e.target.value); set({ failure: { ...f, values: e.target.value.split(",") } }); }} /></Field>
      </div>
      <label className="check">
        <input type="checkbox" checked={!!f.unreachable} onChange={(e) => set({ failure: { ...f, unreachable: e.target.checked } })} />
        It means the person can't get {channel === "email" ? "email" : "messages"} here{fallback ? " (Pulse sends the SMS fallback instead)" : ""}
      </label>
      <TryBox title="Try it" hint="Paste an answer from the gateway's docs; Pulse says how it reads it. Nothing is sent.">
        <div className="row" style={{ alignItems: "flex-start", flexWrap: "nowrap" }}>
          <Field label="Status"><input className="input" type="number" style={{ width: 90 }} value={status} data-no-dirty onChange={(e) => setStatus(Number(e.target.value))} /></Field>
          <div style={{ flex: 1 }}><Field label="Answer body"><textarea className="textarea code" rows={3} value={body} data-no-dirty placeholder={'{"status": "success", "data": {"message_id": "abc123"}}'} onChange={(e) => setBody(e.target.value)} /></Field></div>
        </div>
        <button type="button" className="btn sm" disabled={check.isPending || !body.trim()} onClick={() => check.mutate()}>Check this answer</button>
        <ErrorNote error={check.error} />
        {check.data && (check.data.accepted
          ? <div className="alert ok gw-out">✅ Accepted.{" "}{check.data.matched_by === "pulse" ? "Reports are matched by Pulse's message id." : check.data.provider_message_id ? <>Gateway's message id: <code>{check.data.provider_message_id}</code></> : "No message id."}{check.data.warning && <div className="xs">{check.data.warning}</div>}</div>
          : <div className="alert error gw-out">❌ Refused{check.data.fallback ? " — the person can't get it here, so the SMS fallback would run" : ""}.<div className="xs">{check.data.error}</div></div>)}
      </TryBox>
    </>
  );
}

const WEBHOOK_SAMPLE = '[{"msgid": "abc123", "status": "DELIVRD", "reason": ""}]';

/** ReportsStep: none, they call us (a webhook) or we ask them (a status request); the mapping; Try it. */
function ReportsStep({ r, set, draft, webhookURL, receiptTokenField }: {
  r: CustomRequest; set: (p: Partial<CustomRequest>) => void; draft: Draft; webhookURL?: string; receiptTokenField: ReactNode;
}) {
  const mode = reportMode(r);
  const rc: ReceiptMapping = r.receipt ?? { id_path: "id", status_path: "status", statuses: {}, verify: "query", verify_name: "token" };
  const setRc = (p: Partial<ReceiptMapping>) => set({ receipt: { ...rc, ...p } });
  const poll = r.poll ?? { method: "GET", url: "https://", every_minutes: 10, for_hours: 24 };
  const setPoll = (p: Partial<typeof poll>) => set({ poll: { ...poll, ...p } });
  const [statusText, setStatusText] = useState<string | null>(null);
  const [sample, setSample] = useState("");
  const [askID, setAskID] = useState("");
  const parsed = parseStatuses(statusText ?? formatStatuses(rc.statuses));
  const choose = (m: ReportMode) => {
    if (m === "none") set({ receipt: undefined, poll: undefined });
    if (m === "webhook") set({ receipt: { ...rc, verify: rc.verify || "query", verify_name: rc.verify_name || "token" }, poll: undefined });
    if (m === "poll") set({ receipt: { ...rc, verify: "" }, poll });
  };
  const check = useMutation({ mutationFn: () => api.post<{ receipts: Receipt[] }>("/v1/channels/providers/check-report", { ...offline(draft), body: sample }) });
  const ask = useMutation({
    mutationFn: () => api.post<{ ok: boolean; receipts?: Receipt[]; error?: string }>("/v1/channels/providers/check-status",
      { ...draft, [r.match_by === "pulse" ? "message_id" : "provider_message_id"]: askID.trim() }),
  });
  const fillWords = () => {
    const found = statusWordsIn(sample, rc.items_path ? `${rc.items_path}.${rc.status_path}` : rc.status_path);
    const merged = { ...rc.statuses, ...suggestStatuses(found.length ? found : Object.keys(rc.statuses)) };
    setStatusText(null);
    setRc({ statuses: merged });
  };
  return (
    <>
      <div className="choice-cards gw-modes" role="radiogroup" aria-label="How delivery reports arrive">
        {([["none", "No reports", "Pulse counts a message as sent when the gateway accepts it."],
          ["webhook", "They call us", "The gateway posts each report to a Pulse address (a webhook or callback URL)."],
          ["poll", "We ask them", "The gateway has a status API: Pulse asks about each message until it's final."]] as [ReportMode, string, string][]).map(([m, t, d]) => (
          <button type="button" role="radio" key={m} aria-checked={mode === m} className={mode === m ? "on" : ""} onClick={() => choose(m)}><strong>{t}</strong><span>{d}</span></button>
        ))}
      </div>
      {mode !== "none" && (
        <>
          <Field label="Reports find their message by">
            <select className="select" value={r.match_by ?? "provider"} onChange={(e) => set({ match_by: e.target.value as "provider" | "pulse" })}>
              <option value="provider">The gateway's message id (from the send answer, step 3)</option>
              <option value="pulse">Pulse's message id ({"{{message.id}}"} sent in a field the gateway echoes back)</option>
            </select>
          </Field>
          {mode === "poll" && (
            <fieldset className="fieldset">
              <legend>Status request</legend>
              <div className="row" style={{ flexWrap: "nowrap", alignItems: "flex-end" }}>
                <Field label="Method"><select className="select" value={poll.method} onChange={(e) => setPoll({ method: e.target.value })}><option>GET</option><option>POST</option></select></Field>
                <div style={{ flex: 1 }}><Field label="Address" hint={<>Put <code>{"{{message.provider_id}}"}</code> (the gateway's id) or <code>{"{{message.id}}"}</code> where the message goes. Same sign-in as sends.</>}>
                  <input className="input code" value={poll.url} placeholder="https://api.example.com/v1/status?id={{message.provider_id}}" onChange={(e) => setPoll({ url: e.target.value })} /></Field></div>
              </div>
              {poll.method === "POST" && <Field label="Body"><textarea className="textarea code" rows={2} value={poll.body ?? ""} placeholder={'{"message_id": "{{message.provider_id}}"}'} onChange={(e) => setPoll({ body: e.target.value })} /></Field>}
              <div className="grid-2">
                <Field label="Ask every (minutes)"><input className="input" type="number" min={1} max={1440} value={poll.every_minutes ?? 10} onChange={(e) => setPoll({ every_minutes: Number(e.target.value) })} /></Field>
                <Field label="For up to (hours)" hint="Then Pulse stops asking."><input className="input" type="number" min={1} max={72} value={poll.for_hours ?? 24} onChange={(e) => setPoll({ for_hours: Number(e.target.value) })} /></Field>
              </div>
            </fieldset>
          )}
          {mode === "webhook" && (
            <fieldset className="fieldset">
              <legend>Where the gateway posts</legend>
              <p className="small" style={{ marginTop: 0 }}>Give the gateway {webhookURL ? <code className="gw-url">{webhookURL}{rc.verify === "query" ? `?${rc.verify_name || "token"}=<receipt token>` : ""}</code> : "this provider's report address (shown after you save)"}.</p>
              <div className="grid-2">
                <Field label="Reports prove they're genuine with">
                  <select className="select" value={rc.verify} onChange={(e) => setRc({ verify: e.target.value as ReceiptMapping["verify"] })}>
                    <option value="query">The token in the address (?token=…)</option><option value="header">The token in a header</option><option value="hmac">An HMAC-SHA256 of the body in a header</option></select>
                </Field>
                <Field label={rc.verify === "query" ? "Parameter name" : "Header name"}><input className="input" value={rc.verify_name} onChange={(e) => setRc({ verify_name: e.target.value })} /></Field>
              </div>
              {receiptTokenField}
            </fieldset>
          )}
          <fieldset className="fieldset">
            <legend>Reading a report</legend>
            <div className="grid-2">
              <Field label="Message id at" hint="e.g. msgid or data.id"><input className="input code" value={rc.id_path} onChange={(e) => setRc({ id_path: e.target.value })} /></Field>
              <Field label="Status at" hint="e.g. status or data.state"><input className="input code" value={rc.status_path} onChange={(e) => setRc({ status_path: e.target.value })} /></Field>
              <Field label="List of reports at (optional)" hint="When one post carries several."><input className="input code" value={rc.items_path ?? ""} onChange={(e) => setRc({ items_path: e.target.value })} /></Field>
              <Field label="Failure reason at (optional)"><input className="input code" value={rc.reason_path ?? ""} onChange={(e) => setRc({ reason_path: e.target.value })} /></Field>
            </div>
            <div className="gw-statuses">
              <div className="row" style={{ justifyContent: "space-between", alignItems: "center" }}>
                <strong className="small">The gateway's words</strong>
                <button type="button" className="btn sm" onClick={fillWords} title="Recognises DELIVRD, UNDELIV, REJECTD, SUBMITTED, read, failed…">✨ Fill in common words</button>
              </div>
              <table className="table gw-status-table">
                <thead><tr><th>When the report says</th><th>Pulse counts it as</th><th /></tr></thead>
                <tbody>
                  {Object.entries(rc.statuses).map(([word, st]) => (
                    <tr key={word}>
                      <td><code>{word}</code></td>
                      <td><select className="select" value={st} aria-label={`${word} means`} onChange={(e) => { setStatusText(null); setRc({ statuses: { ...rc.statuses, [word]: e.target.value as ReceiptStatus } }); }}>
                        {RECEIPT_STATUSES.map((s) => <option key={s} value={s}>{RECEIPT_STATUS_LABELS[s]}</option>)}</select></td>
                      <td><button type="button" className="btn ghost sm" onClick={() => { const next = { ...rc.statuses }; delete next[word]; setStatusText(null); setRc({ statuses: next }); }}>Remove</button></td>
                    </tr>
                  ))}
                </tbody>
              </table>
              <AddWord onAdd={(w, st) => { setStatusText(null); setRc({ statuses: { ...rc.statuses, [w]: st } }); }} />
              {parsed.errors.length > 0 && <p className="xs error">Not understood: {parsed.errors.join("; ")}</p>}
              <p className="xs muted">Words not listed are ignored. "Not final yet" words make Pulse ask again (status requests) or wait for the next report.</p>
            </div>
          </fieldset>
          <TryBox title="Try it" hint={mode === "poll" ? "Paste a status answer, or ask the gateway about a real message." : "Paste a report from the gateway's docs; Pulse shows what it understands. Nothing is recorded."}>
            <Field label={mode === "poll" ? "A status answer" : "A report"}><textarea className="textarea code" rows={4} value={sample} data-no-dirty placeholder={WEBHOOK_SAMPLE} onChange={(e) => setSample(e.target.value)} /></Field>
            <div className="row">
              <button type="button" className="btn sm" disabled={!sample.trim() || check.isPending} onClick={() => check.mutate()}>Check this report</button>
              {mode === "poll" && <>
                <input className="input" style={{ maxWidth: 220 }} value={askID} data-no-dirty placeholder={r.match_by === "pulse" ? "Pulse message id" : "Gateway's message id"} aria-label="Message id to ask about" onChange={(e) => setAskID(e.target.value)} />
                <button type="button" className="btn sm" disabled={!askID.trim() || ask.isPending} onClick={() => ask.mutate()}>Ask the gateway now</button>
              </>}
            </div>
            <ErrorNote error={check.error ?? ask.error} />
            {check.data && <ReceiptList rs={check.data.receipts} />}
            {ask.data && (ask.data.ok ? <ReceiptList rs={ask.data.receipts ?? []} /> : <div className="alert error gw-out">{ask.data.error}</div>)}
          </TryBox>
        </>
      )}
    </>
  );
}

function AddWord({ onAdd }: { onAdd: (word: string, st: ReceiptStatus) => void }) {
  const [w, setW] = useState("");
  const [st, setSt] = useState<ReceiptStatus>("delivered");
  return (
    <div className="row" style={{ marginTop: 6 }}>
      <input className="input code" style={{ maxWidth: 200 }} value={w} placeholder="e.g. DELIVRD" aria-label="Gateway's word" onChange={(e) => setW(e.target.value)} />
      <select className="select" style={{ maxWidth: 300 }} value={st} aria-label="Counts as" onChange={(e) => setSt(e.target.value as ReceiptStatus)}>
        {RECEIPT_STATUSES.map((s) => <option key={s} value={s}>{RECEIPT_STATUS_LABELS[s]}</option>)}
      </select>
      <button type="button" className="btn sm" disabled={!w.trim()} onClick={() => { onAdd(w.trim(), st); setW(""); }}>Add word</button>
    </div>
  );
}

const STATUS_ICON: Record<string, string> = { delivered: "✅", read: "👀", failed: "❌", bounced: "↩️", pending: "⏳" };

function ReceiptList({ rs }: { rs: Receipt[] }) {
  if (rs.length === 0) return <div className="alert error gw-out">Nothing understood: check the paths and the words above.</div>;
  return (
    <table className="table gw-out">
      <thead><tr><th>Message id</th><th>Status</th><th>Reason</th></tr></thead>
      <tbody>{rs.map((x, i) => (
        <tr key={i}><td><code>{x.provider_message_id}</code></td>
          <td>{STATUS_ICON[x.status] ?? ""} {x.status === "pending" ? "not final yet" : x.status}{x.unreachable ? " (SMS fallback)" : ""}</td>
          <td className="small muted">{x.reason}</td></tr>
      ))}</tbody>
    </table>
  );
}
