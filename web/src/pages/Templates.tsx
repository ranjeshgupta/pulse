import { TemplateReport } from "./TemplateReport";
import { SpamCheckModal } from "./SpamCheck";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { lazy, Suspense, useEffect, useRef, useState, type Dispatch, type RefObject, type SetStateAction } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { api, CHANNEL_NAMES, CHANNELS, type AssetList, type Channel, type ChannelContent, type Template } from "../api";
import { formatBytes, MessagePreview, PreviewModal, TestSendModal } from "../messagePreview";
import { ask, askDelete, useEditGuard } from "../guard";
import { ChannelLabel, Empty, ErrorNote, Field, PageHead, timeAgo, useToast } from "../ui";
import { DuplicateButton } from "../campaignUI";
import { initialEmailMode, replacesLayout, switchWarning, type EmailMode } from "../editorModes";
import { RichText } from "../RichText";
import { EMAIL_STYLE, unwrapRichText, wrapRichText } from "../richTextHtml";
import { exampleEvent, fillSample, lintAll, SAMPLE_CONTACT, type Sample } from "../personalize";
import { EventScopePicker, PersonalizeScopeProvider, TemplatedInput, TemplatedText, useVariables, VarPicker, VarProblems } from "../VarPicker";
import type { EmailBuilderHandle } from "./EmailBuilder";
import { AmpSection } from "./AmpEditor";
import { LanguageBar, TranslationEditor } from "./TranslationEditor";
import { TemplateAI } from "../AIHelp";

const EmailBuilder = lazy(() => import("./EmailBuilder"));

/** A new rich-text email starts from a greeting. */
export const RICH_STARTER = wrapRichText('<p>Hi {{contact.first_name | default:"there"}},</p><p></p>', EMAIL_STYLE);

const PERSONALIZE = "Personalize with { } Variable: {{contact.first_name | default:\"there\"}}, {{event.product_name}}, {{campaign.name}}.";

/** The event whose properties {{event.x}} offers, remembered per browser. */
const SCOPE_KEY = "pulse.template-event";
function savedScope(): string {
  try { return localStorage.getItem(SCOPE_KEY) ?? ""; } catch { return ""; }
}


/** channelWord: a channel's name inside a sentence ("email", "push", but "SMS", "WhatsApp"). */
const channelWord = (c: Channel) => { const n = CHANNEL_NAMES[c]; return /^[A-Z][a-z]+$/.test(n) ? n.toLowerCase() : n; };
/** knownChannel: a ?channel= value when it names a channel, else "" (never a lookup key like "__proto__"). */
export function knownChannel(v: string | null): Channel | "" {
  return v && (CHANNELS as readonly string[]).includes(v) ? (v as Channel) : "";
}

export function TemplateList() {
  const [params, setParams] = useSearchParams();
  // Only a known channel filters: an unknown ?channel= (a typo, an old link, "__proto__") shows them all.
  const channel = knownChannel(params.get("channel"));
  const q = useQuery({ queryKey: ["templates", channel], queryFn: () => api.get<{ templates: Template[] }>(`/v1/templates${channel ? `?channel=${channel}` : ""}`) });
  const ts = q.data?.templates ?? [];
  return (
    <div className="page">
      <PageHead
        title="Message templates"
        intro="Reusable content for the email, SMS, WhatsApp, RCS, push and app inbox messages your campaigns send. Each template belongs to one channel. (Website and app pop-ups are made under Website & app messages; landing pages under Landing pages.)"
        actions={<>
          <Link className="btn" to="/templates/blocks">🧩 Content blocks</Link>
          <Link className="btn primary" to={`/templates/new?channel=${channel || "email"}`}>New {channelWord(channel || "email")} template</Link>
        </>}
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

export const BLANK: Record<Channel, ChannelContent> = {
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
  // The email body is edited in the builder, as rich text or as HTML; "text" edits the plain-text part.
  const [emailMode, setEmailMode] = useState<EmailMode>("visual");
  const [testing, setTesting] = useState(false);
  const [previewing, setPreviewing] = useState(false);
  const [spam, setSpam] = useState(false);
  const [lang, setLang] = useState(""); // "" is the default; else the translation being edited
  const [scopeEvent, setScopeEvent] = useState(() => params.get("event") ?? savedScope());
  const [tab, setTabState] = useState(() => params.get("tab") === "report" ? "report" : "content");
  const setTab = (v: "content" | "report") => {
    setTabState(v);
    const next = new URLSearchParams(window.location.search);
    if (v === "report") next.set("tab", "report"); else next.delete("tab");
    window.history.replaceState(null, "", `${window.location.pathname}${next.toString() ? `?${next}` : ""}`);
  };
  const builder = useRef<EmailBuilderHandle>(null);
  const setScope = (v: string) => { setScopeEvent(v); try { localStorage.setItem(SCOPE_KEY, v); } catch { /* private window */ } };

  useEffect(() => {
    if (isNew && !t) {
      // An unknown ?channel= (a typo or an old link) starts an email template rather than failing.
      const ch: Channel = knownChannel(params.get("channel")) || "email";
      setT({ id: 0, name: "", channel: ch, subject: "", html: "", content: { ...BLANK[ch] }, updated_at: "" });
    } else if (existing.data && !t) {
      setT({ ...existing.data, content: existing.data.content ?? {} });
      if (existing.data.channel === "email") setEmailMode(initialEmailMode(existing.data));
    }
  }, [existing.data, isNew, params, t]);

  const warnings = useTemplateWarnings(t, scopeEvent);
  // Unsaved changes: every edit outside the builder replaces t, so any t other than the one loaded
  // or last saved is a change; the drag-and-drop builder reports its own edits (onChange).
  const base = useRef<Template | null>(null);
  const builderEdited = useRef(false);
  const guard = useEditGuard(() => { if (!save.isPending) save.mutate(); });
  const { markDirty } = guard;
  const onBuilderChange = () => { builderEdited.current = true; markDirty(); };
  // setT for state read out of an untouched builder (its HTML is only re-serialized): not an edit.
  const setFromBuilder = (next: Template) => {
    if (!builderEdited.current && t === base.current) base.current = next;
    setT(next);
  };
  useEffect(() => {
    if (!t) return;
    if (!base.current) base.current = t;
    else if (t !== base.current) markDirty();
  }, [t, markDirty]);
  const save = useMutation({
    mutationFn: async () => {
      let body = { ...t! };
      if (t!.channel === "email" && emailMode === "visual" && builder.current) {
        const { html, project } = builder.current.read();
        body = { ...body, html, builder: project };
        setFromBuilder(body);
      }
      if (t!.channel === "email" && replacesLayout(emailMode)) body.builder = undefined;
      return isNew ? api.post<{ id: number }>("/v1/templates", body) : api.put<{ id: number }>(`/v1/templates/${id}`, body);
    },
    onSuccess: (r) => {
      guard.saved();
      builderEdited.current = false;
      // What was saved is the new starting point (with its id, for a new template).
      setT((cur) => (base.current = cur && isNew ? { ...cur, id: r.id } : cur));
      qc.invalidateQueries({ queryKey: ["templates"] });
      toast({ text: warnings ? `Template saved. Check ${warnings === 1 ? "1 variable" : `${warnings} variables`}: they may render empty.` : "Template saved" });
      // The page stays mounted under its new address: t keeps the saved id (above) so Send a test and later saves use it.
      if (isNew) nav(`/templates/${r.id}`, { replace: true });
    },
  });
  const remove = useMutation({
    mutationFn: () => api.del(`/v1/templates/${id}`),
    onSuccess: () => { guard.saved(); qc.invalidateQueries({ queryKey: ["templates"] }); toast({ text: "Template deleted" }); nav("/templates"); },
  });

  if (!t) return <div className="page"><ErrorNote error={existing.error} /></div>;

  const switchEmailMode = emailModeSwitcher({ t, setT: setFromBuilder, emailMode, setEmailMode, builder, fresh: isNew });

  return (
    <PersonalizeScopeProvider value={{ event: scopeEvent || undefined, channel: t.channel }}>
    <div className="page" style={t.channel === "email" ? { maxWidth: "none" } : undefined}>
      <PageHead
        back={<Link className="back" to={`/templates?channel=${t.channel}`}>Message templates</Link>}
        title={isNew ? `New ${channelWord(t.channel)} template` : t.name || "Template"}
        actions={<>
          {!isNew && <button className="btn danger" disabled={remove.isPending} onClick={() => void askDelete(existing.data?.name ? `the template ${existing.data.name}` : "this template",
            "Campaigns that use it will fail to send. This can't be undone.", existing.data?.name || undefined).then((ok) => ok && remove.mutate())}>Delete</button>}
          {!isNew && <DuplicateButton path={`/v1/templates/${id}/duplicate`} open={(nid) => `/templates/${nid}`} invalidate={["templates"]} />}
          <TemplateAI t={t} setT={setT} builderLocked={t.channel === "email" && emailMode === "visual"} />
          <button className="btn" onClick={() => { if (t.channel === "email" && emailMode === "visual" && builder.current) setFromBuilder({ ...t, html: builder.current.read().html }); setPreviewing(true); }}>Preview as contact</button>
          {t.channel === "email" && <button className="btn" onClick={() => { if (emailMode === "visual" && builder.current) setFromBuilder({ ...t, html: builder.current.read().html }); setSpam(true); }}>🛡️ Spam check</button>}
          <button className="btn" disabled={isNew} title={isNew ? "Save the template first" : undefined} onClick={() => setTesting(true)}>Send a test</button>
          <button className="btn primary" disabled={save.isPending} onClick={() => save.mutate()}>Save template</button>
        </>}
      />
      <ErrorNote error={save.error ?? remove.error} />
      {!isNew && (
        <div className="tabs tmpl-tabs" role="tablist" aria-label="Template">
          <button role="tab" aria-selected={tab !== "report"} onClick={() => setTab("content")}>✏️ Content</button>
          <button role="tab" aria-selected={tab === "report"} onClick={() => setTab("report")}>📊 Report</button>
        </div>
      )}
      {tab === "report" && !isNew && <TemplateReport id={t.id} channel={t.channel} />}
      {/* The editor stays mounted on the Report tab, so the email builder keeps its state. */}
      <div hidden={tab === "report" && !isNew}>
      {t.hidden && (
        <div className="alert info">
          Written inside a one-time campaign, so it isn't in template lists or pickers.{" "}
          <label className="check" style={{ display: "inline-flex", margin: 0 }}><input type="checkbox" checked={!t.hidden} onChange={() => setT({ ...t, hidden: false })} />Make it a reusable template (when saved)</label>
        </div>
      )}
      <LanguageBar t={t} lang={lang} setLang={setLang} setT={setT} />
      {lang !== "" && <TranslationEditor key={lang} t={t} locale={lang} setT={setT} onRemoved={() => setLang("")} />}
      {/* The default editor stays mounted while a translation is open, so the builder keeps its state. */}
      <div hidden={lang !== ""}>
      <TemplateBody t={t} setT={setT} emailMode={emailMode} switchEmailMode={switchEmailMode} builder={builder} scopeEvent={scopeEvent} setScope={setScope} hadBuilder={existing.data?.builder != null} onBuilderChange={onBuilderChange} onEdit={markDirty} />
      </div>
      </div>
      {testing && <TestSendModal template={t} event={scopeEvent} onClose={() => setTesting(false)} />}
      {previewing && <PreviewModal template={t} event={scopeEvent} onClose={() => setPreviewing(false)} />}
      {spam && <SpamCheckModal subject={t.subject} html={t.html} text={String((t.content as { text?: string })?.text ?? "")} onClose={() => setSpam(false)} />}
    </div>
    </PersonalizeScopeProvider>
  );
}

/** emailModeSwitcher switches the email editor, pulling the builder's state in first and warning
 * before its layout is lost. fresh: nothing is saved yet (rich text then starts from a greeting). */
export function emailModeSwitcher({ t, setT, emailMode, setEmailMode, builder, fresh }: {
  t: Template; setT: (t: Template) => void; emailMode: EmailMode; setEmailMode: (m: EmailMode) => void;
  builder: RefObject<EmailBuilderHandle | null>; fresh: boolean;
}) {
  return (to: EmailMode) => {
    if (to === emailMode) return;
    let next = t;
    if (emailMode === "visual" && builder.current) {
      const r = builder.current.read();
      next = { ...t, html: r.html, builder: r.project };
    }
    const go = (n: Template) => { setT(n); setEmailMode(to); };
    if (fresh && emailMode === "visual" && to === "richtext") {
      // Nothing saved yet: start the rich text email fresh rather than from the builder's layout.
      void ask({ title: "Write this email in the rich text editor?", body: "What's in the drag-and-drop builder is discarded.", confirmText: "Switch to rich text", danger: true })
        .then((ok) => ok && go({ ...next, html: RICH_STARTER, builder: undefined }));
      return;
    }
    const warning = switchWarning(emailMode, to, next.builder != null);
    if (!warning) return go(next);
    void ask({ title: "Leave the drag-and-drop builder?", body: warning, confirmText: "Switch", danger: true }).then((ok) => ok && go(next));
  };
}

/** TemplateBody is the content editor of a template, for every channel: the personalisation bar, the
 * email builder / rich text / HTML / plain text, and the other channels' fields with a live preview.
 * Used by the template page and by the one-time campaign wizard. */
export function TemplateBody({ t, setT, emailMode, switchEmailMode, builder, scopeEvent, setScope, hadBuilder, onBuilderChange, onEdit }: {
  t: Template; setT: Dispatch<SetStateAction<Template | null>>; emailMode: EmailMode; switchEmailMode: (m: EmailMode) => void;
  builder: RefObject<EmailBuilderHandle | null>; scopeEvent: string; setScope: (v: string) => void; hadBuilder: boolean;
  /** onBuilderChange: the drag-and-drop builder was edited (its changes live inside it until read). */
  onBuilderChange?: () => void;
  /** onEdit: an edit made with buttons that the caller's change tracking can't see (AMP blocks). */
  onEdit?: () => void;
}) {
  const toast = useToast();
  const sample = useSample(scopeEvent, t.channel);
  const c = t.content;
  const setC = (patch: Partial<ChannelContent>) => setT({ ...t, content: { ...c, ...patch } });
  return (
    <>
      <div className="vp-toolbar" aria-label="Personalisation">
        {/* The event scope only changes the preview, not the template. */}
        <span data-no-dirty style={{ display: "contents" }}><EventScopePicker value={scopeEvent} onChange={setScope} /></span>
        <label className="vp-policy">
          <span className="xs muted">When a variable has no value and no default</span>
          <select className="select inline" value={t.missing_values ?? "empty"} onChange={(e) => setT({ ...t, missing_values: e.target.value as "empty" | "skip" })} aria-label="When a variable has no value">
            <option value="empty">leave it empty</option>
            <option value="skip">don't send to that contact</option>
          </select>
        </label>
      </div>
      <div className={t.channel === "email" ? "" : "editor-split"}>
        <div>
          <label className="check">
            <input type="checkbox" checked={!!t.transactional} onChange={(e) => setT({ ...t, transactional: e.target.checked })} />
            Transactional (an OTP, receipt or password reset): ignore frequency caps and quiet hours
          </label>
          <div className="grid-2">
            <Field label="Template name"><input className="input" value={t.name} onChange={(e) => setT({ ...t, name: e.target.value })} placeholder="Welcome series, day 1" /></Field>
            {t.channel === "email" && <Field label="Subject" hint={<>{PERSONALIZE} Pulse adds an unsubscribe link and open/click tracking to every email; place the link yourself with <code>{"{{links.unsubscribe}}"}</code>, and link the preference center with <code>{"{{links.preferences}}"}</code>.</>}><TemplatedInput label="Subject" value={t.subject} onChange={(subject) => setT({ ...t, subject })} /></Field>}
          </div>

          {t.channel === "email" && (
            <>
              <div className="tabs" role="tablist">
                <button role="tab" aria-selected={emailMode === "visual"} onClick={() => switchEmailMode("visual")}>Visual builder</button>
                <button role="tab" aria-selected={emailMode === "richtext"} onClick={() => switchEmailMode("richtext")}>Rich text</button>
                <button role="tab" aria-selected={emailMode === "html"} onClick={() => switchEmailMode("html")}>HTML</button>
                {/* The plain-text part keeps the builder's layout: only rich text and HTML replace it. */}
                <button role="tab" aria-selected={emailMode === "text"} onClick={() => switchEmailMode("text")}>Plain text{c.text ? "" : " (auto)"}</button>
              </div>
              {emailMode === "text" ? (
                <PlainTextEditor t={t} setText={(text) => setC({ text })} />
              ) : emailMode === "visual" ? (
                <>
                  <div className="row" style={{ justifyContent: "flex-end", marginBottom: 6 }}>
                    <span className="xs muted">Copy a variable, then paste it into a text block.</span>
                    <VarPicker copyOnly onInsert={(tag) => toast({ text: `Copied ${tag}: paste it into the email` })} />
                  </div>
                  <Suspense fallback={<div className="gjs-host" />}><EmailBuilder ref={builder} html={t.html} project={t.builder} onChange={onBuilderChange} /></Suspense>
                </>
              ) : emailMode === "richtext" ? (
                <div className="grid-2" style={{ gap: 18, gridTemplateColumns: "minmax(0, 1.25fr) minmax(0, 1fr)" }}>
                  <RichText value={unwrapRichText(t.html) ?? t.html} label="Email body" minHeight={420} personalize="email" allowImages allowTables
                    placeholder="Write your email…" lossHint={<>or use the Visual builder</>}
                    onChange={(inner) => setT((cur) => cur && { ...cur, html: wrapRichText(inner, EMAIL_STYLE), builder: undefined })} />
                  <iframe className="preview-frame" title="Email preview, with sample values" sandbox="" srcDoc={fillSample(t.html, sample)} />
                </div>
              ) : (
                <div className="grid-2" style={{ gap: 18 }}>
                  <TemplatedText className="textarea code-editor" label="Email HTML" value={t.html} onChange={(html) => setT({ ...t, html, builder: undefined })} />
                  <iframe className="preview-frame" title="Email preview, with sample values" sandbox="" srcDoc={fillSample(t.html, sample)} />
                </div>
              )}
              {emailMode !== "html" && <BodyProblems html={t.html} />}
              {replacesLayout(emailMode) && hadBuilder && <p className="xs muted" style={{ marginTop: 8 }}>Saving from the {emailMode === "html" ? "HTML" : "Rich text"} tab replaces the visual layout with this HTML.</p>}
              <AttachmentPicker ids={c.attachments ?? []} onChange={(attachments) => setC({ attachments: attachments.length ? attachments : undefined })} />
              <AmpSection t={t} setT={setT} onEdit={onEdit} />
            </>
          )}

          {t.channel === "sms" && (
            <>
              <Field label="Message" hint={<SmsCount text={c.text ?? ""} />}><TemplatedText label="Message" value={c.text ?? ""} onChange={(text) => setC({ text })} /></Field>
              <Field label="Sender ID" hint="Your registered header (e.g. PULSEX for India DLT). Leave empty to use the provider's default."><input className="input" value={c.sender_id ?? ""} onChange={(e) => setC({ sender_id: e.target.value })} /></Field>
              <div className="grid-2">
                <Field label="DLT template ID (India)" hint="The ID this exact text is registered under on the DLT portal. Gupshup sends it with each message."><input className="input" value={c.dlt_template_id ?? ""} onChange={(e) => setC({ dlt_template_id: e.target.value.trim() || undefined })} /></Field>
                <Field label="DLT principal entity ID" hint="Leave empty to use the provider's."><input className="input" value={c.dlt_entity_id ?? ""} onChange={(e) => setC({ dlt_entity_id: e.target.value.trim() || undefined })} /></Field>
              </div>
              <TrackLinks c={c} setC={setC} />
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
              <ListEditor label="Variables" hint={PERSONALIZE} items={c.body_params ?? []} placeholder={(i) => `{{${i + 1}}}, e.g. {{contact.first_name}}`} onChange={(body_params) => setC({ body_params })} />
              <Field label="Header image URL (optional)"><TemplatedInput label="Header image URL" value={c.header_image_url ?? ""} onChange={(header_image_url) => setC({ header_image_url })} /></Field>
            </>
          )}

          {t.channel === "rcs" && <><RcsFields c={c} setC={setC} /><TrackLinks c={c} setC={setC} /></>}

          {t.channel === "inbox" && (
            <>
              <p className="xs muted" style={{ marginTop: 0 }}>Stays in the contact's app inbox (the mobile and web SDKs show it) until it expires or they delete it. Pulse delivers it itself: no provider needed.</p>
              <Field label="Title"><TemplatedInput label="Title" value={c.title ?? ""} onChange={(title) => setC({ title })} /></Field>
              <Field label="Message" hint={PERSONALIZE}><TemplatedText label="Message" value={c.body ?? ""} onChange={(body) => setC({ body })} /></Field>
              <div className="grid-2">
                <Field label="Image URL (optional)"><input className="input" value={c.image_url ?? ""} onChange={(e) => setC({ image_url: e.target.value })} /></Field>
                <Field label="Open this link when tapped"><TemplatedInput label="Open this link when tapped" value={c.url ?? ""} onChange={(url) => setC({ url })} placeholder="https:// or app deep link" /></Field>
                <Field label="Category (optional)" hint="Lets the app filter its inbox, e.g. offers or orders."><input className="input" value={c.category ?? ""} onChange={(e) => setC({ category: e.target.value })} /></Field>
                <Field label="Keep for (days)" hint="Then it disappears from the inbox. 1-365; 30 if empty.">
                  <input className="input" type="number" min={1} max={365} value={c.expires_days ?? ""} onChange={(e) => setC({ expires_days: e.target.value ? Number(e.target.value) : undefined })} />
                </Field>
              </div>
            </>
          )}

          {t.channel === "push" && (
            <>
              <Field label="Title"><TemplatedInput label="Title" value={c.title ?? ""} onChange={(title) => setC({ title })} /></Field>
              <Field label="Message" hint={PERSONALIZE}><TemplatedText label="Message" value={c.body ?? ""} onChange={(body) => setC({ body })} /></Field>
              <div className="grid-2">
                <Field label="Image URL (optional)"><input className="input" value={c.image_url ?? ""} onChange={(e) => setC({ image_url: e.target.value })} /></Field>
                <Field label="Open this link when tapped" hint="Web push and app deep links, e.g. myapp://cart/{{event.cart_id}}"><TemplatedInput label="Open this link when tapped" value={c.url ?? ""} onChange={(url) => setC({ url })} placeholder="https:// or app deep link" /></Field>
              </div>
            </>
          )}
        </div>
        {t.channel !== "email" && <MessagePreview channel={t.channel} r={{ content: fillContent(c, sample) }} label={`${t.channel === "push" ? "Lock screen" : CHANNEL_NAMES[t.channel]} preview, with sample values`} />}
      </div>
    </>
  );
}

/** useTemplateWarnings counts the variable problems of every templated field (for the save message). */
function useTemplateWarnings(t: Template | null, event: string): number {
  const v = useVariables({ event: event || undefined, channel: t?.channel });
  if (!t) return 0;
  const c = t.content ?? {};
  return lintAll([t.subject, t.html, c.text, c.title, c.body, c.url, c.header_image_url, ...(c.body_params ?? []), c.card?.title, c.card?.description,
    ...(c.suggestions ?? []).flatMap((x) => [x.text, x.value])], { contact: v.contact, event: v.event, channel: t.channel }).length;
}

/** useSample: the preview's sample contact and event (the scope event's schema example). */
function useSample(event: string, channel: string): Sample {
  const v = useVariables({ event: event || undefined, channel });
  return { contact: SAMPLE_CONTACT, event: v.schema ? exampleEvent(v.schema) : null,
    campaign: { name: "Sample campaign", type: "journey", id: 1, step: "s1", channel, template: "This template" },
    links: { unsubscribe: "#unsubscribe", preferences: "#preferences" } };
}

/** BodyProblems lists the variable problems of an email body edited visually. */
function BodyProblems({ html }: { html: string }) {
  const v = useVariables();
  return <VarProblems problems={lintAll([html], { contact: v.contact, event: v.event, channel: "email" })} />;
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
      <TemplatedText className="textarea code-editor plain-text-part" value={text} onChange={setText}
        aria-label="Plain-text part" placeholder="Empty: generated from the HTML when the email is sent." />
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
          <Field label="Card title"><TemplatedInput label="Card title" value={card.title} onChange={(title) => setC({ card: { ...card, title } })} /></Field>
          <Field label="Card description"><TemplatedText label="Card description" value={card.description ?? ""} onChange={(description) => setC({ card: { ...card, description } })} /></Field>
          <Field label="Image URL"><input className="input" value={card.image_url ?? ""} onChange={(e) => setC({ card: { ...card, image_url: e.target.value } })} /></Field>
        </>
      ) : (
        <Field label="Message" hint={PERSONALIZE}><TemplatedText label="Message" value={c.text ?? ""} onChange={(text) => setC({ text })} /></Field>
      )}
      <h3 className="small" style={{ margin: "6px 0 8px" }}>Suggested replies and actions <span className="muted">(up to 4)</span></h3>
      {sugg.map((s, i) => (
        <div className="rule-row" key={i}>
          <select className="select" value={s.type} onChange={(e) => setC({ suggestions: sugg.map((x, j) => (j === i ? { ...x, type: e.target.value as "reply" | "url" | "dial" } : x)) })} aria-label="Suggestion type">
            <option value="reply">Reply</option><option value="url">Open link</option><option value="dial">Call</option>
          </select>
          <TemplatedInput placeholder="Button text" value={s.text} onChange={(text) => setC({ suggestions: sugg.map((x, j) => (j === i ? { ...x, text } : x)) })} aria-label="Button text" />
          {s.type !== "reply" && <TemplatedInput placeholder={s.type === "url" ? "https://…" : "+91…"} value={s.value ?? ""} onChange={(value) => setC({ suggestions: sugg.map((x, j) => (j === i ? { ...x, value } : x)) })} aria-label="Link or number" />}
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
        <div className="row" key={i} style={{ alignItems: "flex-start", flexWrap: "nowrap" }}>
          <span className="xs muted" style={{ paddingTop: 8, minWidth: 34 }}>{`{{${i + 1}}}`}</span>
          <div style={{ flex: 1, minWidth: 0 }}><TemplatedInput value={v} placeholder={placeholder(i)} onChange={(nv) => onChange(items.map((x, j) => (j === i ? nv : x)))} aria-label={`Variable ${i + 1}`} /></div>
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
  return <>{text.length} characters, {parts} SMS{gsm ? "" : " (Unicode)"}. Personalized values change the length.</>;
}

// fillContent fills personalization tags with sample values for the live preview while typing.
function fillContent(c: ChannelContent, sample: Sample): ChannelContent {
  const f = (x?: string) => fillSample(x, sample);
  return {
    ...c,
    text: f(c.text),
    body_params: c.body_params?.map((p) => f(p)),
    header_image_url: c.header_image_url ? f(c.header_image_url) : c.header_image_url,
    card: c.card ? { ...c.card, title: f(c.card.title), description: f(c.card.description) } : c.card,
    suggestions: c.suggestions?.map((x) => ({ ...x, text: f(x.text) })),
    title: f(c.title),
    body: f(c.body),
  };
}

// TrackLinks turns on short tracked links for an SMS or RCS template, so clicks count (sms_clicked,
// rcs_clicked) in reports and segments. Each link becomes about 40 characters.
function TrackLinks({ c, setC }: { c: ChannelContent; setC: (p: Partial<ChannelContent>) => void }) {
  return (
    <label className="check" style={{ alignItems: "flex-start", margin: "6px 0 10px" }}>
      <input type="checkbox" style={{ marginTop: 3 }} checked={!!c.track_links} onChange={(e) => setC({ track_links: e.target.checked || undefined })} />
      <span>
        Track link clicks
        <span className="xs muted" style={{ display: "block" }}>Links become short tracked links (…/t/s/abc123XYZ01, about 40 characters, working for 30 days), so “Clicked a link in an SMS” works in segments and reports.</span>
      </span>
    </label>
  );
}
