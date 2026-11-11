import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, type Webhook, type WebhookDelivery } from "../api";
import { useIsAdmin } from "../auth";
import { Empty, ErrorNote, Field, PageHead, timeAgo, useToast, useCopy } from "../ui";
import "./webhooks.css";

type Detail = { webhook: Webhook; deliveries: WebhookDelivery[] };
type TestResult = { ok: boolean; status: number; duration_ms: number; error?: string };

const SEGMENT_EVENTS = ["segment_entered", "segment_exited"];
const COMMON_EVENTS = ["purchase", "form_submitted", "email_opened", "email_clicked", "email_bounced", "sms_received", "unsubscribed"];

const fmt = (s: string) => new Date(s).toLocaleString("en-IN", { day: "numeric", month: "short", hour: "numeric", minute: "2-digit", second: "2-digit" });

function eventsSummary(events: string[]) {
  if (events.includes("*")) return "Every event";
  if (events.length === 0) return "No events";
  return events.length <= 3 ? events.join(", ") : `${events.slice(0, 3).join(", ")} and ${events.length - 3} more`;
}

const delivered = (d: WebhookDelivery) => d.status >= 200 && d.status < 300;

/** DeliveryBadge: delivered, retrying (a later attempt is due), retried (a later attempt exists), or gave up. */
function DeliveryBadge({ d, all }: { d: WebhookDelivery; all: WebhookDelivery[] }) {
  if (d.test) return delivered(d) ? <span className="badge ok">Test delivered</span> : <span className="badge bad">Test failed</span>;
  if (delivered(d)) return <span className="badge ok">Delivered</span>;
  if (d.final) return <span className="badge bad">Gave up</span>;
  const retried = all.some((x) => x.event_id === d.event_id && x.event_name === d.event_name && x.attempt > d.attempt);
  return retried ? <span className="badge">Failed, retried</span> : <span className="badge warn">Retrying</span>;
}


// ---------- list ----------

export function WebhookList() {
  const admin = useIsAdmin();
  const q = useQuery({ queryKey: ["webhooks"], queryFn: () => api.get<{ webhooks: Webhook[] }>("/v1/settings/webhooks"), enabled: admin });
  const hooks = q.data?.webhooks ?? [];
  return (
    <div className="page">
      <PageHead
        title="Webhooks"
        intro="Send events to your own systems as they happen: a purchase, a form sign-up, a contact entering a segment. Pulse posts JSON to your URL and retries if it fails."
        actions={admin && <Link className="btn primary" to="/settings/webhooks/new">New webhook</Link>}
      />
      {!admin ? (
        <div className="panel"><Empty title="Only admins can manage webhooks">Ask an admin in your workspace to set one up.</Empty></div>
      ) : (
        <>
          <ErrorNote error={q.error} />
          <div className="panel">
            {hooks.length === 0 && !q.isLoading ? (
              <Empty title="No webhooks yet" action={<Link className="btn primary" to="/settings/webhooks/new">Add a webhook</Link>}>
                Tell your CRM about every purchase, or ping your team's tool when someone joins "High intent".
              </Empty>
            ) : (
              <table className="table">
                <thead><tr><th>Name</th><th>URL</th><th>Events</th><th>Status</th><th>Last delivery</th></tr></thead>
                <tbody>{hooks.map((h) => (
                  <tr key={h.id}>
                    <td><Link to={`/settings/webhooks/${h.id}`}>{h.name}</Link></td>
                    <td className="xs muted" style={{ maxWidth: 280, wordBreak: "break-all" }}>{h.url}</td>
                    <td className="small">{eventsSummary(h.events)}</td>
                    <td>{h.active ? <span className="badge ok">Active</span> : <span className="badge">Paused</span>}</td>
                    <td><LastDelivery id={h.id} /></td>
                  </tr>
                ))}</tbody>
              </table>
            )}
          </div>
        </>
      )}
    </div>
  );
}

function LastDelivery({ id }: { id: number }) {
  const q = useQuery({ queryKey: ["webhook", String(id)], queryFn: () => api.get<Detail>(`/v1/settings/webhooks/${id}`), refetchInterval: 15000 });
  const ds = q.data?.deliveries ?? [];
  if (!q.data) return <span className="faint small">…</span>;
  if (ds.length === 0) return <span className="muted small">Nothing sent yet</span>;
  const last = ds.reduce((a, b) => (new Date(b.at) > new Date(a.at) ? b : a));
  return <span className="row" style={{ gap: 6 }}><DeliveryBadge d={last} all={ds} /><span className="xs muted">{timeAgo(last.at)}</span></span>;
}

// ---------- editor ----------

type Draft = { name: string; url: string; events: string[]; active: boolean };
const NEW: Draft = { name: "", url: "", events: [], active: true };

export function WebhookEdit() {
  const { id } = useParams();
  const isNew = id === "new";
  const admin = useIsAdmin();
  const nav = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const existing = useQuery({ queryKey: ["webhook", id], queryFn: () => api.get<Detail>(`/v1/settings/webhooks/${id}`), enabled: !isNew && admin, refetchInterval: 5000 });
  const [w, setW] = useState<Draft | null>(isNew ? NEW : null);
  useEffect(() => {
    if (existing.data && !w) {
      const h = existing.data.webhook;
      setW({ name: h.name, url: h.url, events: h.events ?? [], active: h.active });
    }
  }, [existing.data, w]);

  const save = useMutation({
    mutationFn: () => {
      const body = { name: w!.name.trim(), url: w!.url.trim(), events: w!.events, active: w!.active };
      return isNew ? api.post<{ id: number }>("/v1/settings/webhooks", body) : api.put<{ id: number }>(`/v1/settings/webhooks/${id}`, body);
    },
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["webhooks"] });
      if (isNew) {
        toast({ text: "Webhook added. Copy its signing secret below." });
        nav(`/settings/webhooks/${r.id}`, { replace: true });
      } else {
        toast({ text: "Webhook saved" });
        qc.invalidateQueries({ queryKey: ["webhook", id] });
      }
      setW(null);
    },
  });
  const remove = useMutation({
    mutationFn: () => api.del(`/v1/settings/webhooks/${id}`),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["webhooks"] }); toast({ text: "Webhook deleted" }); nav("/settings/webhooks"); },
  });

  if (!admin) {
    return (
      <div className="page">
        <PageHead back={<Link className="back" to="/settings/webhooks">Webhooks</Link>} title="Webhook" />
        <div className="panel"><Empty title="Only admins can manage webhooks">Ask an admin in your workspace to change this one.</Empty></div>
      </div>
    );
  }
  if (!w) return <div className="page"><ErrorNote error={existing.error} /></div>;
  const set = (p: Partial<Draft>) => setW({ ...w, ...p });
  const saved = existing.data?.webhook;
  const problem = !w.name.trim() ? "Name the webhook."
    : !/^https?:\/\/\S+$/.test(w.url.trim()) ? "Enter the full URL, starting with https://."
    : w.events.length === 0 ? "Choose at least one event, or send every event." : null;

  return (
    <div className="page">
      <PageHead
        back={<Link className="back" to="/settings/webhooks">Webhooks</Link>}
        title={isNew ? "New webhook" : saved?.name ?? "Webhook"}
        actions={<>
          {!isNew && <button className="btn danger" disabled={remove.isPending} onClick={() => confirm("Delete this webhook? Pulse stops sending to it right away.") && remove.mutate()}>Delete</button>}
          <button className="btn primary" disabled={!!problem || save.isPending} title={problem ?? undefined} onClick={() => save.mutate()}>{isNew ? "Add webhook" : "Save"}</button>
        </>}
      />
      <ErrorNote error={save.error ?? remove.error ?? existing.error} />
      <div className="grid-2">
        <Field label="Name"><input className="input" value={w.name} onChange={(e) => set({ name: e.target.value })} placeholder="Purchases to CRM" /></Field>
        <Field label="URL" hint="Pulse sends a POST with a JSON body to this address.">
          <input className="input" value={w.url} onChange={(e) => set({ url: e.target.value })} placeholder="https://example.com/hooks/pulse" />
        </Field>
      </div>
      <EventPicker events={w.events} onChange={(events) => set({ events })} />
      <label className="check"><input type="checkbox" checked={w.active} onChange={(e) => set({ active: e.target.checked })} />Active. Turn off to pause sending without deleting the webhook.</label>

      {!isNew && saved && (
        <>
          <SecretPanel webhook={saved} />
          <TestPanel id={id!} />
          <DeliveriesPanel deliveries={existing.data?.deliveries ?? []} />
        </>
      )}
      {isNew && <p className="small muted" style={{ marginTop: 10 }}>Pulse creates a signing secret when you add the webhook. You can then send a test and see every delivery here.</p>}
      <VerifySnippet />
    </div>
  );
}

function EventPicker({ events, onChange }: { events: string[]; onChange: (e: string[]) => void }) {
  const names = useQuery({ queryKey: ["event-names"], queryFn: () => api.get<{ names: string[] }>("/v1/events/names") });
  const [text, setText] = useState("");
  const every = events.includes("*");
  const add = (n: string) => {
    const v = n.trim();
    if (v && !events.includes(v)) onChange([...events.filter((e) => e !== "*"), v]);
    setText("");
  };
  const known = Array.from(new Set([...SEGMENT_EVENTS, ...(names.data?.names ?? []), ...COMMON_EVENTS]));
  const suggestions = known.filter((n) => !events.includes(n)).slice(0, 14);
  return (
    <Field label="Events" group hint={every ? "Every event Pulse records is sent, including segment_entered and segment_exited." : "Any event name works, including ones you haven't sent yet."}>
      <label className="check" style={{ marginBottom: every ? 0 : 10 }}>
        <input type="checkbox" checked={every} onChange={(e) => onChange(e.target.checked ? ["*"] : [])} />Send every event
      </label>
      {!every && (
        <>
          {events.length > 0 && (
            <div className="wh-chips">
              {events.map((e) => (
                <span className="tag-chip" key={e}>{e}<button type="button" aria-label={`Remove ${e}`} onClick={() => onChange(events.filter((x) => x !== e))}>×</button></span>
              ))}
            </div>
          )}
          <div className="row">
            <input className="input" style={{ maxWidth: 260 }} list="wh-event-names" value={text} placeholder="Add an event, e.g. purchase" aria-label="Add an event"
              onChange={(e) => setText(e.target.value)}
              onKeyDown={(e) => { if (e.key === "Enter" || e.key === ",") { e.preventDefault(); add(text); } }} />
            <button type="button" className="btn sm" disabled={!text.trim()} onClick={() => add(text)}>Add</button>
          </div>
          <datalist id="wh-event-names">{known.map((n) => <option key={n} value={n} />)}</datalist>
          {suggestions.length > 0 && (
            <div className="wh-suggest">{suggestions.map((n) => <button type="button" key={n} onClick={() => add(n)}>+ {n}</button>)}</div>
          )}
        </>
      )}
    </Field>
  );
}

function SecretPanel({ webhook }: { webhook: Webhook }) {
  const qc = useQueryClient();
  const toast = useToast();
  const copy = useCopy();
  const [shown, setShown] = useState(false);
  const rotate = useMutation({
    // Rotating sends the saved settings back unchanged, so unsaved edits on the page aren't saved by it.
    mutationFn: () => api.put(`/v1/settings/webhooks/${webhook.id}`, { name: webhook.name, url: webhook.url, events: webhook.events, active: webhook.active, rotate_secret: true }),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["webhook", String(webhook.id)] }); setShown(true); toast({ text: "New secret created. The old one stops working now." }); },
  });
  const secret = webhook.secret ?? "";
  return (
    <section className="panel section">
      <div className="panel-head"><h3>Signing secret</h3><span className="xs muted">Keep it on your server only</span></div>
      <div className="panel-body">
        <p className="small muted" style={{ marginBottom: 12 }}>Every delivery is signed with this secret in the <code>X-Pulse-Signature</code> header. Check it to be sure the request came from Pulse.</p>
        <div className="wh-secret">
          <code>{!secret ? "No secret yet" : shown ? secret : "•".repeat(24) + secret.slice(-4)}</code>
          {secret && <button className="btn sm" onClick={() => setShown(!shown)}>{shown ? "Hide" : "Reveal"}</button>}
          {secret && <button className="btn sm" onClick={() => copy(secret, "Secret copied")}>Copy</button>}
          <button className="btn sm danger" disabled={rotate.isPending}
            onClick={() => confirm("Create a new secret? The current one stops working right away, so update your server too.") && rotate.mutate()}>Rotate secret</button>
        </div>
        <ErrorNote error={rotate.error} />
      </div>
    </section>
  );
}

function TestPanel({ id }: { id: string }) {
  const qc = useQueryClient();
  const send = useMutation({
    mutationFn: () => api.post<TestResult>(`/v1/settings/webhooks/${id}/test`),
    onSettled: () => qc.invalidateQueries({ queryKey: ["webhook", id] }),
  });
  const r = send.data;
  return (
    <section className="panel section">
      <div className="panel-head">
        <h3>Send a test</h3>
        <button className="btn sm" disabled={send.isPending} onClick={() => send.mutate()}>{send.isPending ? "Sending…" : "Send test"}</button>
      </div>
      <div className="panel-body">
        <p className="small muted">Sends a <code>webhook_test</code> event to the saved URL right away, signed like a real one. Save your changes first.</p>
        {r && (r.ok
          ? <div className="alert ok" style={{ marginTop: 12, marginBottom: 0 }}>Your server answered {r.status} in {r.duration_ms} ms.</div>
          : <div className="alert error" style={{ marginTop: 12, marginBottom: 0 }}><strong>{r.status ? `Your server answered ${r.status}` : "No answer from your server"}</strong> after {r.duration_ms} ms.{r.error && <><br />{r.error}</>}</div>)}
        <ErrorNote error={send.error} />
      </div>
    </section>
  );
}

function DeliveriesPanel({ deliveries }: { deliveries: WebhookDelivery[] }) {
  return (
    <section className="section">
      <h3>Deliveries</h3>
      <p className="xs muted" style={{ marginBottom: 10 }}>
        The latest 100 attempts, updated every few seconds. A failed attempt is tried again after 30 seconds, 2 minutes, 10 minutes, 1 hour and 6 hours; after that Pulse gives up.
        Any 2xx answer counts as delivered. Test sends are listed too, marked "test", and are never retried.
      </p>
      <div className="panel">
        {deliveries.length === 0 ? (
          <Empty title="Nothing sent yet">Deliveries show up here as matching events happen. Send a test to try it now: it's listed here too.</Empty>
        ) : (
          <div className="table-scroll">
            <table className="table">
              <thead><tr><th>When</th><th>Event</th><th className="num">Attempt</th><th>Answer</th><th className="num">Time</th><th>Error</th><th>Result</th></tr></thead>
              <tbody>{deliveries.map((d) => (
                <tr key={d.id}>
                  <td className="muted" style={{ whiteSpace: "nowrap" }} title={d.at}>{fmt(d.at)}</td>
                  <td><code>{d.event_name}</code>{d.test && <> <span className="badge">test</span></>}</td>
                  <td className="num">{d.attempt}</td>
                  <td>{d.status ? d.status : <span className="muted">None</span>}</td>
                  <td className="num">{d.duration_ms} ms</td>
                  <td className="xs muted wh-err" title={d.error}>{d.error}</td>
                  <td><DeliveryBadge d={d} all={deliveries} /></td>
                </tr>
              ))}</tbody>
            </table>
          </div>
        )}
      </div>
    </section>
  );
}

const SNIPPETS = {
  node: `const crypto = require("crypto");

// rawBody: the request body exactly as received, before JSON parsing.
// header: the X-Pulse-Signature header, "t=<unix time>,v1=<hex>".
function verifyPulse(rawBody, header, secret) {
  const parts = Object.fromEntries(header.split(",").map((p) => p.split("=")));
  const expected = crypto.createHmac("sha256", secret)
    .update(\`\${parts.t}.\${rawBody}\`)
    .digest("hex");
  const given = Buffer.from(parts.v1 || "");
  const fresh = Math.abs(Date.now() / 1000 - Number(parts.t)) < 300;
  return fresh && given.length === expected.length &&
    crypto.timingSafeEqual(given, Buffer.from(expected));
}`,
  python: `import hashlib, hmac, time

# raw_body: the request body bytes exactly as received.
# header: the X-Pulse-Signature header, "t=<unix time>,v1=<hex>".
def verify_pulse(raw_body: bytes, header: str, secret: str) -> bool:
    parts = dict(p.split("=", 1) for p in header.split(","))
    t = parts.get("t", "0")
    expected = hmac.new(secret.encode(), t.encode() + b"." + raw_body,
                        hashlib.sha256).hexdigest()
    fresh = abs(time.time() - int(t)) < 300
    return fresh and hmac.compare_digest(expected, parts.get("v1", ""))`,
};

function VerifySnippet() {
  const [lang, setLang] = useState<keyof typeof SNIPPETS>("node");
  const copy = useCopy();
  return (
    <section className="panel section">
      <div className="panel-head">
        <h3>Verify the signature</h3>
        <button className="btn sm" onClick={() => copy(SNIPPETS[lang], "Code copied")}>Copy code</button>
      </div>
      <div className="panel-body">
        <p className="small muted" style={{ marginBottom: 12 }}>
          Pulse signs <code>{"<t>.<raw body>"}</code> with HMAC-SHA256 and your secret. Reject requests whose signature doesn't match or whose time is more than 5 minutes off.
          A retry carries the same <code>X-Pulse-Delivery</code> ID, so use it to skip events you've already handled.
        </p>
        <div className="wh-snippet-tabs">
          <button className={`btn sm${lang === "node" ? " primary" : ""}`} aria-pressed={lang === "node"} onClick={() => setLang("node")}>Node.js</button>
          <button className={`btn sm${lang === "python" ? " primary" : ""}`} aria-pressed={lang === "python"} onClick={() => setLang("python")}>Python</button>
        </div>
        <pre className="wh-snippet">{SNIPPETS[lang]}</pre>
      </div>
    </section>
  );
}
