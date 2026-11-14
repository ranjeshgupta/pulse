// Preview and test-send, shared by templates, journey steps and broadcasts, for every channel.
import { useMutation, useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { api, CHANNEL_NAMES, type Channel, type ChannelContent, type EventSchema, type Template } from "./api";
import { ErrorNote, Field, Modal, useToast } from "./ui";
import { testContactAddress, useTestContacts } from "./campaignUI";
import { ProviderPick } from "./ProviderPick";

/** A file attached to an email (template preview). */
export type PreviewAttachment = { id: number; title: string; filename: string; content_type: string; size: number };
export type Rendered = {
  subject?: string; html?: string; content: ChannelContent;
  // email (template preview): the plain-text part as sent, whether it was generated from the HTML, attachments
  text?: string; text_generated?: boolean; attachments?: PreviewAttachment[]; attachment_error?: string;
};

export function formatBytes(n: number): string {
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`;
  if (n >= 1 << 10) return `${Math.round(n / 1024)} KB`;
  return `${n} bytes`;
}

// MessagePreview draws a rendered message the way the recipient sees it.
export function MessagePreview({ channel, r, label }: { channel: Channel; r: Rendered; label?: string }) {
  const c = r.content ?? {};
  if (channel === "email") return <EmailPreview r={r} />;
  let body: React.ReactNode = null;
  if (channel === "sms") body = <div className="bubble">{c.text || <span className="faint">Your message</span>}</div>;
  if (channel === "whatsapp") {
    const text = (c.preview_text || "").replace(/\{\{(\d+)\}\}/g, (_, n) => c.body_params?.[Number(n) - 1] || `{{${n}}}`);
    body = (
      <div className="bubble wa">
        {c.header_image_url && <img src={c.header_image_url} alt="" style={{ width: "100%", borderRadius: 8, marginBottom: 6 }} />}
        {text || <>Approved template "{c.template_name}"{c.body_params?.length ? ` with ${c.body_params.join(", ")}` : ""}</>}
      </div>
    );
  }
  if (channel === "rcs") {
    body = (
      <>
        {c.card ? (
          <div className="rcs-card">
            {c.card.image_url && <img src={c.card.image_url} alt="" />}
            <div><strong>{c.card.title}</strong><br /><span className="muted small">{c.card.description}</span></div>
          </div>
        ) : <div className="bubble">{c.text || <span className="faint">Your message</span>}</div>}
        <div className="chips">{c.suggestions?.map((s, i) => <span key={i}>{s.text || "…"}</span>)}</div>
      </>
    );
  }
  if (channel === "inbox") {
    body = (
      <div className="notif" style={{ alignItems: "flex-start" }}>
        <div style={{ width: 8, height: 8, borderRadius: 4, background: "var(--ch-inbox)", marginTop: 6, flex: "none" }} aria-label="Unread" />
        <div>
          <strong>{c.title || "Title"}</strong><br /><span className="small">{c.body || "Message"}</span>
          {c.image_url && <img src={c.image_url} alt="" style={{ width: "100%", borderRadius: 6, marginTop: 6 }} />}
          {c.category && <div className="xs muted" style={{ marginTop: 4 }}>{c.category}</div>}
        </div>
      </div>
    );
  }
  if (channel === "push") {
    body = (
      <div className="notif">
        <div className="notif-icon" />
        <div>
          <strong>{c.title || "Title"}</strong><br /><span className="small">{c.body || "Message"}</span>
          {c.image_url && <img src={c.image_url} alt="" style={{ width: "100%", borderRadius: 6, marginTop: 6 }} />}
        </div>
      </div>
    );
  }
  return (
    <aside className="phone" aria-label="Preview">
      <div className="phone-top">{label ?? `${channel === "push" ? "Lock screen" : CHANNEL_NAMES[channel]} preview`}</div>
      {body}
    </aside>
  );
}

function EmailPreview({ r }: { r: Rendered }) {
  const [part, setPart] = useState<"html" | "text">("html");
  const hasText = r.text !== undefined;
  return (
    <div>
      <p className="small" style={{ marginBottom: 8 }}><span className="muted">Subject:</span> <strong>{r.subject || "(no subject)"}</strong></p>
      {hasText && (
        <div className="tabs" role="tablist" style={{ marginBottom: 10 }}>
          <button role="tab" aria-selected={part === "html"} onClick={() => setPart("html")}>HTML</button>
          <button role="tab" aria-selected={part === "text"} onClick={() => setPart("text")}>Plain text{r.text_generated ? " (generated)" : ""}</button>
        </div>
      )}
      {part === "html" || !hasText ? (
        <iframe className="preview-frame" title="Email preview" sandbox="" srcDoc={r.html ?? ""} />
      ) : (
        <>
          <pre className="preview-frame" style={{ whiteSpace: "pre-wrap", padding: 14, margin: 0, overflow: "auto", font: "13px/1.55 ui-monospace, monospace" }} aria-label="Plain-text part">{r.text || "(empty)"}</pre>
          <p className="xs muted" style={{ marginTop: 6 }}>Links in the plain-text part go straight to their pages (no click tracking). The unsubscribe link is added at the end when the text doesn't place {"{{links.unsubscribe}}"} itself.</p>
        </>
      )}
      {r.attachments && r.attachments.length > 0 && (
        <div style={{ marginTop: 10 }} aria-label="Attachments">
          <p className="xs muted" style={{ margin: "0 0 4px" }}>Attachments</p>
          <div className="row" style={{ gap: 6, flexWrap: "wrap" }}>
            {r.attachments.map((a) => <span key={a.id} className="tag-chip" style={{ paddingRight: 9 }}>{a.filename} <span className="muted xs">{formatBytes(a.size)}</span></span>)}
          </div>
        </div>
      )}
      {r.attachment_error && <div className="alert error" style={{ marginTop: 8 }}>{r.attachment_error}</div>}
    </div>
  );
}

/** The answer of GET /v1/templates/sample-event. */
type SampleEvent = { source: "latest" | "schema" | "none"; at?: string; props: Record<string, unknown> };

/**
 * SampleEventFields: the trigger event a preview or test send uses for {{event.x}}: the contact's
 * latest real event of that name, else the schema's example, editable as JSON. Empty: no event
 * (as a broadcast).
 */
function SampleEventFields({ name, setName, contact, json, setJson }: { name: string; setName: (s: string) => void; contact: string; json: string; setJson: (s: string) => void }) {
  const schemas = useQuery({ queryKey: ["event-schemas"], queryFn: () => api.get<{ schemas: EventSchema[] }>("/v1/events/schemas"), staleTime: 60_000, retry: false });
  const sample = useQuery({
    queryKey: ["sample-event", name, contact],
    queryFn: () => api.get<SampleEvent>(`/v1/templates/sample-event?event=${encodeURIComponent(name)}${contact ? `&external_id=${encodeURIComponent(contact)}` : ""}`),
    enabled: !!name, retry: false,
  });
  useEffect(() => {
    if (sample.data) setJson(JSON.stringify(sample.data.props, null, 2));
  }, [sample.data, setJson]);
  const bad = json.trim() !== "" && parseEvent(json) === undefined;
  const names = schemas.data?.schemas.map((x) => x.name) ?? [];
  return (
    <div className="sample-event">
      <Field label="Trigger event" hint={!name ? "No event: {{event.…}} renders empty or its default, as in a one-time campaign." : sample.data?.source === "latest" ? `The contact's latest ${name} (${new Date(sample.data.at!).toLocaleString()}), personal properties left out.` : sample.data?.source === "schema" ? `No ${name} for this contact: an example from the event's schema.` : sample.data?.source === "none" ? `No ${name} for this contact and no schema: type the properties.` : undefined}>
        <select className="select" value={name} onChange={(e) => setName(e.target.value)}>
          <option value="">None</option>
          {[...new Set([...names, ...(name ? [name] : [])])].map((n) => <option key={n} value={n}>{n}</option>)}
        </select>
      </Field>
      {name && (
        <Field label="Event properties (JSON)" hint={bad ? <span style={{ color: "var(--danger)" }}>Not a JSON object.</span> : "Edit to try other values."}>
          <textarea className="textarea code-editor" style={{ minHeight: 110 }} value={json} onChange={(e) => setJson(e.target.value)} spellCheck={false} aria-label="Event properties" />
        </Field>
      )}
      <ErrorNote error={sample.error} />
    </div>
  );
}

/** parseEvent reads the JSON object typed for the sample event (undefined when it isn't one). */
export function parseEvent(json: string): Record<string, unknown> | null | undefined {
  if (!json.trim()) return null;
  try {
    const v = JSON.parse(json);
    return v && typeof v === "object" && !Array.isArray(v) ? v : undefined;
  } catch {
    return undefined;
  }
}

// PreviewModal renders a template (saved or still being edited) for a chosen contact and sample
// trigger event, on the server, exactly as it would be sent.
export function PreviewModal({ template, event = "", onClose }: { template: Partial<Template> & { channel: Channel }; event?: string; onClose: () => void }) {
  const [ext, setExt] = useState("");
  const [asWho, setAsWho] = useState("");
  const [evName, setEvName] = useState(event);
  const [evJson, setEvJson] = useState("");
  const ev = parseEvent(evJson);
  const q = useQuery({
    queryKey: ["preview", asWho, template, evName && ev],
    queryFn: () => api.post<Rendered & { contact: { external_id: string; email: string; phone?: string }; missing: string[]; skipped: boolean }>("/v1/templates/preview",
      { template, external_id: asWho, event: evName && ev ? ev : undefined }),
    enabled: ev !== undefined,
  });
  return (
    <Modal title={`Preview: ${template.name || CHANNEL_NAMES[template.channel]}`} onClose={onClose}>
      <form className="row" style={{ marginBottom: 14, flexWrap: "nowrap" }} onSubmit={(e) => { e.preventDefault(); setAsWho(ext.trim()); }}>
        <input className="input" placeholder="Contact's external ID (empty: sample contact)" value={ext} onChange={(e) => setExt(e.target.value)} aria-label="Preview as contact" />
        <button className="btn">Preview as contact</button>
      </form>
      <SampleEventFields name={evName} setName={setEvName} contact={asWho} json={evJson} setJson={setEvJson} />
      <ErrorNote error={q.error} />
      {q.data && (
        <>
          <p className="xs muted" style={{ marginBottom: 10 }}>
            As {q.data.contact.external_id === "sample" ? "a sample contact (Asha from Pune)" : q.data.contact.external_id}
            {template.channel === "email" ? `, to ${q.data.contact.email || "no email"}` : q.data.contact.phone ? `, to ${q.data.contact.phone}` : ""}
          </p>
          {q.data.missing?.length > 0 && (
            <div className={`alert ${q.data.skipped ? "error" : "info"}`}>
              No value for {q.data.missing.map((m) => `{{${m}}}`).join(", ")}{q.data.skipped ? ": this contact would get nothing (the template skips contacts with missing values)." : ": rendered empty. Add a default, or set the template to skip such contacts."}
            </div>
          )}
          <div style={{ display: "flex", justifyContent: "center" }}><MessagePreview channel={template.channel} r={q.data} label="As it will be sent" /></div>
        </>
      )}
    </Modal>
  );
}

// TestSendModal sends the saved template to one contact through the channel's normal delivery path,
// with a sample trigger event for {{event.x}}.
export function TestSendModal({ template, event = "", onClose }: { template: Template; event?: string; onClose: () => void }) {
  const toast = useToast();
  const tcs = useTestContacts();
  const [who, setWho] = useState(""); // "tc:<id>" for a saved test contact, else "" (type an external ID)
  const [ext, setExt] = useState("");
  const [fallback, setFallback] = useState("");
  const [provider, setProvider] = useState(0);
  const [evName, setEvName] = useState(event);
  const [evJson, setEvJson] = useState("");
  const ev = parseEvent(evJson);
  const canFallback = template.channel === "whatsapp" || template.channel === "rcs";
  const sms = useQuery({ queryKey: ["templates", "sms"], queryFn: () => api.get<{ templates: Template[] }>("/v1/templates?channel=sms"), enabled: canFallback });
  const saved = tcs.data?.test_contacts ?? [];
  const chosen = who.startsWith("tc:") ? saved.find((t) => t.id === Number(who.slice(3))) : undefined;
  // A saved contact that is a real contact is personalised with their data; bare addresses use the test contact's name.
  const sampleFor = chosen?.external_id ?? (chosen ? "" : ext.trim());
  const ready = chosen ? !!testContactAddress(chosen, template.channel) : !!ext.trim();
  const send = useMutation({
    mutationFn: () => api.post("/v1/channels/test", {
      template_id: template.id, ...(chosen ? { test_contact_id: chosen.id } : { external_id: ext.trim() }),
      fallback_template_id: Number(fallback) || 0, provider_id: provider || undefined, event: evName && ev ? ev : undefined,
    }),
    onSuccess: () => { toast({ text: "Test queued. Check the test inbox or the contact's device." }); onClose(); },
  });
  return (
    <Modal title={`Send a test: ${template.name}`} onClose={onClose} footer={<><button className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={!ready || send.isPending || ev === undefined} onClick={() => send.mutate()}>Send test</button></>}>
      <ErrorNote error={send.error} />
      <p className="small muted" style={{ marginBottom: 14 }}>Sends the saved version through your {CHANNEL_NAMES[template.channel]} provider, personalized with the contact's fields and the sample event.</p>
      <Field label="Send to" hint={saved.length === 0 ? <>Save test contacts (yours, your team's) under Settings → Channels → Test contacts to pick them here.</> : chosen && !testContactAddress(chosen, template.channel) ? `${chosen.name} has no address for ${CHANNEL_NAMES[template.channel]}.` : chosen && !chosen.external_id ? "A bare address: personalised with the test contact's name only; nothing is tracked." : undefined}>
        <select className="select" value={who} onChange={(e) => setWho(e.target.value)} aria-label="Send to">
          <option value="">A contact (type the external ID)</option>
          {saved.map((t) => {
            const a = testContactAddress(t, template.channel);
            return <option key={t.id} value={`tc:${t.id}`} disabled={!a}>{t.name}{a ? ` (${a})` : " (no address for this channel)"}</option>;
          })}
        </select>
      </Field>
      <ProviderPick channel={template.channel} value={provider} onChange={setProvider} />
      {!chosen && <Field label="Contact's external ID"><input className="input" value={ext} onChange={(e) => setExt(e.target.value)} autoFocus /></Field>}
      <SampleEventFields name={evName} setName={setEvName} contact={sampleFor} json={evJson} setJson={setEvJson} />
      {canFallback && !(chosen && !chosen.external_id) && (
        <Field label="SMS fallback (optional)" hint="Also tests the fallback: if this contact can't get the message, the SMS goes instead.">
          <select className="select" value={fallback} onChange={(e) => setFallback(e.target.value)}>
            <option value="">No fallback</option>
            {sms.data?.templates.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
          </select>
        </Field>
      )}
    </Modal>
  );
}

// TemplateActions are the "Preview" and "Send a test" buttons for a saved template id; event is the
// journey's trigger event, offered as the sample.
export function TemplateActions({ templateId, size = "sm", event }: { templateId?: number; size?: "sm" | ""; event?: string }) {
  const t = useQuery({ queryKey: ["template", String(templateId)], queryFn: () => api.get<Template>(`/v1/templates/${templateId}`), enabled: !!templateId });
  const [open, setOpen] = useState<"preview" | "test" | null>(null);
  if (!templateId) return null;
  return (
    <div className="row" style={{ marginBottom: 12 }}>
      <button type="button" className={`btn ${size}`} disabled={!t.data} onClick={() => setOpen("preview")}>Preview</button>
      <button type="button" className={`btn ${size}`} disabled={!t.data} onClick={() => setOpen("test")}>Send a test</button>
      {open === "preview" && t.data && <PreviewModal template={t.data} event={event} onClose={() => setOpen(null)} />}
      {open === "test" && t.data && <TestSendModal template={t.data} event={event} onClose={() => setOpen(null)} />}
    </div>
  );
}
