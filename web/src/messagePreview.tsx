// Preview and test-send, shared by templates, journey steps and broadcasts, for every channel.
import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { api, CHANNEL_NAMES, type Channel, type ChannelContent, type Template } from "./api";
import { ErrorNote, Field, Modal, useToast } from "./ui";

export type Rendered = { subject?: string; html?: string; content: ChannelContent };

// MessagePreview draws a rendered message the way the recipient sees it.
export function MessagePreview({ channel, r, label }: { channel: Channel; r: Rendered; label?: string }) {
  const c = r.content ?? {};
  if (channel === "email") {
    return (
      <div>
        <p className="small" style={{ marginBottom: 8 }}><span className="muted">Subject:</span> <strong>{r.subject || "(no subject)"}</strong></p>
        <iframe className="preview-frame" title="Email preview" sandbox="" srcDoc={r.html ?? ""} />
      </div>
    );
  }
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

// PreviewModal renders a template (saved or still being edited) for a chosen contact, on the server,
// exactly as it would be sent.
export function PreviewModal({ template, onClose }: { template: Partial<Template> & { channel: Channel }; onClose: () => void }) {
  const [ext, setExt] = useState("");
  const [asWho, setAsWho] = useState("");
  const q = useQuery({
    queryKey: ["preview", asWho, template],
    queryFn: () => api.post<Rendered & { contact: { external_id: string; email: string; phone?: string } }>("/v1/templates/preview", { template, external_id: asWho }),
  });
  return (
    <Modal title={`Preview: ${template.name || CHANNEL_NAMES[template.channel]}`} onClose={onClose}>
      <form className="row" style={{ marginBottom: 14, flexWrap: "nowrap" }} onSubmit={(e) => { e.preventDefault(); setAsWho(ext.trim()); }}>
        <input className="input" placeholder="Contact's external ID (empty: sample contact)" value={ext} onChange={(e) => setExt(e.target.value)} aria-label="Preview as contact" />
        <button className="btn">Preview as contact</button>
      </form>
      <ErrorNote error={q.error} />
      {q.data && (
        <>
          <p className="xs muted" style={{ marginBottom: 10 }}>
            As {q.data.contact.external_id === "sample" ? "a sample contact (Asha from Pune)" : q.data.contact.external_id}
            {template.channel === "email" ? `, to ${q.data.contact.email || "no email"}` : q.data.contact.phone ? `, to ${q.data.contact.phone}` : ""}
          </p>
          <div style={{ display: "flex", justifyContent: "center" }}><MessagePreview channel={template.channel} r={q.data} label="As it will be sent" /></div>
        </>
      )}
    </Modal>
  );
}

// TestSendModal sends the saved template to one contact through the channel's normal delivery path.
export function TestSendModal({ template, onClose }: { template: Template; onClose: () => void }) {
  const toast = useToast();
  const [ext, setExt] = useState("");
  const [fallback, setFallback] = useState("");
  const canFallback = template.channel === "whatsapp" || template.channel === "rcs";
  const sms = useQuery({ queryKey: ["templates", "sms"], queryFn: () => api.get<{ templates: Template[] }>("/v1/templates?channel=sms"), enabled: canFallback });
  const send = useMutation({
    mutationFn: () => api.post("/v1/channels/test", { template_id: template.id, external_id: ext.trim(), fallback_template_id: Number(fallback) || 0 }),
    onSuccess: () => { toast({ text: "Test queued. Check the test inbox or the contact's device." }); onClose(); },
  });
  return (
    <Modal title={`Send a test: ${template.name}`} onClose={onClose} footer={<><button className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={!ext.trim() || send.isPending} onClick={() => send.mutate()}>Send test</button></>}>
      <ErrorNote error={send.error} />
      <p className="small muted" style={{ marginBottom: 14 }}>Sends the saved version to one contact through your {CHANNEL_NAMES[template.channel]} provider, personalized with their attributes.</p>
      <Field label="Contact's external ID"><input className="input" value={ext} onChange={(e) => setExt(e.target.value)} autoFocus /></Field>
      {canFallback && (
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

// TemplateActions are the "Preview" and "Send a test" buttons for a saved template id.
export function TemplateActions({ templateId, size = "sm" }: { templateId?: number; size?: "sm" | "" }) {
  const t = useQuery({ queryKey: ["template", String(templateId)], queryFn: () => api.get<Template>(`/v1/templates/${templateId}`), enabled: !!templateId });
  const [open, setOpen] = useState<"preview" | "test" | null>(null);
  if (!templateId) return null;
  return (
    <div className="row" style={{ marginBottom: 12 }}>
      <button type="button" className={`btn ${size}`} disabled={!t.data} onClick={() => setOpen("preview")}>Preview</button>
      <button type="button" className={`btn ${size}`} disabled={!t.data} onClick={() => setOpen("test")}>Send a test</button>
      {open === "preview" && t.data && <PreviewModal template={t.data} onClose={() => setOpen(null)} />}
      {open === "test" && t.data && <TestSendModal template={t.data} onClose={() => setOpen(null)} />}
    </div>
  );
}
