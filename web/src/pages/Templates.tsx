import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { lazy, Suspense, useEffect, useRef, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { api, CHANNEL_NAMES, CHANNELS, type AssetList, type Channel, type ChannelContent, type Template } from "../api";
import { formatBytes, MessagePreview, PreviewModal, TestSendModal } from "../messagePreview";
import { ChannelLabel, Empty, ErrorNote, Field, PageHead, timeAgo, useToast } from "../ui";
import type { EmailBuilderHandle } from "./EmailBuilder";

const EmailBuilder = lazy(() => import("./EmailBuilder"));

const PERSONALIZE = "Personalize with {{.Attrs.first_name}}, {{.Attrs.city}} or any contact attribute.";

export function TemplateList() {
  const [params, setParams] = useSearchParams();
  const channel = (params.get("channel") ?? "") as Channel | "";
  const q = useQuery({ queryKey: ["templates", channel], queryFn: () => api.get<{ templates: Template[] }>(`/v1/templates${channel ? `?channel=${channel}` : ""}`) });
  const ts = q.data?.templates ?? [];
  return (
    <div className="page">
      <PageHead
        title="Templates"
        intro="The messages journeys send. Each template belongs to one channel."
        actions={<Link className="btn primary" to={`/templates/new?channel=${channel || "email"}`}>New {channel ? CHANNEL_NAMES[channel] : "email"} template</Link>}
      />
      <div className="tabs" role="tablist">
        {(["", ...CHANNELS] as const).map((c) => (
          <button key={c} role="tab" aria-selected={channel === c} onClick={() => setParams(c ? { channel: c } : {})}>{c ? CHANNEL_NAMES[c] : "All"}</button>
        ))}
      </div>
      <ErrorNote error={q.error} />
      <div className="panel">
        {ts.length === 0 && !q.isLoading ? (
          <Empty title={`No ${channel ? CHANNEL_NAMES[channel] + " " : ""}templates yet`} action={<Link className="btn primary" to={`/templates/new?channel=${channel || "email"}`}>Create a template</Link>} />
        ) : (
          <table className="table">
            <thead><tr><th>Name</th><th>Channel</th><th>Preview</th><th>Updated</th></tr></thead>
            <tbody>
              {ts.map((t) => (
                <tr key={t.id}>
                  <td><Link to={`/templates/${t.id}`}>{t.name}</Link></td>
                  <td><ChannelLabel channel={t.channel} /></td>
                  <td className="muted" style={{ maxWidth: 420, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{summary(t)}</td>
                  <td className="muted">{timeAgo(t.updated_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

function summary(t: Template) {
  const c = t.content ?? {};
  switch (t.channel) {
    case "email": return t.subject;
    case "sms": return c.text;
    case "whatsapp": return c.preview_text || `Approved template "${c.template_name}"`;
    case "rcs": return c.text || c.card?.title;
    case "push": case "inbox": return `${c.title}: ${c.body}`;
  }
}

const BLANK: Record<Channel, ChannelContent> = {
  email: {}, sms: { text: "" }, whatsapp: { template_name: "", language: "en", body_params: [] },
  rcs: { text: "", suggestions: [] }, push: { title: "", body: "" }, inbox: { title: "", body: "" },
};

export function TemplateEdit() {
  const { id } = useParams();
  const [params] = useSearchParams();
  const isNew = id === "new";
  const nav = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const existing = useQuery({ queryKey: ["template", id], queryFn: () => api.get<Template>(`/v1/templates/${id}`), enabled: !isNew });
  const [t, setT] = useState<Template | null>(null);
  // The email body is edited in the builder or as HTML; "text" edits the plain-text part.
  const [emailMode, setEmailMode] = useState<"visual" | "html" | "text">("visual");
  const [testing, setTesting] = useState(false);
  const [previewing, setPreviewing] = useState(false);
  const builder = useRef<EmailBuilderHandle>(null);

  useEffect(() => {
    if (isNew && !t) {
      const ch = (params.get("channel") ?? "email") as Channel;
      setT({ id: 0, name: "", channel: ch, subject: "", html: "", content: { ...BLANK[ch] }, updated_at: "" });
    } else if (existing.data && !t) {
      setT({ ...existing.data, content: existing.data.content ?? {} });
      if (existing.data.channel === "email" && !existing.data.builder && existing.data.html) setEmailMode("html");
    }
  }, [existing.data, isNew, params, t]);

  const save = useMutation({
    mutationFn: async () => {
      let body = { ...t! };
      if (t!.channel === "email" && emailMode === "visual" && builder.current) {
        const { html, project } = builder.current.read();
        body = { ...body, html, builder: project };
        setT(body);
      }
      if (t!.channel === "email" && emailMode === "html") body.builder = undefined;
      return isNew ? api.post<{ id: number }>("/v1/templates", body) : api.put<{ id: number }>(`/v1/templates/${id}`, body);
    },
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["templates"] });
      toast({ text: "Template saved" });
      if (isNew) nav(`/templates/${r.id}`, { replace: true });
    },
  });
  const remove = useMutation({
    mutationFn: () => api.del(`/v1/templates/${id}`),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["templates"] }); nav("/templates"); },
  });

  if (!t) return <div className="page"><ErrorNote error={existing.error} /></div>;
  const c = t.content;
  const setC = (patch: Partial<ChannelContent>) => setT({ ...t, content: { ...c, ...patch } });

  return (
    <div className="page" style={t.channel === "email" ? { maxWidth: "none" } : undefined}>
      <PageHead
        back={<Link className="back" to={`/templates?channel=${t.channel}`}>Templates</Link>}
        title={isNew ? `New ${CHANNEL_NAMES[t.channel]} template` : t.name || "Template"}
        actions={<>
          {!isNew && <button className="btn danger" onClick={() => confirm("Delete this template? Journey steps that use it will fail to send.") && remove.mutate()}>Delete</button>}
          <button className="btn" onClick={() => { if (t.channel === "email" && emailMode === "visual" && builder.current) setT({ ...t, html: builder.current.read().html }); setPreviewing(true); }}>Preview as contact</button>
          <button className="btn" disabled={isNew} title={isNew ? "Save the template first" : undefined} onClick={() => setTesting(true)}>Send a test</button>
          <button className="btn primary" disabled={save.isPending} onClick={() => save.mutate()}>Save template</button>
        </>}
      />
      <ErrorNote error={save.error ?? remove.error} />
      <div className={t.channel === "email" ? "" : "editor-split"}>
        <div>
          <label className="check">
            <input type="checkbox" checked={!!t.transactional} onChange={(e) => setT({ ...t, transactional: e.target.checked })} />
            Transactional (an OTP, receipt or password reset): ignore frequency caps and quiet hours
          </label>
          <div className="grid-2">
            <Field label="Template name"><input className="input" value={t.name} onChange={(e) => setT({ ...t, name: e.target.value })} placeholder="Welcome series, day 1" /></Field>
            {t.channel === "email" && <Field label="Subject" hint={<>{PERSONALIZE} Pulse adds an unsubscribe link and open/click tracking to every email; place the link yourself with <code>{"{{.UnsubscribeURL}}"}</code>, and link the preference center with <code>{"{{.PreferencesURL}}"}</code>.</>}><input className="input" value={t.subject} onChange={(e) => setT({ ...t, subject: e.target.value })} /></Field>}
          </div>

          {t.channel === "email" && (
            <>
              <div className="tabs" role="tablist">
                <button role="tab" aria-selected={emailMode === "visual"} onClick={() => setEmailMode("visual")}>Visual builder</button>
                <button role="tab" aria-selected={emailMode === "html"} onClick={() => { if (builder.current) setT({ ...t, html: builder.current.read().html }); setEmailMode("html"); }}>HTML</button>
                <button role="tab" aria-selected={emailMode === "text"} onClick={() => {
                  // Keep the builder's layout: only the HTML tab replaces it.
                  if (emailMode === "visual" && builder.current) { const r = builder.current.read(); setT({ ...t, html: r.html, builder: r.project }); }
                  setEmailMode("text");
                }}>Plain text{c.text ? "" : " (auto)"}</button>
              </div>
              {emailMode === "text" ? (
                <PlainTextEditor t={t} setText={(text) => setC({ text })} />
              ) : emailMode === "visual" ? (
                <Suspense fallback={<div className="gjs-host" />}><EmailBuilder ref={builder} html={t.html} project={t.builder} /></Suspense>
              ) : (
                <div className="grid-2" style={{ gap: 18 }}>
                  <textarea className="textarea code-editor" value={t.html} onChange={(e) => setT({ ...t, html: e.target.value, builder: undefined })} aria-label="Email HTML" spellCheck={false} />
                  <iframe className="preview-frame" title="Email preview, with sample values" sandbox="" srcDoc={fillPreview(t.html)} />
                </div>
              )}
              {emailMode === "html" && t.builder === undefined && !isNew && <p className="xs muted" style={{ marginTop: 8 }}>Saving from the HTML tab replaces the visual layout with this HTML.</p>}
              <AttachmentPicker ids={c.attachments ?? []} onChange={(attachments) => setC({ attachments: attachments.length ? attachments : undefined })} />
            </>
          )}

          {t.channel === "sms" && (
            <>
              <Field label="Message" hint={<SmsCount text={c.text ?? ""} />}><textarea className="textarea" value={c.text ?? ""} onChange={(e) => setC({ text: e.target.value })} /></Field>
              <Field label="Sender ID" hint="Your registered header (e.g. PULSEX for India DLT). Leave empty to use the provider's default."><input className="input" value={c.sender_id ?? ""} onChange={(e) => setC({ sender_id: e.target.value })} /></Field>
              <p className="xs muted">{PERSONALIZE}</p>
            </>
          )}

          {t.channel === "whatsapp" && (
            <>
              <div className="alert info">WhatsApp only delivers business-started messages from templates Meta has approved. Enter the approved template's name and fill its variables in order.</div>
              <div className="grid-2">
                <Field label="Approved template name"><input className="input" value={c.template_name ?? ""} onChange={(e) => setC({ template_name: e.target.value.trim() })} placeholder="order_update" /></Field>
                <Field label="Language code"><input className="input" value={c.language ?? "en"} onChange={(e) => setC({ language: e.target.value.trim() })} placeholder="en, hi, en_US" /></Field>
              </div>
              <Field label="Template text, for the preview" hint="Paste the approved text with {{1}}, {{2}}… where variables go. It isn't sent; Meta uses its own copy.">
                <textarea className="textarea" value={c.preview_text ?? ""} onChange={(e) => setC({ preview_text: e.target.value })} />
              </Field>
              <ListEditor label="Variables" hint={PERSONALIZE} items={c.body_params ?? []} placeholder={(i) => `{{${i + 1}}}, e.g. {{.Attrs.first_name}}`} onChange={(body_params) => setC({ body_params })} />
              <Field label="Header image URL (optional)"><input className="input" value={c.header_image_url ?? ""} onChange={(e) => setC({ header_image_url: e.target.value })} /></Field>
            </>
          )}

          {t.channel === "rcs" && <RcsFields c={c} setC={setC} />}

          {t.channel === "inbox" && (
            <>
              <p className="xs muted" style={{ marginTop: 0 }}>Stays in the contact's app inbox (the mobile and web SDKs show it) until it expires or they delete it. Pulse delivers it itself: no provider needed.</p>
              <Field label="Title"><input className="input" value={c.title ?? ""} onChange={(e) => setC({ title: e.target.value })} /></Field>
              <Field label="Message" hint={PERSONALIZE}><textarea className="textarea" value={c.body ?? ""} onChange={(e) => setC({ body: e.target.value })} /></Field>
              <div className="grid-2">
                <Field label="Image URL (optional)"><input className="input" value={c.image_url ?? ""} onChange={(e) => setC({ image_url: e.target.value })} /></Field>
                <Field label="Open this link when tapped"><input className="input" value={c.url ?? ""} onChange={(e) => setC({ url: e.target.value })} placeholder="https:// or app deep link" /></Field>
                <Field label="Category (optional)" hint="Lets the app filter its inbox, e.g. offers or orders."><input className="input" value={c.category ?? ""} onChange={(e) => setC({ category: e.target.value })} /></Field>
                <Field label="Keep for (days)" hint="Then it disappears from the inbox. 1-365; 30 if empty.">
                  <input className="input" type="number" min={1} max={365} value={c.expires_days ?? ""} onChange={(e) => setC({ expires_days: e.target.value ? Number(e.target.value) : undefined })} />
                </Field>
              </div>
            </>
          )}

          {t.channel === "push" && (
            <>
              <Field label="Title"><input className="input" value={c.title ?? ""} onChange={(e) => setC({ title: e.target.value })} /></Field>
              <Field label="Message" hint={PERSONALIZE}><textarea className="textarea" value={c.body ?? ""} onChange={(e) => setC({ body: e.target.value })} /></Field>
              <div className="grid-2">
                <Field label="Image URL (optional)"><input className="input" value={c.image_url ?? ""} onChange={(e) => setC({ image_url: e.target.value })} /></Field>
                <Field label="Open this link when tapped"><input className="input" value={c.url ?? ""} onChange={(e) => setC({ url: e.target.value })} placeholder="https:// or app deep link" /></Field>
              </div>
            </>
          )}
        </div>
        {t.channel !== "email" && <MessagePreview channel={t.channel} r={{ content: fillContent(c) }} label={`${t.channel === "push" ? "Lock screen" : CHANNEL_NAMES[t.channel]} preview, with sample values`} />}
      </div>
      {testing && <TestSendModal template={t} onClose={() => setTesting(false)} />}
      {previewing && <PreviewModal template={t} onClose={() => setPreviewing(false)} />}
    </div>
  );
}

// PlainTextEditor edits the email's plain-text part. Empty means "generate it from the HTML at send
// time"; "Generate from HTML" fills it with that text (rendered on the server) to edit by hand.
function PlainTextEditor({ t, setText }: { t: Template; setText: (s: string) => void }) {
  const toast = useToast();
  const gen = useMutation({
    // From the template's HTML as written, so personalization tags stay tags.
    mutationFn: () => api.post<{ text: string }>("/v1/templates/text", { html: t.html }),
    onSuccess: (r) => {
      setText(r.text);
      toast({ text: "Plain text generated from the HTML" });
    },
  });
  const text = t.content.text ?? "";
  return (
    <div>
      <div className="row" style={{ justifyContent: "space-between", marginBottom: 8 }}>
        <p className="xs muted" style={{ margin: 0, maxWidth: 640 }}>
          Every email also goes out with a plain-text version for clients that don't show HTML. Leave this empty and Pulse generates it from the HTML when sending (links become "text (url)"). {PERSONALIZE}
        </p>
        <div className="row" style={{ gap: 6 }}>
          {text && <button type="button" className="btn sm ghost" onClick={() => setText("")}>Clear (generate automatically)</button>}
          <button type="button" className="btn sm" disabled={gen.isPending || !t.html} onClick={() => gen.mutate()}>{gen.isPending ? "Generating…" : "Generate from HTML"}</button>
        </div>
      </div>
      <ErrorNote error={gen.error} />
      <textarea className="textarea code-editor" style={{ minHeight: 360, width: "100%" }} value={text} onChange={(e) => setText(e.target.value)}
        aria-label="Plain-text part" spellCheck placeholder="Empty: generated from the HTML when the email is sent." />
    </div>
  );
}

// AttachmentPicker chooses workspace assets to attach to every email from this template.
function AttachmentPicker({ ids, onChange }: { ids: number[]; onChange: (ids: number[]) => void }) {
  const q = useQuery({ queryKey: ["assets"], queryFn: () => api.get<AssetList>("/v1/assets") });
  const [pick, setPick] = useState("");
  const all = q.data?.assets ?? [];
  const max = q.data?.max_attachment_bytes ?? 10 << 20;
  const chosen = ids.map((id) => all.find((a) => a.id === id) ?? { id, title: `File ${id}`, filename: `missing file #${id}`, size: 0 });
  const total = chosen.reduce((n, a) => n + a.size, 0);
  const left = all.filter((a) => !ids.includes(a.id));
  return (
    <section className="panel" style={{ marginTop: 16 }} aria-label="Attachments">
      <div className="panel-head"><h3>Attachments</h3><span className="xs muted">{ids.length}/10 files, {formatBytes(total)} of {formatBytes(max)}</span></div>
      <div className="panel-body">
        {chosen.length === 0 && <p className="small muted" style={{ marginTop: 0 }}>No files attached. Attach files from <Link to="/assets">Assets</Link> (PDF, images, CSV, TXT, ICS, ZIP, Word, Excel, PowerPoint).</p>}
        {chosen.length > 0 && (
          <div className="row" style={{ gap: 6, flexWrap: "wrap", marginBottom: 10 }}>
            {chosen.map((a) => (
              <span key={a.id} className="tag-chip" title={a.title}>
                {a.filename} <span className="xs muted">{formatBytes(a.size)}</span>
                <button type="button" aria-label={`Remove ${a.filename}`} onClick={() => onChange(ids.filter((x) => x !== a.id))}>×</button>
              </span>
            ))}
          </div>
        )}
        {total > max && <div className="alert error">Attachments can be at most {formatBytes(max)} together; remove some before saving.</div>}
        {ids.length < 10 && (
          <div className="row" style={{ gap: 6 }}>
            <select className="select" value={pick} onChange={(e) => setPick(e.target.value)} aria-label="File to attach" disabled={left.length === 0}>
              <option value="">{left.length ? "Choose a file…" : all.length ? "Every file is attached" : "No files uploaded yet"}</option>
              {left.map((a) => <option key={a.id} value={a.id}>{a.title} ({a.filename}, {formatBytes(a.size)})</option>)}
            </select>
            <button type="button" className="btn sm" disabled={!pick} onClick={() => { onChange([...ids, Number(pick)]); setPick(""); }}>Attach</button>
            <Link className="btn sm ghost" to="/assets">Upload files</Link>
          </div>
        )}
        <p className="xs muted" style={{ marginBottom: 0 }}>Attached files go with every message from this template, so mind the size for large sends.</p>
      </div>
    </section>
  );
}

function RcsFields({ c, setC }: { c: ChannelContent; setC: (p: Partial<ChannelContent>) => void }) {
  const card = c.card;
  const sugg = c.suggestions ?? [];
  return (
    <>
      <label className="check"><input type="checkbox" checked={!!card} onChange={(e) => setC({ card: e.target.checked ? { title: "", description: "", image_url: "" } : null })} />Send as a rich card</label>
      {card ? (
        <>
          <Field label="Card title"><input className="input" value={card.title} onChange={(e) => setC({ card: { ...card, title: e.target.value } })} /></Field>
          <Field label="Card description"><textarea className="textarea" value={card.description ?? ""} onChange={(e) => setC({ card: { ...card, description: e.target.value } })} /></Field>
          <Field label="Image URL"><input className="input" value={card.image_url ?? ""} onChange={(e) => setC({ card: { ...card, image_url: e.target.value } })} /></Field>
        </>
      ) : (
        <Field label="Message" hint={PERSONALIZE}><textarea className="textarea" value={c.text ?? ""} onChange={(e) => setC({ text: e.target.value })} /></Field>
      )}
      <h3 className="small" style={{ margin: "6px 0 8px" }}>Suggested replies and actions <span className="muted">(up to 4)</span></h3>
      {sugg.map((s, i) => (
        <div className="rule-row" key={i}>
          <select className="select" value={s.type} onChange={(e) => setC({ suggestions: sugg.map((x, j) => (j === i ? { ...x, type: e.target.value as "reply" | "url" | "dial" } : x)) })} aria-label="Suggestion type">
            <option value="reply">Reply</option><option value="url">Open link</option><option value="dial">Call</option>
          </select>
          <input className="input" placeholder="Button text" value={s.text} onChange={(e) => setC({ suggestions: sugg.map((x, j) => (j === i ? { ...x, text: e.target.value } : x)) })} aria-label="Button text" />
          {s.type !== "reply" && <input className="input" placeholder={s.type === "url" ? "https://…" : "+91…"} value={s.value ?? ""} onChange={(e) => setC({ suggestions: sugg.map((x, j) => (j === i ? { ...x, value: e.target.value } : x)) })} aria-label="Link or number" />}
          <button className="btn ghost sm" onClick={() => setC({ suggestions: sugg.filter((_, j) => j !== i) })}>Remove</button>
        </div>
      ))}
      {sugg.length < 4 && <button className="btn sm" onClick={() => setC({ suggestions: [...sugg, { type: "reply", text: "" }] })}>Add a suggestion</button>}
    </>
  );
}

function ListEditor({ label, hint, items, placeholder, onChange }: { label: string; hint?: string; items: string[]; placeholder: (i: number) => string; onChange: (v: string[]) => void }) {
  return (
    <div className="field">
      <span>{label}</span>
      {items.map((v, i) => (
        <div className="row" key={i}>
          <input className="input" style={{ flex: 1 }} value={v} placeholder={placeholder(i)} onChange={(e) => onChange(items.map((x, j) => (j === i ? e.target.value : x)))} aria-label={`Variable ${i + 1}`} />
          <button className="btn ghost sm" onClick={() => onChange(items.filter((_, j) => j !== i))}>Remove</button>
        </div>
      ))}
      <div><button className="btn sm" onClick={() => onChange([...items, ""])}>Add variable</button></div>
      {hint && <small>{hint}</small>}
    </div>
  );
}

// SMS length in segments: GSM-7 fits 160 (153 per part), anything else is UCS-2 at 70 (67 per part).
function SmsCount({ text }: { text: string }) {
  const gsm = /^[\n\r @£$¥èéùìòÇØøÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ!"#¤%&'()*+,\-./0-9:;<=>?¡A-ZÄÖÑÜ§¿a-zäöñüà^{}\\[~\]|€]*$/.test(text);
  const single = gsm ? 160 : 70, part = gsm ? 153 : 67;
  const parts = text.length <= single ? 1 : Math.ceil(text.length / part);
  return <>{text.length} characters, {parts} SMS {gsm ? "" : "(Unicode)"}. Personalized values change the length.</>;
}

function fillPreview(s = "") {
  return s.replace(/\{\{\s*\.Attrs\.(\w+)\s*\}\}/g, (_, k) => ({ first_name: "Asha", city: "Pune", name: "Asha Rao" } as Record<string, string>)[k] ?? `[${k}]`);
}

// fillContent fills personalization tags with sample values for the live preview while typing.
function fillContent(c: ChannelContent): ChannelContent {
  return {
    ...c,
    text: fillPreview(c.text),
    body_params: c.body_params?.map((p) => fillPreview(p)),
    card: c.card ? { ...c.card, title: fillPreview(c.card.title), description: fillPreview(c.card.description) } : c.card,
    title: fillPreview(c.title),
    body: fillPreview(c.body),
  };
}
